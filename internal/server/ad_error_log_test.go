package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	styx "gitee.com/linpbin/styx"
	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/requestmeta"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/visitor"
	"drama-server/internal/pkg/ratelimit"
	httpapi "drama-server/internal/service/consumer"
	"drama-server/internal/testsupport/memory"
)

// TestAdErrorHTTPAccessLogExcludesRequestBody 覆盖实际 Styx、鉴权、校验与限流链路，防止访问日志绕过现场脱敏。
func TestAdErrorHTTPAccessLogExcludesRequestBody(t *testing.T) {
	const event = `{"id":"ad-log-regression-0001","ad":{"adType":"rewarded","adUnitId":"slot","scene":"vip_reward","clientVersion":"test-build","os":"ios","tiktokVersion":"unknown","stage":"show","reason":"container_unavailable","errorCode":"101150000","subErrorCode":"50000","innerErrorCode":"50008"},"failure":{"error":"SDK container failed token=private-token https://private.test/media","innerError":"bridge hidden; Authorization: Bearer private-bearer","showDocument":"visible","showNative":"unknown","showPrerender":false,"errorDocument":"hidden","errorNative":"hidden","errorPrerender":false,"shown":false,"userInitiated":false,"lockOwnerId":"ad-log-regression-0001","previousAttemptId":"ad-log-previous-0001","previousRelease":"closed","previousCloseMs":42}}`
	valid := `{"events":[` + event + `,` + event + `]}`
	for _, testCase := range []struct {
		name      string
		body      string
		status    int
		failures  int
		anonymous bool
		warmup    bool
	}{
		{name: "成功且批内重复", body: valid, status: http.StatusOK, failures: 1},
		{name: "参数校验拒绝", body: strings.Replace(valid, "container_unavailable", "user_cancel", -1), status: http.StatusBadRequest},
		{name: "未知字段拒绝", body: strings.Replace(valid, `"innerError":`, `"snapshots":[],"innerError":`, 1), status: http.StatusBadRequest},
		{name: "非法JSON拒绝", body: valid + ` {}`, status: http.StatusBadRequest},
		{name: "超出8KiB拒绝", body: valid + strings.Repeat(" ", 8192), status: http.StatusBadRequest},
		{name: "非UTF8正文拒绝", body: "\xff\xfeprivate-binary", status: http.StatusBadRequest},
		{name: "未认证拒绝", body: valid, status: http.StatusUnauthorized, anonymous: true},
		{name: "超限拒绝", body: valid, status: http.StatusTooManyRequests, warmup: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// 业务层沿用默认 slog；逐项串行替换，不能和其他全局 logger 测试并行。
			var output bytes.Buffer
			logger := styx.NewLogger(nil, styx.WithConsoleOutput(&output))
			previous := slog.Default()
			slog.SetDefault(logger)
			t.Cleanup(func() { slog.SetDefault(previous) })
			store := memory.NewStore(memory.Seed{})
			visitors, err := visitor.NewService(contractApplicationResolver{}, store, visitor.Config{
				IdentityTokenEncryptionSecret: "ad-error-test-identity-encryption-key",
			})
			if err != nil {
				t.Fatal(err)
			}
			identity, err := visitors.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "1"})
			if err != nil {
				t.Fatal(err)
			}
			consumer := httpapi.NewHandler(domain.NewService(store, time.Now), visitors, nil, httpapi.Config{
				RateLimiter: ratelimit.NewMemory(10), AdErrorRequestsPerMinute: 1,
			})
			engine, err := buildTestHTTPServer(Dependencies{
				Consumer: consumer, Admin: http.NotFoundHandler(), Styx: newHTTPTestRuntimeWithSlog(t, logger),
				Config: &configv1.HTTPServerConfig{Addr: ":0"},
			})
			if err != nil {
				t.Fatal(err)
			}
			send := func(body string, authenticated bool) *httptest.ResponseRecorder {
				t.Helper()
				reader := strings.NewReader(body)
				request := httptest.NewRequest(http.MethodPost, "/api/v1/ad-errors", reader)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set(requestmeta.HeaderName, "ad-error-log-regression")
				if authenticated {
					request.Header.Set("Authorization", "Bearer "+identity.VisitorToken)
				}
				response := httptest.NewRecorder()
				// 通过生产构造函数创建的完整 HTTP 链路，同步等待访问日志输出后再断言。
				engine.ServeHTTP(response, request)
				if (response.Code == http.StatusUnauthorized || response.Code == http.StatusTooManyRequests) && reader.Len() != len(body) {
					t.Fatal("鉴权或限流拒绝前不应读取错误现场")
				}
				return response
			}
			if testCase.warmup && send(valid, true).Code != http.StatusOK {
				t.Fatal("限流预热未成功接收完整现场")
			}
			output.Reset()
			response := send(testCase.body, !testCase.anonymous)
			if response.Code != testCase.status {
				t.Fatalf("正文隔离改变接口结果：status=%d want=%d", response.Code, testCase.status)
			}
			if testCase.warmup && response.Header().Get("Retry-After") == "" {
				t.Fatal("限流响应缺少重试窗口")
			}
			for _, secret := range []string{"private-token", "private-bearer", "private.test", "private-binary", identity.VisitorToken} {
				if strings.Contains(output.String(), secret) {
					t.Fatal("HTTP 日志链路泄露原始现场或凭据")
				}
			}
			decoder := json.NewDecoder(strings.NewReader(output.String()))
			accesses, failures := 0, 0
			for {
				var entry map[string]any
				if err := decoder.Decode(&entry); err == io.EOF {
					break
				} else if err != nil {
					t.Fatalf("日志不是有效 JSON：%v", err)
				}
				switch entry["msg"] {
				case "http access":
					accesses++
					if entry["method"] != http.MethodPost || entry["path"] != "/api/v1/ad-errors" || entry["status"] != float64(testCase.status) || entry["request_id"] != response.Header().Get(requestmeta.HeaderName) {
						t.Fatal("访问日志丢失方法、路径、状态或请求关联")
					}
					if duration, ok := entry["duration_ms"].(float64); !ok || duration <= 0 || entry["trace_id"] == "" || entry["trace_id"] == nil {
						t.Fatal("访问日志丢失耗时或链路标识")
					}
					for key := range entry {
						if strings.HasPrefix(key, "request_body") {
							t.Fatalf("访问日志仍采集请求正文：%s", key)
						}
					}
				case "client ad failed":
					failures++
					facts, ok := entry["failure"].(map[string]any)
					if !ok || entry["event_id"] != "ad-log-regression-0001" || entry["application_id"] != "1" || entry["user_id"] != identity.VisitorID || entry["inner_error_code"] != "50008" || entry["level"] != "WARN" {
						t.Fatal("错误专用日志丢失可信身份、调用键或原始错误码")
					}
					if facts["error"] != "SDK container failed token=[REDACTED] [URL]" || facts["innerError"] != "bridge hidden; Authorization: [REDACTED]" || facts["showNative"] != "unknown" || facts["errorDocument"] != "hidden" || facts["previousCloseMs"] != float64(42) {
						t.Fatal("错误专用日志丢失脱敏消息或失败现场")
					}
				}
			}
			if accesses != 1 || failures != testCase.failures {
				t.Fatalf("访问或错误专用日志重复、缺失：access=%d failure=%d", accesses, failures)
			}
		})
	}
}
