package operations_test

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
	"errors"
	"maps"
	"reflect"
	"testing"
	"time"
)

// reviewProviderStub 只模拟官方审核事实，不访问真实平台。
type reviewProviderStub struct {
	publicationProviderStub
	afterAppeal   int
	appealError   error
	appealCalls   int
	withdrawCalls int
	// withdrawError 模拟撤回请求的明确拒绝或响应丢失。
	withdrawError error
	// afterWithdrawError 模拟撤回返回错误时，平台已更新为指定状态。
	afterWithdrawError int
	// queryStatuses 按次序模拟平台状态可见性延迟，耗尽后返回当前真实状态。
	queryStatuses []int
	// queryErrorAfterWithdraw 模拟撤回错误发生后，查询平台结果也不可用。
	queryErrorAfterWithdraw error
	// itemState 非零时模拟平台单项申诉状态，包括短暂保留旧驳回结果。
	itemState int
	// reviewItems 模拟平台只返回实际审核项目，包括没有剧目信息结果的分集驳回。
	reviewItems map[string]int
	// lastAppeal 只核对实际提交对象，不记录申诉理由或证据地址。
	lastAppeal operations.TikTokReviewAppealInput
	// reviewPriority 记录实际提交给官方接口的优先级，验证加急重提的参数。
	reviewPriority int
}

// SubmitReview 记录送审优先级；撤回后重新受理时恢复审核中状态。
func (p *reviewProviderStub) SubmitReview(ctx context.Context, albumID string, version, priority int) (string, error) {
	p.mu.Lock()
	p.reviewPriority = priority
	if p.reviewStatus == 4 {
		p.reviewStatus = 1
	}
	p.mu.Unlock()
	return p.publicationProviderStub.SubmitReview(ctx, albumID, version, priority)
}

func (p *reviewProviderStub) QueryAlbum(context.Context, string, int) (operations.AlbumObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.withdrawCalls > 0 && p.queryErrorAfterWithdraw != nil {
		return operations.AlbumObservation{}, p.queryErrorAfterWithdraw
	}
	state := 3
	if p.appealCalls > 0 {
		state = 6
	}
	if p.itemState != 0 {
		state = p.itemState
	}
	status := p.reviewStatus
	if len(p.queryStatuses) > 0 {
		status, p.queryStatuses = p.queryStatuses[0], p.queryStatuses[1:]
	}
	items := map[string]int{"7637420375425239060": state}
	if p.reviewItems != nil {
		items = maps.Clone(p.reviewItems)
	}
	return operations.AlbumObservation{ReviewStatus: status, ReviewItems: items}, nil
}

// TestMissingAppealTargetIsRejectedBeforeQueue 验证有单项结果时，不能把未返回的剧目或分集排入申诉队列。
func TestMissingAppealTargetIsRejectedBeforeQueue(t *testing.T) {
	for _, albumTarget := range []bool{true, false} {
		t.Run(map[bool]string{true: "剧目没有单项结果", false: "分集没有单项结果"}[albumTarget], func(t *testing.T) {
			ctx := context.Background()
			provider := &reviewProviderStub{publicationProviderStub: publicationProviderStub{reviewStatus: 3}}
			service, id := startReviewFixture(t, provider)
			waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
			p, err := service.FindTikTokPublication(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			episode := p.EpisodeIDsBySequence[1]
			input := operations.TikTokReviewAppealInput{ItemType: 1, ItemID: p.PlatformAlbumID, Reason: "Please review"}
			items := map[string]int{episode: 3}
			if !albumTarget {
				items = map[string]int{p.PlatformAlbumID: 3}
				input.ItemType, input.ItemID = 2, episode
			}
			provider.mu.Lock()
			provider.reviewItems = items
			provider.mu.Unlock()
			if _, err := service.RefreshTikTokReview(ctx, id); err != nil {
				t.Fatal(err)
			}
			if _, err := service.AppealTikTokReview(ctx, id, input); !errors.Is(err, operations.ErrConflict) {
				t.Fatalf("未返回的项目仍可排入申诉队列: %v", err)
			}
			current, err := service.FindTikTokPublication(ctx, id)
			if err != nil || current.Status != operations.PublicationStatusRejected || current.Appeal != nil {
				t.Fatalf("拒绝排队改变了待处理审核: %+v %v", current, err)
			}
			provider.mu.Lock()
			defer provider.mu.Unlock()
			if provider.appealCalls != 0 {
				t.Fatal("资格不明确仍调用平台申诉")
			}
		})
	}
}

// TestFailedAppealWithoutEligibleTargetRestoresReview 验证旧任务的错误对象不会无限重试，恢复后可改选真正被拒的剧集。
func TestFailedAppealWithoutEligibleTargetRestoresReview(t *testing.T) {
	for _, test := range []struct {
		name, status, stage, publishStatus string
		overall, target                    int
	}{
		{"只有剧集被拒", operations.PublicationStatusRejected, operations.PublicationStagePollReview, "rejected", 3, -1},
		{"目标已通过但其他项目被拒", operations.PublicationStatusRejected, operations.PublicationStagePollReview, "rejected", 3, 2},
		{"整版已通过", operations.PublicationStatusApproved, operations.PublicationStageAwaitPublish, "publishing", 2, 2},
		{"审核已撤回", operations.PublicationStatusRejected, operations.PublicationStageDone, "draft", 4, 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			provider := &reviewProviderStub{publicationProviderStub: publicationProviderStub{reviewStatus: 3}, afterAppeal: 3}
			service, store, id := publicationFixture(t, provider)
			if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
			p, err := store.FindTikTokPublicationByDramaID(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			episode := p.EpisodeIDsBySequence[1]
			items := map[string]int{episode: 3}
			if test.target >= 0 {
				items[p.PlatformAlbumID] = test.target
			}
			provider.mu.Lock()
			provider.reviewItems, provider.reviewStatus = items, test.overall
			provider.mu.Unlock()
			p.Status, p.Stage, p.ErrorCode = operations.PublicationStatusFailed, operations.PublicationStageAppealReview, "review_action_conflict"
			p.Appeal = &operations.TikTokReviewAppealInput{ItemType: 1, ItemID: p.PlatformAlbumID, Reason: "Please review"}
			if err := store.UpdateTikTokPublication(ctx, p, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := service.RetryTikTokPublication(ctx, id); err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, id, test.status)
			current, err := service.FindTikTokPublication(ctx, id)
			if err != nil || current.Stage != test.stage || current.ExternalWriteStarted || current.PublishRequested || !reflect.DeepEqual(current.ReviewItems, items) {
				t.Fatalf("申诉恢复丢失官方状态或仍卡在失败阶段: %+v %v", current, err)
			}
			drama, err := service.FindDrama(ctx, id)
			if err != nil || drama.PublishStatus != "ready" || drama.Published != nil {
				t.Fatalf("恢复改变编辑或人工上线门禁: %+v %v", drama, err)
			}
			provider.mu.Lock()
			calls := provider.appealCalls
			provider.mu.Unlock()
			if calls != 0 {
				t.Fatal("恢复过程重放了不符合资格的申诉")
			}
			if test.overall == 3 {
				if _, err := service.AppealTikTokReview(ctx, id, *p.Appeal); !errors.Is(err, operations.ErrConflict) {
					t.Fatalf("恢复后仍允许申诉剧目信息: %v", err)
				}
				if _, err := service.AppealTikTokReview(ctx, id, operations.TikTokReviewAppealInput{ItemType: 2, ItemID: episode, Reason: "Please review episode"}); err != nil {
					t.Fatal(err)
				}
				waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
				provider.mu.Lock()
				defer provider.mu.Unlock()
				if provider.appealCalls != 1 || provider.lastAppeal.ItemType != 2 || provider.lastAppeal.ItemID != episode {
					t.Fatalf("没有向真正被拒的剧集提交一次申诉: calls=%d type=%d id=%s", provider.appealCalls, provider.lastAppeal.ItemType, provider.lastAppeal.ItemID)
				}
			}
		})
	}
}

// TestDuplicateAppealResumesQueries 验证重复申诉回复进入查询，旧驳回结果不会触发重发或提前上线。
func TestDuplicateAppealResumesQueries(t *testing.T) {
	ctx := context.Background()
	provider := &reviewProviderStub{publicationProviderStub: publicationProviderStub{reviewStatus: 3}, itemState: 3,
		appealError: &operations.PlatformCallError{Code: "appeal_already_submitted"}}
	service, id := startReviewFixture(t, provider)
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	if _, err := service.RefreshTikTokReview(ctx, id); err != nil {
		t.Fatal(err)
	}
	input := operations.TikTokReviewAppealInput{ItemType: 1, ItemID: "7637420375425239060", Reason: "Please review"}
	if _, err := service.AppealTikTokReview(ctx, id, input); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	for i := 0; i < 2; i++ {
		p, err := service.RefreshTikTokReview(ctx, id)
		if err != nil || p.Stage != operations.PublicationStagePollAppeal || p.Status != operations.PublicationStatusUnderReview ||
			p.ExternalWriteStarted || p.ErrorCode != "" || p.NextAttemptAt.IsZero() {
			t.Fatalf("重复申诉没有转为持续查询: %+v %v", p, err)
		}
	}
	provider.mu.Lock()
	provider.itemState = 7
	provider.mu.Unlock()
	p, err := service.RefreshTikTokReview(ctx, id)
	if err != nil || p.Status != operations.PublicationStatusRejected {
		t.Fatalf("申诉被拒未回到修改内容流程: %+v %v", p, err)
	}
	if _, err := service.AppealTikTokReview(ctx, id, input); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("申诉失败仍可再次申诉: %v", err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.appealCalls != 1 {
		t.Fatalf("恢复查询时重复提交申诉: %d", provider.appealCalls)
	}
}

// TestFailedAppealRetryUsesOfficialState 验证旧失败任务重试时复用官方申诉状态，并保留整版审核门禁。
func TestFailedAppealRetryUsesOfficialState(t *testing.T) {
	for _, item := range []struct {
		name           string
		state, overall int
		expected       string
	}{
		{"申诉中", 5, 3, operations.PublicationStatusUnderReview},
		{"单项通过但其他项目被拒", 6, 3, operations.PublicationStatusRejected},
		{"申诉被拒", 7, 3, operations.PublicationStatusRejected},
		{"整版通过", 6, 2, operations.PublicationStatusApproved},
	} {
		t.Run(item.name, func(t *testing.T) {
			ctx := context.Background()
			provider := &reviewProviderStub{publicationProviderStub: publicationProviderStub{reviewStatus: item.overall}, itemState: item.state}
			service, store, id := publicationFixture(t, provider)
			if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
			p, err := store.FindTikTokPublicationByDramaID(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			p.Status, p.Stage, p.ErrorCode = operations.PublicationStatusFailed, operations.PublicationStageAppealReview, "api_rejected"
			p.Appeal = &operations.TikTokReviewAppealInput{ItemType: 1, ItemID: p.PlatformAlbumID, Reason: "Please review"}
			if err := store.UpdateTikTokPublication(ctx, p, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := service.RetryTikTokPublication(ctx, id); err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, id, item.expected)
			result, err := service.FindTikTokPublication(ctx, id)
			if err != nil || result.ExternalWriteStarted || result.ReviewItems[p.PlatformAlbumID] != item.state {
				t.Fatalf("未恢复官方申诉结果: %+v %v", result, err)
			}
			provider.mu.Lock()
			defer provider.mu.Unlock()
			if provider.appealCalls != 0 {
				t.Fatalf("官方已申诉仍重复提交: %d", provider.appealCalls)
			}
		})
	}
}
func (p *reviewProviderStub) AppealReview(_ context.Context, _, _ string, input operations.TikTokReviewAppealInput) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.appealCalls++
	p.lastAppeal = operations.TikTokReviewAppealInput{ItemType: input.ItemType, ItemID: input.ItemID}
	if p.appealError != nil {
		return p.appealError
	}
	p.reviewStatus = p.afterAppeal
	return nil
}
func (p *reviewProviderStub) WithdrawReview(context.Context, string, int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.withdrawCalls++
	if p.withdrawError != nil {
		if p.afterWithdrawError != 0 {
			p.reviewStatus = p.afterWithdrawError
		}
		return p.withdrawError
	}
	p.reviewStatus = 4
	return nil
}

func startReviewFixture(t *testing.T, provider *reviewProviderStub) (*operations.Service, string) {
	t.Helper()
	store := memory.NewStore(memory.Seed{Applications: []application.Application{{TikTokAppID: "app-owner", Name: "Owner", ClientKey: "mn-owner", SecretRef: "OWNER_SECRET", BytePlusAccountID: "123456789", BytePlusSpaceName: "short-drama", Status: application.StatusActive}}})
	service := operations.NewService(memory.OperationsRepositories(store), &publicationMediaProviderStub{})
	if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, operations.PublicationJobConfig{PollInterval: time.Millisecond, MediaPollInterval: 30 * time.Millisecond, RetryBaseDelay: time.Millisecond, LeaseDuration: time.Second, WorkerID: "review-test"}); err != nil {
		t.Fatal(err)
	}
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{Title: "Review drama", Description: "Review synopsis", CoverURL: "https://assets.example/cover.png", TagIDs: []string{"1"}, Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode"}}})
	if err != nil {
		t.Fatal(err)
	}
	episode := drama.Episodes[0]
	episode.VID, episode.MediaStatus = "vid", "ready"
	if err := store.SaveEpisodeMedia(context.Background(), episode, *drama, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := submitPreparedPublication(t, service, context.Background(), drama.ID, operations.TikTokPublicationInput{Priority: "normal"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := service.StartTikTokPublicationWorker(ctx); err != nil {
		t.Fatal(err)
	}
	return service, drama.ID
}

func TestReviewAppealRunsOnceAndRequiresWholeVersionApproval(t *testing.T) {
	for _, item := range []struct {
		name     string
		after    int
		failure  error
		expected string
	}{
		{"整版审核通过后等待手动上线", 2, nil, operations.PublicationStatusApproved},
		{"another item still rejected", 3, nil, operations.PublicationStatusRejected},
		{"unknown outcome cannot replay", 3, &operations.PlatformCallError{Code: "network_error", Retryable: true, OutcomeUnknown: true}, operations.PublicationStatusFailed},
	} {
		t.Run(item.name, func(t *testing.T) {
			provider := &reviewProviderStub{publicationProviderStub: publicationProviderStub{reviewStatus: 3}, afterAppeal: item.after, appealError: item.failure}
			service, id := startReviewFixture(t, provider)
			waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
			if _, err := service.RefreshTikTokReview(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, id, operations.PublicationStatusRejected)
			input := operations.TikTokReviewAppealInput{ItemType: 1, ItemID: "7637420375425239060", Reason: "Please review", AttachmentURLs: []string{"https://assets.example/evidence.png"}}
			wrong := input
			wrong.ItemID = "999"
			if _, err := service.AppealTikTokReview(context.Background(), id, wrong); !errors.Is(err, operations.ErrConflict) {
				t.Fatalf("cross album allowed: %v", err)
			}
			if _, err := service.AppealTikTokReview(context.Background(), id, input); (err != nil) != (item.failure != nil) {
				t.Fatal("同步申诉错误传播不符", err)
			}
			if item.failure == nil {
				waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
				if _, err := service.RefreshTikTokReview(context.Background(), id); err != nil {
					t.Fatal(err)
				}
			}
			waitForPublicationStatus(t, service, id, item.expected)
			provider.mu.Lock()
			count := provider.appealCalls
			provider.mu.Unlock()
			if count != 1 {
				t.Fatalf("appeal called %d times", count)
			}
			p, err := service.FindTikTokPublication(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if p.Appeal == nil || p.Appeal.Reason != input.Reason {
				t.Fatal("appeal payload lost")
			}
			if item.failure != nil {
				if p.ErrorCode != "external_write_uncertain" {
					t.Fatalf("lost uncertainty: %+v", p)
				}
				if _, err := service.RetryTikTokPublication(context.Background(), id); !errors.Is(err, operations.ErrConflict) {
					t.Fatalf("unsafe replay: %v", err)
				}
			}
		})
	}
}

func TestWithdrawReviewReturnsDraftOnlyAfterOfficialSuccess(t *testing.T) {
	provider := &reviewProviderStub{publicationProviderStub: publicationProviderStub{reviewStatus: 1}}
	service, id := startReviewFixture(t, provider)
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	if _, err := service.WithdrawTikTokReview(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusRejected)
	p, err := service.FindTikTokPublication(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if p.ReviewStatus != 4 || p.ErrorCode != "review_withdrawn" {
		t.Fatalf("not withdrawn: %+v", p)
	}
	drama, err := service.FindDrama(context.Background(), id)
	if err != nil || drama.PublishStatus != "ready" {
		t.Fatalf("not editable: %v", err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.withdrawCalls != 1 {
		t.Fatal("withdraw repeated")
	}
}

// TestReviewRejectionRequiresContentSaveBeforeResubmit 验证驳回状态只提供修改或申诉，保存内容后才恢复为可同步草稿。
func TestReviewRejectionRequiresContentSaveBeforeResubmit(t *testing.T) {
	provider := &reviewProviderStub{publicationProviderStub: publicationProviderStub{reviewStatus: 3}}
	service, id := startReviewFixture(t, provider)
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	if _, err := service.RefreshTikTokReview(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusRejected)
	drama, err := service.FindDrama(context.Background(), id)
	if err != nil || drama.PublishStatus != "ready" {
		t.Fatalf("审核驳回未进入待处理状态: drama=%+v err=%v", drama, err)
	}
	if _, err := service.UpdateDrama(context.Background(), id, operations.DramaInput{
		Title: drama.Title + " revised", Description: drama.Description, CoverURL: drama.CoverURL,
		Status: drama.Status, Year: drama.Year, Language: drama.Language, DramaType: drama.DramaType,
		TagIDs: append([]string(nil), drama.TagIDs...), Episodes: []operations.EpisodeInput{{
			ID: drama.Episodes[0].ID, Number: drama.Episodes[0].Number, Title: drama.Episodes[0].Title,
			Description: drama.Episodes[0].Description,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	drama, err = service.FindDrama(context.Background(), id)
	if err != nil || drama.PublishStatus != "draft" {
		t.Fatalf("保存修改后未恢复草稿状态: drama=%+v err=%v", drama, err)
	}
}

func TestWithdrawnReviewCanResubmitUnchangedContent(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "主动撤回后加急重提"
		if manual {
			name = "查询得知已撤回后加急重提"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			provider := &reviewProviderStub{publicationProviderStub: publicationProviderStub{reviewStatus: 1}}
			service, id := startReviewFixture(t, provider)
			waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
			if manual {
				provider.mu.Lock()
				provider.reviewStatus = 4
				provider.mu.Unlock()
				if _, err := service.RefreshTikTokReview(ctx, id); err != nil {
					t.Fatal(err)
				}
			} else if _, err := service.WithdrawTikTokReview(ctx, id); err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, id, operations.PublicationStatusRejected)
			withdrawn, err := service.FindTikTokPublication(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if withdrawn.ReviewStatus != 4 || withdrawn.ErrorMessage != "本次 TikTok 审核已撤回，可保留现有内容并选择审核优先级重新提交。" {
				t.Fatal("撤回状态应明确允许保留原内容重提")
			}
			unchanged, err := service.FindDrama(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			// 不调用修改或保存草稿接口，直接以原内容和新的优先级发起任务。
			next, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{Priority: "urgent"})
			if err != nil {
				t.Fatalf("内容未变化时重新送审失败：%v", err)
			}
			if next.ID == withdrawn.ID || next.PlatformAlbumID != withdrawn.PlatformAlbumID || next.ContentSnapshot == nil {
				t.Fatal("重提应创建新任务、复用官方剧目并保留原内容")
			}
			// 详情接口额外投影标签名称，只比较运营保存的字段与分集，避免把展示投影当成内容变更。
			frozen := next.ContentSnapshot
			for i := range unchanged.Episodes {
				unchanged.Episodes[i].PlatformEpisodeID = frozen.Episodes[i].PlatformEpisodeID
			}
			if frozen.Title != unchanged.Title || frozen.Description != unchanged.Description || frozen.CoverURL != unchanged.CoverURL ||
				frozen.Status != unchanged.Status || frozen.Year != unchanged.Year || frozen.Language != unchanged.Language ||
				frozen.DramaType != unchanged.DramaType || !reflect.DeepEqual(frozen.TagIDs, unchanged.TagIDs) || !reflect.DeepEqual(frozen.Episodes, unchanged.Episodes) {
				t.Fatal("重新送审改变了剧目内容或分集")
			}
			waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
			current, err := service.RefreshTikTokReview(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			provider.mu.Lock()
			priority := provider.reviewPriority
			provider.mu.Unlock()
			if current.Priority != "urgent" || priority != 1 || current.ReviewStatus != 1 || current.PublishRequested {
				t.Fatal("重新送审应使用官方加急参数并停留在待审核状态")
			}
			page, err := service.ListDramaPage(ctx, operations.DramaListQuery{Page: 1, PageSize: 20})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 1 || page.Items[0].Publication == nil || page.Items[0].Publication.Priority != "urgent" || page.Items[0].Publication.Status != operations.PublicationStatusUnderReview || page.Items[0].Publication.Stage != operations.PublicationStagePollReview || page.Items[0].Publication.ReviewStatus != 1 {
				t.Fatalf("列表未返回最新的加急审核状态：%+v", page.Items)
			}
		})
	}
}
