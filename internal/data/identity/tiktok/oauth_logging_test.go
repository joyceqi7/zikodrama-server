package tiktok

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"drama-server/internal/biz/visitor"
)

// TestOAuthFailureLogsActualCause 验证网络故障保留诊断细节和错误链，只遮盖当前调用已知凭据。
func TestOAuthFailureLogsActualCause(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, tc := range []struct {
		name, level string
		cause       error
	}{
		{name: "连接拒绝", level: "ERROR", cause: errors.New("dial tcp: connection refused; credential=private-client-secret; code=private-login-code")},
		{name: "超时", level: "ERROR", cause: fmt.Errorf("upstream wait: %w", context.DeadlineExceeded)},
		{name: "取消", level: "INFO", cause: fmt.Errorf("upstream request: %w", context.Canceled)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			client := NewOAuthClient("client", "private-client-secret", &http.Client{Transport: oauthTransportFailure{err: tc.cause}})
			_, err := client.ExchangeCode(context.Background(), "private-login-code")
			if !errors.Is(err, tc.cause) || !errors.Is(err, visitor.ErrIdentityUnavailable) {
				t.Fatalf("OAuth 错误链或分类改变：%v", err)
			}
			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			if entry["level"] != tc.level || entry["msg"] != "tiktok identity service unavailable" || !strings.Contains(entry["error"].(string), strings.Split(tc.cause.Error(), ";")[0]) {
				t.Fatalf("OAuth 故障缺少实际原因或级别错误：%v", entry)
			}
			for _, secret := range []string{"private-client-secret", "private-login-code"} {
				if strings.Contains(logs.String(), secret) || strings.Contains(err.Error(), secret) {
					t.Fatal("日志或对外错误泄漏凭据")
				}
			}
		})
	}
}

// TestOAuthLogLevelsPreservePlatformResults 验证成功不记错，平台拒绝、限流和平台故障仍保持原 HTTP 分类。
func TestOAuthLogLevelsPreservePlatformResults(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, tc := range []struct {
		name, body, level string
		status            int
		want              error
	}{
		{name: "成功", status: 200, body: "{\"open_id\":\"private-open-id\",\"access_token\":\"private-access-token\"}"},
		{name: "登录码拒绝", status: 400, body: "{\"error\":\"invalid_grant\",\"error_description\":\"private-provider-body\"}", level: "WARN", want: visitor.ErrUnauthorized},
		{name: "平台限流", status: 429, body: "private-provider-body", level: "WARN", want: visitor.ErrIdentityUnavailable},
		{name: "平台故障", status: 503, body: "private-provider-body", level: "ERROR", want: visitor.ErrIdentityUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := NewOAuthClient("client", "private-client-secret", server.Client())
			client.endpoint = server.URL
			identity, err := client.ExchangeCode(context.Background(), "private-login-code")
			if tc.want == nil {
				if err != nil || identity.OpenID != "private-open-id" || logs.Len() != 0 {
					t.Fatalf("成功路径或日志改变：%v", err)
				}
			} else {
				var entry map[string]any
				if !errors.Is(err, tc.want) || json.Unmarshal(logs.Bytes(), &entry) != nil || entry["level"] != tc.level {
					t.Fatalf("平台失败分类或级别改变：%v %v", err, entry)
				}
			}
			for _, secret := range []string{"private-provider-body", "private-open-id", "private-access-token", "private-client-secret", "private-login-code"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("OAuth 日志泄漏请求或响应正文")
				}
			}
		})
	}
}

// TestOAuthLogsControlledProviderErrorCode 验证平台错误码可区分，未知字段不泄漏凭据，原响应分类与错误字符串保持不变。
func TestOAuthLogsControlledProviderErrorCode(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, tc := range []struct {
		name, body, code, reason, level string
		want                            error
	}{
		{name: "客户端配置", body: `{"error":"invalid_client","error_description":"private-description","access_token":"private-platform-token"}`, code: "invalid_client", reason: "configuration", level: "ERROR", want: visitor.ErrIdentityUnavailable},
		{name: "未授权客户端结构化错误", body: `{"error":{"code":"unauthorized_client","message":"private-description"}}`, code: "unauthorized_client", reason: "configuration", level: "ERROR", want: visitor.ErrIdentityUnavailable},
		{name: "权限范围错误", body: `{"error":"invalid_scope"}`, code: "invalid_scope", reason: "provider_rejected", level: "ERROR", want: visitor.ErrIdentityUnavailable},
		{name: "未知值可能含凭据", body: `{"error":"private-platform-token"}`, code: "unknown", reason: "provider_rejected", level: "ERROR", want: visitor.ErrIdentityUnavailable},
		{name: "错误字段含URL", body: `{"error":"https://private-provider-url/?token=private-platform-token"}`, code: "unknown", reason: "provider_rejected", level: "ERROR", want: visitor.ErrIdentityUnavailable},
		{name: "无平台错误码", body: `{}`, reason: "provider_rejected", level: "ERROR", want: visitor.ErrIdentityUnavailable},
		{name: "平台错误结构损坏", body: `{"error":{"unexpected":"private-platform-token"}}`, code: "invalid_response", reason: "provider_rejected", level: "ERROR", want: visitor.ErrIdentityUnavailable},
		{name: "登录码拒绝", body: `{"error":"invalid_grant"}`, code: "invalid_grant", level: "WARN", want: visitor.ErrUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := NewOAuthClient("client", "private-client-secret", server.Client())
			client.endpoint = server.URL
			identity, err := client.ExchangeCode(context.Background(), "private-login-code")
			if !errors.Is(err, tc.want) || err.Error() != tc.want.Error() || identity.OpenID != "" {
				t.Fatalf("OAuth 原返回分类改变: %v", err)
			}
			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			if entry["level"] != tc.level || entry["http_status"] != float64(400) {
				t.Fatalf("原日志级别或 HTTP 状态改变: %v", entry)
			}
			if tc.code == "" {
				if _, exists := entry["provider_error_code"]; exists {
					t.Fatal("虚构平台错误码")
				}
			} else if entry["provider_error_code"] != tc.code {
				t.Fatalf("平台错误码丢失或未受控: %v", entry)
			}
			if tc.reason != "" && entry["reason"] != tc.reason {
				t.Fatal("原故障分类改变")
			}
			for _, secret := range []string{"private-description", "private-platform-token", "private-provider-url", "private-client-secret", "private-login-code"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("OAuth 错误字段泄漏敏感数据")
				}
			}
		})
	}
}
