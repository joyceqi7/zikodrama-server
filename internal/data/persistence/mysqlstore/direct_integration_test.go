package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/operations"
	"errors"
	"testing"
	"time"
)

func seedDirect(t *testing.T, f *iaaFixture) (*DirectRepository, context.Context) {
	t.Helper()
	ctx := application.WithSubject(context.Background(), "1")
	db := f.store.data.DB()
	if err := db.Create(&applicationPlaybackProfileModel{ApplicationID: "102", Provider: "byteplus_web", Revision: 1}).Error; err != nil {
		t.Fatal(err)
	}
	d, err := f.store.FindDrama(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	d.Title = "Independent source"
	d.CoverURL = "https://image.example/direct.jpg"
	d.TagIDs = []string{"21"}
	if err := f.store.SaveDrama(ctx, *d); err != nil {
		t.Fatal(err)
	}
	for _, e := range d.Episodes {
		e.MediaSpaceName = "fixture-space"
		if err := f.store.SaveEpisode(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	return NewDirectRepository(f.store), application.WithPlayback(application.WithContext(ctx, "102"), "byteplus_web")
}
func submitDirect(t *testing.T, r *DirectRepository, kind string, version string) directplay.Operation {
	t.Helper()
	ctx := application.WithSubject(context.Background(), "1")
	w, err := r.Workbench(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	if kind == "publish" && version == "" {
		_, err := r.store.PrepareDrama(ctx, "201", "operator")
		if err != nil {
			t.Fatal(err)
		}
	}
	c := directplay.Command{RequestID: kind + "-" + time.Now().Format("150405.000000000"), Kind: kind, ExpectedRevision: w.Publication.Revision}
	o, err := r.Accept(ctx, "201", c, "operator")
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// TestDirectPublicationIntegration 验证上架才发号、幂等发布、草稿隔离及全应用共用目录。
func TestDirectPublicationIntegration(t *testing.T) {
	f := newIAAFixture(t)
	r, ctx := seedDirect(t, f)
	bg := application.WithSubject(context.Background(), "1")
	d, err := f.store.PrepareDrama(bg, "201", "operator")
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.store.PrepareDrama(bg, "201", "operator")
	if err != nil || again.PublishStatus != "ready" {
		t.Fatal("重复确认增加版本", err)
	}
	w, err := r.Workbench(bg, "201")
	if err != nil {
		t.Fatal(err)
	}
	c := directplay.Command{RequestID: "publish-fixture-1", Kind: "publish", ExpectedRevision: w.Publication.Revision}
	first, err := r.Accept(bg, "201", c, "operator")
	if err != nil || first.Status != "succeeded" {
		t.Fatal(err)
	}
	repeat, err := r.Accept(bg, "201", c, "operator")
	if err != nil || repeat.ID != first.ID {
		t.Fatal("重试幂等失效", err)
	}
	changed := c
	changed.Kind = "offline"
	if _, err := r.Accept(bg, "201", changed, "operator"); !errors.Is(err, directplay.ErrConflict) {
		t.Fatal("幂等输入未校验", err)
	}
	d.Title = "Later private draft"
	if err := f.store.SaveDrama(bg, *d); err != nil {
		t.Fatal(err)
	}
	visible, err := f.store.FindDrama(ctx, "201")
	if err != nil || visible.Published == nil || visible.Published.Title != "Independent source" {
		t.Fatal("草稿影响已上线内容", err)
	}
	versions, err := f.store.listDirectPublicationVersions(bg, "201")
	if err != nil || len(versions) != 1 {
		t.Fatal("保存草稿增加版本", err)
	}
	// 新直连应用不用绑定任何短剧，立即共用线上目录。
	if err := f.store.data.DB().Create(&applicationPlaybackProfileModel{ApplicationID: "101", Provider: "byteplus_web", Revision: 1}).Error; err != nil {
		t.Fatal(err)
	}
	other := application.WithPlayback(application.WithContext(bg, "101"), "byteplus_web")
	page, err := f.store.DiscoverDramas(other, consumer.DiscoveryQuery{Limit: 20})
	if err != nil || len(page.Items) != 1 || page.Items[0].Title != "Independent source" {
		t.Fatal("直连应用未共用目录", err)
	}
	next, err := f.store.PrepareDrama(bg, "201", "operator")
	if err != nil || next.PublishStatus != "ready" {
		t.Fatal("修改后就绪提前增加版本", err)
	}
	submitDirect(t, r, "publish", "")
	visible, err = f.store.FindDrama(ctx, "201")
	if err != nil || visible.Published.Title != "Later private draft" {
		t.Fatal("未发布新版本", err)
	}
	submitDirect(t, r, "offline", "")
	page, err = f.store.DiscoverDramas(other, consumer.DiscoveryQuery{Limit: 20})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("全应用下架失效", err)
	}
	used, err := f.store.IsMediaResourceUsed(bg, operations.MediaJob{ResourceClass: operations.MediaResourceVideo, RemoteVID: "fixture-vid-1", SpaceName: "fixture-space"})
	if err != nil || !used {
		t.Fatal("历史版本媒资未保护", err)
	}
	submitDirect(t, r, "publish", "")
	visible, err = f.store.FindDrama(ctx, "201")
	if err != nil || visible.Published.Title != "Later private draft" {
		t.Fatal("重复发布未复用当前就绪版本", err)
	}
}

// TestDirectHomepageIntegration 验证三类运营物理隔离、隐藏语义、应用下架过滤。
func TestDirectHomepageIntegration(t *testing.T) {
	f := newIAAFixture(t)
	r, ctx := seedDirect(t, f)
	bg := application.WithSubject(context.Background(), "1")
	submitDirect(t, r, "publish", "")
	home := homepage.NewService(f.store, f.store, f.store)
	management := application.WithPlayback(homepage.WithManagementContext(bg), "byteplus_web")
	if _, err := home.SaveCarousel(management, homepage.Carousel{DramaIDs: []string{"201"}, Items: []homepage.CarouselItem{{DramaID: "201", Title: "Direct title", Visible: true}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := home.SaveHot(management, homepage.Carousel{DramaIDs: []string{"201"}, Items: []homepage.CarouselItem{{DramaID: "201", Visible: false}}}); err != nil {
		t.Fatal(err)
	}
	carousel, err := home.HomeCarousel(ctx)
	if err != nil || len(carousel) != 1 || carousel[0].Title != "Direct title" {
		t.Fatal("直连轮播", carousel, err)
	}
	hot, _ := home.HomeHot(ctx)
	if len(hot) != 0 {
		t.Fatal("隐藏热播仍展示")
	}
	official, _ := f.store.GetCarousel(bg)
	if len(official.DramaIDs) != 0 {
		t.Fatal("覆盖官方配置")
	}
	ranking, err := home.CategoryRanking(management, "21")
	if err != nil || len(ranking.Items) != 1 {
		t.Fatal("直连自动分类", ranking, err)
	}
	ranking.Items[0].Visible = false
	if _, err := home.SaveCategoryRanking(management, "21", ranking.Ranking); err != nil {
		t.Fatal(err)
	}
	page, err := f.store.DiscoverDramas(ctx, consumer.DiscoveryQuery{Limit: 20})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("隐藏分类影响更多目录")
	}
	if err := f.store.data.DB().Model(&applicationModel{}).Where("id = ?", "102").Update("status", "inactive").Error; err != nil {
		t.Fatal(err)
	}
	carousel, _ = home.HomeCarousel(ctx)
	if len(carousel) != 0 {
		t.Fatal("应用下架仍推荐")
	}
	if _, err := home.SaveCarousel(homepage.WithManagementContext(bg), homepage.Carousel{DramaIDs: []string{"201"}, Items: []homepage.CarouselItem{{DramaID: "201", Visible: true}}}); err != nil {
		t.Fatal(err)
	}

}

// TestDirectRevisionFenceIntegration 验证过期上下架命令不能覆盖当前状态。
func TestDirectRevisionFenceIntegration(t *testing.T) {
	f := newIAAFixture(t)
	r, _ := seedDirect(t, f)
	bg := application.WithSubject(context.Background(), "1")
	_, err := f.store.PrepareDrama(bg, "201", "operator")
	if err != nil {
		t.Fatal(err)
	}
	c := directplay.Command{RequestID: "revision-fixture-one", Kind: "publish"}
	if _, err := r.Accept(bg, "201", c, "operator"); err != nil {
		t.Fatal(err)
	}
	c.RequestID = "revision-fixture-two"
	c.Kind = "offline"

	if _, err := r.Accept(bg, "201", c, "operator"); !errors.Is(err, directplay.ErrConflict) {
		t.Fatal("过期状态修改未拒绝", err)
	}
}

// TestDirectTagsAndProfilesIntegration 验证标签迁移、状态筛选与应用模式固定，保留隐藏排序和公共草稿。
func TestDirectTagsAndProfilesIntegration(t *testing.T) {
	f := newIAAFixture(t)
	r, ctx := seedDirect(t, f)
	bg := application.WithSubject(context.Background(), "1")
	home := homepage.NewService(f.store, f.store, f.store)
	management := homepage.WithManagementContext(ctx)
	submitDirect(t, r, "publish", "")
	rank, err := home.CategoryRanking(management, "21")
	if err != nil || len(rank.Items) != 1 {
		t.Fatal(rank, err)
	}
	rank.Items[0].Visible = false
	if _, err := home.SaveCategoryRanking(management, "21", rank.Ranking); err != nil {
		t.Fatal(err)
	}
	d, _ := f.store.FindDrama(bg, "201")
	d.TagIDs = []string{"21", "22"}
	if err := f.store.SaveDrama(bg, *d); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PrepareDrama(bg, "201", "operator"); err != nil {
		t.Fatal(err)
	}
	works, err := r.List(bg)
	if err != nil || len(works) != 1 || !works[0].Summary.PendingChanges {
		t.Fatal("直连菜单未显示待发布内容", works, err)
	}
	submitDirect(t, r, "publish", "")
	rank, _ = home.CategoryRanking(management, "21")
	added, _ := home.CategoryRanking(management, "22")
	if len(rank.Items) != 1 || rank.Items[0].Visible || len(added.Items) != 1 || !added.Items[0].Visible {
		t.Fatal("保留和新增标签错误", rank, added)
	}
	d, _ = f.store.FindDrama(bg, "201")
	d.TagIDs = []string{"22"}
	if err := f.store.SaveDrama(bg, *d); err != nil {
		t.Fatal(err)
	}
	submitDirect(t, r, "publish", "")
	rank, _ = home.CategoryRanking(management, "21")
	if len(rank.Items) != 0 {
		t.Fatal("移除标签没有退榜")
	}
	submitDirect(t, r, "offline", "")
	added, _ = home.CategoryRanking(management, "22")
	if len(added.Items) != 0 {
		t.Fatal("全局下架未退榜")
	}
	submitDirect(t, r, "publish", "")
	added, _ = home.CategoryRanking(management, "22")
	if len(added.Items) != 1 || !added.Items[0].Visible {
		t.Fatal("恢复没有重新入榜")
	}
	candidates, err := home.Targets(management, consumer.DiscoveryQuery{Limit: 20})
	if err != nil || candidates.Metadata["201"].Version != 3 || candidates.Metadata["201"].Applications != 1 {
		t.Fatal("管理候选信息", candidates, err)
	}
	ordinary, err := home.Targets(ctx, consumer.DiscoveryQuery{Limit: 20})
	if err != nil || ordinary.Metadata != nil {
		t.Fatal("消费者不应携带管理元数据", err)
	}
	works, err = r.List(bg)
	if err != nil || len(works) != 1 || works[0].Publication.Status != "published" {
		t.Fatal(works, err)
	}
	app, err := f.store.FindApplication(bg, "102")
	if err != nil {
		t.Fatal(err)
	}
	// 普通资料编辑保留当前通道与修订；显式通道切换由独立的整体更新回归覆盖。
	app.Name = "保留直连的资料编辑"
	if err := f.store.SaveApplication(bg, app); err != nil {
		t.Fatal(err)
	}
	kept, _ := f.store.FindApplication(bg, "102")
	if kept.PlaybackProvider != "byteplus_web" || kept.PlaybackRevision != 1 {
		t.Fatal("普通资料编辑改变了模式或播放修订")
	}
	kept, err = f.store.FindApplication(bg, "102")
	if err != nil {
		t.Fatal(err)
	}
	kept.Status = application.StatusActive
	// 直连运行参数在启动文件校验，启用应用不依赖数据库中的播放配置。
	if err := f.store.SaveApplication(bg, kept); err != nil {
		t.Fatal("无全局播放配置时启用失败", err)
	}
}
