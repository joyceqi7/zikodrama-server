package consumer_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	biz "drama-server/internal/biz"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/visitor"
	httpapi "drama-server/internal/service/consumer"
	"drama-server/internal/testsupport/memory"
)

// TestOfficialAppIDHTTPBoundary 使用真实解析器、启动编排和认证入口验证官方 ID 合同。
func TestOfficialAppIDHTTPBoundary(t *testing.T) {
	const officialID = "0009007199254740993123"
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	store := memory.NewStore(memory.Seed{
		PublishedOwnerID: officialID,
		Applications: []application.Application{
			{TikTokAppID: officialID, Status: application.StatusActive},
			{TikTokAppID: "official-b", Status: application.StatusActive},
			{TikTokAppID: "official-disabled", Status: application.StatusDisabled},
		},
		Dramas: []domain.Drama{{ID: "5019", Title: "应用 A 的短剧", PublishStatus: "published", Episodes: []domain.Episode{{ID: "5013", Number: 1}}}},
	})
	resolver, err := biz.NewApplicationResolver(store, func(application.Application) visitor.IdentityProvider { return nil })
	if err != nil {
		t.Fatal(err)
	}
	visitors := newTestVisitorServiceWithResolver(t, resolver, store)
	handler := newConsumerHandler(domain.NewService(store, time.Now), visitors, httpapi.Config{})
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
		return response
	}

	for _, tc := range []struct {
		query         string
		status, count int
	}{
		{"tiktokAppId=" + officialID, 200, 1},
		{"tiktokAppId=official-b", 200, 0},
		{"tiktokAppId=business-a", 403, 0},
		{"tiktokAppId=internal-a", 403, 0},
		{"tiktokAppId=official-disabled", 403, 0},
		{"tiktokAppId=missing", 403, 0},
		{"appCode=business-a", 400, 0},
		{"tiktokAppId=invalid!", 400, 0},
		{"tiktokAppId=" + strings.Repeat("1", 256), 400, 0},
	} {
		response := request("GET", "/api/v1/open/catalog?"+tc.query, "")
		if response.Code != tc.status {
			t.Fatalf("目录状态=%d，期望=%d", response.Code, tc.status)
		}
		if tc.status != 200 {
			continue
		}
		var body struct {
			Data biz.PublicCatalogResult `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Data.Dramas) != tc.count {
			t.Fatal("目录没有按官方 AppID 隔离")
		}
		if strings.Contains(response.Body.String(), "appCode") || strings.Contains(response.Body.String(), "visitorToken") {
			t.Fatal("目录包含废弃标识或访客凭据")
		}
	}
	for _, body := range []string{
		`{}`, `{"appCode":"business-a"}`, `{"tiktokAppId":9007199254740993123}`,
		`{"tiktokAppId":"` + officialID + `","appCode":"business-a"}`,
		`{"tiktokAppId":"invalid!"}`, `{"tiktokAppId":"   "}`,
	} {
		if response := request("POST", "/api/v1/bootstrap", body); response.Code != 400 {
			t.Fatalf("无效启动参数状态=%d", response.Code)
		}
	}
	logs.Reset()
	response := request("POST", "/api/v1/bootstrap", `{"tiktokAppId":"  `+officialID+`  ","launchQuery":{}}`)
	if response.Code != http.StatusOK {
		t.Fatalf("官方 AppID 启动失败，状态=%d", response.Code)
	}
	var body struct {
		Data biz.BootstrapResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.TikTokAppID != officialID || body.Data.ApplicationID != officialID || body.Data.VisitorToken == "" {
		t.Fatal("启动没有统一使用官方 AppID")
	}
	if strings.Contains(response.Body.String(), "appCode") {
		t.Fatal("启动响应仍包含废弃 appCode")
	}
	req := httptest.NewRequest("GET", "/api/v1/home", nil)
	req.Header.Set("Authorization", "Bearer "+body.Data.VisitorToken)
	authenticated := httptest.NewRecorder()
	handler.ServeHTTP(authenticated, req)
	if authenticated.Code != 200 {
		t.Fatalf("新凭据不能恢复官方应用，状态=%d", authenticated.Code)
	}
	if strings.Contains(logs.String(), `"level":"ERROR"`) || strings.Contains(logs.String(), body.Data.VisitorToken) {
		t.Fatal("成功流程误报故障或泄漏凭据")
	}
}
