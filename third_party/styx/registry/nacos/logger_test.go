package nacos

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	nacoslogger "github.com/nacos-group/nacos-sdk-go/v2/common/logger"

	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

// captureHandler captures slog output for testing.
type captureHandler struct {
	buf *bytes.Buffer
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.buf.WriteString(r.Level.String() + ":" + r.Message + "\n")
	return nil
}
func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }

func setupCapture(t *testing.T) (*bytes.Buffer, *slog.Logger) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger := slog.New(&captureHandler{buf: buf})
	return buf, logger
}

func TestNewSlogAdapter_DefaultLevel(t *testing.T) {
	a := newSlogAdapter("", slog.Default())
	if a.level != slog.LevelWarn {
		t.Errorf("expected default level Warn, got %v", a.level)
	}

	a = newSlogAdapter("unknown", slog.Default())
	if a.level != slog.LevelWarn {
		t.Errorf("expected default level Warn for unknown, got %v", a.level)
	}
}

func TestNewSlogAdapter_ExplicitLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			a := newSlogAdapter(tt.input, slog.Default())
			if a.level != tt.want {
				t.Errorf("newSlogAdapter(%q).level = %v, want %v", tt.input, a.level, tt.want)
			}
		})
	}
}

func TestSlogAdapter_LevelFiltering(t *testing.T) {
	tests := []struct {
		name      string
		level     string
		wantDebug bool
		wantInfo  bool
		wantWarn  bool
		wantError bool
	}{
		{"debug level", "debug", true, true, true, true},
		{"info level", "info", false, true, true, true},
		{"warn level", "warn", false, false, true, true},
		{"error level", "error", false, false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf, logger := setupCapture(t)
			a := newSlogAdapter(tt.level, logger)

			a.Debug("dmsg")
			a.Info("imsg")
			a.Warn("wmsg")
			a.Error("emsg")

			output := buf.String()
			checkContains(t, output, "DEBUG:dmsg", tt.wantDebug)
			checkContains(t, output, "INFO:imsg", tt.wantInfo)
			checkContains(t, output, "WARN:wmsg", tt.wantWarn)
			checkContains(t, output, "ERROR:emsg", tt.wantError)
		})
	}
}

func TestSlogAdapter_FormattedMethods(t *testing.T) {
	buf, logger := setupCapture(t)
	a := newSlogAdapter("info", logger)

	a.Debugf("d%s", "msg")
	a.Infof("i%s", "msg")
	a.Warnf("w%s", "msg")
	a.Errorf("e%s", "msg")

	output := buf.String()
	checkContains(t, output, "DEBUG:dmsg", false)
	checkContains(t, output, "INFO:imsg", true)
	checkContains(t, output, "WARN:wmsg", true)
	checkContains(t, output, "ERROR:emsg", true)
}

func TestSlogAdapter_Close(t *testing.T) {
	a := newSlogAdapter("info", slog.Default())
	if err := a.Close(); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

func TestSlogAdapterRedactsConfiguredCredentials(t *testing.T) {
	buf, logger := setupCapture(t)
	adapter := newSlogAdapter("debug", logger, "user-secret", "password-secret")
	adapter.Infof("client user=%s password=%s", "user-secret", "password-secret")
	output := buf.String()
	if bytes.Contains([]byte(output), []byte("user-secret")) || bytes.Contains([]byte(output), []byte("password-secret")) {
		t.Fatalf("Nacos SDK 日志泄露配置凭据：%s", output)
	}
}

func TestSlogAdapterLeaseRejectsAnotherRuntimeAndRestoresLogger(t *testing.T) {
	previous := nacoslogger.GetLogger()
	first := newLoggerRuntime(t, "first")
	second := newLoggerRuntime(t, "second")
	release, err := acquireSlogAdapter(first, "warn", slog.Default(), "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireSlogAdapter(second, "warn", slog.Default()); err == nil {
		t.Fatal("另一个 Runtime 不得覆盖 Nacos SDK 全局 Logger")
	}
	release()
	if got := nacoslogger.GetLogger(); got != previous {
		t.Fatalf("释放 Nacos Logger 租约后未恢复原 Logger：%T", got)
	}
}

func newLoggerRuntime(t *testing.T, name string) *styxruntime.Runtime {
	t.Helper()
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity:     styxruntime.Identity{Name: name},
		Dependencies: observe.NoopDependencies(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func checkContains(t *testing.T, output, substr string, want bool) {
	t.Helper()
	got := bytes.Contains([]byte(output), []byte(substr))
	if got != want {
		if want {
			t.Errorf("expected output to contain %q, got:\n%s", substr, output)
		} else {
			t.Errorf("expected output NOT to contain %q, got:\n%s", substr, output)
		}
	}
}
