package operations

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"drama-server/internal/pkg/ratelimit"
)

// loggingFailureWriter 模拟客户端断开，使响应写入返回实际 IO 错误。
type loggingFailureWriter struct {
	header http.Header
	err    error
}

// Header 返回响应头，不记录响应正文。
func (w loggingFailureWriter) Header() http.Header { return w.header }

// WriteHeader 保持测试关注点为正文写入故障。
func (w loggingFailureWriter) WriteHeader(int) {}

// Write 注入传输错误，不保存包含敏感字段的正文。
func (w loggingFailureWriter) Write([]byte) (int, error) { return 0, w.err }

// loggingFailureLimiter 模拟限流依赖故障以检查原有放行策略。
type loggingFailureLimiter struct{ err error }

// Ping 返回与限流调用相同的依赖故障。
func (l loggingFailureLimiter) Ping(context.Context) error { return l.err }

// Allow 返回指定依赖错误。
func (l loggingFailureLimiter) Allow(context.Context, string, string, int, time.Duration) (ratelimit.Decision, error) {
	return ratelimit.Decision{}, l.err
}

func TestHTTPErrorLoggingPreservesResponseAndFallback(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	failure := errors.New("SELECT id FROM admin_users: connection refused")
	for _, test := range []struct {
		name, message string
		run           func(*testing.T)
	}{
		{"HTTP映射保留原始日志", "operations request failed", func(t *testing.T) {
			response := httptest.NewRecorder()
			writeOperationsError(response, httptest.NewRequest(http.MethodGet, "/admin/v1/users", nil), failure)
			if response.Code != 500 || strings.Contains(response.Body.String(), failure.Error()) {
				t.Fatal("HTTP状态或错误脱敏发生变化")
			}
		}},
		{"响应写入故障", "write API response failed", func(t *testing.T) {
			writeData(loggingFailureWriter{header: make(http.Header), err: failure}, httptest.NewRequest(http.MethodGet, "/admin/v1/users", nil), http.StatusOK, map[string]string{"password": "private-password"})
		}},
		{"限流故障仍然放行", "operations rate limiter unavailable", func(t *testing.T) {
			h := &Handler{config: Config{RateLimiter: loggingFailureLimiter{err: failure}}}
			if !h.allowRequest(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/admin/v1/session", nil), "login", "private-account", 1, time.Minute) {
				t.Fatal("限流故障的放行行为改变")
			}
		}},
		{"未配置审计回调仍有日志", "admin audit persistence failed", func(t *testing.T) { (&Handler{}).reportAuditError(failure) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			test.run(t)
			raw := output.String()
			if !strings.Contains(raw, test.message) || !strings.Contains(raw, failure.Error()) {
				t.Fatalf("日志缺少实际错误：%s", raw)
			}
			for _, secret := range []string{"private-password", "private-account"} {
				if strings.Contains(raw, secret) {
					t.Fatal("日志泄漏请求或响应内容")
				}
			}
		})
	}
}
