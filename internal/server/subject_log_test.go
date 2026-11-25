package server

import (
	"bytes"
	"golang.org/x/crypto/bcrypt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
	styx "gitee.com/linpbin/styx"
	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

// TestSubjectAccessLogExcludesSecret 在真实 HTTP 日志链验证主体写入、校验拒绝和鉴权拒绝均不采集密钥正文。
func TestSubjectAccessLogExcludesSecret(t *testing.T) {
	var output bytes.Buffer
	logger := styx.NewLogger(nil, styx.WithConsoleOutput(&output))
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })
	store := memory.NewStore(memory.Seed{})
	hash, err := bcrypt.GenerateFromPassword([]byte("local-admin-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAdminUser(t.Context(), &adminrbac.User{Username: "admin", PasswordHash: string(hash), Status: "active", IsSuper: true, AuthVersion: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rbac := adminrbac.NewService(store, adminrbac.Config{SigningKey: []byte("subject-access-log-fixture-signing-key")})
	login, err := rbac.Login(t.Context(), "admin", "local-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	h := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), rbac, operationshttp.Config{})
	engine, err := buildTestHTTPServer(Dependencies{Admin: h, Consumer: http.NotFoundHandler(), Styx: newHTTPTestRuntimeWithSlog(t, logger), Config: &configv1.HTTPServerConfig{Addr: ":0"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method, path, body string
		status             int
		anonymous          bool
	}{
		{"POST", "/admin/v1/subjects", `{"name":"测试主体","tiktokAppId":"123","clientKey":"key","clientSecret":"private-publisher-secret"}`, 201, false},
		{"POST", "/admin/v1/subjects", `{"name":"","clientSecret":"private-publisher-secret"}`, 400, false},
		{"PUT", "/admin/v1/subjects/1", `{"name":"默认主体","tiktokAppId":"124","clientKey":"key-2","clientSecret":"private-publisher-secret"}`, 200, false},
		{"PUT", "/admin/v1/subjects/invalid", `{"clientSecret":"private-publisher-secret"}`, 400, false},
		{"POST", "/admin/v1/subjects", `{"clientSecret":"private-publisher-secret"}`, 401, true},
	} {
		output.Reset()
		req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		req.Header.Set("Content-Type", "application/json")
		if !test.anonymous {
			req.Header.Set("Authorization", "Bearer "+login.Token)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != test.status {
			t.Fatalf("正文隔离改变接口行为：%s %d", test.method, w.Code)
		}
		if strings.Contains(output.String(), "private-publisher-secret") || strings.Contains(output.String(), login.Token) || strings.Contains(output.String(), "request_body") {
			t.Fatal("主体写入访问日志泄露正文或凭据")
		}
		if !strings.Contains(output.String(), "http access") {
			t.Fatal("访问日志被整体丢弃")
		}
	}
}
