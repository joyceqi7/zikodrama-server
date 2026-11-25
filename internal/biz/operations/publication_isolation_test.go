package operations_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

// uncertainAlbumProvider 模拟官方已经接收创建请求，但响应在网络中丢失。
type uncertainAlbumProvider struct{ publicationProviderStub }

func (p *uncertainAlbumProvider) CreateAlbum(context.Context) (operations.CreateAlbumResult, error) {
	p.record("uncertain-create")
	return operations.CreateAlbumResult{}, &operations.PlatformCallError{Code: "network_error", Retryable: true, OutcomeUnknown: true}
}

// TestUncertainWriteIsNeverBlindlyReplayed 确认网络与进程未知结果不会重复创建剧目。
func TestUncertainWriteIsNeverBlindlyReplayed(t *testing.T) {
	provider := &uncertainAlbumProvider{}
	service, _, id := publicationFixture(t, provider)
	if _, err := submitPreparedPublication(t, service, context.Background(), id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusFailed)
	result, err := service.FindTikTokPublication(context.Background(), id)
	if err != nil || result.ErrorCode != "external_write_uncertain" || result.DiagnosticCode != "network_error" || !result.ExternalWriteStarted {
		t.Fatalf("未保留未知结果：%+v %v", result, err)
	}
	if _, err := service.RetryTikTokPublication(context.Background(), id); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("允许盲目重放：%v", err)
	}
	if _, err := submitPreparedPublication(t, service, context.Background(), id, operations.TikTokPublicationInput{}); err == nil {
		t.Fatal("允许新任务绕过未知结果检查点")
	}
	calls := provider.callSnapshot()
	count := 0
	for _, call := range calls {
		if call == "uncertain-create" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("重复创建：%v", calls)
	}
}

// publicationFixture 提供纯本地的发布业务闭环，官方协议由独立 HTTP 合同测试验证。
func publicationFixture(t *testing.T, provider operations.TikTokMediaProvider, stallTimeout ...time.Duration) (*operations.Service, *memory.Store, string) {
	t.Helper()
	store := memory.NewStore(memory.Seed{Applications: []application.Application{
		{TikTokAppID: "owner", ClientKey: "key-owner", SecretRef: "TEST_ONLY_SECRET", BytePlusAccountID: "123", BytePlusSpaceName: "drama", Status: application.StatusActive},
		{TikTokAppID: "target-a", ClientKey: "key-a", Status: application.StatusActive},
		{TikTokAppID: "target-b", ClientKey: "key-b", Status: application.StatusActive},
	}})
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	config := operations.PublicationJobConfig{PollInterval: time.Millisecond, MediaPollInterval: time.Millisecond, ReviewPollInterval: time.Hour, RetryBaseDelay: time.Millisecond, LeaseDuration: time.Second, WorkerID: "fixture-worker"}
	if len(stallTimeout) > 0 {
		config.MediaStallTimeout = stallTimeout[0]
	}
	if len(stallTimeout) > 1 {
		config.ReviewPollInterval = stallTimeout[1]
	}
	if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, config); err != nil {
		t.Fatal(err)
	}
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{Title: "Original title", Description: "Original synopsis", CoverURL: "https://cdn.example.test/cover.png", Language: "en", Status: domain.DramaStatusCompleted, TagIDs: []string{"1"}, Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode 1"}}})
	if err != nil {
		t.Fatal(err)
	}
	episode := drama.Episodes[0]
	episode.VID, episode.MediaStatus = "vid-1", "ready"
	if err := store.SaveEpisodeMedia(context.Background(), episode, *drama, "", ""); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := service.StartTikTokPublicationWorker(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		wait, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = service.WaitTikTokPublicationWorker(wait)
	})
	return service, store, drama.ID
}

// TestOwnerNeedsNoAuthorizationAndReusesAlbum 验证纯英文无需外挂字幕、拥有方无需授权以及更新复用 Album。
func TestOwnerNeedsNoAuthorizationAndReusesAlbum(t *testing.T) {
	provider := &publicationProviderStub{}
	service, store, id := publicationFixture(t, provider)
	input := operations.TikTokPublicationInput{}
	if _, err := submitPreparedPublication(t, service, context.Background(), id, input); err != nil {
		t.Fatal(err)
	}
	manuallyApproveAndPublish(t, service, id)
	first, err := service.FindTikTokPublication(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(provider.callSnapshot(), "authorize") {
		t.Fatal("拥有方不应调用授权接口")
	}
	draft, err := store.FindDrama(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := draft.PublishedForApplication("owner"); !ok {
		t.Fatal("拥有方无法播放")
	}
	_, err = service.UpdateDrama(context.Background(), id, operations.DramaInput{Title: "Rejected new title", Description: draft.Description, CoverURL: draft.CoverURL, Language: "en", Status: domain.DramaStatusCompleted, TagIDs: draft.TagIDs,
		Episodes: []operations.EpisodeInput{{ID: draft.Episodes[0].ID, Number: 1, Title: "Episode 1"}}})
	if err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.reviewStatus = 3
	provider.mu.Unlock()
	if _, err := submitPreparedPublication(t, service, context.Background(), id, input); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	if _, err := service.RefreshTikTokReview(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusRejected)
	second, err := service.FindTikTokPublication(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if first.PlatformAlbumID != second.PlatformAlbumID || second.PlatformVersion <= first.PlatformVersion {
		t.Fatalf("未复用官方 Album/version：first=%+v second=%+v", first, second)
	}
	creates := 0
	for _, call := range provider.callSnapshot() {
		if call == "create" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("更新重复创建了 %d 个 Album", creates)
	}
	current, err := store.FindDrama(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	online, ok := current.PublishedForApplication("owner")
	if !ok || online.Title != "Original title" || current.Title != "Rejected new title" {
		t.Fatalf("审核失败污染线上：%+v", current)
	}
}

// TestPublicationDoesNotWaitForDistribution 发布结束后由独立任务授权，目标失败不能锁住内容编辑。
func TestPublicationDoesNotWaitForDistribution(t *testing.T) {
	provider := &publicationProviderStub{authorizationFailures: map[string]error{"key-a": operations.ErrConflict}}
	service, store, id := publicationFixture(t, provider)
	if _, err := submitPreparedPublication(t, service, context.Background(), id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	manuallyApproveAndPublish(t, service, id)
	drama, err := store.FindDrama(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := drama.PublishedForApplication("owner"); !ok {
		t.Fatal("拥有方上线不可见")
	}
	if _, ok := drama.PublishedForApplication("target-a"); ok {
		t.Fatal("未经独立授权不得可见")
	}
	for _, call := range provider.callSnapshot() {
		if call == "authorize" {
			t.Fatal("发布任务不应调用授权")
		}
	}
}

// wrongTranscodeProvider 模拟任务显示成功，但平台返回了其他视频的结果。
type wrongTranscodeProvider struct{ publicationProviderStub }

// QueryTranscode 有意替换视频身份，用于验证发布门禁。
func (provider *wrongTranscodeProvider) QueryTranscode(ctx context.Context, id, account string) (operations.TikTokTranscodeObservation, error) {
	result, err := provider.Capabilities.QueryTranscode(ctx, id, account)
	result.SuccessVIDs = []string{"another-video"}
	return result, err
}

func TestTranscodeSuccessCannotSubstituteAnotherVideo(t *testing.T) {
	provider := &wrongTranscodeProvider{}
	service, store, id := publicationFixture(t, provider)
	if _, err := submitPreparedPublication(t, service, context.Background(), id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusFailed)
	if slices.Contains(provider.callSnapshot(), "review") {
		t.Fatal("视频集合不匹配仍然送审")
	}
	drama, err := store.FindDrama(context.Background(), id)
	if err != nil || drama.Published != nil {
		t.Fatalf("错误视频任务变成线上内容：%+v %v", drama, err)
	}
}

// failedMediaOnceProvider 的首个官方任务永久失败，只有新任务才能成功。
type failedMediaOnceProvider struct {
	publicationProviderStub
	failedTaskID string
}

// QueryTranscode 永久保留首个任务的失败结果，验证重试必须创建新任务而非反复查询。
func (p *failedMediaOnceProvider) QueryTranscode(ctx context.Context, taskID, account string) (operations.TikTokTranscodeObservation, error) {
	result, err := p.Capabilities.QueryTranscode(ctx, taskID, account)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failedTaskID == "" {
		p.failedTaskID = taskID
	}
	if taskID == p.failedTaskID {
		result.Status, result.FailedVIDs, result.SuccessVIDs = "FAILED", result.SuccessVIDs, nil
	}
	return result, err
}

func TestFailedTranscodeCanEditAndRestartWithNewTask(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "允许修正素材", true: "兼容旧失败锁"}[legacy], func(t *testing.T) {
			ctx := context.Background()
			provider := &failedMediaOnceProvider{}
			service, store, id := publicationFixture(t, provider)
			input := operations.TikTokPublicationInput{}
			if _, err := submitPreparedPublication(t, service, ctx, id, input); err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, id, operations.PublicationStatusFailed)
			first, _ := service.FindTikTokPublication(ctx, id)
			drama, _ := store.FindDrama(ctx, id)
			if drama.PublishStatus != "ready" {
				t.Fatalf("明确失败未解除编辑锁：%s", drama.PublishStatus)
			}
			if legacy {
				drama.PublishStatus = "publishing"
				if err := store.UpdateTikTokPublication(ctx, *first, drama); err != nil {
					t.Fatal(err)
				}
			} else {
				_, err := service.UpdateDrama(ctx, id, operations.DramaInput{Title: "修正后的内容", Description: drama.Description, CoverURL: drama.CoverURL, Language: "en", Status: domain.DramaStatusCompleted, TagIDs: drama.TagIDs, Episodes: []operations.EpisodeInput{{ID: drama.Episodes[0].ID, Number: 1, Title: "修正后的分集"}}})
				if err != nil {
					t.Fatal(err)
				}
			}
			var next *operations.TikTokPublication
			var err error
			if !legacy {
				_, prepareErr := service.PrepareDrama(ctx, id)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				next, err = service.SubmitTikTokPublication(ctx, id, operations.TikTokPublicationInput{})
			} else {
				next, err = service.RetryTikTokPublication(ctx, id)
			}
			if err != nil {
				t.Fatal(err)
			}
			if next.ID == first.ID {
				t.Fatal("重新送审复用了旧发布幂等键")
			}
			manuallyApproveAndPublish(t, service, id)
			next, _ = service.FindTikTokPublication(ctx, id)
			if next.Transcodes[0].TaskID == first.Transcodes[0].TaskID {
				t.Fatal("仍在查询失败终态的原转码任务")
			}
			drama, _ = store.FindDrama(ctx, id)
			if _, visible := drama.PublishedForApplication("target-a"); visible {
				t.Fatal("重新送审不能绕过独立分发任务授权")
			}
			if !legacy && drama.Published.Title != "修正后的内容" {
				t.Fatal("重新送审没有读取修正后的草稿")
			}
		})
	}
}

// rejectedVideoProvider 模拟查询明确拒绝登记，不能把未知状态当作失败终态处理。
type rejectedVideoProvider struct{ publicationProviderStub }

// QueryVideo 返回官方明确拒绝状态，验证编辑区可以恢复修正。
func (p *rejectedVideoProvider) QueryVideo(context.Context, string, string, string) (operations.TikTokVideoObservation, error) {
	return operations.TikTokVideoObservation{Status: 3}, nil
}
func TestRejectedVideoUnlocksDraftForCorrection(t *testing.T) {
	service, store, id := publicationFixture(t, &rejectedVideoProvider{})
	if _, err := submitPreparedPublication(t, service, context.Background(), id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusFailed)
	drama, _ := store.FindDrama(context.Background(), id)
	if drama.PublishStatus != "ready" {
		t.Fatal("官方已拒绝登记却无法修正素材")
	}
}
