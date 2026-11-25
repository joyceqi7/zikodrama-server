package operations_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"drama-server/internal/biz/operations"
)

// recoverableAlbumProvider 模拟旧版已写入但未取得映射，恢复仅允许查询原冻结草稿。
type recoverableAlbumProvider struct {
	publicationProviderStub
	resolveErr error
}

// versionProtectedAlbumProvider 分开统计只读恢复和显式人工版本保护恢复。
type versionProtectedAlbumProvider struct {
	recoverableAlbumProvider
}

// RetryAlbumUpdate 模拟适配器已经完成原版本查询和并发保护，只返回当前冻结内容的新版本。
func (p *versionProtectedAlbumProvider) RetryAlbumUpdate(_ context.Context, draft operations.AlbumDraft) (operations.UpdateAlbumResult, error) {
	p.record("retry-version-protected")
	return operations.UpdateAlbumResult{Version: draft.Version + 1, EpisodeIDsBySequence: map[int]string{1: "7685603384053909501"}}, nil
}

// TestManualAlbumRetrySelectsVersionProtectedRecovery 验证人工恢复只处理原冻结任务，首次同步仍保持只读。
func TestManualAlbumRetrySelectsVersionProtectedRecovery(t *testing.T) {
	for _, version := range []int{0, 1} {
		t.Run(map[int]string{0: "首次同步只核对", 1: "已有原版本受保护恢复"}[version], func(t *testing.T) {
			provider := &versionProtectedAlbumProvider{}
			service, store, id := publicationFixture(t, provider)
			ctx := context.Background()
			if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, id, operations.PublicationStatusFailed)
			previous, err := store.FindTikTokPublicationByDramaID(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			previous.PlatformVersion = version
			if err := store.UpdateTikTokPublication(ctx, previous, nil); err != nil {
				t.Fatal(err)
			}
			result, err := service.RetryTikTokPublication(ctx, id)
			if err != nil || result.ID != previous.ID || result.Status != operations.PublicationStatusSynced || result.Stage != operations.PublicationStageAwaitSubmitReview || result.PlatformVersion != version+1 || result.ExternalWriteStarted || result.ErrorCode != "" || result.ContentSnapshot.Title != previous.ContentSnapshot.Title {
				t.Fatalf("恢复未保留原任务或未停在待送审：%+v %v", result, err)
			}
			calls := provider.callSnapshot()
			if slices.Contains(calls, "retry-version-protected") != (version > 0) || slices.Contains(calls, "resolve-update") != (version == 0) || slices.Contains(calls, "review") || slices.Contains(calls, "list") {
				t.Fatalf("恢复越过版本或人工送审门禁：%v", calls)
			}
		})
	}
}

// UpdateAlbum 模拟旧部署版本丢失更新响应，保留第一次写入计数。
func (p *recoverableAlbumProvider) UpdateAlbum(context.Context, operations.AlbumDraft) (operations.UpdateAlbumResult, error) {
	p.record("update-unknown")
	return operations.UpdateAlbumResult{}, &operations.PlatformCallError{Code: "invalid_episode_mapping"}
}

// ResolveAlbumUpdate 仅查询原剧目，允许测试注入尚未核实或依赖失败。
func (p *recoverableAlbumProvider) ResolveAlbumUpdate(_ context.Context, draft operations.AlbumDraft) (operations.UpdateAlbumResult, error) {
	p.record("resolve-update")
	if p.resolveErr != nil {
		return operations.UpdateAlbumResult{}, p.resolveErr
	}
	return operations.UpdateAlbumResult{Version: draft.Version + 1, EpisodeIDsBySequence: map[int]string{1: "7685603384053909501"}}, nil
}

func TestRetryUnknownAlbumUpdateQueriesOriginalVersionAndContinues(t *testing.T) {
	provider := &recoverableAlbumProvider{}
	service, _, id := publicationFixture(t, provider)
	first, err := submitPreparedPublication(t, service, context.Background(), id, operations.TikTokPublicationInput{})
	if err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusFailed)
	if _, err = service.RetryTikTokPublication(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	result, err := service.FindTikTokPublication(context.Background(), id)
	if err != nil || result.ID != first.ID || result.ExternalWriteStarted || result.PlatformVersion != 1 || result.EpisodeIDsBySequence[1] != "7685603384053909501" {
		t.Fatalf("原任务没有安全恢复：%+v %v", result, err)
	}
	calls := provider.callSnapshot()
	for _, call := range []string{"create", "update-unknown", "resolve-update", "review", "transcode-start"} {
		count := 0
		for _, got := range calls {
			if got == call {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s 次数异常：%v", call, calls)
		}
	}
	if slices.Contains(calls, "list") {
		t.Fatal("查询恢复擅自上线")
	}
}

func TestUnconfirmedAlbumRecoveryKeepsCheckpointAndNeverRewrites(t *testing.T) {
	for _, failure := range []error{&operations.PlatformCallError{Code: "album_update_unconfirmed", OutcomeUnknown: true}, errors.New("query dependency unavailable")} {
		t.Run(failure.Error(), func(t *testing.T) {
			provider := &recoverableAlbumProvider{resolveErr: failure}
			service, _, id := publicationFixture(t, provider)
			if _, err := submitPreparedPublication(t, service, context.Background(), id, operations.TikTokPublicationInput{}); err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, id, operations.PublicationStatusFailed)
			for i := 0; i < 2; i++ {
				if _, err := service.RetryTikTokPublication(context.Background(), id); err == nil {
					t.Fatal("同步核对失败未直接返回错误")
				}
				waitForPublicationStatus(t, service, id, operations.PublicationStatusFailed)
				p, err := service.FindTikTokPublication(context.Background(), id)
				if err != nil || !p.ExternalWriteStarted || p.ErrorCode != "external_write_uncertain" || p.Stage != operations.PublicationStageUpdateAlbum {
					t.Fatalf("未知写入检查点丢失：%+v %v", p, err)
				}
			}
			calls := provider.callSnapshot()
			writes := 0
			queries := 0
			for _, call := range calls {
				if call == "update-unknown" {
					writes++
				}
				if call == "resolve-update" {
					queries++
				}
			}
			if writes != 1 || queries != 2 || slices.Contains(calls, "review") {
				t.Fatalf("恢复错误地重写或送审：%v", calls)
			}
		})
	}
}

func TestLegacyUnknownAlbumUpdateRestoresMissingCheckpointBeforeRetry(t *testing.T) {
	provider := &recoverableAlbumProvider{}
	service, store, id := publicationFixture(t, provider)
	ctx := context.Background()
	if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusFailed)
	p, err := store.FindTikTokPublicationByDramaID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p.ExternalWriteStarted = false
	if err := store.UpdateTikTokPublication(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	queued, err := service.RetryTikTokPublication(ctx, id)
	if err != nil || queued.ExternalWriteStarted || queued.Status != operations.PublicationStatusSynced {
		t.Fatalf("旧记录没有恢复查询检查点：%+v %v", queued, err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	writes, queries := 0, 0
	for _, call := range provider.callSnapshot() {
		if call == "update-unknown" {
			writes++
		}
		if call == "resolve-update" {
			queries++
		}
	}
	if writes != 1 || queries != 1 {
		t.Fatalf("旧任务错误地重发更新：%v", provider.callSnapshot())
	}
}
