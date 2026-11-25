package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"gitee.com/linpbin/styx/observe"
)

type auditLogCapture struct {
	records []slog.Record
}

func TestLoggingDoesNotRecordPayloadOrErrorMessage(t *testing.T) {
	capture := &auditLogCapture{}
	dependencies := auditDependencies(t, slog.New(capture))
	middleware := Logging(dependencies)
	handler := middleware(func(context.Context, any) (any, error) {
		return map[string]string{"token": "reply-secret"}, kratoserrors.BadRequest("INVALID", "error-secret")
	})
	_, _ = handler(context.Background(), map[string]string{"token": "request-secret"})

	serialized := ""
	for _, record := range capture.records {
		serialized += record.Message
		record.Attrs(func(attribute slog.Attr) bool {
			serialized += fmt.Sprint(attribute.Value.Any())
			return true
		})
	}
	for _, secret := range []string{"request-secret", "reply-secret", "error-secret"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("请求日志泄露敏感正文 %q", secret)
		}
	}
}

func TestRecoveryDoesNotRecordPanicValue(t *testing.T) {
	capture := &auditLogCapture{}
	dependencies := auditDependencies(t, slog.New(capture))
	handler := Recovery("audit", dependencies)(func(context.Context, any) (any, error) {
		panic("panic-secret")
	})
	_, err := handler(context.Background(), nil)
	if err == nil {
		t.Fatal("panic 必须转换为服务错误")
	}

	for _, record := range capture.records {
		serialized := record.Message
		record.Attrs(func(attribute slog.Attr) bool {
			serialized += fmt.Sprint(attribute.Value.Any())
			return true
		})
		if strings.Contains(serialized, "panic-secret") {
			t.Fatal("恢复日志泄露 panic 值")
		}
	}
}

func (h *auditLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (h *auditLogCapture) Handle(_ context.Context, record slog.Record) error {
	h.records = append(h.records, record)
	return nil
}
func (h *auditLogCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *auditLogCapture) WithGroup(string) slog.Handler      { return h }

func auditDependencies(t *testing.T, logger *slog.Logger) observe.Dependencies {
	t.Helper()
	dependencies, err := observe.NewDependencies(observe.Config{
		Logger:         logger,
		TracerProvider: tracenoop.NewTracerProvider(),
		MeterProvider:  metricnoop.NewMeterProvider(),
		Propagator:     propagation.TraceContext{},
	})
	if err != nil {
		t.Fatalf("创建测试运行依赖：%v", err)
	}
	return dependencies
}

func TestAuditAccessLogRedactsSensitiveQuery(t *testing.T) {
	capture := &auditLogCapture{}
	dependencies := auditDependencies(t, slog.New(capture))

	handler := HTTPAccessLog(dependencies)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/users/path-secret?access_token=query-secret", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	serialized := ""
	for _, record := range capture.records {
		record.Attrs(func(attr slog.Attr) bool {
			serialized += fmt.Sprint(attr.Value.Any())
			return true
		})
	}
	// 访问路径用于定位包括 404 在内的请求，查询参数仍禁止采集。
	if !strings.Contains(serialized, "/users/path-secret") || strings.Contains(serialized, "query-secret") {
		t.Fatal("访问日志必须保留原始路径并排除查询参数中的敏感值")
	}
}

func TestAuditAccessLogPreservesFlusher(t *testing.T) {
	flusherAvailable := false
	handler := HTTPAccessLog(observe.NoopDependencies())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, flusherAvailable = w.(http.Flusher)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/stream", nil))
	if !flusherAvailable {
		t.Fatal("访问日志包装器破坏了 http.Flusher 能力")
	}
}

func TestHTTPRecoveryProtectsRawHandlersWithoutPanicDetails(t *testing.T) {
	capture := &auditLogCapture{}
	dependencies := auditDependencies(t, slog.New(capture))
	handler := HTTPRecovery("audit", dependencies)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("raw-handler-secret")
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/raw", nil))
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "raw-handler-secret") {
		t.Fatalf("原始 HTTP panic 响应不安全：status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	for _, record := range capture.records {
		if strings.Contains(record.Message, "raw-handler-secret") {
			t.Fatal("原始 HTTP panic 日志泄露 panic 值")
		}
	}
}
