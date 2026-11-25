// Package styx provides infrastructure initialization for Kratos applications.
package styx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

// parseLogLevel parses a string log level to slog.Level.
func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// replaceAttr customizes log attribute formatting.
// - Time: 日期与时间用空格分隔，保留毫秒和原有时区偏移，便于直接阅读
// - Source: "internal/data/session.go:42" (last 2 dirs + filename)
func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.TimeKey:
		if t, ok := a.Value.Any().(time.Time); ok {
			a.Value = slog.StringValue(t.Format("2006-01-02 15:04:05.000-07:00"))
		}
	case slog.SourceKey:
		if src, ok := a.Value.Any().(*slog.Source); ok {
			if src.File == "" {
				return slog.Attr{} // skip empty source (callerPC returned 0)
			}
			a.Value = slog.StringValue(shortSource(src.File, 2) + ":" + strconv.Itoa(src.Line))
		}
	}
	return a
}

// shortSource returns the last n path segments of a file path.
// e.g., shortSource("/a/b/c/d/file.go", 2) => "c/d/file.go"
func shortSource(file string, depth int) string {
	parts := strings.Split(file, string(filepath.Separator))
	if len(parts) <= depth+1 {
		return file
	}
	return strings.Join(parts[len(parts)-depth-1:], "/")
}

// levelHandler wraps a slog.Handler with level filtering.
type levelHandler struct {
	level   slog.Level
	handler slog.Handler
}

func (h *levelHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *levelHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.handler.Handle(ctx, r)
}

func (h *levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelHandler{level: h.level, handler: h.handler.WithAttrs(attrs)}
}

func (h *levelHandler) WithGroup(name string) slog.Handler {
	return &levelHandler{level: h.level, handler: h.handler.WithGroup(name)}
}

// traceHandler injects OpenTelemetry trace context into log records.
type traceHandler struct {
	handler slog.Handler
}

func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	addTraceAttrs(ctx, &r)
	return h.handler.Handle(ctx, r)
}

func addTraceAttrs(ctx context.Context, r *slog.Record) {
	spanCtx := trace.SpanContextFromContext(ctx)
	hasTraceID, hasSpanID := false, false
	r.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case "trace_id":
			hasTraceID = true
		case "span_id":
			hasSpanID = true
		}
		return !hasTraceID || !hasSpanID
	})
	if spanCtx.HasTraceID() && !hasTraceID {
		r.AddAttrs(slog.String("trace_id", spanCtx.TraceID().String()))
	}
	if spanCtx.HasSpanID() && !hasSpanID {
		r.AddAttrs(slog.String("span_id", spanCtx.SpanID().String()))
	}
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{handler: h.handler.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{handler: h.handler.WithGroup(name)}
}

// otelSafeHandler converts custom types (e.g., jsonBody) to plain strings
// before passing to the otelslog handler. This is needed because the OTel SDK's
// log.AnyValue() only recognizes built-in types and outputs "unhandled: (type)"
// for custom types, even if they implement fmt.Stringer.
type otelSafeHandler struct {
	handler slog.Handler
}

func (h *otelSafeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *otelSafeHandler) Handle(ctx context.Context, r slog.Record) error {
	newRecord := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		newRecord.AddAttrs(convertAttrForOTel(a))
		return true
	})
	addTraceAttrs(ctx, &newRecord)
	return h.handler.Handle(ctx, newRecord)
}

// convertAttrForOTel converts KindAny values with fmt.Stringer to StringValue.
func convertAttrForOTel(a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindAny {
		if s, ok := a.Value.Any().(fmt.Stringer); ok {
			a.Value = slog.StringValue(s.String())
		}
	}
	return a
}

func (h *otelSafeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &otelSafeHandler{handler: h.handler.WithAttrs(attrs)}
}

func (h *otelSafeHandler) WithGroup(name string) slog.Handler {
	return &otelSafeHandler{handler: h.handler.WithGroup(name)}
}

// multiHandler writes to multiple handlers.
type multiHandler struct {
	handlers []slog.Handler
}

func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var handlerErrors []error
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, r.Level) {
			// 各输出目标会追加 Trace 字段，必须隔离 Record 的共享属性存储。
			if err := handler.Handle(ctx, r.Clone()); err != nil {
				handlerErrors = append(handlerErrors, err)
			}
		}
	}
	return errors.Join(handlerErrors...)
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithAttrs(attrs)
	}
	return &multiHandler{handlers: handlers}
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithGroup(name)
	}
	return &multiHandler{handlers: handlers}
}

// NewLogger 根据配置创建日志器，不修改进程级默认日志器。
func NewLogger(cfg *configv1.LogConfig, opts ...LogOption) *slog.Logger {
	options := &logOptions{consoleOutput: os.Stdout}
	for _, opt := range opts {
		opt(options)
	}

	// Default to console output if config is nil
	if cfg == nil {
		cfg = &configv1.LogConfig{
			Level:   "info",
			Console: true,
		}
	}

	level := parseLogLevel(cfg.Level)
	var handlers []slog.Handler

	// Console handler
	console := cfg.Console
	if !cfg.Enabled && !cfg.Console {
		// If neither OTel nor console is enabled, default to console
		console = true
	}
	if console {
		consoleHandler := slog.NewJSONHandler(options.consoleOutput, &slog.HandlerOptions{
			Level:       level,
			AddSource:   true,
			ReplaceAttr: replaceAttr,
		})
		handlers = append(handlers, &traceHandler{handler: consoleHandler})
	}
	// 文件输出独立于控制台和 OTel，任一输出失败不阻断其余处理器。
	if options.fileOutput != nil {
		fileHandler := slog.NewJSONHandler(options.fileOutput, &slog.HandlerOptions{
			Level:       level,
			AddSource:   true,
			ReplaceAttr: replaceAttr,
		})
		handlers = append(handlers, &traceHandler{handler: fileHandler})
	}

	// OTel handler
	if cfg.Enabled && options.provider != nil && options.serviceName != "" {
		otelHandler := otelslog.NewHandler(options.serviceName,
			otelslog.WithLoggerProvider(options.provider),
		)
		// Wrap with otelSafeHandler (convert custom types to string) + level filter
		handlers = append(handlers, &levelHandler{level: level, handler: &otelSafeHandler{handler: otelHandler}})
	}
	if len(handlers) == 0 {
		consoleHandler := slog.NewJSONHandler(options.consoleOutput, &slog.HandlerOptions{
			Level:       level,
			AddSource:   true,
			ReplaceAttr: replaceAttr,
		})
		handlers = append(handlers, &traceHandler{handler: consoleHandler})
	}

	if len(handlers) == 1 {
		return slog.New(handlers[0])
	}
	return slog.New(&multiHandler{handlers: handlers})
}

// LogOption configures the logger.
type LogOption func(*logOptions)

type logOptions struct {
	serviceName   string
	provider      *log.LoggerProvider
	consoleOutput io.Writer
	fileOutput    io.Writer
}

// WithOTelProvider sets the OTel logger provider for log export.
func WithOTelProvider(serviceName string, provider *log.LoggerProvider) LogOption {
	return func(o *logOptions) {
		o.serviceName = serviceName
		o.provider = provider
	}
}

// WithConsoleOutput 设置控制台日志的输出目标。
func WithConsoleOutput(w io.Writer) LogOption {
	return func(options *logOptions) {
		if w != nil {
			options.consoleOutput = w
		}
	}
}

// WithFileOutput 为同一条 JSON 日志增加独立文件输出，不接管控制台输出。
func WithFileOutput(w io.Writer) LogOption {
	return func(options *logOptions) {
		options.fileOutput = w
	}
}
