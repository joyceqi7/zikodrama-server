package operations_test

import (
	"context"
	"testing"

	"drama-server/internal/biz/operations"
)

// transcodeKeyProvider 记录官方转码收到的幂等键，不记录视频地址或凭据。
type transcodeKeyProvider struct {
	publicationProviderStub
	keys []string
}

// StartTranscode 保存调用键并沿用内存官方任务替身。
func (p *transcodeKeyProvider) StartTranscode(ctx context.Context, input operations.TikTokTranscodeInput) (string, error) {
	p.mu.Lock()
	p.keys = append(p.keys, input.IdempotencyKey)
	p.mu.Unlock()
	return p.publicationProviderStub.StartTranscode(ctx, input)
}

// keySnapshot 返回已提交的键副本，避免与发布 Worker 并发读取。
func (p *transcodeKeyProvider) keySnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.keys...)
}

// TestTranscodeKeysDifferAcrossIndependentStores 验证本地发布 ID 相同时仍不会复用平台幂等键。
func TestTranscodeKeysDifferAcrossIndependentStores(t *testing.T) {
	keys := make([]string, 0, 2)
	ids := make([]string, 0, 2)
	for range 2 {
		provider := &transcodeKeyProvider{}
		service, _, dramaID := publicationFixture(t, provider)
		publication, err := submitPreparedPublication(t, service, context.Background(), dramaID, operations.TikTokPublicationInput{})
		if err != nil {
			t.Fatal(err)
		}
		waitForPublicationStatus(t, service, dramaID, operations.PublicationStatusSynced)
		called := provider.keySnapshot()
		if len(called) != 1 || publication.TranscodeKeySeed == "" || called[0] != publication.TranscodeKeySeed+"-0" {
			t.Fatalf("转码键未与持久化随机前缀绑定：keys=%v publication=%+v", called, publication)
		}
		keys = append(keys, called[0])
		ids = append(ids, publication.ID)
	}
	if ids[0] != ids[1] || keys[0] == keys[1] {
		t.Fatalf("独立存储的本地 ID 相同但转码键不独立：ids=%v keys=%v", ids, keys)
	}
}

// TestManualRetryOfRejectedTranscodeCreatesNewKey 验证新旧转码键被明确拒绝后人工重试都不再循环提交原键。
func TestManualRetryOfRejectedTranscodeCreatesNewKey(t *testing.T) {
	for _, test := range []struct {
		name, errorCode string
		legacy          bool
	}{
		{name: "旧任务通用拒绝", errorCode: "api_rejected", legacy: true},
		{name: "明确的幂等键冲突", errorCode: "transcode_idempotency_conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			provider := &transcodeKeyProvider{}
			service, store, dramaID := publicationFixture(t, provider)
			if _, err := submitPreparedPublication(t, service, ctx, dramaID, operations.TikTokPublicationInput{}); err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, dramaID, operations.PublicationStatusSynced)
			old, err := store.FindTikTokPublicationByDramaID(ctx, dramaID)
			if err != nil {
				t.Fatal(err)
			}
			old.Status, old.Stage, old.ErrorCode = operations.PublicationStatusFailed, operations.PublicationStageTranscodeVideos, test.errorCode
			if test.legacy {
				old.TranscodeKeySeed = ""
			}
			old.Transcodes = []operations.PublicationTranscode{{VIDs: []string{"vid-1"}}}
			if err := store.UpdateTikTokPublication(ctx, old, nil); err != nil {
				t.Fatal(err)
			}
			restarted, err := service.RetryTikTokPublication(ctx, dramaID)
			if err != nil {
				t.Fatal(err)
			}
			if restarted.ID == old.ID || restarted.TranscodeKeySeed == "" || restarted.TranscodeKeySeed == old.TranscodeKeySeed {
				t.Fatalf("人工重试仍沿用被拒绝的旧任务或旧键：%+v", restarted)
			}
			waitForPublicationStatus(t, service, dramaID, operations.PublicationStatusSynced)
			keys := provider.keySnapshot()
			if len(keys) != 2 || keys[0] == keys[1] || keys[1] != restarted.TranscodeKeySeed+"-0" {
				t.Fatalf("人工重试没有使用新键：%v", keys)
			}
		})
	}
}

// TestManualRetryKeepsTaskWhenConflictIsUnconfirmed 验证普通拒绝或已有任务 ID 不会误建第二个转码任务。
func TestManualRetryKeepsTaskWhenConflictIsUnconfirmed(t *testing.T) {
	for _, test := range []struct {
		name, errorCode string
		taskID          string
	}{
		{name: "新任务普通拒绝", errorCode: "api_rejected"},
		{name: "已知官方任务", errorCode: "transcode_idempotency_conflict", taskID: "task-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			provider := &transcodeKeyProvider{}
			service, store, dramaID := publicationFixture(t, provider)
			if _, err := submitPreparedPublication(t, service, ctx, dramaID, operations.TikTokPublicationInput{}); err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, dramaID, operations.PublicationStatusSynced)
			old, err := store.FindTikTokPublicationByDramaID(ctx, dramaID)
			if err != nil {
				t.Fatal(err)
			}
			old.Status, old.Stage, old.ErrorCode = operations.PublicationStatusFailed, operations.PublicationStageTranscodeVideos, test.errorCode
			old.Transcodes = []operations.PublicationTranscode{{VIDs: []string{"vid-1"}, TaskID: test.taskID}}
			if err := store.UpdateTikTokPublication(ctx, old, nil); err != nil {
				t.Fatal(err)
			}
			retried, err := service.RetryTikTokPublication(ctx, dramaID)
			if retried == nil || retried.ID != old.ID || retried.TranscodeKeySeed != old.TranscodeKeySeed {
				t.Fatalf("未确认可重建时替换了发布任务：%+v %v", retried, err)
			}
		})
	}
}
