package styx

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

func TestReplaceAttrFormatsTimeWithMillisecondsAndOffset(t *testing.T) {
	original := time.Date(2026, time.August, 3, 12, 0, 0, 873522000, time.FixedZone("UTC+8", 8*60*60))
	attribute := replaceAttr(nil, slog.Time(slog.TimeKey, original))
	if got := attribute.Value.String(); got != "2026-08-03 12:00:00.873+08:00" {
		t.Fatalf("日志时间=%q，期望保留毫秒和原有时区偏移", got)
	}
}

func TestLoggerWritesOneTraceIDToConsoleAndFile(t *testing.T) {
	var console, file bytes.Buffer
	logger := NewLogger(&configv1.LogConfig{Level: "info", Console: true},
		WithConsoleOutput(&console), WithFileOutput(&file))
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
		SpanID:  trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18},
	})
	logger.InfoContext(trace.ContextWithSpanContext(context.Background(), spanContext), "dual output")
	for name, output := range map[string]string{"console": console.String(), "file": file.String()} {
		if got := bytes.Count([]byte(output), []byte(`"trace_id"`)); got != 1 {
			t.Fatalf("%s trace_id 出现 %d 次：%s", name, got, output)
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(output), &record); err != nil {
			t.Fatalf("%s 不是单行 JSON：%v", name, err)
		}
		if record["msg"] != "dual output" || record["trace_id"] != spanContext.TraceID().String() {
			t.Fatalf("%s 内容不一致：%v", name, record)
		}
		loggedTime, ok := record["time"].(string)
		if !ok {
			t.Fatalf("%s 缺少日志时间：%v", name, record)
		}
		if _, err := time.Parse("2006-01-02 15:04:05.000-07:00", loggedTime); err != nil {
			t.Fatalf("%s 日志时间格式不正确：%v", name, err)
		}
	}
}

func TestLoggerDoesNotDuplicateExplicitTraceID(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(&configv1.LogConfig{Level: "info", Console: true}, WithConsoleOutput(&output))
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2},
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	logger.InfoContext(ctx, "request handled", "trace_id", spanContext.TraceID().String())
	if got := bytes.Count(output.Bytes(), []byte(`"trace_id"`)); got != 1 {
		t.Fatalf("显式 trace_id 与自动字段不能重复，实际 %d 次：%s", got, output.String())
	}
}

type captureSlogHandler struct {
	record slog.Record
}

func (h *captureSlogHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h *captureSlogHandler) Handle(_ context.Context, r slog.Record) error {
	h.record = r
	return nil
}

func (h *captureSlogHandler) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

func (h *captureSlogHandler) WithGroup(string) slog.Handler {
	return h
}

func TestOTelSafeHandlerAddsTraceAttrs(t *testing.T) {
	traceID := trace.TraceID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	spanID := trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18}
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanCtx)

	capture := &captureSlogHandler{}
	handler := &otelSafeHandler{handler: capture}
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "mysql query", 0)
	record.AddAttrs(slog.String("db.system", "mysql"))

	if err := handler.Handle(ctx, record); err != nil {
		t.Fatalf("handle log record: %v", err)
	}

	attrs := map[string]string{}
	capture.record.Attrs(func(a slog.Attr) bool {
		if a.Value.Kind() == slog.KindString {
			attrs[a.Key] = a.Value.String()
		}
		return true
	})

	if attrs["trace_id"] != traceID.String() {
		t.Fatalf("trace_id = %q, want %q", attrs["trace_id"], traceID.String())
	}
	if attrs["span_id"] != spanID.String() {
		t.Fatalf("span_id = %q, want %q", attrs["span_id"], spanID.String())
	}
	if attrs["db.system"] != "mysql" {
		t.Fatalf("db.system = %q, want mysql", attrs["db.system"])
	}
}
