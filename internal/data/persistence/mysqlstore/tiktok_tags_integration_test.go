package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/subject"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCurrentSchemaIntegrationTikTokTagEnglish 验证真实 HTTP、数据库、线上快照和英文筛选一致。
func TestCurrentSchemaIntegrationTikTokTagEnglish(t *testing.T) {
	f := newIAAFixture(t)
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	var session struct {
		// Token 仅用于隔离测试鉴权，禁止写入日志。
		Token string `json:"token"`
	}
	if err := json.Unmarshal(iaaRequest(t, server.URL, http.MethodPost, "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200), &session); err != nil {
		t.Fatal(err)
	}
	ctx := application.WithContext(application.WithSubject(context.Background(), "1"), "1024")
	for _, id := range []string{"21", "22"} {
		online := domain.Drama{ID: "92" + id, Title: "translation fixture", PublishStatus: "published", OwnerApplicationID: "1024", TagIDs: []string{id}, Status: "completed"}
		draft := online
		draft.Published = &online
		if err := f.store.SaveDrama(ctx, draft); err != nil {
			t.Fatal(err)
		}
		rawSnapshot, _ := json.Marshal(online)
		if err := seedOfficialProjection(f.store.data.DB().WithContext(ctx), draft.ID, rawSnapshot); err != nil {
			t.Fatal(err)
		}
		iaaRequest(t, server.URL, http.MethodPut, "/admin/v1/tiktok-tags/"+id, session.Token, "", map[string]string{"englishName": "Mystery"}, 200)
	}
	// 两个官方标签可共用英文名，筛选不能只匹配第一个。
	page, err := f.store.DiscoverDramas(ctx, domain.DiscoveryQuery{Tag: "Mystery", Query: "mystery", Limit: 20})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("英文搜索筛选失败：%+v %v", page, err)
	}
	detail, err := domain.NewService(f.store, nil).FindDrama(ctx, "9221")
	if err != nil || detail.Tags[0] != "Mystery" {
		t.Fatalf("详情翻译失败：%v", err)
	}
	summaries, err := f.store.FindVisibleSummaries(ctx, []string{"9221"})
	if err != nil || len(summaries) != 1 || summaries[0].Tags[0] != "Mystery" {
		t.Fatalf("首页翻译失败：%v", err)
	}
	var tags []domain.TikTokTag
	if err := json.Unmarshal(iaaRequest(t, server.URL, http.MethodGet, "/admin/v1/tiktok-tags", session.Token, "", nil, 200), &tags); err != nil {
		t.Fatal(err)
	}
	if tags[20].EnglishName != "Mystery" || tags[20].OfficialName != "悬疑推理" {
		t.Fatal("数据库读回不一致")
	}
	iaaRequest(t, server.URL, http.MethodPut, "/admin/v1/tiktok-tags/21", session.Token, "", map[string]string{"englishName": ""}, 200)
	detail, err = domain.NewService(f.store, nil).FindDrama(ctx, "9221")
	if err != nil || detail.Tags[0] != "悬疑推理" {
		t.Fatal("清空未即时回退")
	}
}

// TestCurrentSchemaIntegrationTikTokTagDramaCounts 验证真实 HTTP 的主体内去重统计、编辑标签变更和删除，不以线上快照或应用关联重复计数。
func TestCurrentSchemaIntegrationTikTokTagDramaCounts(t *testing.T) {
	f := newIAAFixture(t)
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	var session struct {
		// Token 仅用于隔离测试鉴权，不写入日志。
		Token string `json:"token"`
	}
	if err := json.Unmarshal(iaaRequest(t, server.URL, http.MethodPost, "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200), &session); err != nil {
		t.Fatal(err)
	}
	ctx := application.WithSubject(context.Background(), "1")
	online := domain.Drama{ID: "9301", Title: "线上剧", PublishStatus: "published", OwnerApplicationID: "101", AuthorizedApplicationIDs: []string{"101", "102"}, TagIDs: []string{"21"}, Status: "completed"}
	edited := online
	edited.TagIDs = []string{"22", "22"}
	edited.Published = &online
	draft := domain.Drama{ID: "9302", Title: "未上线剧", PublishStatus: "draft", OwnerApplicationID: "102", TagIDs: []string{"21", "22"}}
	for _, drama := range []domain.Drama{edited, draft, {ID: "9303", Title: "无标签剧", PublishStatus: "draft"}} {
		if err := f.store.SaveDrama(ctx, drama); err != nil {
			t.Fatal(err)
		}
	}
	rawSnapshot, _ := json.Marshal(online)
	if err := seedOfficialProjection(f.store.data.DB().WithContext(ctx), edited.ID, rawSnapshot); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]int64 {
		t.Helper()
		var tags []operations.TikTokTagSummary
		if err := json.Unmarshal(iaaRequest(t, server.URL, http.MethodGet, "/admin/v1/tiktok-tags", session.Token, "", nil, 200), &tags); err != nil {
			t.Fatal(err)
		}
		if len(tags) != 45 {
			t.Fatal("官方目录不完整")
		}
		counts := map[string]int64{}
		for _, tag := range tags {
			counts[tag.ID] = tag.DramaCount
		}
		return counts
	}
	if counts := read(); counts["21"] != 1 || counts["22"] != 2 || counts["23"] != 0 {
		t.Fatalf("关联统计错误：%v", counts)
	}
	iaaRequest(t, server.URL, http.MethodPut, "/admin/v1/tiktok-tags/22", session.Token, "", map[string]string{"englishName": "Fantasy"}, 200)
	if counts := read(); counts["22"] != 2 {
		t.Fatal("翻译修改影响关联数")
	}
	draft.TagIDs = []string{"23"}
	if err := f.store.SaveDrama(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if counts := read(); counts["21"] != 0 || counts["22"] != 1 || counts["23"] != 1 {
		t.Fatal("标签变更后统计未更新")
	}
	if err := f.store.DeleteDrama(ctx, draft.ID); err != nil {
		t.Fatal(err)
	}
	if counts := read(); counts["23"] != 0 {
		t.Fatal("删除后统计未更新")
	}
	stored, err := f.store.FindDrama(ctx, edited.ID)
	if err != nil || stored.Published == nil || len(stored.Published.TagIDs) != 1 || stored.Published.TagIDs[0] != "21" {
		t.Fatal("读取统计或翻译修改影响线上快照")
	}
}

// TestTikTokTagSubjectIsolationIntegration 验证标签目录仍共用，关联数量、编辑和删除按主体隔离，无主体请求拒绝读取。
func TestTikTokTagSubjectIsolationIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := t.Context()
	subjects := subject.NewService(f.store)
	b, err := subjects.Save(ctx, "", subject.Input{Name: "标签隔离主体 B"})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := subjects.Save(ctx, "", subject.Input{Name: "标签隔离空主体"})
	if err != nil {
		t.Fatal(err)
	}
	aContext := application.WithSubject(ctx, "1")
	bContext := application.WithSubject(ctx, b.ID)
	for _, item := range []struct {
		scope context.Context
		drama domain.Drama
	}{
		{aContext, domain.Drama{ID: "9501", Title: "主体 A", TagIDs: []string{"21", "21"}}},
		{bContext, domain.Drama{ID: "9502", Title: "主体 B 第一部", TagIDs: []string{"21", "22"}}},
		{bContext, domain.Drama{ID: "9503", Title: "主体 B 第二部", TagIDs: []string{"21"}}},
	} {
		if err := f.store.SaveDrama(item.scope, item.drama); err != nil {
			t.Fatal(err)
		}
	}
	handler := f.handler(t)
	var login struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(subjectAdminHTTP(t, handler, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200), &login); err != nil {
		t.Fatal(err)
	}
	subjectAdminHTTP(t, handler, "PUT", "/admin/v1/tiktok-tags/21", login.Token, "", map[string]string{"englishName": "Shared Mystery"}, 200)
	read := func(subjectID string) map[string]int64 {
		t.Helper()
		var tags []operations.TikTokTagSummary
		if err := json.Unmarshal(subjectAdminHTTP(t, handler, "GET", "/admin/v1/tiktok-tags", login.Token, subjectID, nil, 200), &tags); err != nil {
			t.Fatal(err)
		}
		if len(tags) != 45 {
			t.Fatal("主体切换改变了全局标签目录")
		}
		counts := map[string]int64{}
		for _, tag := range tags {
			counts[tag.ID] = tag.DramaCount
			if tag.ID == "21" && tag.EnglishName != "Shared Mystery" {
				t.Fatal("全局翻译被主体分区")
			}
		}
		return counts
	}
	if counts := read("1"); counts["21"] != 1 || counts["22"] != 0 {
		t.Fatal("主体 A 计入其他主体的关联", counts)
	}
	if counts := read(b.ID); counts["21"] != 2 || counts["22"] != 1 {
		t.Fatal("主体 B 关联统计错误", counts)
	}
	for _, count := range read(empty.ID) {
		if count != 0 {
			t.Fatal("空主体读取到其他主体关联")
		}
	}
	subjectAdminHTTP(t, handler, "GET", "/admin/v1/tiktok-tags", login.Token, "", nil, 400)
	subjectAdminHTTP(t, handler, "GET", "/admin/v1/tiktok-tags", login.Token, "999999", nil, 400)
	if counts, err := f.store.CountTikTokTagDramas(ctx); err != nil || len(counts) != 0 {
		t.Fatal("仓储缺少主体时回退全局", err, counts)
	}
	if err := f.store.DeleteDrama(bContext, "9503"); err != nil {
		t.Fatal(err)
	}
	if counts := read(b.ID); counts["21"] != 1 {
		t.Fatal("删除后的主体 B 统计未更新", counts)
	}
	if counts := read("1"); counts["21"] != 1 {
		t.Fatal("主体 B 删除影响主体 A", counts)
	}
}
