package mysqlstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/subject"
)

// subjectAdminHTTP 通过实际 Handler 验证指定主体或缺失主体的管理请求，不输出凭据与响应正文。
func subjectAdminHTTP(t *testing.T, handler http.Handler, method, path, token, subjectID string, input any, status int) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if subjectID != "" {
		req.Header.Set("X-Subject-ID", subjectID)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != status {
		t.Fatalf("主体管理请求 %s %s HTTP=%d，期望=%d", method, path, recorder.Code, status)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

// TestSubjectDirectContentIsolationIntegration 贯通两主体、同主体多应用的上架、推荐、分类、目录和旧 Mini 播放门禁。
func TestSubjectDirectContentIsolationIntegration(t *testing.T) {
	f := newIAAFixture(t)
	repo, a := seedDirect(t, f)
	bSubject, err := subject.NewService(f.store).Save(t.Context(), "", subject.Input{Name: "独立运营主体 B"})
	if err != nil {
		t.Fatal(err)
	}
	apps := []application.Application{
		{ID: "103", SubjectID: bSubject.ID, TikTokAppID: "000103", Name: "主体 B 直连", ClientKey: "fixture-direct-b", ClientSecret: "fixture-secret", Status: "active", MonetizationMode: "IAP", PlaybackProvider: "byteplus_web", TimeZone: "UTC"},
		{ID: "104", SubjectID: "1", TikTokAppID: "000104", Name: "主体 A 另一个应用", ClientKey: "fixture-direct-a2", ClientSecret: "fixture-secret", Status: "active", MonetizationMode: "IAP", PlaybackProvider: "byteplus_web", TimeZone: "UTC"},
	}
	for i := range apps {
		if err := f.store.CreateApplication(t.Context(), &apps[i]); err != nil {
			t.Fatal(err)
		}
	}
	b := application.ContextForApplication(t.Context(), apps[0])
	a2 := application.ContextForApplication(t.Context(), apps[1])
	signer := &directHTTPPlayback{}
	f.signer = signer
	f.direct = directplay.NewService(repo, nil)
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	login := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var admin adminrbac.LoginResult
	if err := json.Unmarshal(login, &admin); err != nil {
		t.Fatal(err)
	}
	handler := f.handler(t)
	// HTTP 管理入口必须明确主体，但没有发布方凭据的直连主体仍可独立运营。
	for _, path := range []string{"/admin/v1/direct/dramas", "/admin/v1/direct/carousel", "/admin/v1/direct/hot-dramas", "/admin/v1/direct/category-rankings", "/admin/v1/carousel", "/admin/v1/hot-dramas", "/admin/v1/category-rankings"} {
		for _, sid := range []string{"", "999999", "invalid"} {
			subjectAdminHTTP(t, handler, "GET", path, admin.Token, sid, nil, 400)
		}
		subjectAdminHTTP(t, handler, "GET", path, admin.Token, bSubject.ID, nil, 200)
	}
	command := directplay.Command{RequestID: "same-key-across-subjects", Kind: "publish"}
	first, err := repo.Accept(a, "201", command, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Workbench(b, "201")
	if !errors.Is(err, consumer.ErrNotFound) {
		t.Fatal("新主体继承了其他主体的发布状态", err)
	}
	if _, err := repo.Operation(b, first.ID); !errors.Is(err, consumer.ErrNotFound) {
		t.Fatal("跨主体操作结果可读", err)
	}
	subjectAdminHTTP(t, handler, "GET", "/admin/v1/direct/publication-operations/"+first.ID, admin.Token, bSubject.ID, nil, 404)
	tokenB, _ := iaaLogin(t, server.URL, "000103", "user-b")
	for _, path := range []string{"/api/v1/open/catalog?tiktokAppId=000103", "/api/v1/open/home/carousel?tiktokAppId=000103", "/api/v1/open/home/hot?tiktokAppId=000103", "/api/v1/open/home/rankings?tiktokAppId=000103"} {
		raw := iaaRequest(t, server.URL, "GET", path, "", "", nil, 200)
		if strings.Contains(path, "/catalog?") {
			var data struct {
				Dramas []consumer.DramaSummary `json:"dramas"`
			}
			if err := json.Unmarshal(raw, &data); err != nil || len(data.Dramas) != 0 {
				t.Fatal("公开更多目录继承其他主体", err)
			}
		} else {
			var rows []json.RawMessage
			if err := json.Unmarshal(raw, &rows); err != nil || len(rows) != 0 {
				t.Fatal("公开推荐继承其他主体", err)
			}
		}
	}
	page, err := f.store.DiscoverDramas(b, consumer.DiscoveryQuery{Limit: 20})
	if err != nil || len(page.Items) != 0 || len(page.Tags) != 0 {
		t.Fatal("更多或搜索泄漏其他主体目录", err)
	}
	iaaRequest(t, server.URL, "GET", "/api/v1/dramas/201", tokenB, "", nil, 404)
	beforeCalls := signer.calls
	iaaRequest(t, server.URL, "POST", "/api/v1/play/resolve", tokenB, "", map[string]string{"dramaId": "201", "episodeId": "301"}, 404)
	if signer.calls != beforeCalls {
		t.Fatal("跨主体播放签发了凭据")
	}
	home := homepage.NewService(f.store, f.store, f.store)
	ma, mb := homepage.WithManagementContext(a), homepage.WithManagementContext(b)
	for _, save := range []func(context.Context, homepage.Carousel) (homepage.CarouselView, error){home.SaveCarousel, home.SaveHot} {
		if _, err := save(ma, homepage.Carousel{DramaIDs: []string{"201"}, Items: []homepage.CarouselItem{{DramaID: "201", Title: "主体 A 推荐", Visible: true}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := save(mb, homepage.Carousel{DramaIDs: []string{"201"}, Items: []homepage.CarouselItem{{DramaID: "201", Visible: true}}}); err == nil {
			t.Fatal("推荐可以新增其他主体上线的剧")
		}
	}
	for _, get := range []func(context.Context) (homepage.Carousel, error){f.store.GetCarousel, f.store.GetHot} {
		c, err := get(b)
		if err != nil || c.Revision != 0 || len(c.DramaIDs) != 0 {
			t.Fatal("新主体自动继承旧推荐", err)
		}
	}
	// 两主体分别编辑自己的草稿，不共用视频或发布指针。
	draft, err := f.store.FindDrama(a, "201")
	if err != nil {
		t.Fatal(err)
	}
	bDraft := *draft
	bDraft.ID = ""
	bDraft.Title = "主体 B 的 V1"
	bDraft.TagIDs = []string{"22"}
	bDraft.Episodes = append([]consumer.Episode(nil), draft.Episodes...)
	for i := range bDraft.Episodes {
		bDraft.Episodes[i].ID = ""
		bDraft.Episodes[i].DramaID = ""
		bDraft.Episodes[i].VID = "fixture-b-vid"
		bDraft.Episodes[i].MediaSpaceName = "fixture-b-space"
	}
	if err := f.store.SaveDramaAggregate(b, &bDraft, nil); err != nil {
		t.Fatal(err)
	}
	second, err := repo.Accept(b, bDraft.ID, command, "fixture")
	if err != nil || first.ID == second.ID || first.ResultVersionID == second.ResultVersionID {
		t.Fatal("主体之间的幂等键或冻结版本串用", err)
	}
	for _, save := range []func(context.Context, homepage.Carousel) (homepage.CarouselView, error){home.SaveCarousel, home.SaveHot} {
		if _, err := save(mb, homepage.Carousel{DramaIDs: []string{bDraft.ID}, Items: []homepage.CarouselItem{{DramaID: bDraft.ID, Title: "主体 B 推荐", Visible: true}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []context.Context{a, a2, b} {
		page, err := f.store.DiscoverDramas(scope, consumer.DiscoveryQuery{Limit: 20})
		if err != nil || len(page.Items) != 1 {
			t.Fatal("主体目录或同主体共享失败", err)
		}
		expected := "Independent source"
		if application.SubjectIDFromContext(scope) == bSubject.ID {
			expected = "主体 B 的 V1"
		}
		if page.Items[0].Title != expected {
			t.Fatal("主体快照串用")
		}
		hot, err := home.HomeHot(scope)
		hotTitle := "主体 A 推荐"
		if application.SubjectIDFromContext(scope) == bSubject.ID {
			hotTitle = "主体 B 推荐"
		}
		if err != nil || len(hot) != 1 || hot[0].Title != hotTitle {
			t.Fatal("主体热播串用", err)
		}
		config, err := f.store.GetCarousel(scope)
		if err != nil || config.Revision != 1 || len(config.Items) != 1 {
			t.Fatal("配置修订或条目被其他主体覆盖", err)
		}
		title := "主体 A 推荐"
		if application.SubjectIDFromContext(scope) == bSubject.ID {
			title = "主体 B 推荐"
		}
		if config.Items[0].Title != title {
			t.Fatal("推荐运营字段串用")
		}
	}
	rankingsA, err := f.store.GetRankings(a)
	if err != nil || len(rankingsA["21"].Items) != 1 || len(rankingsA["22"].Items) != 0 {
		t.Fatal("主体 A 分类混入主体 B", err)
	}
	rankingsB, err := f.store.GetRankings(b)
	if err != nil || len(rankingsB["22"].Items) != 1 || len(rankingsB["21"].Items) != 0 {
		t.Fatal("主体 B 分类混入主体 A", err)
	}
	if _, err := f.store.ReplaceRanking(b, "22", homepage.Ranking{Revision: rankingsB["22"].Revision, Items: []homepage.RankingItem{{DramaID: bDraft.ID, Visible: false}}}); err != nil {
		t.Fatal(err)
	}
	rankingsA, err = f.store.GetRankings(a)
	if err != nil || !rankingsA["21"].Items[0].Visible {
		t.Fatal("隐藏分类影响其他主体", err)
	}
	draft.Title = "主体 A 的 V2"
	draft.TagIDs = []string{"21"}
	if err := f.store.SaveDrama(a, *draft); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.Accept(a, "201", directplay.Command{RequestID: "subject-a-version-two", Kind: "publish", ExpectedRevision: 1}, "fixture")
	if err != nil || updated.ResultVersionID == first.ResultVersionID {
		t.Fatal("主体 A 新版本未生成", err)
	}
	wb, err := repo.Workbench(b, bDraft.ID)
	if err != nil || len(wb.Versions) != 1 || wb.Versions[0].Number != 1 || wb.Publication.CurrentVersionID != second.ResultVersionID {
		t.Fatal("主体 A 更新影响主体 B 的版本选择", err)
	}

	ca, _ := f.store.GetCarousel(a)
	if _, err := f.store.ReplaceCarousel(a, homepage.Carousel{Revision: ca.Revision, DramaIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	ha, _ := f.store.GetHot(a)
	if _, err := f.store.ReplaceHot(a, homepage.Carousel{Revision: ha.Revision, DramaIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Accept(a, "201", directplay.Command{RequestID: "subject-a-only-offline", Kind: "offline", ExpectedRevision: 2}, "fixture"); err != nil {
		t.Fatal("其他主体推荐阻止本主体下架", err)
	}
	for _, scope := range []context.Context{a, a2} {
		page, err := f.store.DiscoverDramas(scope, consumer.DiscoveryQuery{Limit: 20})
		if err != nil || len(page.Items) != 0 {
			t.Fatal("同主体下架未生效", err)
		}
	}
	page, err = f.store.DiscoverDramas(b, consumer.DiscoveryQuery{Limit: 20})
	if err != nil || len(page.Items) != 1 || page.Items[0].Title != "主体 B 的 V1" {
		t.Fatal("下架影响其他主体目录", err)
	}
	iaaRequest(t, server.URL, "POST", "/api/v1/play/resolve", tokenB, "", map[string]string{"dramaId": bDraft.ID, "episodeId": bDraft.Episodes[0].ID}, 200)
	if err := f.store.DeleteDrama(a, "201"); !errors.Is(err, operations.ErrConflict) {
		t.Fatal("未保护本主体既有官方线上内容", err)
	}
	used, err := f.store.IsMediaResourceUsed(a, operations.MediaJob{ResourceClass: operations.MediaResourceVideo, RemoteVID: "fixture-b-vid", SpaceName: "fixture-b-space"})
	if err != nil || !used {
		t.Fatal("主体切换丢失全局媒资保护", err)
	}
}
