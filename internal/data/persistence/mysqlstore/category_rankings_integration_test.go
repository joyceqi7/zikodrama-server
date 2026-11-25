package mysqlstore

import (
	"context"
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
)

// seedCategoryRankingDramas 使用真实发布快照准备两个官方标签，含相同英文译名。
func seedCategoryRankingDramas(t *testing.T, f *iaaFixture) {
	t.Helper()
	ctx := application.WithSubject(context.Background(), "1")
	for i := 202; i <= 205; i++ {
		id := fmt.Sprint(i)
		tag := "21"
		owner := "101"
		if i == 204 {
			tag = "22"
		}
		if i == 205 {
			owner = "102"
		}
		drama := consumer.Drama{ID: id, Title: "Ranking Drama " + id, Description: "分类榜隔离测试", Status: "completed", PublishStatus: "published", OwnerApplicationID: owner, TagIDs: []string{tag}, Episodes: []consumer.Episode{{ID: fmt.Sprint(i * 10), DramaID: id, Number: 1}}}
		published := drama
		drama.Published = &published
		if err := f.store.SaveDrama(ctx, drama); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(published)
		if err != nil {
			t.Fatal(err)
		}
		if err := seedOfficialProjection(f.store.data.DB().WithContext(ctx), drama.ID, raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"21", "22"} {
		if err := f.store.SaveTikTokTagEnglishName(ctx, id, "Mystery"); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCategoryRankingHTTPIntegration 验证上线剧自动入榜、共享配置与应用可见性、排序隐藏移除恢复及公开首页。
func TestCategoryRankingHTTPIntegration(t *testing.T) {
	f := newIAAFixture(t)
	seedCategoryRankingDramas(t, f)
	home := homepage.NewService(f.store, f.store, f.store)
	server := httptest.NewServer(f.handler(t, home))
	defer server.Close()
	base := server.URL
	var admin struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(iaaRequest(t, base, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200), &admin); err != nil {
		t.Fatal(err)
	}
	path := "/admin/v1/category-rankings/21"
	read := func(app string) homepage.RankingView {
		t.Helper()
		var value homepage.RankingView
		if err := json.Unmarshal(iaaRequest(t, base, "GET", path, admin.Token, app, nil, 200), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	save := func(input homepage.Ranking) homepage.RankingView {
		t.Helper()
		var value homepage.RankingView
		if err := json.Unmarshal(iaaRequest(t, base, "PUT", path, admin.Token, "101", input, 200), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	public := func() homepage.Sections {
		t.Helper()
		return publicHomepageSections(t, base, "101")
	}
	iaaRequest(t, base, "GET", path, "", "101", nil, 401)
	iaaRequest(t, base, "GET", path, admin.Token, "", nil, 200)
	iaaRequest(t, base, "GET", "/admin/v1/category-rankings/99999", admin.Token, "101", nil, 404)
	initial := read("101")
	if len(initial.Items) != 3 || initial.Items[0].DramaID != "202" || initial.Items[1].DramaID != "203" || len(initial.Dramas) != 3 {
		t.Fatal("已上线剧没有自动按标签入榜", initial)
	}
	foreign := read("102")
	if len(foreign.Items) != 3 || foreign.Revision != initial.Revision {
		t.Fatal("管理端未共享全局榜单")
	}
	recipient := publicHomepageSections(t, base, "102")
	if len(recipient.Rankings) != 1 || len(recipient.Rankings[0].Dramas) != 1 || recipient.Rankings[0].Dramas[0].ID != "205" {
		t.Fatal("共享榜单泄漏未授权短剧", recipient)
	}
	if len(public().Rankings) != 2 {
		t.Fatal("无需手动保存即可展示两类榜单，同译名不能合并")
	}
	for _, input := range []any{map[string]any{}, homepage.Ranking{}, homepage.Ranking{Revision: -1, Items: []homepage.RankingItem{}}, homepage.Ranking{Items: []homepage.RankingItem{{DramaID: "202"}, {DramaID: "202"}}}} {
		iaaRequest(t, base, "PUT", path, admin.Token, "101", input, 400)
	}
	for _, id := range []string{"204", "999999"} {
		iaaRequest(t, base, "PUT", path, admin.Token, "101", homepage.Ranking{Revision: initial.Revision, Items: []homepage.RankingItem{{DramaID: id, Visible: true}}}, 404)
	}
	input := homepage.Ranking{Revision: initial.Revision, Items: []homepage.RankingItem{{DramaID: "203", Visible: true}, {DramaID: "202", Visible: false}}}
	saved := save(input)
	iaaRequest(t, base, "PUT", path, admin.Token, "101", input, 409)
	if read("101").Items[0].DramaID != "203" || len(public().Rankings[0].Dramas) != 1 || public().Rankings[0].Dramas[0].ID != "203" {
		t.Fatal("排序或隐藏未持久化")
	}
	// 省略成员仅转为隐藏，仍保留关联和排序。
	saved = save(homepage.Ranking{Revision: saved.Revision, Items: []homepage.RankingItem{{DramaID: "202", Visible: true}}})
	if len(saved.Items) != 3 || saved.Items[1].Visible || read("101").Items[1].DramaID != "203" {
		t.Fatal("隐藏记录未保留")
	}
	if public().Rankings[0].Dramas[0].ID != "202" {
		t.Fatal("隐藏成员仍展示")
	}
	var categories []homepage.RankingCategory
	if err := json.Unmarshal(iaaRequest(t, base, "GET", "/admin/v1/category-rankings", admin.Token, "101", nil, 200), &categories); err != nil {
		t.Fatal(err)
	}
	for _, category := range categories {
		if category.Tag.ID == "21" && category.Count != 3 {
			t.Fatal("数量未包含隐藏项")
		}
	}
	saved.Items[1].Visible = true
	saved = save(saved.Ranking)
	if len(public().Rankings[0].Dramas) != 2 {
		t.Fatal("恢复没有生效")
	}
	if err := f.store.SaveTikTokTagEnglishName(context.Background(), "21", "Suspense"); err != nil {
		t.Fatal(err)
	}
	if public().Rankings[0].Name != "Suspense" {
		t.Fatal("英文翻译未联动")
	}
	saved = save(homepage.Ranking{Revision: saved.Revision, Items: []homepage.RankingItem{}})
	if len(public().Rankings) != 1 || len(read("101").Items) != 3 {
		t.Fatal("隐藏全部成员后错误展示或丢失关联")
	}
	if len(read("102").Items) != 3 {
		t.Fatal("共享配置条目丢失")
	}
	assertRankingReadOnly(t, f, base)
}

func assertRankingReadOnly(t *testing.T, f *iaaFixture, base string) {
	t.Helper()
	ctx := application.WithSubject(context.Background(), "1")
	rbac := adminrbac.NewService(f.store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")})
	user, err := rbac.CreateUser(ctx, adminrbac.UserCreateInput{Username: "ranking-reader", Password: "LocalReader123!", Nickname: "分类只读", Status: adminrbac.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	role, err := rbac.CreateRole(ctx, adminrbac.RoleInput{Code: "ranking-reader", Name: "分类只读", Status: adminrbac.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	permissions, err := f.store.ListAdminPermissions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, permission := range permissions {
		if permission.Code == "menu.categoryrankings" || permission.Code == adminrbac.PermissionRankingRead {
			ids = append(ids, permission.ID)
		}
	}
	if len(ids) != 2 {
		t.Fatal("分类权限种子缺失")
	}
	if err = rbac.SetRolePermissions(ctx, role.ID, ids); err != nil {
		t.Fatal(err)
	}
	if err = f.store.ReplaceAdminUserRoles(ctx, user.ID, []string{role.ID}); err != nil {
		t.Fatal(err)
	}
	login, err := rbac.Login(ctx, "ranking-reader", "LocalReader123!")
	if err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, base, "GET", "/admin/v1/category-rankings", login.Token, "101", nil, 200)
	iaaRequest(t, base, "GET", "/admin/v1/category-rankings/21/targets", login.Token, "101", nil, 200)
	iaaRequest(t, base, "PUT", "/admin/v1/category-rankings/21", login.Token, "101", homepage.Ranking{Revision: 3, Items: []homepage.RankingItem{}}, 403)
}
