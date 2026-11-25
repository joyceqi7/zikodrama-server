package operations_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	domain "drama-server/internal/biz/operations"
	"drama-server/internal/pkg/ratelimit"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
)

type operationsRateLimiterStub struct {
	decision  ratelimit.Decision
	denyScope string
	err       error
	observe   func(scope, subject string)
}

func (stub operationsRateLimiterStub) Allow(_ context.Context, scope, subject string, _ int, _ time.Duration) (ratelimit.Decision, error) {
	if stub.observe != nil {
		stub.observe(scope, subject)
	}
	if stub.err != nil || stub.denyScope == "" || stub.denyScope == scope {
		return stub.decision, stub.err
	}
	return ratelimit.Decision{Allowed: true}, nil
}

func TestAdminLoginNormalizesAccountRateLimitSubject(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	accountSubject := ""
	handler := operationshttp.NewHandler(nil, newRBACService(t, store), operationshttp.Config{
		RateLimiter: operationsRateLimiterStub{
			decision: ratelimit.Decision{Allowed: true},
			observe: func(scope, subject string) {
				if scope == "admin_login_account" {
					accountSubject = subject
				}
			},
		},
	})
	response := requestWithHeaders(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "",
		strings.NewReader(`{"username":"  AdMiN  ","password":"wrong-password"}`), nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("错误凭据应返回未授权，实际 %d：%s", response.Code, response.Body.String())
	}
	if accountSubject != "admin" {
		t.Fatalf("账号限流主体未按认证规则规范化：%q", accountSubject)
	}
}

func (stub operationsRateLimiterStub) Ping(context.Context) error { return stub.err }

func TestAdminLoginReturnsFriendlyRateLimitResponse(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	handler := operationshttp.NewHandler(nil, newRBACService(t, store), operationshttp.Config{
		RateLimiter: operationsRateLimiterStub{
			decision:  ratelimit.Decision{Allowed: false, RetryAfter: 9 * time.Second},
			denyScope: "admin_login_ip",
		},
	})
	response := requestWithHeaders(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "",
		strings.NewReader(`{"username":"admin","password":"wrong-password"}`), map[string]string{"Accept-Language": "zh-CN"})
	if response.Code != http.StatusTooManyRequests || response.Header().Get("retry-after") != "9" ||
		!strings.Contains(response.Body.String(), `"reason":"too_many_requests"`) ||
		!strings.Contains(response.Body.String(), "Too many operations were submitted. Please wait a moment and try again.") {
		t.Fatalf("运营限流响应不符合友好错误合同：status=%d retry=%q body=%s",
			response.Code, response.Header().Get("retry-after"), response.Body.String())
	}
}

func TestAdminMutationReturnsFriendlyRateLimitResponse(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	handler := operationshttp.NewHandler(
		domain.NewService(memory.OperationsRepositories(store), nil),
		newRBACService(t, store),
		operationshttp.Config{RateLimiter: operationsRateLimiterStub{
			decision:  ratelimit.Decision{Allowed: false, RetryAfter: 12 * time.Second},
			denyScope: "admin_mutation_user",
		}},
	)
	login := requestWithHeaders(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "",
		strings.NewReader(`{"username":"admin","password":"local-admin-password"}`), nil)
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)

	response := requestWithHeaders(t, handler, http.MethodPost, "/admin/v1/dramas", "application/json", credentials.Token,
		strings.NewReader(`{}`), map[string]string{"Accept-Language": "zh-CN"})
	if response.Code != http.StatusTooManyRequests || response.Header().Get("retry-after") != "12" ||
		!strings.Contains(response.Body.String(), `"reason":"too_many_requests"`) ||
		!strings.Contains(response.Body.String(), "Too many operations were submitted. Please wait a moment and try again.") {
		t.Fatalf("运营写操作限流响应不符合友好错误合同：status=%d retry=%q body=%s",
			response.Code, response.Header().Get("retry-after"), response.Body.String())
	}
}

func TestBatchMediaUploadSkipsGenericMutationRateLimit(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	mutationChecks := 0
	handler := operationshttp.NewHandler(
		domain.NewService(memory.OperationsRepositories(store), nil),
		newRBACService(t, store),
		operationshttp.Config{RateLimiter: operationsRateLimiterStub{
			decision:  ratelimit.Decision{Allowed: false, RetryAfter: time.Minute},
			denyScope: "admin_mutation_user",
			observe: func(scope, _ string) {
				if scope == "admin_mutation_user" {
					mutationChecks++
				}
			},
		}},
	)
	login := requestWithHeaders(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "",
		strings.NewReader(`{"username":"admin","password":"local-admin-password"}`), nil)
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)

	// 一次 70 集会分别发起媒资预留和上传完成提交；两轮请求均不能占用普通写操作配额。
	for _, path := range []string{
		"/admin/v1/episodes/5016/media-uploads",
		"/admin/v1/episodes/5016/media/direct",
	} {
		for episode := 0; episode < 70; episode++ {
			response := requestWithHeaders(t, handler, http.MethodPost, path, "application/json", credentials.Token,
				strings.NewReader(`{}`), nil)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("第 %d 集的 %s 请求应进入参数校验，实际 %d：%s", episode+1, path, response.Code, response.Body.String())
			}
		}
	}
	if mutationChecks != 0 {
		t.Fatalf("批量媒资请求不应进入普通写操作限流，实际检查 %d 次", mutationChecks)
	}

	// 普通管理写操作仍走原有频次保护。
	response := requestWithHeaders(t, handler, http.MethodPost, "/admin/v1/dramas", "application/json", credentials.Token,
		strings.NewReader(`{}`), nil)
	if response.Code != http.StatusTooManyRequests || mutationChecks != 1 {
		t.Fatalf("普通管理写操作应被限流，status=%d checks=%d", response.Code, mutationChecks)
	}
}
