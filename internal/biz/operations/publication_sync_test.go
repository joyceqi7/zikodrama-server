package operations_test

import (
	"context"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"
)

// TestPublicationReadState 验证升级前同步队列及中断请求成为人工待办，真实异步结果继续等待。
func TestPublicationReadState(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name         string
		p            operations.TikTokPublication
		status, code string
	}{
		{"旧同步队列", operations.TikTokPublication{SubjectID: "1", Status: operations.PublicationStatusQueued, Stage: operations.PublicationStageUploadCover}, operations.PublicationStatusFailed, "dependency_error"},
		{"旧未知写入", operations.TikTokPublication{SubjectID: "1", Status: operations.PublicationStatusQueued, Stage: operations.PublicationStageUpdateAlbum, ExternalWriteStarted: true}, operations.PublicationStatusFailed, "external_write_uncertain"},
		{"真实媒体等待", operations.TikTokPublication{SubjectID: "1", Status: operations.PublicationStatusQueued, Stage: operations.PublicationStageTranscodeVideos}, operations.PublicationStatusQueued, ""},
		{"活跃请求", operations.TikTokPublication{SubjectID: "1", Status: operations.PublicationStatusProcessing, LeaseOwner: "request", LeaseExpiresAt: now.Add(time.Minute)}, operations.PublicationStatusProcessing, ""},
		{"中断请求", operations.TikTokPublication{SubjectID: "1", Status: operations.PublicationStatusProcessing, LeaseOwner: "request", LeaseExpiresAt: now.Add(-time.Second)}, operations.PublicationStatusFailed, "dependency_error"},
		{"占用前的刚创建记录", operations.TikTokPublication{SubjectID: "1", Status: operations.PublicationStatusProcessing, UpdatedAt: now}, operations.PublicationStatusProcessing, ""},
		{"占用前进程已退出", operations.TikTokPublication{SubjectID: "1", Status: operations.PublicationStatusProcessing, UpdatedAt: now.Add(-3 * time.Minute)}, operations.PublicationStatusFailed, "dependency_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.p
			p.Revision = 42
			got := operations.PublicationReadState(p, now)
			if got.Status != tc.status || got.ErrorCode != tc.code || got.Revision != p.Revision || got.LeaseOwner != p.LeaseOwner || got.ExternalWriteStarted != p.ExternalWriteStarted {
				t.Fatalf("读取改变执行门禁或待办错误: %+v", got)
			}
		})
	}
}

// synchronousFixture 不启动 Worker，直接验证请求是否调用平台和保存确认结果。
func synchronousFixture(t *testing.T) (*operations.Service, *memory.Store, *publicationProviderStub, string, string) {
	t.Helper()
	now := time.Now().UTC()
	owner := application.Application{SubjectID: "1", ID: "101", TikTokAppID: "owner", ClientKey: "key-owner", SecretRef: "fixture", BytePlusAccountID: "account", BytePlusSpaceName: "space", Status: application.StatusActive}
	online := domain.Drama{ID: "201", Title: "V1", Description: "description", CoverURL: "https://fixture.example/cover", Language: "en", Status: domain.DramaStatusCompleted, PublishStatus: "published", TagIDs: []string{"1"}, PlatformAlbumID: "album", PlatformVersion: 1, OwnerApplicationID: "owner", Episodes: []domain.Episode{{ID: "301", DramaID: "201", Number: 1, Title: "episode", VID: "vid", MediaStatus: "ready", PlatformEpisodeID: "episode-1"}}}
	draft := domain.FreezeContent(online)
	draft.PublishStatus = "ready"
	draft.Published = &online
	store := memory.NewStore(memory.Seed{Applications: []application.Application{owner}, Dramas: []domain.Drama{draft}})
	frozen := domain.FreezeContent(online)
	p := operations.TikTokPublication{SubjectID: "1", ID: "401", DramaID: "201", OwnerApplicationID: "owner", OwnerScope: operations.PublicationOwnerScope{ClientKey: owner.ClientKey, AccountID: owner.BytePlusAccountID, SpaceName: owner.BytePlusSpaceName}, ContentSnapshot: &frozen, PlatformAlbumID: "album", PlatformVersion: 1, PlatformCoverID: "cover", EpisodeIDsBySequence: map[int]string{1: "episode-1"}, ReviewStatus: 2, Status: operations.PublicationStatusOnline, Stage: operations.PublicationStageDone, OnlineAt: &now, CreatedAt: now, UpdatedAt: now, Videos: []operations.PublicationVideo{{EpisodeID: "301", VID: "vid", Status: "ready"}}, Transcodes: []operations.PublicationTranscode{{VIDs: []string{"vid"}, CompletedVIDs: []string{"vid"}, Ready: true}}}
	store.SeedTikTokPublication(&p)
	provider := &publicationProviderStub{}
	repos := memory.OperationsRepositories(store)
	repos.Content.OwnerApplication = func(context.Context, string) (application.Application, error) { return owner, nil }
	s := operations.NewService(repos, nil)
	if err := s.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, operations.PublicationJobConfig{}); err != nil {
		t.Fatal(err)
	}
	return s, store, provider, draft.ID, p.ID
}

// TestOfficialCommandsCompleteWithoutWorker 验证所有立即可完成的命令在请求内确认，旧版本上下架独立于新审核。
func TestOfficialCommandsCompleteWithoutWorker(t *testing.T) {
	ctx := context.Background()
	s, store, provider, id, v1 := synchronousFixture(t)
	draft, _ := store.FindDrama(ctx, id)
	draft.Description = "V2 description"
	if err := store.SaveDramaAggregate(ctx, draft, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareDrama(ctx, id); err != nil {
		t.Fatal(err)
	}
	v2, err := s.SubmitTikTokPublication(ctx, id, operations.TikTokPublicationInput{})
	if err != nil || v2.Status != operations.PublicationStatusSynced || v2.PlatformVersion != 2 {
		t.Fatal("同步接口返回前未完成平台更新", v2, err)
	}
	if got := provider.callSnapshot(); !reflect.DeepEqual(got, []string{"update"}) {
		t.Fatal("重复登记或转码原媒体", got)
	}
	if _, err := s.SubmitTikTokReview(ctx, id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	current, _ := s.FindTikTokPublication(ctx, id)
	if current.Status != operations.PublicationStatusUnderReview || current.ReviewID == "" {
		t.Fatal("送审未同步受理")
	}
	before := *current
	for _, action := range []string{"delist", "list"} {
		result, err := s.ChangeTikTokRelease(ctx, id, operations.TikTokReleaseInput{Action: action, SelectedPublicationID: v1})
		if err != nil {
			t.Fatal(err)
		}
		want := operations.PublicationStatusOnline
		if action == "delist" {
			want = operations.PublicationStatusOffline
		}
		if result.Status != want || result.ID != v1 || result.PlatformVersion != 1 {
			t.Fatal("上下架创建了额外任务或切换版本", result)
		}
		after, _ := s.FindTikTokPublication(ctx, id)
		if after.ID != before.ID || after.Revision != before.Revision || after.Status != before.Status {
			t.Fatal("V1 上下架覆盖 V2 审核")
		}
	}
	v2, err = s.RefreshTikTokReview(ctx, id)
	if err != nil || v2.Status != operations.PublicationStatusApproved {
		t.Fatal(v2, err)
	}
	state, _ := s.FindTikTokReleaseState(ctx, id)
	if state.Version != 1 {
		t.Fatal("审核通过自动切换线上版本")
	}
	selected, err := s.PublishTikTokPublication(ctx, id, v2.ID)
	if err != nil || selected.Status != operations.PublicationStatusOnline {
		t.Fatal("新版上线未在请求内完成", selected, err)
	}
	if _, err := s.ChangeTikTokRelease(ctx, id, operations.TikTokReleaseInput{Action: "delist", SelectedPublicationID: v1}); !errors.Is(err, operations.ErrConflict) {
		t.Fatal("迟到页面操作了另一版本", err)
	}
}

// existingEpisodeUpdateProvider 要求元数据更新携带原分集身份，避免把既有分集误当成新增内容。
type existingEpisodeUpdateProvider struct {
	publicationProviderStub
	draft operations.AlbumDraft
}

// UpdateAlbum 保留捕获的请求；缺少官方分集标识时模拟无法确认的更新结果。
func (p *existingEpisodeUpdateProvider) UpdateAlbum(_ context.Context, draft operations.AlbumDraft) (operations.UpdateAlbumResult, error) {
	p.draft = draft
	if len(draft.Episodes) != 1 || draft.Episodes[0].PlatformEpisodeID != "episode-1" {
		return operations.UpdateAlbumResult{}, &operations.PlatformCallError{Code: "album_update_unconfirmed", OutcomeUnknown: true}
	}
	return operations.UpdateAlbumResult{Version: 2, EpisodeIDsBySequence: map[int]string{1: "episode-1"}}, nil
}

// TestOfficialMetadataUpdatePreservesEpisodeIdentity 验证冻结快照仍与渠道隔离，更新请求从任务事实恢复原官方分集身份。
func TestOfficialMetadataUpdatePreservesEpisodeIdentity(t *testing.T) {
	s, store, _, id, _ := synchronousFixture(t)
	ctx := context.Background()
	draft, err := store.FindDrama(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	draft.Description = "新版简介"
	if err := store.SaveDramaAggregate(ctx, draft, nil); err != nil {
		t.Fatal(err)
	}
	provider := &existingEpisodeUpdateProvider{}
	if err := s.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, operations.PublicationJobConfig{}); err != nil {
		t.Fatal(err)
	}
	result, err := s.SubmitTikTokPublication(ctx, id, operations.TikTokPublicationInput{})
	if err != nil || result.Status != operations.PublicationStatusSynced || result.PlatformVersion != 2 {
		t.Fatalf("修改简介丢失原官方分集身份：%+v %v", result, err)
	}
	if provider.draft.Description != draft.Description || provider.draft.Version != 1 || result.EpisodeIDsBySequence[1] != "episode-1" {
		t.Fatal("更新没有保持版本或分集关联")
	}
	current, err := store.FindDrama(ctx, id)
	if err != nil || current.Published == nil || current.Published.PlatformVersion != 1 {
		t.Fatal("同步元数据改变了原线上版本", err)
	}
}

// TestSynchronousReviewFailureIsReturnedAndNeverQueued 验证送审失败直接返回，人工重试前不重放平台调用。
func TestSynchronousReviewFailureIsReturnedAndNeverQueued(t *testing.T) {
	s, store, _, id, _ := synchronousFixture(t)
	ctx := context.Background()
	p, _ := store.FindTikTokPublicationByDramaID(ctx, id)
	p.Status, p.Stage, p.ReviewID, p.OnlineAt = operations.PublicationStatusSynced, operations.PublicationStageAwaitSubmitReview, "", nil
	if err := store.UpdateTikTokPublication(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	provider := &quotaReviewProvider{}
	if err := s.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, operations.PublicationJobConfig{}); err != nil {
		t.Fatal(err)
	}
	_, err := s.SubmitTikTokReview(ctx, id, operations.TikTokPublicationInput{Priority: "urgent"})
	var platformErr *operations.PlatformCallError
	if !errors.As(err, &platformErr) || platformErr.Code != "urgent_review_quota_exceeded" {
		t.Fatal("同步拒绝未直接返回", err)
	}
	failed, _ := s.FindTikTokPublication(ctx, id)
	if failed.Status != operations.PublicationStatusFailed || failed.ExternalWriteStarted {
		t.Fatal("明确拒绝保存为排队或未知写入", failed)
	}
	now := time.Now().UTC().Add(time.Hour)
	job, err := store.ClaimTikTokPublication(ctx, "unexpected", now, now.Add(time.Minute))
	if err != nil || job != nil {
		t.Fatal("同步失败仍由 Worker 重放", job, err)
	}
	result, err := s.RetryTikTokPublication(ctx, id, operations.TikTokPublicationInput{Priority: "normal"})
	if err != nil || result.Status != operations.PublicationStatusUnderReview {
		t.Fatal("重试未同步受理", result, err)
	}
}

// TestSelectedVersionSurvivesListingFailure 验证平台已确认版本选择后，上架异常不能使本地误认仍选择旧版本。
func TestSelectedVersionSurvivesListingFailure(t *testing.T) {
	s, store, provider, id, _ := synchronousFixture(t)
	ctx := context.Background()
	p, _ := store.FindTikTokPublicationByDramaID(ctx, id)
	p.ID = "402"
	p.PlatformVersion = 2
	p.Status, p.Stage = operations.PublicationStatusApproved, operations.PublicationStageAwaitPublish
	p.OnlineAt = nil
	p.CreatedAt = time.Now().UTC().Add(time.Second)
	store.SeedTikTokPublication(&p)
	provider.listFailure = &operations.PlatformCallError{Code: "api_rejected"}
	if _, err := s.PublishTikTokPublication(ctx, id, p.ID); err == nil {
		t.Fatal("上架失败未返回错误")
	}
	state, _ := s.FindTikTokReleaseState(ctx, id)
	drama, _ := store.FindDrama(ctx, id)
	if state == nil || state.SelectedPublicationID != p.ID || state.Version != 2 || drama.Published == nil || drama.Published.PlatformVersion != 2 {
		t.Fatal("已确认选择被丢失", state)
	}
}

// TestDraftOfficialSyncWithoutTranscode 验证草稿直接同步仍登记新视频，显式源视频策略不调用转码且不改变线上版本。
func TestDraftOfficialSyncWithoutTranscode(t *testing.T) {
	s, store, provider, id, _ := synchronousFixture(t)
	ctx := context.Background()
	d, err := store.FindDrama(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	d.Title = "Draft metadata update"
	d.PublishStatus = "draft"
	episode := d.Episodes[0]
	episode.VID = "new-source-vid"
	if err := store.SaveDramaAggregate(ctx, d, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveEpisode(ctx, episode); err != nil {
		t.Fatal(err)
	}
	noTranscode := false
	result, err := s.SubmitTikTokPublication(ctx, id, operations.TikTokPublicationInput{Transcode: &noTranscode})
	if err != nil || result.Status != operations.PublicationStatusSynced || !result.SkipTranscode {
		t.Fatal("草稿源视频同步失败", result, err)
	}
	if result.PlatformVersion != 2 || result.Stage != operations.PublicationStageAwaitSubmitReview {
		t.Fatal("同步没有停在待送审", result)
	}
	calls := provider.callSnapshot()
	if !slices.Contains(calls, "video-register") || slices.Contains(calls, "transcode-start") || slices.Contains(calls, "submit") || slices.Contains(calls, "list") {
		t.Fatal("源视频流程漏登记或自动转码/送审/上线", calls)
	}
	after, err := store.FindDrama(ctx, id)
	if err != nil || after.PublishStatus != "draft" || after.Published == nil || after.Published.PlatformVersion != 1 {
		t.Fatal("同步草稿改变旧线上版本", err)
	}
}
