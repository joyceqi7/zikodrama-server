package mysqlstore

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"drama-server/internal/biz"
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"golang.org/x/crypto/bcrypt"
)

// TestReviewSettingsHTTPIntegration 覆盖独立菜单权限、应用隔离、清单保留与并发修订，不连接外部平台。
func TestReviewSettingsHTTPIntegration(t *testing.T) {
	f := newIAAFixture(t)
	repo, scope := seedDirect(t, f)
	f.direct = directplay.NewService(repo, nil)
	if _, err := repo.Accept(scope, "201", directplay.Command{Kind: "publish", RequestID: "review-settings-content"}, "fixture"); err != nil {
		t.Fatal(err)
	}
	app := application.Application{ID: "103", SubjectID: "1", TikTokAppID: "review-settings-app", Name: "审核应用", ClientKey: "review-settings-key", ClientSecret: "fixture-secret", MonetizationMode: application.MonetizationModeIAP, Status: application.StatusActive, PlaybackProvider: "byteplus_web", TimeZone: "UTC"}
	if err := f.store.CreateApplication(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	handler := f.handler(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	login := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var admin adminrbac.LoginResult
	if err := json.Unmarshal(login, &admin); err != nil {
		t.Fatal(err)
	}
	get := func(token, id string, status int) json.RawMessage {
		return iaaRequest(t, server.URL, "GET", "/admin/v1/review-settings", token, id, nil, status)
	}
	save := func(token string, input any, status int) json.RawMessage {
		return iaaRequest(t, server.URL, "PUT", "/admin/v1/review-settings", token, "103", input, status)
	}
	get("", "103", 401)
	get(admin.Token, "", 400)
	get(admin.Token, "999999", 400)
	for _, input := range []map[string]any{
		{"enabled": true, "version": "", "revision": 0},
		{"enabled": true, "version": "01.0.0", "revision": 0},
		{"enabled": false, "version": "1.0.0"},
		{"version": "1.0.0", "revision": 0},
		{"enabled": false, "version": "1.0.0", "revision": 0, "dramaIds": []string{"201"}},
	} {
		save(admin.Token, input, 400)
	}
	// 开启前必须先配置可见内容，独立设置接口不能替代内容清单接口。
	save(admin.Token, map[string]any{"enabled": true, "version": "1.0.0", "revision": 0}, 400)
	var list operations.ReviewWhitelist
	if err := json.Unmarshal(iaaRequest(t, server.URL, "PUT", "/admin/v1/direct/review-list", admin.Token, "103", map[string]any{"revision": 0, "dramaIds": []string{"201"}}, 200), &list); err != nil {
		t.Fatal(err)
	}
	var settings application.MiniReview
	if err := json.Unmarshal(save(admin.Token, map[string]any{"version": "1.0.0", "enabled": true, "revision": list.Revision}, 200), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Revision != list.Revision+1 || !settings.Enabled || len(settings.DramaIDs) != 0 {
		t.Fatal("设置响应包含清单或修订不正确")
	}
	stored, err := f.store.FindApplication(t.Context(), "103")
	if err != nil || stored.Name != app.Name || stored.ClientKey != app.ClientKey || stored.ClientSecret != app.ClientSecret || len(stored.MiniReview.DramaIDs) != 1 || stored.MiniReview.DramaIDs[0] != "201" {
		t.Fatal("设置覆盖了清单、应用资料或凭据", err)
	}
	t.Run("直连版本命中后读取审核首页", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/open/review-home?tiktokAppId="+app.TikTokAppID, nil)
		req.Header.Set("X-Mini-Version", "1.0.0")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		var envelope iaaResponse
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK {
			t.Fatalf("读取直连审核首页返回 HTTP %d，错误码 %s", response.Code, envelope.Reason)
		}
		var home biz.ReviewHomeResult
		if err := json.Unmarshal(envelope.Data, &home); err != nil {
			t.Fatal(err)
		}
		if !home.Enabled || len(home.Dramas) != 1 || home.Dramas[0].ID != "201" {
			t.Fatalf("直连审核首页未返回配置的短剧: %+v", home)
		}
	})
	save(admin.Token, map[string]any{"version": "2.0.0", "enabled": false, "revision": list.Revision}, 409)
	iaaRequest(t, server.URL, "PUT", "/admin/v1/direct/review-list", admin.Token, "103", map[string]any{"revision": list.Revision, "dramaIds": []string{}}, 409)
	var other application.MiniReview
	if err := json.Unmarshal(get(admin.Token, "101", 200), &other); err != nil || other.Enabled {
		t.Fatal("审核开关串应用", err)
	}
	// 仅有应用编辑或清单编辑权限不能修改开关，独立读写权限分别生效。
	hash, err := bcrypt.GenerateFromPassword([]byte("ReviewMenuLocal123!"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		codes       []string
		read, write int
	}{
		{"settings-reader", []string{"directory.whitelist", "menu.review_settings", adminrbac.PermissionReviewSettingsRead}, 200, 403},
		{"settings-writer", []string{adminrbac.PermissionReviewSettingsUpdate}, 403, 200},
		{"settings-app-editor", []string{adminrbac.PermissionApplicationUpdate}, 403, 403},
		{"settings-list-editor", []string{adminrbac.PermissionDirectReviewListUpdate}, 403, 403},
	} {
		role := adminrbac.Role{Name: tc.name, Code: tc.name, Status: adminrbac.StatusActive}
		if err := f.store.CreateAdminRole(t.Context(), &role); err != nil {
			t.Fatal(err)
		}
		var ids []string
		if err := f.store.data.DB().Table("admin_permissions").Where("code IN ?", tc.codes).Pluck("id", &ids).Error; err != nil {
			t.Fatal(err)
		}
		if len(ids) != len(tc.codes) {
			t.Fatal("独立菜单迁移缺少权限节点")
		}
		if err := f.store.ReplaceAdminRolePermissions(t.Context(), role.ID, ids); err != nil {
			t.Fatal(err)
		}
		user := adminrbac.User{Username: tc.name, Nickname: tc.name, PasswordHash: string(hash), Status: adminrbac.StatusActive, AuthVersion: 1}
		if err := f.store.CreateAdminUser(t.Context(), &user); err != nil {
			t.Fatal(err)
		}
		if err := f.store.ReplaceAdminUserRoles(t.Context(), user.ID, []string{role.ID}); err != nil {
			t.Fatal(err)
		}
		var actor adminrbac.LoginResult
		if err := json.Unmarshal(iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": tc.name, "password": "ReviewMenuLocal123!"}, 200), &actor); err != nil {
			t.Fatal(err)
		}
		get(actor.Token, "103", tc.read)
		if tc.name == "settings-reader" {
			// 登录用户可读取分发候选安全摘要，但不会因此获得应用资料权限或凭据。
			options := iaaRequest(t, server.URL, "GET", "/admin/v1/applications/options", actor.Token, "", nil, 200)
			var candidates []map[string]any
			if err := json.Unmarshal(options, &candidates); err != nil || len(candidates) < 3 {
				t.Fatal("安全应用选项缺失", err)
			}
			for _, candidate := range candidates {
				if _, ok := candidate["clientKeyConfigured"].(bool); !ok || candidate["playbackProvider"] == nil {
					t.Fatal("候选缺少分发所需安全摘要")
				}
			}
			for _, field := range []string{`"clientKey":`, `"clientSecret":`, `"secretRef":`, `"adUnits":`} {
				if strings.Contains(string(options), field) {
					t.Fatal("安全候选暴露应用配置字段", field)
				}
			}
			iaaRequest(t, server.URL, "GET", "/admin/v1/applications", actor.Token, "", nil, 403)
		}
		save(actor.Token, map[string]any{"version": "1.0.0", "enabled": false, "revision": settings.Revision}, tc.write)
		if tc.name == "settings-app-editor" {
			input := operations.ApplicationInput{SubjectID: app.SubjectID, Name: app.Name, TikTokAppID: app.TikTokAppID, ClientKey: app.ClientKey, MonetizationMode: app.MonetizationMode, Status: app.Status, MiniReview: &application.MiniReview{Version: "2.0.0", Enabled: false, Revision: 3}}
			iaaRequest(t, server.URL, "PUT", "/admin/v1/applications/103", actor.Token, "", input, 403)
		}
	}
	// 纯 IAA 的独立开关沿用逐集规则门禁，关掉之后才允许移除逐集规则。
	currentIAA, err := f.store.FindApplication(scope, "102")
	if err != nil {
		t.Fatal(err)
	}
	iaaRevision := currentIAA.MiniReview.Revision
	iaaRequest(t, server.URL, "PUT", "/admin/v1/review-settings", admin.Token, "102", map[string]any{"version": "1.0.0", "enabled": true, "revision": iaaRevision}, 200)
	iaaRequest(t, server.URL, "PUT", "/admin/v1/review-settings", admin.Token, "102", map[string]any{"version": "1.0.0", "enabled": false, "revision": iaaRevision + 1}, 200)
	strategy, err := f.store.GetApplicationStrategy(scope)
	if err != nil || strategy.Strategy == nil {
		t.Fatal("缺少 IAA 测试策略", err)
	}
	doc := strategy.Strategy.Document
	doc.Rules = nil
	if _, err := f.store.SaveApplicationStrategy(scope, monetization.ApplicationStrategyInput{Document: doc}); err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, "PUT", "/admin/v1/review-settings", admin.Token, "102", map[string]any{"version": "1.0.0", "enabled": true, "revision": iaaRevision + 2}, 400)
	if _, err := f.store.SaveMiniReviewSettings(scope, "102", application.MiniReview{Version: "1.0.0", Enabled: true, Revision: iaaRevision + 2}); !errors.Is(err, operations.ErrInvalidInput) {
		t.Fatal("事务内未检查当前逐集规则", err)
	}
	// 版本关闭和删除使用同一修订合同，删除最后一项也不能恢复历史单版本配置或清空共享清单。
	var current application.MiniReview
	if err := json.Unmarshal(get(admin.Token, "103", 200), &current); err != nil {
		t.Fatal(err)
	}
	changeVersions := func(versions []application.MiniReviewVersion) {
		t.Helper()
		if err := json.Unmarshal(save(admin.Token, map[string]any{"revision": current.Revision, "versions": versions}, 200), &current); err != nil {
			t.Fatal(err)
		}
	}
	changeVersions([]application.MiniReviewVersion{{Version: "1.0.0", Enabled: true}, {Version: "2.0.0", Enabled: true}})
	changeVersions([]application.MiniReviewVersion{{Version: "1.0.0", Enabled: false}, {Version: "2.0.0", Enabled: true}})
	closed, err := f.store.FindApplication(t.Context(), "103")
	if err != nil || len(closed.MiniReview.ConfiguredVersions()) != 2 || closed.MiniReview.ConfiguredVersions()[0].Enabled || !closed.MiniReview.ConfiguredVersions()[1].Enabled {
		t.Fatal("关闭影响了其他版本或移除了记录", err)
	}
	changeVersions([]application.MiniReviewVersion{{Version: "2.0.0", Enabled: true}})
	remaining, err := f.store.FindApplication(t.Context(), "103")
	if err != nil || len(remaining.MiniReview.ConfiguredVersions()) != 1 || remaining.MiniReview.ConfiguredVersions()[0].Version != "2.0.0" {
		t.Fatal("未仅删除指定版本", err)
	}
	stale := current.Revision
	changeVersions([]application.MiniReviewVersion{})
	removed, err := f.store.FindApplication(t.Context(), "103")
	if err != nil || len(removed.MiniReview.ConfiguredVersions()) != 0 || removed.MiniReview.AnyEnabled() || removed.MiniReview.Version != "" || len(removed.MiniReview.DramaIDs) != 1 || removed.MiniReview.DramaIDs[0] != "201" {
		t.Fatal("删除最后版本恢复了旧配置或改写了清单", err)
	}
	save(admin.Token, map[string]any{"revision": stale, "versions": []application.MiniReviewVersion{}}, 409)
}

// TestReviewMultiVersionSettingsHTTPIntegration 验证独立版本开关、共用应用清单及旧单版本接口兼容。
func TestReviewMultiVersionSettingsHTTPIntegration(t *testing.T) {
	f := newIAAFixture(t)
	repo, scope := seedDirect(t, f)
	f.direct = directplay.NewService(repo, nil)
	if _, err := repo.Accept(scope, "201", directplay.Command{Kind: "publish", RequestID: "review-multiple-content"}, "fixture"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	var admin adminrbac.LoginResult
	json.Unmarshal(iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200), &admin)
	call := func(method, path string, input any, status int) json.RawMessage {
		return iaaRequest(t, server.URL, method, path, admin.Token, "102", input, status)
	}
	call("PUT", "/admin/v1/direct/review-list", map[string]any{"revision": 0, "dramaIds": []string{"201"}}, 200)
	for _, versions := range []any{
		[]map[string]any{{"version": "1.0.0"}},
		[]map[string]any{{"version": "01.0.0", "enabled": false}},
		[]map[string]any{{"version": "1.0.0", "enabled": false}, {"version": "1.0.0", "enabled": true}},
	} {
		call("PUT", "/admin/v1/review-settings", map[string]any{"revision": 1, "versions": versions}, 400)
	}
	versions := []application.MiniReviewVersion{{Version: "1.0.0", Enabled: false}, {Version: "1.1.0", Enabled: true}, {Version: "1.2.0", Enabled: true}}
	var saved application.MiniReview
	if err := json.Unmarshal(call("PUT", "/admin/v1/review-settings", map[string]any{"revision": 1, "versions": versions}, 200), &saved); err != nil || saved.Revision != 2 || len(saved.Versions) != 3 || len(saved.DramaIDs) != 0 {
		t.Fatal("多版本保存或返回字段错误", saved, err)
	}
	// 首条版本关闭仍须维持其他开启版本的清单和逐集策略门禁。
	call("PUT", "/admin/v1/direct/review-list", map[string]any{"revision": 2, "dramaIds": []string{}}, 400)
	strategy, err := f.store.GetApplicationStrategy(scope)
	if err != nil || strategy.Strategy == nil {
		t.Fatal("缺少逐集策略", err)
	}
	doc := strategy.Strategy.Document
	doc.Rules = nil
	if _, err := f.store.SaveApplicationStrategy(scope, monetization.ApplicationStrategyInput{Document: doc}); !errors.Is(err, operations.ErrInvalidInput) {
		t.Fatal("其他版本开启时仍允许清空逐集策略", err)
	}
	call("PUT", "/admin/v1/review-settings", map[string]any{"revision": 1, "versions": versions}, 409)
	// 旧请求仅修改指定版本，新版本配置不能丢失。
	call("PUT", "/admin/v1/review-settings", map[string]any{"revision": 2, "version": "1.1.0", "enabled": false}, 200)
	stored, err := f.store.FindApplication(t.Context(), "102")
	if err != nil || len(stored.MiniReview.Versions) != 3 || stored.MiniReview.Versions[1].Enabled || !stored.MiniReview.Versions[2].Enabled || len(stored.MiniReview.DramaIDs) != 1 {
		t.Fatal("旧接口覆盖其他版本或共享清单", err)
	}
	// 更新共用清单不得覆盖版本设置。
	call("PUT", "/admin/v1/direct/review-list", map[string]any{"revision": 3, "dramaIds": []string{"201"}}, 200)
	var current application.MiniReview
	json.Unmarshal(call("GET", "/admin/v1/review-settings", nil, 200), &current)
	if current.Revision != 4 || len(current.Versions) != 3 || !current.Versions[2].Enabled {
		t.Fatal("清单保存覆盖了版本开关", current)
	}
	versions[1].Enabled, versions[2].Enabled = false, false
	call("PUT", "/admin/v1/review-settings", map[string]any{"revision": 4, "versions": versions}, 200)
	call("PUT", "/admin/v1/direct/review-list", map[string]any{"revision": 5, "dramaIds": []string{}}, 200)
}
