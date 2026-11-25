package operations_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// TestTikTokSyncRequiresTagsBeforePlatformWrites 验证空标签不会创建任务或触发外部写入，补齐后仍可同步。
func TestTikTokSyncRequiresTagsBeforePlatformWrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags []string
	}{{"未选择标签", nil}, {"空标签清单", []string{}}} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &publicationProviderStub{}
			service, store, id := publicationFixture(t, provider)
			ctx := context.Background()
			draft, err := store.FindDrama(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			draft.TagIDs = tc.tags
			if err := store.SaveDrama(ctx, *draft); err != nil {
				t.Fatal(err)
			}
			// 无标签仍允许草稿就绪；标签仅是官方同步的前置条件。
			if _, err := service.PrepareDrama(ctx, id); err != nil {
				t.Fatal("无标签阻止了渠道无关的内容就绪", err)
			}
			transcode := false
			publication, err := service.SubmitTikTokPublication(ctx, id, operations.TikTokPublicationInput{Transcode: &transcode})
			if !errors.Is(err, operations.ErrPublicationNotReady) || publication != nil {
				t.Fatal("空标签未在同步前拒绝", err)
			}
			if calls := provider.callSnapshot(); len(calls) != 0 {
				t.Fatal("空标签仍触发平台写入", calls)
			}
			if _, err := store.FindTikTokPublicationByDramaID(ctx, id); !errors.Is(err, consumer.ErrNotFound) {
				t.Fatal("校验拒绝后仍创建了同步任务", err)
			}
			// 运营补选标签后复用原草稿及视频，不需要人工清理失败任务。
			if _, err := service.UpdateDrama(ctx, id, operations.DramaInput{
				Title: draft.Title, Description: draft.Description, CoverURL: draft.CoverURL,
				Language: draft.Language, Status: draft.Status, DramaType: draft.DramaType,
				TagIDs: []string{"1"}, Episodes: []operations.EpisodeInput{{ID: draft.Episodes[0].ID, Number: 1, Title: draft.Episodes[0].Title}},
			}); err != nil {
				t.Fatal(err)
			}
			publication, err = service.SubmitTikTokPublication(ctx, id, operations.TikTokPublicationInput{Transcode: &transcode})
			if err != nil || publication == nil || publication.Status != operations.PublicationStatusSynced {
				t.Fatal("补齐标签后无法同步", err)
			}
			if !slices.Equal(publication.ContentSnapshot.TagIDs, []string{"1"}) || !slices.Contains(provider.callSnapshot(), "create") {
				t.Fatal("同步没有使用补齐后的标签")
			}
		})
	}
}

// TestRejectedAlbumWithoutTagsRequiresEditedContent 验证历史空标签失败任务不盲目重试，补齐草稿后复用原 Album。
func TestRejectedAlbumWithoutTagsRequiresEditedContent(t *testing.T) {
	service, store, provider, id, _ := synchronousFixture(t)
	ctx := context.Background()
	previous, err := store.FindTikTokPublicationByDramaID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	previous.ContentSnapshot.TagIDs = nil
	previous.Status, previous.Stage = operations.PublicationStatusFailed, operations.PublicationStageUpdateAlbum
	previous.ErrorCode = "api_rejected"
	if err := store.UpdateTikTokPublication(ctx, previous, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RetryTikTokPublication(ctx, id); !errors.Is(err, operations.ErrPublicationNotReady) {
		t.Fatal("历史空标签任务允许原样重试", err)
	}
	if calls := provider.callSnapshot(); len(calls) != 0 {
		t.Fatal("历史空标签任务仍调用平台", calls)
	}
	// 草稿已包含补齐后的标签，重新同步应冻结当前草稿并复用原官方剧目。
	transcode := false
	publication, err := service.SubmitTikTokPublication(ctx, id, operations.TikTokPublicationInput{Transcode: &transcode})
	if err != nil || publication == nil || publication.Status != operations.PublicationStatusSynced {
		t.Fatal("补齐标签后无法恢复同步", err)
	}
	if publication.PlatformAlbumID != previous.PlatformAlbumID || !slices.Equal(publication.ContentSnapshot.TagIDs, []string{"1"}) {
		t.Fatal("重新同步丢失原 Album 或未使用当前标签")
	}
	if calls := provider.callSnapshot(); slices.Contains(calls, "create") || !slices.Contains(calls, "update") {
		t.Fatal("恢复同步重复创建了 Album 或未更新标签", calls)
	}
}
