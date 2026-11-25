package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitee.com/linpbin/styx/requestmeta"
	"go.opentelemetry.io/otel/trace"
)

// diagnosticCallbackRepository 保留回调布尔结果与错误，验证日志字段不改变幂等返回。
type diagnosticCallbackRepository struct {
	MediaJobRepository
	applied bool
	err     error
}

// ApplyVODCallback 返回指定回调结果，不访问真实平台。
func (r diagnosticCallbackRepository) ApplyVODCallback(context.Context, VODEvent) (bool, error) {
	return r.applied, r.err
}

// TestVODCallbackSeparatesHTTPAndPlatformRequestIDs 验证平台事件号不会覆盖 HTTP 请求号，重复回调与故障仍保留原结果。
func TestVODCallbackSeparatesHTTPAndPlatformRequestIDs(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, tc := range []struct {
		name    string
		applied bool
		err     error
	}{
		{name: "首次生效", applied: true}, {name: "重复回调"}, {name: "数据库失败", err: errors.New("callback transaction unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			service := NewService(Repositories{Media: MediaPorts{Jobs: diagnosticCallbackRepository{applied: tc.applied, err: tc.err}}}, nil)
			span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
			request := httptest.NewRequest("POST", "/callbacks/vod", nil).WithContext(trace.ContextWithSpanContext(context.Background(), span))
			request.Header.Set(requestmeta.HeaderName, "http-request-3")
			requestmeta.HTTPMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				applied, err := service.ReceiveVODCallback(r.Context(), VODEvent{EventType: VODEventFileUploadComplete, RequestID: "vod-event-4"})
				if applied != tc.applied || err != tc.err {
					t.Fatal("回调结果改变")
				}
			})).ServeHTTP(httptest.NewRecorder(), request)
			var entry map[string]any
			if json.Unmarshal(logs.Bytes(), &entry) != nil || entry["request_id"] != "http-request-3" || entry["platform_request_id"] != "vod-event-4" || entry["trace_id"] != span.TraceID().String() {
				t.Fatalf("回调标识混淆或丢失: %s", logs.String())
			}
			level := "INFO"
			if tc.err != nil {
				level = "ERROR"
				if entry["error"] != tc.err.Error() {
					t.Fatal("回调实际错误丢失")
				}
			}
			if entry["level"] != level {
				t.Fatal("回调正常处理被误报错误")
			}
		})
	}
}
