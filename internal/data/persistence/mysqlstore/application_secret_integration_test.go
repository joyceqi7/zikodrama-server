package mysqlstore

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
)

// TestApplicationSecretHTTP 验证最终结构上的 HTTP 登记与轮换、重连读回、空值保留和响应日志无密钥。
func TestApplicationSecretHTTP(t *testing.T) {
	f := newIAAFixture(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	handler := operationshttp.NewHandler(operations.NewService(operations.Repositories{Application: operations.ApplicationPorts{Subjects: f.store, Registry: f.store}, Reporting: operations.ReportingPorts{Audits: f.store}}, nil), adminrbac.NewService(f.store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")}), operationshttp.Config{})
	server := httptest.NewServer(handler)
	defer server.Close()
	var session struct {
		Token string `json:"token"`
	}
	mustDecodeApplicationStrategy(t, iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200), &session)
	body := map[string]string{"name": "Secret QA", "subjectId": "1", "tiktokAppId": "secret-test", "clientKey": "secret-app-key", "clientSecret": "first-test-secret", "monetizationMode": "IAP"}
	var saved application.Application
	mustDecodeApplicationStrategy(t, iaaRequest(t, server.URL, "POST", "/admin/v1/applications", session.Token, "", body, 201), &saved)
	if saved.ClientSecret != "" || !saved.ClientSecretConfigured {
		t.Fatal("创建响应必须只包含配置状态")
	}
	// 从 HTTP 编辑入口更换密钥，再空值保存；返回状态及数据库现值都必须正确。
	body["clientSecret"] = "http-rotated-secret"
	body["status"] = "disabled"
	path := "/admin/v1/applications/" + saved.ID
	mustDecodeApplicationStrategy(t, iaaRequest(t, server.URL, "PUT", path, session.Token, "", body, 200), &saved)
	body["clientSecret"] = ""
	mustDecodeApplicationStrategy(t, iaaRequest(t, server.URL, "PUT", path, session.Token, "", body, 200), &saved)
	if saved.ClientSecret != "" || !saved.ClientSecretConfigured {
		t.Fatal("编辑响应泄漏密钥或丢失状态")
	}
	// 重开连接证明凭据来自数据库，而非注册时的内存。
	if err := f.store.data.pool.Close(); err != nil {
		t.Fatal(err)
	}
	f.open(t)
	found, err := f.store.FindApplication(t.Context(), saved.ID)
	if err != nil || found.ClientSecret != "http-rotated-secret" {
		t.Fatal("重连未读回应用密钥", err)
	}
	// 从数据库旧快照构造无密钥的广告/名称更新，轮换后提交仍不能覆盖新密钥。
	stale := found
	stale.ClientSecret = ""
	found.ClientSecret = "rotated-test-secret"
	if err := f.store.SaveApplication(t.Context(), found); err != nil {
		t.Fatal(err)
	}
	stale.Name = "Updated name"
	if err := f.store.SaveApplication(t.Context(), stale); err != nil {
		t.Fatal(err)
	}
	found, err = f.store.FindApplication(t.Context(), saved.ID)
	if err != nil || found.ClientSecret != "rotated-test-secret" || found.Name != stale.Name {
		t.Fatal("无关更新覆盖轮换结果", err)
	}
	payload, err := json.Marshal(found)
	if err != nil || strings.Contains(string(payload), "rotated-test-secret") || strings.Contains(string(payload), `"clientSecret":`) {
		t.Fatal("序列化泄漏密钥")
	}
	// 数据库正常路径不应记录错误或凭据。
	if strings.Contains(logs.String(), "first-test-secret") || strings.Contains(logs.String(), "rotated-test-secret") || strings.Contains(logs.String(), "http-rotated-secret") || strings.Contains(logs.String(), "level=ERROR") {
		t.Fatal("日志泄漏凭据或成功路径误报")
	}
}
