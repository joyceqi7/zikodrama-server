package operations_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/operations"
)

// TestManualReviewAndPublication 验证等待期间不查询平台，审核通过与观众可见之间必须有人工指令。
func TestManualReviewAndPublication(t *testing.T) {
	ctx := context.Background()
	provider := &publicationProviderStub{reviewStatus: 1}
	service, store, id := publicationFixture(t, provider)
	p, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{})
	if err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusSynced)
	if slices.Contains(provider.callSnapshot(), "review") {
		t.Fatal("内容同步完成后自动提交了审核")
	}
	if _, err := service.PublishTikTokPublication(ctx, id, p.ID); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("送审前允许上线：%v", err)
	}
	if _, err := service.SubmitTikTokReview(ctx, id, operations.TikTokPublicationInput{Priority: operations.PublicationPriorityUrgent}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	if slices.Contains(provider.callSnapshot(), "query") {
		t.Fatal("等待审核时自动查询了平台")
	}
	for _, status := range []int{1, 2} {
		provider.mu.Lock()
		provider.reviewStatus = status
		provider.mu.Unlock()
		p, err = service.RefreshTikTokReview(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		want := operations.PublicationStatusUnderReview
		if status == 2 {
			want = operations.PublicationStatusApproved
		}
		if p.Status != want {
			t.Fatalf("手动查询状态=%s，期望%s", p.Status, want)
		}
		if status == 1 && p.NextAttemptAt.IsZero() {
			t.Fatal("审核中任务没有安排下一次自动查询")
		}
		if status == 2 {
			future := time.Now().Add(365 * 24 * time.Hour)
			claimed, err := store.ClaimTikTokPublication(ctx, "unexpected-worker", future, future.Add(time.Minute))
			if err != nil || claimed != nil {
				t.Fatalf("待上线任务仍可被定时领取：%v %v", claimed, err)
			}
		}
		drama, err := service.FindDrama(ctx, id)
		if err != nil || drama.PublishStatus != "ready" || drama.Published != nil {
			t.Fatalf("确认前已经上线或解除冻结：%v %v", drama, err)
		}
	}
	if _, err := service.PublishTikTokPublication(ctx, id, "old-publication"); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("旧页面允许上线当前版本：%v", err)
	}
	if _, err := service.PublishTikTokPublication(ctx, id, p.ID); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusOnline)
	if _, err := service.PublishTikTokPublication(ctx, id, p.ID); err != nil {
		t.Fatalf("重复确认不幂等：%v", err)
	}
	calls := provider.callSnapshot()
	for call, want := range map[string]int{"query": 2, "online-version": 1, "list": 1} {
		count := 0
		for _, value := range calls {
			if value == call {
				count++
			}
		}
		if count != want {
			t.Fatalf("%s 调用%d次，期望%d次：%v", call, count, want, calls)
		}
	}
}

// TestUncertainReviewInspectionNeverReplaysSubmit 验证官方查询只补充观察事实，不把可能重复的送审请求再次发出。
func TestUncertainReviewInspectionNeverReplaysSubmit(t *testing.T) {
	ctx := context.Background()
	provider := &publicationProviderStub{reviewStatus: 1}
	service, store, id := publicationFixture(t, provider)
	if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusSynced)
	p, err := store.FindTikTokPublicationByDramaID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p.Status, p.Stage = operations.PublicationStatusFailed, operations.PublicationStageSubmitReview
	p.ExternalWriteStarted, p.ErrorCode = true, "external_write_uncertain"
	p.ReviewID = ""
	if err := store.UpdateTikTokPublication(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	before := provider.callSnapshot()
	result, err := service.RefreshTikTokReview(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != operations.PublicationStatusFailed || result.Stage != operations.PublicationStageSubmitReview ||
		result.ErrorCode != "external_write_uncertain" || !result.ExternalWriteStarted || result.ReviewID != "" || result.ReviewStatus != 1 {
		t.Fatalf("未知送审结果被错误推进: %+v", result)
	}
	if !slices.Equal(provider.callSnapshot(), append(before, "query")) {
		t.Fatalf("只允许官方只读查询，实际调用: %v", provider.callSnapshot())
	}
	if _, err := service.RetryTikTokPublication(ctx, id); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("仍不能重发未知结果的送审: %v", err)
	}
}

// TestSyncedDraftCanBeEditedBeforeReview 验证待送审版本仍可修改，修改后必须重新同步，不能误审旧平台版本。
func TestSyncedDraftCanBeEditedBeforeReview(t *testing.T) {
	ctx := context.Background()
	provider := &publicationProviderStub{reviewStatus: 1}
	service, _, id := publicationFixture(t, provider)
	if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusSynced)
	provider.resetCalls()
	drama, err := service.FindDrama(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateDrama(ctx, id, operations.DramaInput{
		Title: drama.Title + " revised", Description: drama.Description, CoverURL: drama.CoverURL,
		Status: drama.Status, Year: drama.Year, Language: drama.Language, DramaType: drama.DramaType,
		TagIDs: append([]string(nil), drama.TagIDs...), Episodes: []operations.EpisodeInput{{
			ID: drama.Episodes[0].ID, Number: drama.Episodes[0].Number, Title: drama.Episodes[0].Title,
			Description: drama.Episodes[0].Description,
		}},
	})
	if err != nil || updated.PublishStatus != "draft" || !strings.HasSuffix(updated.Title, " revised") {
		t.Fatalf("待送审内容未恢复为可编辑草稿：drama=%+v err=%v", updated, err)
	}
	invalidated, err := service.FindTikTokPublication(ctx, id)
	if err != nil || invalidated.Status != operations.PublicationStatusSynced || invalidated.ContentSnapshot.Title == updated.Title {
		t.Fatal("草稿修改覆盖已同步版本", err)
	}
	next, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{})
	if err != nil || next.ID == invalidated.ID {
		t.Fatalf("修改后无法创建新的同步任务：next=%+v err=%v", next, err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusSynced)
	if calls := provider.callSnapshot(); !slices.Equal(calls, []string{"update"}) {
		t.Fatalf("仅修改元数据时发生了多余媒资处理：%v", calls)
	}
}

// TestChangedEpisodeOnlyProcessesChangedVideo 验证替换单集视频时复用封面及其他媒资，只处理新 VID。
func TestChangedEpisodeOnlyProcessesChangedVideo(t *testing.T) {
	ctx := context.Background()
	provider := &publicationProviderStub{}
	service, store, id := publicationFixture(t, provider)
	if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusSynced)
	drama, err := service.FindDrama(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateDrama(ctx, id, operations.DramaInput{
		Title: drama.Title, Description: drama.Description + " revised", CoverURL: drama.CoverURL,
		Status: drama.Status, Year: drama.Year, Language: drama.Language, DramaType: drama.DramaType,
		TagIDs: append([]string(nil), drama.TagIDs...), Episodes: []operations.EpisodeInput{{
			ID: drama.Episodes[0].ID, Number: drama.Episodes[0].Number, Title: drama.Episodes[0].Title,
			Description: drama.Episodes[0].Description,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	episode := updated.Episodes[0]
	episode.VID, episode.MediaStatus = "vid-2", "ready"
	if err := store.SaveEpisodeMedia(ctx, episode, *updated, "vid-1", ""); err != nil {
		t.Fatal(err)
	}
	provider.resetCalls()
	if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusSynced)
	want := []string{"video-register", "video-query", "transcode-start", "transcode-query", "update"}
	if calls := provider.callSnapshot(); !slices.Equal(calls, want) {
		t.Fatalf("单集替换执行了多余的官方处理：got=%v want=%v", calls, want)
	}
}

// TestAutomaticReviewRefresh 验证到期审核任务由 Worker 自动查询并停在人工确认上线节点。
func TestAutomaticReviewRefresh(t *testing.T) {
	ctx := context.Background()
	provider := &publicationProviderStub{reviewStatus: 1}
	service, store, id := publicationFixture(t, provider, 0, 50*time.Millisecond)
	if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	provider.mu.Lock()
	provider.reviewStatus = 2
	provider.mu.Unlock()
	p, err := store.FindTikTokPublicationByDramaID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p.NextAttemptAt = time.Now().UTC()
	if err := store.UpdateTikTokPublication(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusApproved)
	if !slices.Contains(provider.callSnapshot(), "query") {
		t.Fatal("审核到期后没有自动查询官方结果")
	}
}

// TestPublishRecoversWhenOfficialStateAlreadyCompleted 验证上线写接口返回错误时，
// 只要查询确认目标版本和上架状态已经生效，就按幂等成功完成本地状态收敛。
func TestPublishRecoversWhenOfficialStateAlreadyCompleted(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*publicationProviderStub)
	}{
		{
			name: "设置线上版本已生效",
			configure: func(provider *publicationProviderStub) {
				provider.onlineVersionFailure = &operations.PlatformCallError{Code: "api_rejected"}
			},
		},
		{
			name: "Album 已经上架",
			configure: func(provider *publicationProviderStub) {
				provider.listFailure = &operations.PlatformCallError{Code: "api_rejected"}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			defer slog.SetDefault(previous)
			provider := &publicationProviderStub{confirmOfficialFailure: true}
			test.configure(provider)
			service, _, id := publicationFixture(t, provider)
			if _, err := submitPreparedPublication(t, service, context.Background(), id, operations.TikTokPublicationInput{}); err != nil {
				t.Fatal(err)
			}
			manuallyApproveAndPublish(t, service, id)
			publication, err := service.FindTikTokPublication(context.Background(), id)
			if err != nil || publication.Status != operations.PublicationStatusOnline || publication.Stage != operations.PublicationStageDone || publication.ErrorCode != "" {
				t.Fatalf("官方已完成时本地状态没有收敛：%+v %v", publication, err)
			}
			output := logs.String()
			if !strings.Contains(output, "official state is confirmed") || !strings.Contains(output, "TikTok platform call failed: api_rejected") || !strings.Contains(output, `"level":"WARN"`) {
				t.Fatalf("幂等恢复缺少操作、级别或实际错误：%s", output)
			}
			if strings.Contains(output, `"level":"ERROR"`) || strings.Contains(output, "TEST_ONLY_SECRET") || strings.Contains(output, "https://") {
				t.Fatalf("幂等成功误报故障或泄漏敏感信息：%s", output)
			}
		})
	}
}

// TestLegacyQueuedPublishPause 验证旧部署留下的上线任务不会绕过人工确认门禁。
func TestLegacyQueuedPublishPause(t *testing.T) {
	for _, stage := range []string{operations.PublicationStageSetOnlineVersion, operations.PublicationStageListAlbum} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			provider := &publicationProviderStub{}
			service, store, id := publicationFixture(t, provider)
			if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
			p, err := store.FindTikTokPublicationByDramaID(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			p.Status, p.Stage, p.ReviewStatus = operations.PublicationStatusQueued, stage, 2
			p.PublishRequested = false
			p.NextAttemptAt = time.Now().UTC()
			if err := store.UpdateTikTokPublication(ctx, p, nil); err != nil {
				t.Fatal(err)
			}
			future := time.Now().UTC().Add(time.Hour)
			claimed, err := store.ClaimTikTokPublication(ctx, "unexpected", future, future.Add(time.Minute))
			if err != nil || claimed != nil {
				t.Fatal("旧同步步骤仍由 Worker 自动执行", claimed, err)
			}
			for _, call := range provider.callSnapshot() {
				if call == "query" || call == "online-version" || call == "list" {
					t.Fatalf("旧任务自动调用了%s", call)
				}
			}
		})
	}
}

// manualReviewProvider 只在人工查询入口注入故障或延迟，其他官方步骤保持本地替身。
type manualReviewProvider struct {
	publicationProviderStub
	failure error
	started chan struct{}
	release chan struct{}
}

// QueryAlbum 返回一次可控的官方观察，用于验证失败不改状态与并发旧结果隔离。
func (p *manualReviewProvider) QueryAlbum(ctx context.Context, albumID string, version int) (operations.AlbumObservation, error) {
	if p.started != nil {
		close(p.started)
		<-p.release
	}
	if p.failure != nil {
		return operations.AlbumObservation{}, p.failure
	}
	return p.publicationProviderStub.QueryAlbum(ctx, albumID, version)
}

// TestManualReviewFailurePreservesStateAndLogs 验证查询失败保留审核事实并记录可定位错误。
func TestManualReviewFailurePreservesStateAndLogs(t *testing.T) {
	ctx := context.Background()
	provider := &manualReviewProvider{failure: errors.New("review connection refused")}
	service, store, id := publicationFixture(t, provider)
	if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	before, err := store.FindTikTokPublicationByDramaID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(old)
	if _, err := service.RefreshTikTokReview(ctx, id); !errors.Is(err, provider.failure) {
		t.Fatalf("实际故障未返回：%v", err)
	}
	after, err := store.FindTikTokPublicationByDramaID(ctx, id)
	if err != nil || before.Revision != after.Revision || after.Status != operations.PublicationStatusUnderReview {
		t.Fatalf("失败更改了审核事实：%v", err)
	}
	logs := output.String()
	if !strings.Contains(logs, "query TikTok review manually failed") || !strings.Contains(logs, provider.failure.Error()) || !strings.Contains(logs, `"level":"ERROR"`) {
		t.Fatalf("缺少故障诊断：%s", logs)
	}
	if strings.Contains(logs, "TEST_ONLY_SECRET") || strings.Contains(logs, "https://") || strings.Contains(logs, "key-owner") {
		t.Fatal("日志泄漏平台凭据或媒体地址")
	}
}

// TestManualReviewCannotOverwriteConcurrentPublish 验证查询中的旧结果不能将人工上线恢复为待上线。
func TestManualReviewCannotOverwriteConcurrentPublish(t *testing.T) {
	ctx := context.Background()
	provider := &manualReviewProvider{}
	service, _, id := publicationFixture(t, provider)
	if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	p, err := service.RefreshTikTokReview(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	provider.started, provider.release = make(chan struct{}), make(chan struct{})
	defer close(provider.release)
	result := make(chan error, 1)
	go func() { _, err := service.RefreshTikTokReview(ctx, id); result <- err }()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("手动查询未开始")
	}
	if _, err := service.PublishTikTokPublication(ctx, id, p.ID); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusOnline)
	provider.release <- struct{}{}
	select {
	case err := <-result:
		if !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("旧结果覆盖了新修订：%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("并发查询未结束")
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusOnline)
}

// TestHistoricalPublicationActions 验证旧记录无法查询或改变新任务，且拒绝分支保留诊断。
func TestHistoricalPublicationActions(t *testing.T) {
	provider := &publicationProviderStub{reviewStatus: 1}
	service, _, id := publicationFixture(t, provider)
	if _, err := submitPreparedPublication(t, service, context.Background(), id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	ctx := operations.WithExpectedPublication(context.Background(), "obsolete-record")
	if _, err := service.RefreshTikTokReview(ctx, id); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("旧审核记录未拒绝: %v", err)
	}
	if _, err := service.RetryTikTokPublication(ctx, id); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("旧重试未拒绝: %v", err)
	}
	if !strings.Contains(logs.String(), "historical publication action rejected") {
		t.Fatal("拒绝分支缺少日志")
	}
	if slices.Contains(provider.callSnapshot(), "query") {
		t.Fatal("历史操作触发平台调用")
	}
}
