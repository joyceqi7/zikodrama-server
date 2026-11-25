package styx

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

func TestAuditConsoleOutputIsHonored(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(
		&configv1.LogConfig{Level: "info", Console: true},
		WithConsoleOutput(&output),
	)
	logger.Info("audit-console-output")

	if !strings.Contains(output.String(), "audit-console-output") {
		t.Fatal("WithConsoleOutput 配置的输出目标未收到日志")
	}
}

func TestNewDoesNotModifyGlobalProvidersAndSnapshotsIdentity(t *testing.T) {
	previousLogger := slog.Default()
	previousTracer := otel.GetTracerProvider()
	previousMeter := otel.GetMeterProvider()
	previousPropagator := otel.GetTextMapPropagator()
	app := &configv1.AppInfo{Name: "audit", Version: "v1"}

	runtime, err := New(context.Background(), Config{App: app})
	if err != nil {
		t.Fatal(err)
	}
	app.Name = "mutated"
	app.Version = "v2"
	if runtime.Name() != "audit" || runtime.Version() != "v1" {
		t.Fatalf("Runtime 身份被调用方修改: %s@%s", runtime.Name(), runtime.Version())
	}
	if slog.Default() != previousLogger || otel.GetTracerProvider() != previousTracer ||
		otel.GetMeterProvider() != previousMeter || !reflect.DeepEqual(otel.GetTextMapPropagator(), previousPropagator) {
		t.Fatal("New 不得修改进程全局 Provider")
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLoggerFallsBackToConsoleWhenOTelProviderIsUnavailable(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(
		&configv1.LogConfig{Level: "info", Enabled: true},
		WithConsoleOutput(&output),
	)
	logger.Info("provider-unavailable")
	if !strings.Contains(output.String(), "provider-unavailable") {
		t.Fatal("OTel Provider 缺失时不能静默丢弃日志")
	}
}
