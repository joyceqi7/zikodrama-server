package operations_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
	"drama-server/internal/testsupport/officialmedia"
)

type publicationProviderStub struct {
	officialmedia.Capabilities
	mu           sync.Mutex
	calls        []string
	reviewStatus int
	// authorizationFailures 为测试显式注入逐小程序失败，不影响其他目标。
	authorizationFailures    map[string]error
	authorizationStarted     chan struct{}
	authorizationRelease     chan struct{}
	authorizationStartedOnce sync.Once
	authorizationReleaseOnce sync.Once
	onlineVersionFailure     error
	listFailure              error
	confirmOfficialFailure   bool
	onlineVersionFailed      bool
	listFailed               bool
}

func (p *publicationProviderStub) CreateImage(context.Context, string) (string, error) {
	p.record("image")
	return "cover-platform-1", nil
}

// RegisterVideo 将官方视频步骤纳入顺序断言，避免只验证后半段审核流程。
func (p *publicationProviderStub) RegisterVideo(ctx context.Context, input operations.TikTokVideoInput) (operations.TikTokVideoRegistration, error) {
	p.record("video-register")
	return p.Capabilities.RegisterVideo(ctx, input)
}

// QueryVideo 必须先确认 VID，不能只提交登记就进入转码。
func (p *publicationProviderStub) QueryVideo(ctx context.Context, jobID, vid, accountID string) (operations.TikTokVideoObservation, error) {
	p.record("video-query")
	return p.Capabilities.QueryVideo(ctx, jobID, vid, accountID)
}

// StartTranscode 保留真实幂等任务替身并记录开始转码。
func (p *publicationProviderStub) StartTranscode(ctx context.Context, input operations.TikTokTranscodeInput) (string, error) {
	p.record("transcode-start")
	return p.Capabilities.StartTranscode(ctx, input)
}

// QueryTranscode 记录逐 VID 核验必须发生在创建或更新 Album 之前。
func (p *publicationProviderStub) QueryTranscode(ctx context.Context, taskID, accountID string) (operations.TikTokTranscodeObservation, error) {
	p.record("transcode-query")
	return p.Capabilities.QueryTranscode(ctx, taskID, accountID)
}

func (p *publicationProviderStub) CreateAlbum(context.Context) (operations.CreateAlbumResult, error) {
	p.record("create")
	return operations.CreateAlbumResult{AlbumID: "7637420375425239060"}, nil
}

func (p *publicationProviderStub) UpdateAlbum(_ context.Context, draft operations.AlbumDraft) (operations.UpdateAlbumResult, error) {
	p.record("update")
	mapping := make(map[int]string, len(draft.Episodes))
	for _, episode := range draft.Episodes {
		mapping[episode.Sequence] = "763743736130702747" + string(rune('0'+episode.Sequence))
	}
	return operations.UpdateAlbumResult{Version: draft.Version + 1, EpisodeIDsBySequence: mapping}, nil
}

func (p *publicationProviderStub) SubmitReview(context.Context, string, int, int) (string, error) {
	p.record("review")
	return "7637437361307027000", nil
}

func (p *publicationProviderStub) QueryAlbum(_ context.Context, _ string, version int) (operations.AlbumObservation, error) {
	p.record("query")
	p.mu.Lock()
	status := p.reviewStatus
	onlineVersionFailed, listFailed := p.onlineVersionFailed, p.listFailed
	confirmOfficialFailure := p.confirmOfficialFailure
	p.mu.Unlock()
	if status == 0 {
		status = 2
	}
	observation := operations.AlbumObservation{ReviewStatus: status}
	if confirmOfficialFailure && (onlineVersionFailed || listFailed) {
		observation.OnlineVersion = version
		if listFailed {
			observation.PublishStatus = 1
		}
	}
	return observation, nil
}

func (p *publicationProviderStub) AuthorizeAlbum(_ context.Context, _ string, targets []string) error {
	p.record("authorize")
	p.mu.Lock()
	var failure error
	for _, key := range targets {
		if p.authorizationFailures[key] != nil {
			failure = p.authorizationFailures[key]
		}
	}
	p.mu.Unlock()
	if p.authorizationStarted != nil {
		p.authorizationStartedOnce.Do(func() { close(p.authorizationStarted) })
	}
	if p.authorizationRelease != nil {
		<-p.authorizationRelease
	}
	return failure
}

// releaseAuthorization 允许测试在核对“已上线但未授权”状态后继续完成官方流程。
func (p *publicationProviderStub) releaseAuthorization() {
	if p.authorizationRelease == nil {
		return
	}
	p.authorizationReleaseOnce.Do(func() { close(p.authorizationRelease) })
}

func (p *publicationProviderStub) SetOnlineVersion(context.Context, string, int) error {
	p.record("online-version")
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.onlineVersionFailure != nil {
		p.onlineVersionFailed = true
	}
	return p.onlineVersionFailure
}

func (p *publicationProviderStub) SetAlbumListed(_ context.Context, _ string, listed bool) error {
	if listed {
		p.record("list")
	} else {
		p.record("delist")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if listed && p.listFailure != nil {
		p.listFailed = true
		return p.listFailure
	}
	return nil
}

type publicationMediaProviderStub struct {
	spaceName string
}

func (*publicationMediaProviderStub) Delete(context.Context, string, string, string, string) error {
	return nil
}

func (p *publicationProviderStub) record(call string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, call)
}

func (p *publicationProviderStub) callSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

// resetCalls 清空已记录的官方调用，供同一测试分段核对增量同步行为。
func (p *publicationProviderStub) resetCalls() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = nil
}

func TestTikTokPublicationRunsOfficialStagesBeforeConsumerPublication(t *testing.T) {
	store := memory.NewStore(memory.Seed{Applications: []application.Application{{
		TikTokAppID: "app-owner", Name: "Owner", ClientKey: "mn-owner", SecretRef: "OWNER_SECRET",
		BytePlusAccountID: "123456789", BytePlusSpaceName: "short-drama",
		Status: application.StatusActive,
	}, {
		TikTokAppID: "app-target", Name: "Target", ClientKey: "mn-target", Status: application.StatusActive,
	}}})
	provider := &publicationProviderStub{
		authorizationStarted: make(chan struct{}),
		authorizationRelease: make(chan struct{}),
	}
	defer provider.releaseAuthorization()
	service := operations.NewService(memory.OperationsRepositories(store), &publicationMediaProviderStub{})
	if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, operations.PublicationJobConfig{
		PollInterval: time.Millisecond, MediaPollInterval: time.Millisecond, RetryBaseDelay: time.Millisecond,
		LeaseDuration: time.Second, WorkerID: "publication-worker-test",
	}); err != nil {
		t.Fatal(err)
	}
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "Official drama", Description: "Official synopsis", CoverURL: "https://assets.example/cover.png", TagIDs: []string{"1"},
		Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode 1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	episode := drama.Episodes[0]
	if _, err := submitPreparedPublication(t, service, context.Background(), drama.ID, operations.TikTokPublicationInput{
		Priority: operations.PublicationPriorityNormal,
	}); !errors.Is(err, operations.ErrPublicationNotReady) {
		t.Fatalf("媒资未就绪时必须阻止提交，got %v", err)
	}
	episode.VID = "byteplus-vid-1"
	episode.MediaStatus = "ready"
	if err := store.SaveEpisodeMedia(context.Background(), episode, *drama, "", ""); err != nil {
		t.Fatal(err)
	}
	publication, err := submitPreparedPublication(t, service, context.Background(), drama.ID, operations.TikTokPublicationInput{
		Priority: operations.PublicationPriorityNormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if publication.Status != operations.PublicationStatusQueued {
		t.Fatalf("提交只应进入队列，got %+v", publication)
	}
	locked, err := service.FindDrama(context.Background(), drama.ID)
	if err != nil || locked.PublishStatus != "ready" {
		t.Fatalf("同步期间不应改变内容状态，drama=%+v err=%v", locked, err)
	}
	workerContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.StartTikTokPublicationWorker(workerContext); err != nil {
		t.Fatal(err)
	}
	manuallyApproveAndPublish(t, service, drama.ID)
	published, err := service.FindDrama(context.Background(), drama.ID)
	if err != nil {
		t.Fatal(err)
	}
	if published.PublishStatus != "ready" || published.PlatformAlbumID == "" || published.Episodes[0].PlatformEpisodeID == "" {
		t.Fatalf("官方流程完成前后状态不正确：%+v", published)
	}
	wantCalls := []string{
		"image", "video-register", "video-query", "transcode-start", "transcode-query", "create",

		"update", "review", "query", "online-version", "list",
	}
	if got := provider.callSnapshot(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("官方调用顺序=%v，期望=%v", got, wantCalls)
	}
	if _, err := service.QueueTikTokUnpublish(context.Background(), drama.ID); err != nil {
		t.Fatal(err)
	}
	waitForReleaseStatus(t, service, drama.ID, operations.PublicationStatusOffline)
	offline, err := service.FindDrama(context.Background(), drama.ID)
	if err != nil || offline.PublishStatus != "ready" {
		t.Fatalf("官方下架后站内目录状态不正确：%+v err=%v", offline, err)
	}
}

func waitForPublicationStatus(t *testing.T, service *operations.Service, dramaID, status string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		publication, err := service.FindTikTokPublication(context.Background(), dramaID)
		if err == nil && status == operations.PublicationStatusUnderReview && publication.Status == operations.PublicationStatusSynced && publication.Stage == operations.PublicationStageAwaitSubmitReview {
			if _, submitErr := service.SubmitTikTokReview(context.Background(), dramaID, operations.TikTokPublicationInput{Priority: publication.Priority}); submitErr != nil {
				t.Fatalf("提交已同步版本审核失败：%v", submitErr)
			}
			continue
		}
		if err == nil && publication.Status == status {
			return
		}
		time.Sleep(time.Millisecond)
	}
	publication, _ := service.FindTikTokPublication(context.Background(), dramaID)
	t.Fatalf("发布状态未到达 %s：%+v", status, publication)
}

// manuallyApproveAndPublish 显式模拟运营查询审核并确认上线，不把人工操作藏进状态等待。
func manuallyApproveAndPublish(t *testing.T, service *operations.Service, dramaID string) {
	t.Helper()
	waitForPublicationStatus(t, service, dramaID, operations.PublicationStatusUnderReview)
	publication, err := service.RefreshTikTokReview(context.Background(), dramaID)
	if err != nil || publication.Status != operations.PublicationStatusApproved {
		t.Fatalf("手动查询未进入待上线：%+v %v", publication, err)
	}
	if _, err := service.PublishTikTokPublication(context.Background(), dramaID, publication.ID); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, dramaID, operations.PublicationStatusOnline)
}

func waitForReleaseStatus(t *testing.T, store *operations.Service, id, status string) {
	t.Helper()
	state, err := store.FindTikTokReleaseState(context.Background(), id)
	if err != nil || state == nil || state.Status != status {
		t.Fatalf("release=%+v err=%v", state, err)
	}
}
