package mysqlstore

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
)

// TestCurrentSchemaIntegrationApplicationMode 验证最终 Schema 下三种模式的保存读回、必填校验和应用隔离。
func TestCurrentSchemaIntegrationApplicationMode(t *testing.T) {
	f := newIAAFixture(t)
	defer f.store.data.pool.Close()
	handler := operationshttp.NewHandler(operations.NewService(operations.Repositories{Application: operations.ApplicationPorts{Subjects: f.store, Registry: f.store}, Monetization: operations.MonetizationPorts{Settings: f.store}, Reporting: operations.ReportingPorts{Audits: f.store}}, nil), adminrbac.NewService(f.store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")}), operationshttp.Config{})
	server := httptest.NewServer(handler)
	defer server.Close()
	var session struct {
		Token string `json:"token"`
	}
	mustDecodeApplicationStrategy(t, iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200), &session)
	var saved application.Application
	for i, mode := range []string{"IAA", "IAP", "IAA+IAP"} {
		body := map[string]any{"clientSecret": "mode-test-secret", "name": "模式测试", "clientKey": "key-" + string(rune('a'+i)), "subjectId": "1", "tiktokAppId": "mode-" + string(rune('a'+i)), "monetizationMode": " " + strings.ToLower(mode) + " "}
		mustDecodeApplicationStrategy(t, iaaRequest(t, server.URL, "POST", "/admin/v1/applications", session.Token, "", body, 201), &saved)
		if string(saved.MonetizationMode) != mode {
			t.Fatal("创建未保存规范化模式", saved.MonetizationMode)
		}
		found, err := f.store.FindApplication(context.Background(), saved.ID)
		if err != nil || found.MonetizationMode != saved.MonetizationMode {
			t.Fatal("持久化回读失败", err)
		}
	}
	// 更新必须携带明确模式；缺失或非法请求不得改变已有配置。
	body := map[string]any{"name": "内购应用", "clientKey": saved.ClientKey, "subjectId": "1", "tiktokAppId": saved.TikTokAppID, "status": "disabled", "monetizationMode": "IAP"}
	path := "/admin/v1/applications/" + saved.ID
	iaaRequest(t, server.URL, "PUT", path, session.Token, "", body, 200)
	delete(body, "monetizationMode")
	iaaRequest(t, server.URL, "PUT", path, session.Token, "", body, 400)
	for _, invalid := range []any{nil, "", "unknown", "IAA + IAP", 1, []string{"IAA"}} {
		body["monetizationMode"] = invalid
		iaaRequest(t, server.URL, "PUT", path, session.Token, "", body, 400)
	}
	var apps []application.Application
	mustDecodeApplicationStrategy(t, iaaRequest(t, server.URL, "GET", "/admin/v1/applications", session.Token, "", nil, 200), &apps)
	found := false
	for _, app := range apps {
		if app.ID == saved.ID {
			found = true
			if app.MonetizationMode != application.MonetizationModeIAP || app.Name != "内购应用" {
				t.Fatal("非法请求改变记录")
			}
		}
		if app.ID == "101" && app.MonetizationMode != application.MonetizationModeIAA {
			t.Fatal("修改影响其他应用")
		}
	}
	if !found {
		t.Fatal("列表未返回应用")
	}
	// 新建同样拒绝缺失模式。
	iaaRequest(t, server.URL, "POST", "/admin/v1/applications", session.Token, "", map[string]string{"name": "缺失模式", "subjectId": "1", "tiktokAppId": "missing-mode"}, 400)
}
