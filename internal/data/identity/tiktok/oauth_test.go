package tiktok

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"drama-server/internal/biz/visitor"
)

func TestOAuthClientRejectsMissingHTTPClient(t *testing.T) {
	t.Parallel()
	client := NewOAuthClient("client-key", "client-secret", nil)
	if _, err := client.ExchangeCode(context.Background(), "code"); err == nil {
		t.Fatal("缺少受托管 HTTP Client 时不应发起 OAuth 请求")
	}
}

// TestOAuthFailureClassification 确认只有平台明确拒绝登录码才要求恢复用户身份。
func TestOAuthFailureClassification(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"登录码失效", 400, `{"error":"invalid_grant","error_description":"secret-must-not-leak"}`, visitor.ErrUnauthorized},
		{"401登录码失效", 401, `{"error":"invalid_grant"}`, visitor.ErrUnauthorized},
		{"结构化登录码失效", 400, `{"error":{"code":"invalid_grant"}}`, visitor.ErrUnauthorized},
		{"配置错误", 400, `{"error":"invalid_client"}`, visitor.ErrIdentityUnavailable},
		{"401配置错误", 401, `{"error":"invalid_client"}`, visitor.ErrIdentityUnavailable},
		{"未知401", 401, `{}`, visitor.ErrIdentityUnavailable},
		{"平台限流优先", 429, `{"error":"invalid_grant"}`, visitor.ErrIdentityUnavailable},
		{"平台故障优先", 500, `{"error":"invalid_grant"}`, visitor.ErrIdentityUnavailable},
		{"上游网关超时", 504, `{}`, context.DeadlineExceeded},
		{"上游请求超时", 408, `{}`, context.DeadlineExceeded},
		{"坏JSON", 200, `invalid-secret-must-not-leak`, visitor.ErrIdentityUnavailable},
		{"缺身份", 200, `{"access_token":"secret-must-not-leak"}`, visitor.ErrIdentityUnavailable},
		{"超大响应", 200, strings.Repeat("x", (1<<20)+1), visitor.ErrIdentityUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client := NewOAuthClient("key", "secret-must-not-leak", server.Client())
			client.endpoint = server.URL
			identity, err := client.ExchangeCode(context.Background(), "code-secret-must-not-leak")
			if !errors.Is(err, test.want) || identity.OpenID != "" {
				t.Fatalf("错误分类不符合合同：want=%v got=%v", test.want, err)
			}
			if strings.Contains(err.Error(), "secret-must-not-leak") {
				t.Fatal("错误字符串泄露平台响应或凭据")
			}
		})
	}
}

// oauthTransportFailure 仅在测试中模拟网络依赖，不访问真实平台。
type oauthTransportFailure struct{ err error }

func (transport oauthTransportFailure) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, transport.err
}

// TestOAuthPreservesCancellationAndTimeout 防止依赖错误包装吞掉取消与超时因果链。
func TestOAuthPreservesCancellationAndTimeout(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("network secret-must-not-leak")} {
		client := NewOAuthClient("key", "secret", &http.Client{Transport: oauthTransportFailure{err: cause}})
		_, err := client.ExchangeCode(context.Background(), "code")
		if !errors.Is(err, cause) || errors.Is(err, visitor.ErrUnauthorized) || strings.Contains(err.Error(), "secret-must-not-leak") {
			t.Fatalf("网络错误被错误分类或泄露：%v", err)
		}
	}
}

func TestOAuthClientExchangesAuthorizationCodeServerSide(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("content-type") != "application/x-www-form-urlencoded" {
			t.Fatalf("unexpected request: method=%s content-type=%s", r.Method, r.Header.Get("content-type"))
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("client_key") != "client-key" || r.Form.Get("client_secret") != "client-secret" || r.Form.Get("code") != "one-time-code" {
			t.Fatalf("unexpected oauth form: %+v", r.Form)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"open_id":"open-id-1","access_token":"access-1","refresh_token":"refresh-1"}`))
	}))
	defer server.Close()

	client := NewOAuthClient("client-key", "client-secret", server.Client())
	client.endpoint = server.URL
	identity, err := client.ExchangeCode(context.Background(), "one-time-code")
	if err != nil {
		t.Fatal(err)
	}
	if identity.OpenID != "open-id-1" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
}
