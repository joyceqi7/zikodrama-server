package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/visitor"
	"drama-server/internal/pkg/ratelimit"
)

const adErrorJSON = `{"id":"ad-failure-example-01","ad":{"adType":"rewarded","adUnitId":"slot","scene":"entry_reward","clientVersion":"build","os":"ios","tiktokVersion":"46.1","stage":"show","reason":"container_unavailable","errorCode":"101150000","subErrorCode":"50000","innerErrorCode":"50008"},"failure":{"error":"container unavailable token=private https://private.test","innerError":"bridge hidden cookie=private","showDocument":"visible","showNative":"unknown","showPrerender":false,"errorDocument":"hidden","errorNative":"unknown","errorPrerender":false,"shown":false,"userInitiated":false,"lockOwnerId":"ad-failure-example-01","previousAttemptId":"ad-previous-example-01","previousRelease":"closed","previousCloseMs":42}}`

func adErrorLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &output
}
func sendAdErrors(h *Handler, app, user, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/ad-errors", strings.NewReader(body))
	r = r.WithContext(application.WithContext(r.Context(), app))
	w := httptest.NewRecorder()
	h.collectAdErrors(w, r, visitor.Context{VisitorID: user})
	return w
}
func adErrorHandler() *Handler {
	return &Handler{config: Config{RateLimiter: ratelimit.NewMemory(100), AdErrorRequestsPerMinute: 6}}
}

// TestAdErrorLogOnly 验证没有 Analytics 依赖也可收集脱敏错误；批内去重且不回显现场。
func TestAdErrorLogOnly(t *testing.T) {
	output := adErrorLogs(t)
	response := sendAdErrors(adErrorHandler(), "101", "107", `{"events":[`+adErrorJSON+`,`+adErrorJSON+`]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("采集失败: %d %s", response.Code, response.Body)
	}
	logs := output.String()
	if strings.Count(logs, `"msg":"client ad failed"`) != 1 {
		t.Fatalf("批内日志未去重: %s", logs)
	}
	for _, want := range []string{`"application_id":"101"`, `"user_id":"107"`, `"event_id":"ad-failure-example-01"`, `"inner_error_code":"50008"`, `"showNative":"unknown"`, `"errorDocument":"hidden"`, `"previousCloseMs":42`, "container unavailable", "bridge hidden", `"level":"WARN"`} {
		if !strings.Contains(logs, want) {
			t.Fatalf("日志缺少 %s: %s", want, logs)
		}
	}
	if strings.Contains(logs, "private") || strings.Contains(response.Body.String(), "failure") {
		t.Fatal("日志泄漏敏感值或响应回显现场")
	}
	output.Reset()
	event := strings.ReplaceAll(adErrorJSON, "container_unavailable", "sdk_error")
	if w := sendAdErrors(adErrorHandler(), "101", "107", `{"events":[`+event+`]}`); w.Code != 200 || !strings.Contains(output.String(), `"level":"ERROR"`) {
		t.Fatal("未知 SDK 故障没有记录实际 ERROR")
	}
}

// TestAdErrorAuthorizationRedaction 验证外层与内部消息的完整 Bearer 凭据均在日志入口脱敏。
func TestAdErrorAuthorizationRedaction(t *testing.T) {
	for _, header := range []string{
		"Authorization: Bearer private-value",
		"AUTHORIZATION = bEaReR\tprivate-value",
		`"Authorization": "Bearer private-value"`,
		`Authorization: Bearer 'private-value'`,
		"Bearer private-value",
		"token=private-value",
	} {
		t.Run(header, func(t *testing.T) {
			output := adErrorLogs(t)
			var event adErrorInput
			if err := json.Unmarshal([]byte(adErrorJSON), &event); err != nil {
				t.Fatal(err)
			}
			event.Failure.Error = "outer SDK failure; " + header + "; retryable"
			event.Failure.InnerError = "inner bridge failure; " + header + "; retryable"
			body, err := json.Marshal(map[string]any{"events": []adErrorInput{event}})
			if err != nil {
				t.Fatal(err)
			}
			response := sendAdErrors(adErrorHandler(), "101", "107", string(body))
			if response.Code != http.StatusOK {
				t.Fatalf("脱敏不能改变接收结果: %d", response.Code)
			}
			logs := output.String()
			if strings.Contains(logs, "private-value") || strings.Count(logs, `"msg":"client ad failed"`) != 1 {
				t.Fatalf("凭据泄漏或错误日志数量变化: %s", logs)
			}
			for _, want := range []string{"outer SDK failure", "inner bridge failure", "; retryable", "[REDACTED]", `"level":"WARN"`} {
				if !strings.Contains(logs, want) {
					t.Fatalf("脱敏丢失诊断内容 %q: %s", want, logs)
				}
			}
		})
	}
}

// TestAdErrorCookieRedaction 验证多值 Cookie 的全部凭据在日志入口隐藏，保留操作与后续故障说明。
func TestAdErrorCookieRedaction(t *testing.T) {
	for _, header := range []string{
		"Cookie: a=private-first; session=private-second",
		"cOoKiE = a=private-first; session=private-second",
		`"Cookie": "a=private-first; session=private-second"`,
		`"Cookie": "a=private-first; session=\"two private-second\""`,
		"Set-Cookie: session=private-first; HttpOnly; Path=/",
		"Cookie:",
	} {
		t.Run(header, func(t *testing.T) {
			output := adErrorLogs(t)
			var event adErrorInput
			if err := json.Unmarshal([]byte(adErrorJSON), &event); err != nil {
				t.Fatal(err)
			}
			event.Failure.Error = "outer SDK failure\n" + header + "\nbridge unavailable"
			event.Failure.InnerError = "inner bridge failure\n" + header + "\nretryable"
			body, err := json.Marshal(map[string]any{"events": []adErrorInput{event}})
			if err != nil {
				t.Fatal(err)
			}
			if response := sendAdErrors(adErrorHandler(), "101", "107", string(body)); response.Code != http.StatusOK {
				t.Fatalf("脱敏不能改变接收结果: %d", response.Code)
			}
			logs := output.String()
			if strings.Contains(logs, "private-") || strings.Count(logs, `"msg":"client ad failed"`) != 1 {
				t.Fatal("多值 Cookie 泄漏或日志数量变化")
			}
			for _, want := range []string{"outer SDK failure", "inner bridge failure", "bridge unavailable", "retryable", "[REDACTED]"} {
				if !strings.Contains(logs, want) {
					t.Fatalf("诊断说明丢失: %s", want)
				}
			}
		})
	}
}

// TestAdErrorBoundary 拒绝成功事件、通用日志、未知字段、超长消息、大包与批量越界。
func TestAdErrorBoundary(t *testing.T) {
	output := adErrorLogs(t)
	bodies := []string{
		`{"events":[]}`, `{"events":[null]}`, `{"events":[{}]}`,
		`{"events":[` + strings.Replace(adErrorJSON, `"ad":`, `"kind":"ad_shown","ad":`, 1) + `]}`,
		`{"events":[` + strings.Replace(adErrorJSON, `"failure":`, `"userId":"private","failure":`, 1) + `]}`,
		`{"events":[` + strings.Replace(adErrorJSON, `"container_unavailable"`, `"user_cancel"`, 1) + `]}`,
		`{"events":[` + strings.Replace(adErrorJSON, `"innerError":`, `"snapshots":[],"innerError":`, 1) + `]}`,
		`{"events":[` + strings.Replace(adErrorJSON, "container unavailable", strings.Repeat("界", 65), 1) + `]}`,
		`{"events":[` + strings.TrimSuffix(strings.Repeat(adErrorJSON+",", 6), ",") + `]}`,
		`{"events":[` + adErrorJSON + `]}` + strings.Repeat(" ", 8192),
		`{"events":[` + adErrorJSON + `]} {}`,
	}
	for i, body := range bodies {
		output.Reset()
		w := sendAdErrors(adErrorHandler(), "101", "107", body)
		if w.Code != 400 {
			t.Fatalf("非法请求 %d 被接受: %d", i, w.Code)
		}
		if output.Len() == 0 || strings.Contains(output.String(), "client ad failed") || strings.Contains(output.String(), "private") {
			t.Fatalf("拒绝日志缺失或泄漏请求 %d: %s", i, output.String())
		}
	}
}

// TestAdErrorRateLimit 验证限流先于解析、按应用和用户隔离，不影响独立统计请求。
func TestAdErrorRateLimit(t *testing.T) {
	output := adErrorLogs(t)
	h := adErrorHandler()
	for i := 0; i < 6; i++ {
		if w := sendAdErrors(h, "101", "107", `{"events":[`+adErrorJSON+`]}`); w.Code != 200 {
			t.Fatalf("额度内错误: %d", w.Code)
		}
	}
	output.Reset()
	w := sendAdErrors(h, "101", "107", "invalid private body")
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || output.Len() != 0 {
		t.Fatalf("超限应直接拒绝且不刷错误日志: %d %s", w.Code, output.String())
	}
	for _, pair := range [][2]string{{"102", "107"}, {"101", "108"}} {
		if w := sendAdErrors(h, pair[0], pair[1], `{"events":[`+adErrorJSON+`]}`); w.Code != 200 {
			t.Fatalf("限流错误影响其他主体: %d", w.Code)
		}
	}
	repository := &serverTimeMetricRepository{}
	h.config.Analytics = domain.NewAnalyticsService(repository)
	output.Reset()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", strings.NewReader(`{"events":[{"id":"metric-example-0001","kind":"active"}]}`))
	response := httptest.NewRecorder()
	h.collectMetrics(response, r, visitor.Context{VisitorID: "107"})
	if response.Code != 200 || len(repository.events) != 1 || output.Len() != 0 {
		t.Fatal("日志限流影响统计或正常指标误报错误")
	}
}

type failedAdLimiter struct{ ratelimit.Limiter }

// Allow 注入限流依赖故障，验证不能绕过保护而打印现场。
func (failedAdLimiter) Allow(context.Context, string, string, int, time.Duration) (ratelimit.Decision, error) {
	return ratelimit.Decision{}, errors.New("limiter unavailable")
}

// TestAdErrorLimiterFailure 验证限流故障关闭日志入口，记录具体错误而不输出现场。
func TestAdErrorLimiterFailure(t *testing.T) {
	output := adErrorLogs(t)
	h := adErrorHandler()
	h.config.RateLimiter = failedAdLimiter{}
	w := sendAdErrors(h, "101", "107", `{"events":[`+adErrorJSON+`]}`)
	if w.Code != 503 || !strings.Contains(output.String(), "limiter unavailable") || !strings.Contains(output.String(), `"level":"ERROR"`) || strings.Contains(output.String(), "client ad failed") {
		t.Fatalf("限流故障处理错误: %d %s", w.Code, output.String())
	}
}

// TestMetricsExcludeAdErrorFacts 验证旧现场合同直接移除；统计独立保留错误事件和三种码。
func TestMetricsExcludeAdErrorFacts(t *testing.T) {
	output := adErrorLogs(t)
	repository := &serverTimeMetricRepository{}
	h := &Handler{config: Config{Analytics: domain.NewAnalyticsService(repository)}}
	var event adErrorInput
	if err := json.Unmarshal([]byte(adErrorJSON), &event); err != nil {
		t.Fatal(err)
	}
	metric := domain.MetricEvent{ID: event.ID, Kind: "ad_failure", Ad: event.Ad}
	payload, err := json.Marshal(map[string]any{"events": []any{metric}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	h.collectMetrics(w, r, visitor.Context{VisitorID: "107"})
	if w.Code != 200 || len(repository.events) != 1 || repository.events[0].Ad.InnerErrorCode != "50008" || output.Len() != 0 {
		t.Fatal("广告错误统计缺失或误打现场日志")
	}
	legacy := strings.Replace(string(payload), `"kind":"ad_failure"`, `"kind":"ad_failure","adFailure":{"error":"private"}`, 1)
	r = httptest.NewRequest(http.MethodPost, "/api/v1/metrics", strings.NewReader(legacy))
	w = httptest.NewRecorder()
	h.collectMetrics(w, r, visitor.Context{VisitorID: "107"})
	if w.Code != 400 || len(repository.events) != 1 || strings.Contains(output.String(), "private") {
		t.Fatal("旧的现场合同未删除或参数拒绝日志泄漏正文")
	}
}

// TestAdFailureValidation 验证有限字段不能扩大成快照、自由标识或无限错误消息。
func TestAdFailureValidation(t *testing.T) {
	event := &domain.MetricEvent{ID: "ad-failure-example-01", Kind: "ad_failure", Ad: &domain.AdMetricDetails{}}
	valid := func() *adFailureFacts {
		return &adFailureFacts{Error: "SDK unavailable", ErrorDocument: "visible", ErrorNative: "unknown"}
	}
	if !validateAdFailure(event, valid()) {
		t.Fatal("合法失败事实被拒绝")
	}
	for _, mutate := range []func(*adFailureFacts){
		func(p *adFailureFacts) { p.Error = strings.Repeat("a", 193) },
		func(p *adFailureFacts) { p.ErrorNative = "secret" },
		func(p *adFailureFacts) { p.LockOwnerID = "token=secret" },
		func(p *adFailureFacts) { p.ShowDocument = "visible" },
		func(p *adFailureFacts) { p.PreviousRelease = "closed" },
		func(p *adFailureFacts) { p.PreviousAttemptID = event.ID; p.PreviousRelease = "closed" },
		func(p *adFailureFacts) {
			p.PreviousAttemptID = "ad-previous-example-01"
			p.PreviousRelease = "user supplied"
		},
	} {
		p := valid()
		mutate(p)
		if validateAdFailure(event, p) {
			t.Fatalf("非法失败事实被接受: %+v", p)
		}
	}
	event.Kind = "ad_shown"
	if validateAdFailure(event, valid()) {
		t.Fatal("成功事件不能附带失败诊断")
	}
}
