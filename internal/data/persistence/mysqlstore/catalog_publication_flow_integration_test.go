package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/operations"
	"errors"
	"testing"
)

// TestCatalogDirectPublicationIntegration 验证草稿直接上线、编辑隔离、恢复原版本和新内容校验。
func TestCatalogDirectPublicationIntegration(t *testing.T) {
	f := newIAAFixture(t)
	repo, ctx := seedDirect(t, f)
	bg := application.WithSubject(context.Background(), "1")
	draft, err := f.store.FindDrama(bg, "201")
	if err != nil {
		t.Fatal(err)
	}
	draft.PublishStatus = "draft"
	if err := f.store.SaveDrama(bg, *draft); err != nil {
		t.Fatal(err)
	}
	first, err := repo.Accept(bg, "201", directplay.Command{RequestID: "catalog-first-publish", Kind: "publish", ExpectedRevision: 0}, "operator")
	if err != nil {
		t.Fatal("完整草稿不能直接上线", err)
	}
	old, err := f.store.FindDrama(ctx, "201")
	if err != nil || old.Published == nil {
		t.Fatal(err)
	}
	draft.Title = "尚未发布的新标题"
	if err := f.store.SaveDrama(bg, *draft); err != nil {
		t.Fatal(err)
	}
	// 新草稿缺少视频也不能改变消费者使用的原版本。
	if err := f.store.UpdateEpisodeMediaStatus(bg, draft.Episodes[0].ID, draft.Episodes[0].VID, "pending"); err != nil {
		t.Fatal(err)
	}
	page, err := f.store.ListDramaPage(bg, operations.DramaListQuery{Page: 1, PageSize: 20, DirectStatus: "published"})
	if err != nil || len(page.Items) != 1 || page.Items[0].DirectPublication == nil || !page.Items[0].DirectPublication.PendingChanges || page.Items[0].DirectPublication.Revision != 1 {
		t.Fatal("内容列表缺少直连状态或待发布变化", err)
	}
	_, err = repo.Accept(bg, "201", directplay.Command{RequestID: "catalog-offline", Kind: "offline", ExpectedRevision: 1}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := repo.Accept(bg, "201", directplay.Command{RequestID: "catalog-relist", Kind: "relist", ExpectedRevision: 2}, "operator")
	if err != nil || restored.ResultVersionID != first.ResultVersionID {
		t.Fatal("恢复原版本读了未就绪草稿", err)
	}
	visible, err := f.store.FindDrama(ctx, "201")
	if err != nil || visible.Published == nil || visible.Published.Title != old.Published.Title {
		t.Fatal("重新上线替换了旧内容", err)
	}
	_, err = repo.Accept(bg, "201", directplay.Command{RequestID: "catalog-invalid-update", Kind: "publish", ExpectedRevision: 3}, "operator")
	if !errors.Is(err, operations.ErrPublicationNotReady) {
		t.Fatal("未就绪新视频绕过发布校验", err)
	}
	w, err := repo.Workbench(bg, "201")
	if err != nil || w.Publication.Revision != 3 || w.Publication.CurrentVersionID != first.ResultVersionID {
		t.Fatal("失败发布改变线上修订", err)
	}
	if err := f.store.UpdateEpisodeMediaStatus(bg, draft.Episodes[0].ID, draft.Episodes[0].VID, "ready"); err != nil {
		t.Fatal(err)
	}
	_, err = repo.Accept(bg, "201", directplay.Command{RequestID: "catalog-valid-update", Kind: "publish", ExpectedRevision: 3}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	visible, err = f.store.FindDrama(ctx, "201")
	if err != nil || visible.Published == nil || visible.Published.Title != draft.Title {
		t.Fatal("发布更新未切换快照", err)
	}
	versions, err := f.store.listDirectPublicationVersions(bg, "201")
	if err != nil || len(versions) != 2 {
		t.Fatal("恢复原版本不应增加发布版本", err)
	}
	current, err := f.store.FindDrama(bg, "201")
	if err != nil || current.PublishStatus != "draft" {
		t.Fatal("发布动作不应要求或切换 ready", err)
	}
	// 已有官方快照的身份和版本不被直连动作改写。
	if current.Published == nil || current.Published.PlaybackProvider == "byteplus_web" || current.Published.PlatformAlbumID == "" {
		t.Fatal("直连发布覆盖官方快照")
	}
}
