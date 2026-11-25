package logutil

import (
	"bytes"
	"context"
	"log/slog"
	"runtime"
	"strings"
	"testing"
)

func TestIsFrameworkFrame(t *testing.T) {
	tests := []struct {
		funcName string
		want     bool
	}{
		// styx module
		{"gitee.com/linpbin/styx/middleware.HTTPAccessLog.func1.1", true},
		{"gitee.com/linpbin/styx/mysql.(*mysqlLogger).Trace", true},
		{"gitee.com/linpbin/styx/redis.(*LoggingHook).logCommand", true},
		{"gitee.com/linpbin/styx/internal/logutil.Info", true},

		// gorm
		{"gorm.io/gorm.(*DB).Create", true},
		{"gorm.io/gorm/callbacks.Create", true},

		// go-redis
		{"github.com/redis/go-redis/v9.(*Client).Process", true},

		// kratos
		{"github.com/go-kratos/kratos/v2/middleware.Chain.func1.1", true},

		// grpc
		{"google.golang.org/grpc.(*Server).Serve", true},

		// stdlib
		{"net/http.(*Server).Serve", true},
		{"database/sql.(*DB).Query", true},
		{"runtime.goexit", true},
		{"reflect.Value.Call", true},

		// business code — should NOT be framework
		{"example.com/acme/service/internal/data.(*accountRepo).GetByUID", false},
		{"example.com/acme/service/internal/biz.(*AuthUsecase).Login", false},
		{"main.main", false},
		{"testing.tRunner", false},
	}

	for _, tt := range tests {
		t.Run(tt.funcName, func(t *testing.T) {
			got := isFrameworkFrame(tt.funcName)
			if got != tt.want {
				t.Errorf("isFrameworkFrame(%q) = %v, want %v", tt.funcName, got, tt.want)
			}
		})
	}
}

func TestCallerPC_ReturnsNonZero(t *testing.T) {
	// When called from a test (non-framework code is on stack via testing.tRunner),
	// callerPC should return a non-zero PC.
	// Note: the test itself is in the styx module so it's skipped,
	// but testing.tRunner is not a framework frame.
	pc := callerPC()
	if pc == 0 {
		t.Error("callerPC() returned 0, expected non-zero PC")
	}
}

func TestCallerPC_SkipsFrameworkFrames(t *testing.T) {
	pc := callerPC()
	if pc == 0 {
		t.Skip("callerPC returned 0, cannot verify frame")
	}

	frames := runtime.CallersFrames([]uintptr{pc})
	frame, _ := frames.Next()

	// The resolved function should NOT be from a framework package
	if isFrameworkFrame(frame.Function) {
		t.Errorf("callerPC resolved to framework frame: %s (%s:%d)",
			frame.Function, frame.File, frame.Line)
	}
}

// captureHandler captures slog records for testing.
type captureHandler struct {
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }

func TestInfo_RecordHasCorrectFields(t *testing.T) {
	handler := &captureHandler{}
	orig := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(orig)

	Info(slog.Default(), context.Background(), "test message", "key", "value")

	if len(handler.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(handler.records))
	}

	r := handler.records[0]

	if r.Message != "test message" {
		t.Errorf("message = %q, want %q", r.Message, "test message")
	}
	if r.Level != slog.LevelInfo {
		t.Errorf("level = %v, want %v", r.Level, slog.LevelInfo)
	}

	// Check attributes
	var foundKey bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "key" && a.Value.String() == "value" {
			foundKey = true
		}
		return true
	})
	if !foundKey {
		t.Error("expected attr key=value not found")
	}
}

func TestError_RecordLevel(t *testing.T) {
	handler := &captureHandler{}
	orig := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(orig)

	Error(slog.Default(), context.Background(), "error message")

	if len(handler.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(handler.records))
	}
	if handler.records[0].Level != slog.LevelError {
		t.Errorf("level = %v, want %v", handler.records[0].Level, slog.LevelError)
	}
}

func TestWarn_RecordLevel(t *testing.T) {
	handler := &captureHandler{}
	orig := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(orig)

	Warn(slog.Default(), context.Background(), "warn message")

	if len(handler.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(handler.records))
	}
	if handler.records[0].Level != slog.LevelWarn {
		t.Errorf("level = %v, want %v", handler.records[0].Level, slog.LevelWarn)
	}
}

func TestInfo_SourceNotFromLogutil(t *testing.T) {
	handler := &captureHandler{}
	orig := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(orig)

	Info(slog.Default(), context.Background(), "test")

	if len(handler.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(handler.records))
	}

	r := handler.records[0]
	if r.PC == 0 {
		t.Skip("PC is 0, cannot verify source")
	}

	frames := runtime.CallersFrames([]uintptr{r.PC})
	frame, _ := frames.Next()

	// Source should NOT point to logutil package
	if strings.Contains(frame.File, "logutil/caller.go") {
		t.Errorf("source points to logutil itself: %s:%d", frame.File, frame.Line)
	}
}

func TestInfo_OutputContainsMessage(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: true,
	})
	orig := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(orig)

	Info(slog.Default(), context.Background(), "hello world", "foo", "bar")

	output := buf.String()
	if !strings.Contains(output, "hello world") {
		t.Errorf("output missing message, got: %s", output)
	}
	if !strings.Contains(output, "foo=bar") {
		t.Errorf("output missing attr foo=bar, got: %s", output)
	}
	// Source should NOT contain logutil/caller.go
	if strings.Contains(output, "logutil/caller.go") {
		t.Errorf("source should not point to logutil, got: %s", output)
	}
}

func TestLogRecord_DisabledLevel(t *testing.T) {
	handler := &captureHandler{}
	// Set a handler that only enables ERROR level
	levelHandler := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{
		Level: slog.LevelError,
	})
	_ = levelHandler
	orig := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(orig)

	// Override to only accept ERROR
	disabledHandler := &levelFilterHandler{level: slog.LevelError, h: handler}
	slog.SetDefault(slog.New(disabledHandler))

	Info(slog.Default(), context.Background(), "should be skipped")

	if len(handler.records) != 0 {
		t.Errorf("expected 0 records for disabled level, got %d", len(handler.records))
	}
}

type levelFilterHandler struct {
	level slog.Level
	h     *captureHandler
}

func (h *levelFilterHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}
func (h *levelFilterHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.h.Handle(ctx, r)
}
func (h *levelFilterHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *levelFilterHandler) WithGroup(name string) slog.Handler       { return h }
