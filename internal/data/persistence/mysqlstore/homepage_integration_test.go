package mysqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
)

// TestHomepageHTTPIntegration 连续验证后台配置到公开首页、人工热播，并使用真实隔离 MySQL。
func TestHomepageHTTPIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithSubject(context.Background(), "1")
	repo := f.store
	home := homepage.NewService(repo, f.store, f.store)
	server := httptest.NewServer(f.handler(t, home))
	defer server.Close()
	base := server.URL
	var admin struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(iaaRequest(t, base, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200), &admin); err != nil {
		t.Fatal(err)
	}

	for i := 202; i <= 225; i++ {
		id := fmt.Sprint(i)
		drama := consumer.Drama{ID: id, Title: "首页联调短剧 " + id, Description: "隔离测试", CoverURL: "", Status: "completed", PublishStatus: "published", OwnerApplicationID: "101", TagIDs: []string{"23"}, Episodes: []consumer.Episode{{ID: fmt.Sprint(i * 10), DramaID: id, Number: 1, PosterURL: "/first-" + id + ".jpg"}}}
		published := drama
		drama.Published = &published
		if err := f.store.SaveDrama(ctx, drama); err != nil {
			t.Fatal(err)
		}
		rawSnapshot, _ := json.Marshal(published)
		if err := seedOfficialProjection(f.store.data.DB().WithContext(ctx), drama.ID, rawSnapshot); err != nil {
			t.Fatal(err)
		}
	}
	// 未登录与无效输入不能保存；全局管理不再要求应用头。
	iaaRequest(t, base, "PUT", "/admin/v1/carousel", "", "101", homepage.Carousel{DramaIDs: []string{"201"}}, 401)
	iaaRequest(t, base, "GET", "/admin/v1/carousel", admin.Token, "", nil, 200)
	iaaRequest(t, base, "PUT", "/admin/v1/carousel", admin.Token, "101", homepage.Carousel{DramaIDs: []string{"201", "201"}}, 400)
	iaaRequest(t, base, "PUT", "/admin/v1/carousel", admin.Token, "101", homepage.Carousel{DramaIDs: []string{"999999"}}, 404)
	iaaRequest(t, base, "PUT", "/admin/v1/carousel", admin.Token, "101", homepage.Carousel{DramaIDs: []string{"202", "201"}}, 200)
	iaaRequest(t, base, "PUT", "/admin/v1/carousel", admin.Token, "101", homepage.Carousel{DramaIDs: []string{"201"}}, 409)
	var settings homepage.CarouselView
	if err := json.Unmarshal(iaaRequest(t, base, "GET", "/admin/v1/carousel", admin.Token, "101", nil, 200), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Revision != 1 || len(settings.DramaIDs) != 2 || settings.DramaIDs[0] != "202" {
		t.Fatal("配置没有持久读回")
	}
	assertHomepageReadOnlyPermission(t, f, base)
	var foreign homepage.CarouselView
	if err := json.Unmarshal(iaaRequest(t, base, "GET", "/admin/v1/carousel", admin.Token, "102", nil, 200), &foreign); err != nil {
		t.Fatal(err)
	}
	if len(foreign.DramaIDs) != 2 || foreign.Revision != settings.Revision {
		t.Fatal("两个应用未共享轮播")
	}
	iaaRequest(t, base, "PUT", "/admin/v1/hot-dramas", admin.Token, "101", homepage.Carousel{Items: []homepage.CarouselItem{{DramaID: "225", Title: "人工标题", Visible: true}, {DramaID: "224", Visible: false}, {DramaID: "202", Visible: true}}}, 200)
	iaaRequest(t, base, "PUT", "/admin/v1/hot-dramas", admin.Token, "101", homepage.Carousel{DramaIDs: []string{}}, 409)
	sections := publicHomepageSections(t, base, "101")
	if len(sections.Carousel) != 2 || sections.Carousel[0].ID != "202" || len(sections.Hot) != 2 || sections.Hot[0].ID != "225" || sections.Hot[0].Title != "人工标题" || sections.Hot[1].ID != "202" {
		t.Fatal("公开首页未按人工配置返回有效内容")
	}
	if sections.Carousel[0].FirstEpisodePosterURL != "/first-202.jpg" || sections.Hot[0].FirstEpisodePosterURL != "/first-225.jpg" {
		t.Fatal("首页未返回首集首帧图")
	}
	// 全局配置相同，消费端仍按当前应用授权过滤。
	recipient := publicHomepageSections(t, base, "102")
	if len(recipient.Carousel) != 1 || recipient.Carousel[0].ID != "201" || len(recipient.Hot) != 0 {
		t.Fatal("共享配置泄漏未授权短剧")
	}
	authorized, err := f.store.FindDrama(ctx, "202")
	if err != nil {
		t.Fatal(err)
	}
	authorized.Published.AuthorizedApplicationIDs = []string{"102"}
	// 此处准备官方授权后的快照；授权事务保持排序的行为由分类生命周期用例覆盖。
	raw, err := json.Marshal(authorized.Published)
	if err != nil {
		t.Fatal(err)
	}
	if err := seedOfficialProjection(f.store.data.DB(), "202", raw); err != nil {
		t.Fatal(err)
	}
	recipient = publicHomepageSections(t, base, "102")
	if len(recipient.Carousel) != 2 || recipient.Carousel[0].ID != "202" || recipient.Carousel[1].ID != "201" || len(recipient.Hot) != 1 || recipient.Hot[0].ID != "202" {
		t.Fatal("共享配置未按授权展示", recipient)
	}
	// 无应用上下文不能通过空值意外获取全局目录。
	absent, err := f.store.FindVisibleSummaries(ctx, []string{"201", "202"})
	if err != nil || len(absent) != 0 {
		t.Fatal("空应用上下文绕过了授权", err)
	}
	// 访问统计路由已移除。
	removed, err := http.Post(base+"/api/v1/dramas/201/visits", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	removed.Body.Close()
	if removed.StatusCode != http.StatusNotFound {
		t.Fatal("统计接口仍存在")
	}
	// More Drama 独立分页，包含已经出现在轮播与热门中的所有可见剧目。
	ids := map[string]bool{}
	cursor := ""
	for {
		var page struct {
			Dramas     []consumer.DramaSummary `json:"dramas"`
			NextCursor string                  `json:"nextCursor"`
		}
		if err := json.Unmarshal(iaaRequest(t, base, "GET", "/api/v1/open/catalog?tiktokAppId=101&limit=7&cursor="+cursor, "", "", nil, 200), &page); err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Dramas {
			if ids[item.ID] {
				t.Fatal("分页重复")
			}
			ids[item.ID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(ids) != 25 || !ids["202"] || !ids["225"] {
		t.Fatal("全部目录被推荐位错误去重")
	}
	// 上线快照移除后，不论 Redis 保留什么人工配置，都不再展示。
	if err := seedOfficialProjection(f.store.data.DB().WithContext(ctx), "202", nil); err != nil {
		t.Fatal(err)
	}
	sections = publicHomepageSections(t, base, "101")
	if len(sections.Carousel) != 1 || sections.Carousel[0].ID != "201" {
		t.Fatal("下架轮播未消失")
	}
}

// publicHomepageSections 按新公开合同分别读取三个区域，测试端聚合仅用于断言跨区域配置。
func publicHomepageSections(t *testing.T, base, appID string) homepage.Sections {
	t.Helper()
	result := homepage.Sections{}
	for _, section := range []struct {
		path  string
		value any
	}{
		{"carousel", &result.Carousel},
		{"hot", &result.Hot},
		{"rankings", &result.Rankings},
	} {
		if err := json.Unmarshal(iaaRequest(t, base, "GET", "/api/v1/open/home/"+section.path+"?tiktokAppId="+appID, "", "", nil, 200), section.value); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

// assertHomepageReadOnlyPermission 验证普通角色可读取轮播，但直接调用写接口仍被拒绝。
func assertHomepageReadOnlyPermission(t *testing.T, f *iaaFixture, base string) {
	t.Helper()
	ctx := application.WithSubject(context.Background(), "1")
	rbac := adminrbac.NewService(f.store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")})
	user, err := rbac.CreateUser(ctx, adminrbac.UserCreateInput{Username: "carousel-reader", Password: "LocalReader123!", Nickname: "轮播只读验收", Status: adminrbac.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	role, err := rbac.CreateRole(ctx, adminrbac.RoleInput{Code: "carousel-reader", Name: "轮播只读", Status: adminrbac.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	permissions, err := f.store.ListAdminPermissions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, permission := range permissions {
		if permission.Code == "menu.carousel" || permission.Code == adminrbac.PermissionCarouselRead || permission.Code == "menu.hot" || permission.Code == adminrbac.PermissionHotRead {
			ids = append(ids, permission.ID)
		}
	}
	if err := rbac.SetRolePermissions(ctx, role.ID, ids); err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReplaceAdminUserRoles(ctx, user.ID, []string{role.ID}); err != nil {
		t.Fatal(err)
	}
	login, err := rbac.Login(ctx, "carousel-reader", "LocalReader123!")
	if err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, base, "GET", "/admin/v1/hot-dramas", login.Token, "101", nil, 200)
	iaaRequest(t, base, "PUT", "/admin/v1/hot-dramas", login.Token, "101", homepage.Carousel{DramaIDs: []string{}}, 403)
	iaaRequest(t, base, "GET", "/admin/v1/carousel", login.Token, "101", nil, 200)
	iaaRequest(t, base, "GET", "/admin/v1/carousel/targets", login.Token, "101", nil, 200)
	iaaRequest(t, base, "PUT", "/admin/v1/carousel", login.Token, "101", homepage.Carousel{Revision: 1, DramaIDs: []string{}}, 403)
}

// TestHomepageFirstPosterIntegration 验证首集图片补齐及编辑区换片不会污染线上摘要。
func TestHomepageFirstPosterIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithContext(application.WithSubject(context.Background(), "1"), "101")
	online := consumer.Drama{ID: "9901", Title: "首帧测试", Status: "completed", PublishStatus: "published", OwnerApplicationID: "101", TagIDs: []string{}, Episodes: []consumer.Episode{{ID: "99011", DramaID: "9901", Number: 1, VID: "online-vid"}}}
	draft := online
	draft.Published = &online
	if err := f.store.SaveDrama(ctx, draft); err != nil {
		t.Fatal(err)
	}
	rawSnapshot, _ := json.Marshal(online)
	if err := seedOfficialProjection(f.store.data.DB().WithContext(ctx), draft.ID, rawSnapshot); err != nil {
		t.Fatal(err)
	}
	assertPoster := func(want string) {
		t.Helper()
		summaries, err := f.store.FindVisibleSummaries(ctx, []string{"9901"})
		if err != nil || len(summaries) != 1 {
			t.Fatalf("query failed: %v, count=%d", err, len(summaries))
		}
		if summaries[0].FirstEpisodeID != "99011" || summaries[0].FirstEpisodePosterURL != want {
			t.Fatalf("unexpected first episode poster: %+v", summaries[0])
		}
	}
	assertPoster("")
	if err := f.store.data.DB().Create(&episodeModel{ID: "99011", DramaID: "9901", Number: 1, VID: "online-vid"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().Model(&episodeModel{}).Where("id = ?", "99011").Update("poster_url", "/generated-first.jpg").Error; err != nil {
		t.Fatal(err)
	}
	assertPoster("/generated-first.jpg")
	if err := f.store.data.DB().Model(&episodeModel{}).Where("id = ?", "99011").Updates(map[string]any{"vid": "draft-vid", "poster_url": "/draft.jpg"}).Error; err != nil {
		t.Fatal(err)
	}
	assertPoster("")
}
