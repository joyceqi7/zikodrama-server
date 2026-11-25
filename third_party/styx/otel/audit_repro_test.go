package otel

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

func TestAuditDefaultRatioSamplerSamples(t *testing.T) {
	sampler, err := NewSampler("", 0)
	if err != nil {
		t.Fatalf("创建默认采样器：%v", err)
	}
	result := sampler.ShouldSample(sdktrace.SamplingParameters{})
	if result.Decision != sdktrace.RecordAndSample {
		t.Fatalf("默认采样决策为 %v，预期 RecordAndSample", result.Decision)
	}
}

func TestAuditDisabledLogDoesNotCreateProvider(t *testing.T) {
	provider, err := New(context.Background(), &configv1.OTelConfig{
		Endpoint: "127.0.0.1:4317",
		Protocol: "grpc",
		Log:      &configv1.LogConfig{Enabled: false},
	}, &configv1.AppInfo{Name: "audit"})
	if err != nil {
		t.Fatalf("初始化 OTel：%v", err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	if provider.LoggerProvider() != nil {
		t.Fatal("日志导出关闭时仍创建了 LoggerProvider")
	}
	_ = provider.Shutdown(context.Background())
}
