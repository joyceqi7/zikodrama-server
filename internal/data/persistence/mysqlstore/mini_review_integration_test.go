package mysqlstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	biz "drama-server/internal/biz"
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func TestMiniReviewHTTPIntegration(t *testing.T) {
	f := newIAAFixture(t)
	defer f.store.data.pool.Close()
	ctx := application.WithSubject(application.WithContext(context.Background(), "101"), "1")
	// 两部已上线内容同时进入普通首页，只有第一部进入审核清单。
	for _, id := range []string{"201", "202"} {
		drama := domain.Drama{ID: id, Title: "Review Drama " + id, TagIDs: []string{"21"}, Status: domain.DramaStatusCompleted, PublishStatus: "published", OwnerApplicationID: "101", AuthorizedApplicationIDs: []string{"102"}, PlatformAlbumID: "album-" + id, PlatformVersion: 1}
		for n := 1; n <= 3; n++ {
			drama.Episodes = append(drama.Episodes, domain.Episode{ID: fmt.Sprintf("%s%d", id, n), DramaID: id, Number: n, Title: "Episode", MediaStatus: "ready", VID: fmt.Sprintf("vid-%s-%d", id, n), PlatformEpisodeID: fmt.Sprintf("ep-%s-%d", id, n)})
		}
		published := drama
		drama.Published = &published
		if err := f.store.SaveDrama(ctx, drama); err != nil {
			t.Fatal(err)
		}
		for _, ep := range drama.Episodes {
			if err := f.store.SaveEpisode(ctx, ep); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.store.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := saveSubjectState(tx, id, drama.PlatformAlbumID, &published); err != nil {
				return err
			}
			raw, err := json.Marshal(published)
			if err != nil {
				return err
			}
			return syncCategoryRankings(tx, id, nil, raw)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.ReplaceCarousel(ctx, homepage.Carousel{DramaIDs: []string{"201", "202"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ReplaceHot(ctx, homepage.Carousel{DramaIDs: []string{"202", "201"}}); err != nil {
		t.Fatal(err)
	}
	strategy, err := f.store.GetApplicationStrategy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	doc := strategy.Strategy.Document
	doc.Rules = append(doc.Rules,
		monetization.UnlockRule{Code: "review_entry", Type: "entry_rewarded_ad", Enabled: true, Config: json.RawMessage(`{"requiredAdCount":1}`)},
		monetization.UnlockRule{Code: "review_vip", Type: "rewarded_ad", Enabled: true, Config: json.RawMessage(`{"requiredAdCount":2}`), Grants: []monetization.GrantTemplate{{Type: "vip", Config: json.RawMessage(`{"scope":"application","adFree":true,"validity":"day"}`)}}},
	)
	if _, err := f.store.SaveApplicationStrategy(ctx, monetization.ApplicationStrategyInput{Document: doc}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	call := func(method, path, token, app, version string, body any, status int) json.RawMessage {
		t.Helper()
		var data bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&data).Encode(body); err != nil {
				t.Fatal(err)
			}
		}
		req, _ := http.NewRequest(method, server.URL+path, &data)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-Subject-ID", "1")
		}
		if app != "" {
			req.Header.Set("X-Application-ID", app)
		}
		if version != "" {
			req.Header.Set("X-Mini-Version", version)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var envelope struct {
			Data    json.RawMessage `json:"data"`
			Message string          `json:"message"`
		}
		if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status {
			t.Fatalf("%s %s: status=%d want=%d message=%s", method, path, response.StatusCode, status, envelope.Message)
		}
		return envelope.Data
	}
	var admin struct {
		Token string `json:"token"`
	}
	json.Unmarshal(call("POST", "/admin/v1/session", "", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200), &admin)
	// 清单权限独立于应用编辑权限，普通角色不会因父目录或应用权限获得修改能力。
	hash, err := bcrypt.GenerateFromPassword([]byte("ReviewLocal123!"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		codes      []string
		readStatus int
	}{
		{"review-reader", []string{"directory.whitelist", "menu.official_review_list", "operations.official_review_list.read"}, 200},
		{"application-editor", []string{"applications.update"}, 403},
	} {
		role := adminrbac.Role{Name: tc.name, Code: tc.name, Status: adminrbac.StatusActive}
		if err := f.store.CreateAdminRole(ctx, &role); err != nil {
			t.Fatal(err)
		}
		var ids []string
		if err := f.store.data.DB().Table("admin_permissions").Where("code IN ?", tc.codes).Pluck("id", &ids).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.store.ReplaceAdminRolePermissions(ctx, role.ID, ids); err != nil {
			t.Fatal(err)
		}
		user := adminrbac.User{Username: tc.name, Nickname: tc.name, PasswordHash: string(hash), Status: adminrbac.StatusActive, AuthVersion: 1}
		if err := f.store.CreateAdminUser(ctx, &user); err != nil {
			t.Fatal(err)
		}
		if err := f.store.ReplaceAdminUserRoles(ctx, user.ID, []string{role.ID}); err != nil {
			t.Fatal(err)
		}
		var login struct {
			Token string `json:"token"`
		}
		json.Unmarshal(call("POST", "/admin/v1/session", "", "", "", map[string]string{"username": tc.name, "password": "ReviewLocal123!"}, 200), &login)
		call("GET", "/admin/v1/official/review-list", login.Token, "101", "", nil, tc.readStatus)
		call("GET", "/admin/v1/official/review-list/targets", login.Token, "101", "", nil, tc.readStatus)
		call("PUT", "/admin/v1/official/review-list", login.Token, "101", "", map[string]any{"revision": 0, "dramaIds": []string{"201"}}, 403)
	}
	var session biz.ConsumerSessionResult
	json.Unmarshal(call("POST", "/api/v1/session", "", "", "1.0.1", map[string]any{"tiktokAppId": "101", "loginCode": "user-a", "launchQuery": map[string]string{}}, 200), &session)
	call("GET", "/admin/v1/official/review-list", "", "101", "", nil, 401)
	var whitelist operations.ReviewWhitelist
	json.Unmarshal(call("PUT", "/admin/v1/official/review-list", admin.Token, "101", "", map[string]any{"revision": 0, "dramaIds": []string{"201"}}, 200), &whitelist)
	if whitelist.Revision != 1 || len(whitelist.Dramas) != 1 {
		t.Fatalf("清单保存错误 %+v", whitelist)
	}
	call("PUT", "/admin/v1/official/review-list", admin.Token, "101", "", map[string]any{"revision": 0, "dramaIds": []string{"202"}}, 409)
	app, err := f.store.FindApplication(ctx, "101")
	if err != nil {
		t.Fatal(err)
	}
	settings := operations.ApplicationInput{SubjectID: app.SubjectID, Name: app.Name, TikTokAppID: app.TikTokAppID, ClientKey: app.ClientKey, RegionCode: app.RegionCode, TimeZone: app.TimeZone, Status: app.Status, AdUnits: app.AdUnits, MonetizationMode: application.MonetizationModeIAA, MiniReview: &application.MiniReview{Version: "1.0.1", Enabled: true, Revision: 1, DramaIDs: []string{"202"}}}
	call("PUT", "/admin/v1/applications/101", admin.Token, "", "", settings, 200)
	stored, err := f.store.FindApplication(ctx, "101")
	if err != nil || len(stored.MiniReview.DramaIDs) != 1 || stored.MiniReview.DramaIDs[0] != "201" {
		t.Fatal("应用编辑覆盖了独立白名单", err)
	}
	for _, tc := range []struct {
		app, version     string
		enabled, episode bool
	}{{"101", "1.0.1", true, true}, {"101", "1.0.2", false, false}, {"101", "", false, false}, {"102", "1.0.1", false, false}} {
		var cfg biz.ConsumerAppConfig
		json.Unmarshal(call("GET", "/api/v1/open/app-config?tiktokAppId="+tc.app, "", "", tc.version, nil, 200), &cfg)
		if cfg.MiniReview.Enabled != tc.enabled || cfg.MiniReview.EpisodeOnly != tc.episode {
			t.Fatalf("应用版本隔离错误 %+v %+v", tc, cfg.MiniReview)
		}
		if tc.episode && (cfg.Monetization.VIP.Enabled || cfg.InterstitialAdUnitID != "") {
			t.Fatal("审核仍提供 VIP 或插屏")
		}
	}
	// 原首页接口即使收到命中版本头，也继续返回原栏目和完整短剧目录。
	for _, section := range []string{"carousel", "hot"} {
		var rows []domain.DramaSummary
		json.Unmarshal(call("GET", "/api/v1/open/home/"+section+"?tiktokAppId=101", "", "", "1.0.1", nil, 200), &rows)
		if len(rows) != 2 {
			t.Fatalf("原 %s 接口被审核配置改变", section)
		}
	}
	var rankings []homepage.RankingSection
	json.Unmarshal(call("GET", "/api/v1/open/home/rankings?tiktokAppId=101", "", "", "1.0.1", nil, 200), &rankings)
	if len(rankings) == 0 {
		t.Fatal("原分类接口被审核配置隐藏")
	}
	var home biz.PublicCatalogResult
	json.Unmarshal(call("GET", "/api/v1/open/catalog?tiktokAppId=101", "", "", "1.0.1", nil, 200), &home)
	if len(home.Dramas) != 2 {
		t.Fatal("原短剧列表接口被审核配置过滤")
	}
	for _, tc := range []struct {
		app, version string
		enabled      bool
	}{{"101", "1.0.1", true}, {"101", "1.0.2", false}, {"101", "", false}, {"102", "1.0.1", false}} {
		var reviewHome biz.ReviewHomeResult
		json.Unmarshal(call("GET", "/api/v1/open/review-home?tiktokAppId="+tc.app, "", "", tc.version, nil, 200), &reviewHome)
		if reviewHome.Enabled != tc.enabled || (tc.enabled && (len(reviewHome.Dramas) != 1 || reviewHome.Dramas[0].ID != "201")) || (!tc.enabled && len(reviewHome.Dramas) != 0) {
			t.Fatal("新增审核首页未正确隔离应用或版本")
		}
	}
	var search biz.PublicCatalogResult
	json.Unmarshal(call("GET", "/api/v1/open/catalog?tiktokAppId=101&query=202", "", "", "1.0.1", nil, 200), &search)
	if len(search.Dramas) != 1 || search.Dramas[0].ID != "202" {
		t.Fatal("审核误限制搜索", search)
	}
	call("GET", "/api/v1/dramas/202", session.VisitorToken, "", "1.0.1", nil, 200)
	var entry struct {
		Enabled bool `json:"enabled"`
	}
	json.Unmarshal(call("GET", "/api/v1/me/entry-ad", session.VisitorToken, "", "1.0.1", nil, 200), &entry)
	if entry.Enabled {
		t.Fatal("审核仍启用入剧广告")
	}
	var play domain.PlayResult
	json.Unmarshal(call("POST", "/api/v1/play/resolve", session.VisitorToken, "", "1.0.1", map[string]string{"dramaId": "201", "episodeId": "2012"}, 200), &play)
	if play.Offer == nil || play.Offer.Kind != "rewarded_episode" || play.VIPOffer != nil {
		t.Fatal("审核逐集入口错误", play)
	}
	invalid := doc
	invalid.Rules = nil
	if _, err := f.store.SaveApplicationStrategy(ctx, monetization.ApplicationStrategyInput{Document: invalid}); !errors.Is(err, operations.ErrInvalidInput) {
		t.Fatal("审核中移除全部逐集规则", err)
	}
	settings.MiniReview.Enabled = false
	settings.MiniReview.Revision = 2
	call("PUT", "/admin/v1/applications/101", admin.Token, "", "", settings, 200)
	var restored biz.ConsumerAppConfig
	json.Unmarshal(call("GET", "/api/v1/open/app-config?tiktokAppId=101", "", "", "1.0.1", nil, 200), &restored)
	if restored.MiniReview.Enabled || !restored.Monetization.VIP.Enabled || restored.InterstitialAdUnitID == "" {
		t.Fatal("关闭未恢复完整配置", restored)
	}
	json.Unmarshal(call("GET", "/api/v1/open/catalog?tiktokAppId=101&limit=1", "", "", "1.0.1", nil, 200), &home)
	if home.NextCursor == "" {
		t.Fatal("缺少首页分页游标")
	}
	call("PUT", "/admin/v1/official/review-list", admin.Token, "101", "", map[string]any{"revision": 3, "dramaIds": []string{}}, 200)
	call("GET", "/api/v1/open/catalog?tiktokAppId=101&cursor="+url.QueryEscape(home.NextCursor), "", "", "1.0.1", nil, 200)
}
