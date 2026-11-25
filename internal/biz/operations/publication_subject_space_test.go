package operations_test

import (
	"context"
	"testing"

	"drama-server/internal/biz/operations"
)

// TestPublicationNewSpaceCreatesAlbum 验证历史主体配置独立空间后使用新 Album，准备新版本不覆盖旧线上视频。
func TestPublicationNewSpaceCreatesAlbum(t *testing.T) {
	ctx := context.Background()
	provider := &publicationProviderStub{reviewStatus: 2}
	service, store, id := publicationFixture(t, provider)
	if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	manuallyApproveAndPublish(t, service, id)
	owner, err := store.FindApplicationByTikTokAppID(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	owner.BytePlusSpaceName = "subject-new-space"
	if err := store.SaveApplication(ctx, owner); err != nil {
		t.Fatal(err)
	}
	draft, err := store.FindDrama(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	oldVID := draft.Episodes[0].VID
	episode := draft.Episodes[0]
	episode.VID = "new-space-vid"
	episode.MediaSpaceName = "subject-new-space"
	if err := store.SaveEpisodeMedia(ctx, episode, *draft, oldVID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusSynced)
	creates := 0
	for _, call := range provider.callSnapshot() {
		if call == "create" {
			creates++
		}
	}
	if creates != 2 {
		t.Fatal("新空间仍复用了旧 Album", creates)
	}
	current, err := store.FindDrama(ctx, id)
	if err != nil || current.Published == nil || current.Published.Episodes[0].VID != oldVID {
		t.Fatal("准备新空间版本改变旧线上内容", err)
	}
}
