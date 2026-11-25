package otel

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/types/known/durationpb"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

func TestValidateConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		config *configv1.OTelConfig
	}{
		{name: "protocol", config: &configv1.OTelConfig{Protocol: "udp"}},
		{name: "compression", config: &configv1.OTelConfig{Compression: "brotli"}},
		{name: "timeout", config: &configv1.OTelConfig{Timeout: durationpb.New(0)}},
		{name: "metric interval", config: &configv1.OTelConfig{
			Metric: &configv1.MetricConfig{Enabled: true, Interval: durationpb.New(-time.Second)},
		}},
		{name: "sampler", config: &configv1.OTelConfig{
			Trace: &configv1.TraceConfig{Sampler: "unknown"},
		}},
		{name: "sample rate", config: &configv1.OTelConfig{
			Trace: &configv1.TraceConfig{Sampler: "traceidratio", SampleRate: 1.1},
		}},
		{name: "retry order", config: &configv1.OTelConfig{
			Retry: &configv1.RetryConfig{
				Enabled:         true,
				InitialInterval: durationpb.New(2 * time.Second),
				MaxInterval:     durationpb.New(time.Second),
			},
		}},
		{name: "metric missing endpoint", config: &configv1.OTelConfig{
			Metric: &configv1.MetricConfig{Enabled: true},
		}},
		{name: "log missing endpoint", config: &configv1.OTelConfig{
			Log: &configv1.LogConfig{Enabled: true},
		}},
		{name: "log level", config: &configv1.OTelConfig{
			Log: &configv1.LogConfig{Level: "verbose"},
		}},
		{name: "empty header", config: &configv1.OTelConfig{
			Headers: map[string]string{" ": "value"},
		}},
		{name: "disabled retry options", config: &configv1.OTelConfig{
			Retry: &configv1.RetryConfig{InitialInterval: durationpb.New(time.Second)},
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateConfig(test.config); err == nil {
				t.Fatal("无效 OTel 配置必须被拒绝")
			}
		})
	}
}

func TestLocalTraceWithoutEndpointGeneratesIDsWithoutRecording(t *testing.T) {
	cfg := &configv1.OTelConfig{Trace: &configv1.TraceConfig{Enabled: true}}
	provider, err := New(context.Background(), cfg, &configv1.AppInfo{Name: "local-trace-test"})
	if err != nil {
		t.Fatalf("无 endpoint 时应创建本地 Trace Provider：%v", err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	if provider.TracerProvider() == nil || provider.LoggerProvider() != nil || provider.MeterProvider() != nil {
		t.Fatal("本地 Trace 只应创建 TracerProvider，不创建远程日志或指标导出器")
	}
	tracer := provider.TracerProvider().Tracer("local-trace-test")
	firstCtx, first := tracer.Start(context.Background(), "first request")
	defer first.End()
	firstID := first.SpanContext().TraceID()
	if !firstID.IsValid() || !first.SpanContext().SpanID().IsValid() || first.IsRecording() {
		t.Fatal("本地模式必须生成有效 Trace/Span ID 且不记录 Span")
	}
	childCtx, child := tracer.Start(firstCtx, "database")
	defer child.End()
	if child.SpanContext().TraceID() != firstID || !child.SpanContext().SpanID().IsValid() || child.IsRecording() {
		t.Fatal("同一次请求的下游操作必须沿用 Trace ID，且不记录 Span")
	}
	if childCtx == nil {
		t.Fatal("下游操作必须返回可传播的 Context")
	}
	_, second := tracer.Start(context.Background(), "second request")
	defer second.End()
	if second.SpanContext().TraceID() == firstID {
		t.Fatal("不同请求不能复用 Trace ID")
	}
	upstream := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{9}, SpanID: trace.SpanID{8}, Remote: true,
	})
	_, continued := tracer.Start(trace.ContextWithRemoteSpanContext(context.Background(), upstream), "upstream request")
	defer continued.End()
	if continued.SpanContext().TraceID() != upstream.TraceID() || continued.IsRecording() {
		t.Fatal("本地模式必须沿用有效的上游 Trace ID，且不记录 Span")
	}
}

func TestNewHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(ctx, &configv1.OTelConfig{}, &configv1.AppInfo{Name: "audit"}); err == nil {
		t.Fatal("已取消的初始化 Context 必须立即失败")
	}
}

func TestProviderShutdownIsIdempotent(t *testing.T) {
	provider, err := New(context.Background(), &configv1.OTelConfig{}, &configv1.AppInfo{Name: "audit"})
	if err != nil {
		t.Fatalf("创建空 Provider：%v", err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("首次关闭 Provider：%v", err)
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("重复关闭 Provider：%v", err)
	}
}

func TestConfigNormalizationMatchesExporterSelection(t *testing.T) {
	cfg := &configv1.OTelConfig{Protocol: " HTTP ", Compression: " GZIP ", Endpoint: " collector:4318 "}
	if err := validateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if normalizedProtocol(cfg) != "http" || normalizedCompression(cfg) != "gzip" || normalizedEndpoint(cfg) != "collector:4318" {
		t.Fatal("配置校验与 exporter 选择必须使用同一套归一化规则")
	}
}

func TestProviderShutdownRejectsNilContext(t *testing.T) {
	provider, err := New(context.Background(), &configv1.OTelConfig{}, &configv1.AppInfo{Name: "audit"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	//lint:ignore SA1012 本测试必须验证公开接口能拒绝 nil Context
	if err := provider.Shutdown(nil); err == nil {
		t.Fatal("关闭 Provider 必须提供 Context")
	}
}
