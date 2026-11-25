package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestCurrentSchemaIntegrationDistributions 验证真实 MySQL 的双向分发、逐应用失败、撤权和旧租约隔离。
func TestCurrentSchemaIntegrationDistributions(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithSubject(context.Background(), "1")
	store := f.store
	drama, err := store.FindDrama(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	owner := drama.Published.OwnerApplicationID
	source := operations.DistributionSource{SubjectID: "1", DramaID: drama.ID, OwnerID: owner, AlbumID: drama.PlatformAlbumID, Online: true}
	// 显式构造已上线的官方任务供自动对账读取归属。
	now := time.Now().UTC()
	p, err := newTikTokPublicationModel(operations.TikTokPublication{SubjectID: "1", ID: "1005", DramaID: drama.ID, OwnerApplicationID: owner, PlatformAlbumID: drama.PlatformAlbumID, Status: "online", Stage: "done", CreatedAt: now, UpdatedAt: now, NextAttemptAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.data.DB().Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	service := operations.NewService(operations.Repositories{Application: operations.ApplicationPorts{Subjects: store, Registry: store}, Content: operations.ContentPorts{OwnerApplication: store.FindApplication, Distributions: store, Catalog: store, Publications: store}}, nil)
	serviceProvider := &syncDistributionFixtureProvider{}
	if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return serviceProvider }, operations.PublicationJobConfig{}); err != nil {
		t.Fatal(err)
	}

	add := func(id string) application.Application {
		app := application.Application{SubjectID: "1", TikTokAppID: id, Name: id, ClientKey: "key-" + id, Status: application.StatusActive, TimeZone: "UTC", CreatedAt: now, UpdatedAt: now}
		if err := store.CreateApplication(ctx, &app); err != nil {
			t.Fatal(err)
		}
		return app
	}
	a := add("dist-a")
	b := add("dist-b")
	if err := service.ReconcileDistributions(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListDistributions(ctx, drama.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]operations.DramaDistribution{}
	for _, row := range rows {
		found[row.ApplicationID] = row
	}
	if _, ok := found[a.ID]; !ok {
		t.Fatalf("新增应用未生成候选关系: %+v", rows)
	}
	if _, ok := found[b.ID]; !ok {
		t.Fatalf("新增应用未生成候选关系: %+v", rows)
	}
	if _, ok := found[owner]; !ok {
		t.Fatalf("错误遗漏拥有方关系: %+v", rows)
	}
	if found[a.ID].Enabled || found[a.ID].Desired || found[b.ID].Enabled || found[b.ID].Desired {
		t.Fatalf("跨应用关系未经人工选择即自动分发: %+v", rows)
	}
	// 清空其他夹具应用的任务，仅保留本测试两个目标。
	if err := store.data.DB().Where("application_id NOT IN ?", []string{a.ID, b.ID}).Delete(&distributionModel{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.SetDistribution(ctx, drama.ID, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDistribution(ctx, drama.ID, b.ID, true); err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimDistribution(ctx, "worker-1", time.Now().UTC())
	if err != nil || first == nil {
		t.Fatalf("claim %v %v", first, err)
	}
	if err := store.FinishDistribution(ctx, *first, errors.New("platform unavailable")); err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimDistribution(ctx, "worker-2", time.Now().UTC())
	if err != nil || second == nil || second.ApplicationID == first.ApplicationID {
		t.Fatalf("失败阻塞其他目标: %+v %v", second, err)
	}
	if err := store.FinishDistribution(ctx, *second, nil); err != nil {
		t.Fatal(err)
	}
	check := func(id string, want bool) {
		t.Helper()
		d, err := store.FindDrama(ctx, drama.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, ok := d.PublishedForApplication(id)
		if ok != want {
			t.Fatalf("应用 %s 可见=%v 期望=%v", id, ok, want)
		}
	}
	check(first.ApplicationID, false)
	check(second.ApplicationID, true)
	failedPage, err := store.ListApplicationDramaAuthorizations(ctx, first.ApplicationID, operations.ApplicationDramaAuthorizationQuery{Page: 1, PageSize: 20, View: "tracked"})
	if err != nil || failedPage.Total != 1 || failedPage.Items[0].Status != "failed" {
		t.Fatalf("自动重试中的授权需展示真实失败状态: %+v %v", failedPage, err)
	}
	if err := store.SetDistribution(ctx, drama.ID, first.ApplicationID, true); err != nil {
		t.Fatal(err)
	}
	retry, err := store.ClaimDistribution(ctx, "worker-3", time.Now().UTC())
	if err != nil || retry == nil {
		t.Fatal(err)
	}
	if err := store.FinishDistribution(ctx, *retry, nil); err != nil {
		t.Fatal(err)
	}
	check(first.ApplicationID, true)
	if err := store.SetDistribution(ctx, drama.ID, first.ApplicationID, false); err != nil {
		t.Fatal(err)
	}
	check(first.ApplicationID, false)
	old, err := store.ClaimDistribution(ctx, "worker-old", time.Now().UTC())
	if err != nil || old == nil {
		t.Fatal(err)
	}
	if err := store.SetDistribution(ctx, drama.ID, first.ApplicationID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishDistribution(ctx, *old, nil); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("旧撤权覆盖新意图: %v", err)
	}
	// 旧租约未结束时不能并发执行相反授权。
	busy, err := store.ClaimDistribution(ctx, "worker-new", time.Now().UTC())
	if err != nil || busy != nil {
		t.Fatalf("在途租约被绕过: %+v %v", busy, err)
	}
	if err := store.data.DB().Model(&distributionModel{}).Where("application_id=?", first.ApplicationID).Update("lease_expires_at", now.Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	fresh, err := store.ClaimDistribution(ctx, "worker-new", time.Now().UTC())
	if err != nil || fresh == nil || !fresh.Desired {
		t.Fatalf("未恢复新意图: %+v %v", fresh, err)
	}
	if err := store.FinishDistribution(ctx, *fresh, nil); err != nil {
		t.Fatal(err)
	}
	// 人工撤权后周期对账不得重新授权。
	stale, err := store.FindDrama(ctx, drama.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SetDistribution(ctx, drama.ID, a.ID, false); err != nil {
		t.Fatal(err)
	}

	publication, err := store.FindTikTokPublicationByDramaID(ctx, drama.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTikTokPublication(ctx, publication, stale); err != nil {
		t.Fatal(err)
	}
	check(a.ID, false)
	if _, err := store.EnsureDistribution(ctx, source, a); err != nil {
		t.Fatal(err)
	}
	rows, err = store.ListDistributions(ctx, drama.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ApplicationID == a.ID && (row.Enabled || row.Desired) {
			t.Fatal("人工撤权被自动分发覆盖")
		}
	}
	b.Status = application.StatusDisabled
	if err := store.SaveApplication(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileDistributions(ctx); err != nil {
		t.Fatal(err)
	}
	check(b.ID, false)
	// 新增第三个应用，既有上线剧自动补齐关系。
	c := add("dist-c")
	if err := service.ReconcileDistributions(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err = store.ListDistributions(ctx, drama.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundC := false
	for _, row := range rows {
		if row.ApplicationID == c.ID {
			foundC = !row.Enabled && !row.Desired && row.Status == "revoked"
		}
	}
	if !foundC {
		t.Fatal("新增应用未以待选择状态补齐既有剧库")
	}
}

// TestCurrentSchemaIntegrationQueueApplicationDistributions 验证按钮批量入队、重复点击幂等和撤权后重新授权。
func TestCurrentSchemaIntegrationQueueApplicationDistributions(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithSubject(context.Background(), "1")
	store := f.store
	drama, err := store.FindDrama(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	publication, err := newTikTokPublicationModel(operations.TikTokPublication{SubjectID: "1", ID: "1005", DramaID: drama.ID, OwnerApplicationID: drama.Published.OwnerApplicationID, Status: "online", Stage: "done", CreatedAt: now, UpdatedAt: now, NextAttemptAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.data.DB().Create(&publication).Error; err != nil {
		t.Fatal(err)
	}
	other := *drama
	other.ID = "202"
	other.Title = "未选中的线上短剧"
	other.PlatformAlbumID = "fixture-album-2"
	other.Episodes = nil
	otherPublished := *drama.Published
	otherPublished.ID = other.ID
	otherPublished.Title = other.Title
	otherPublished.PlatformAlbumID = other.PlatformAlbumID
	otherPublished.Episodes = nil
	other.Published = &otherPublished
	if err := store.SaveDrama(ctx, other); err != nil {
		t.Fatal(err)
	}
	rawSnapshot, _ := json.Marshal(otherPublished)
	if err := seedOfficialProjection(store.data.DB().WithContext(ctx), other.ID, rawSnapshot); err != nil {
		t.Fatal(err)
	}
	otherPublication, err := newTikTokPublicationModel(operations.TikTokPublication{SubjectID: "1", ID: "1006", DramaID: other.ID, OwnerApplicationID: otherPublished.OwnerApplicationID, Status: "online", Stage: "done", CreatedAt: now, UpdatedAt: now, NextAttemptAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.data.DB().Create(&otherPublication).Error; err != nil {
		t.Fatal(err)
	}
	app := application.Application{SubjectID: "1", TikTokAppID: "bulk-target", Name: "bulk-target", ClientKey: "bulk-client-key", Status: application.StatusActive, TimeZone: "UTC", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateApplication(ctx, &app); err != nil {
		t.Fatal(err)
	}
	list := func(view string, query string) operations.ApplicationDramaAuthorizationPage {
		t.Helper()
		page, err := store.ListApplicationDramaAuthorizations(ctx, app.ID, operations.ApplicationDramaAuthorizationQuery{Page: 1, PageSize: 20, Query: query, View: view})
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	if page := list("available", ""); page.Total != 2 || len(page.Items) != 2 || page.Items[0].Status != "none" {
		t.Fatalf("未授权栏未包含全部线上剧: %+v", page)
	}
	if page := list("tracked", ""); page.Total != 0 {
		t.Fatalf("未授权剧错误进入状态栏: %+v", page)
	}
	if page := list("available", "未选中的线上短剧"); page.Total != 1 || page.Items[0].ID != other.ID {
		t.Fatalf("标题搜索错误: %+v", page)
	}
	firstPage, err := store.ListApplicationDramaAuthorizations(ctx, app.ID, operations.ApplicationDramaAuthorizationQuery{Page: 1, PageSize: 1, View: "available"})
	if err != nil || firstPage.Total != 2 || len(firstPage.Items) != 1 {
		t.Fatalf("未授权栏首页分页错误: %+v %v", firstPage, err)
	}
	secondPage, err := store.ListApplicationDramaAuthorizations(ctx, app.ID, operations.ApplicationDramaAuthorizationQuery{Page: 2, PageSize: 1, View: "available"})
	if err != nil || secondPage.Total != 2 || len(secondPage.Items) != 1 || secondPage.Items[0].ID == firstPage.Items[0].ID {
		t.Fatalf("未授权栏后续分页错误: %+v %v", secondPage, err)
	}
	ownerPage, err := store.ListApplicationDramaAuthorizations(ctx, drama.Published.OwnerApplicationID, operations.ApplicationDramaAuthorizationQuery{Page: 1, PageSize: 20, View: "tracked"})
	if err != nil || ownerPage.Total != 2 || ownerPage.Items[0].Status != "owner" {
		t.Fatalf("发布方短剧未自动归入授权状态栏: %+v %v", ownerPage, err)
	}
	count, err := store.QueueApplicationDistributions(ctx, app.ID, []string{drama.ID, "999"})
	if count != 0 || !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("不可用剧目不能触发全库授权 count=%d err=%v", count, err)
	}
	preflightRows, err := store.ListDistributions(ctx, drama.ID)
	if err != nil || len(preflightRows) != 0 {
		t.Fatalf("选择含不可用剧目时不应部分入队: %+v %v", preflightRows, err)
	}
	count, err = store.QueueApplicationDistributions(ctx, app.ID, []string{drama.ID})
	if err != nil || count != 1 {
		t.Fatalf("首次入队 count=%d err=%v", count, err)
	}
	rows, err := store.ListDistributions(ctx, drama.ID)
	if err != nil {
		t.Fatal(err)
	}
	var first operations.DramaDistribution
	for _, row := range rows {
		if row.ApplicationID == app.ID {
			first = row
		}
	}
	if !first.Enabled || !first.Desired || first.Status != "pending" {
		t.Fatalf("未生成待处理授权: %+v", first)
	}
	if page := list("tracked", ""); page.Total != 1 || page.Items[0].ID != drama.ID || page.Items[0].Status != "pending" {
		t.Fatalf("处理中剧目未进入状态栏: %+v", page)
	}
	if page := list("available", ""); page.Total != 1 || page.Items[0].ID != other.ID {
		t.Fatalf("未授权栏未移除处理中剧目: %+v", page)
	}
	otherRows, err := store.ListDistributions(ctx, other.ID)
	if err != nil || len(otherRows) != 0 {
		t.Fatalf("未选中的短剧被加入授权队列: %+v %v", otherRows, err)
	}
	count, err = store.QueueApplicationDistributions(ctx, app.ID, []string{drama.ID})
	if err != nil || count != 0 {
		t.Fatalf("重复点击不应重置任务 count=%d err=%v", count, err)
	}
	rows, err = store.ListDistributions(ctx, drama.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ApplicationID == app.ID && row.Revision != first.Revision {
			t.Fatal("重复点击改变了在途任务修订号")
		}
	}
	job, err := store.ClaimDistribution(ctx, "bulk-worker", time.Now().UTC())
	if err != nil || job == nil || job.ApplicationID != app.ID {
		t.Fatalf("未领取目标任务: %+v %v", job, err)
	}
	if err := store.FinishDistribution(ctx, *job, nil); err != nil {
		t.Fatal(err)
	}
	if page := list("tracked", ""); page.Total != 1 || page.Items[0].Status != "authorized" {
		t.Fatalf("平台确认结果未显示已授权: %+v", page)
	}
	count, err = store.QueueApplicationDistributions(ctx, app.ID, []string{drama.ID})
	if err != nil || count != 0 {
		t.Fatalf("已授权不应重新入队 count=%d err=%v", count, err)
	}
	if err := store.SetDistribution(ctx, drama.ID, app.ID, false); err != nil {
		t.Fatal(err)
	}
	if page := list("available", ""); page.Total != 2 {
		t.Fatalf("撤权后应回到未授权栏: %+v", page)
	}
	count, err = store.QueueApplicationDistributions(ctx, app.ID, []string{drama.ID})
	if err != nil || count != 1 {
		t.Fatalf("撤权后再次点击应重新授权 count=%d err=%v", count, err)
	}
}

// syncDistributionFixtureProvider 只模拟同步授权调用，确保集成测试不会访问平台。
type syncDistributionFixtureProvider struct{ operations.TikTokMediaProvider }

func (*syncDistributionFixtureProvider) AuthorizeAlbum(context.Context, string, []string) error {
	return nil
}
func (*syncDistributionFixtureProvider) RevokeAlbum(context.Context, string, []string) error {
	return nil
}
