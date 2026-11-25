package otel

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

// Default timeout for exporter operations.
const defaultTimeout = 10 * time.Second

// newTracerProvider 根据 endpoint 创建仅生成本地关联标识或带 OTLP 导出器的 TracerProvider。
// 未配置 endpoint 时不记录或导出 Span；远程导出支持 gRPC 和 HTTP 协议。
//
// The function configures the exporter based on the OTelConfig:
//   - Protocol: "grpc" (default) or "http"
//   - Endpoint: Collector endpoint (e.g., localhost:4317)
//   - Insecure: Skip TLS verification
//   - Timeout: Export timeout (default 10s)
//   - Headers: Custom headers for exporter
//   - Compression: "gzip" or "none"
//   - Retry: Retry configuration for failed exports
//
// The sampler is configured based on TraceConfig:
//   - Sampler: Sampler type (see NewSampler)
//   - SampleRate: Sample rate for ratio-based samplers
func newTracerProvider(ctx context.Context, cfg *configv1.OTelConfig, res *resource.Resource) (*trace.TracerProvider, error) {
	traceCfg := cfg.GetTrace()
	if traceCfg == nil || !traceCfg.GetEnabled() {
		return nil, nil
	}
	if normalizedEndpoint(cfg) == "" {
		// NeverSample 仍会为每个请求创建有效的 SpanContext，供日志和下游传播关联；
		// 不安装 SpanProcessor，因此不会记录或导出 Span。
		return trace.NewTracerProvider(trace.WithResource(res), trace.WithSampler(trace.NeverSample())), nil
	}

	// Get timeout with default
	timeout := defaultTimeout
	if cfg.GetTimeout() != nil {
		timeout = cfg.GetTimeout().AsDuration()
	}

	// Build exporter based on protocol
	var exporter trace.SpanExporter
	var err error

	switch normalizedProtocol(cfg) {
	case "http":
		exporter, err = newHTTPTraceExporter(ctx, cfg, timeout)
		if err != nil {
			return nil, fmt.Errorf("otel: failed to create HTTP trace exporter: %w", err)
		}
	default: // grpc is the default
		exporter, err = newGRPCTraceExporter(ctx, cfg, timeout)
		if err != nil {
			return nil, fmt.Errorf("otel: failed to create gRPC trace exporter: %w", err)
		}
	}

	// Get sampler configuration (traceCfg is guaranteed non-nil at this point)
	sampler, err := NewSampler(traceCfg.GetSampler(), traceCfg.GetSampleRate())
	if err != nil {
		return nil, err
	}

	// Create TracerProvider
	tp := trace.NewTracerProvider(
		trace.WithResource(res),
		trace.WithBatcher(exporter),
		trace.WithSampler(sampler),
	)

	return tp, nil
}

// newGRPCTraceExporter creates a gRPC-based OTLP trace exporter.
func newGRPCTraceExporter(ctx context.Context, cfg *configv1.OTelConfig, timeout time.Duration) (trace.SpanExporter, error) {
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(normalizedEndpoint(cfg)),
		otlptracegrpc.WithTimeout(timeout),
	}

	// Configure retry
	if retry := cfg.GetRetry(); retry != nil && retry.GetEnabled() {
		retryCfg := otlptracegrpc.RetryConfig{
			Enabled: true,
		}
		if retry.GetInitialInterval() != nil {
			retryCfg.InitialInterval = retry.GetInitialInterval().AsDuration()
		}
		if retry.GetMaxInterval() != nil {
			retryCfg.MaxInterval = retry.GetMaxInterval().AsDuration()
		}
		if retry.GetMaxElapsedTime() != nil {
			retryCfg.MaxElapsedTime = retry.GetMaxElapsedTime().AsDuration()
		}
		opts = append(opts, otlptracegrpc.WithRetry(retryCfg))
	}

	// Configure insecure connection
	if cfg.GetInsecure() {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	// Configure headers
	if headers := cfg.GetHeaders(); len(headers) > 0 {
		opts = append(opts, otlptracegrpc.WithHeaders(headers))
	}

	// Configure compression
	if normalizedCompression(cfg) == "gzip" {
		opts = append(opts, otlptracegrpc.WithCompressor("gzip"))
	}

	return otlptracegrpc.New(ctx, opts...)
}

// newHTTPTraceExporter creates an HTTP-based OTLP trace exporter.
func newHTTPTraceExporter(ctx context.Context, cfg *configv1.OTelConfig, timeout time.Duration) (trace.SpanExporter, error) {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(normalizedEndpoint(cfg)),
		otlptracehttp.WithTimeout(timeout),
	}

	// Configure retry
	if retry := cfg.GetRetry(); retry != nil && retry.GetEnabled() {
		retryCfg := otlptracehttp.RetryConfig{
			Enabled: true,
		}
		if retry.GetInitialInterval() != nil {
			retryCfg.InitialInterval = retry.GetInitialInterval().AsDuration()
		}
		if retry.GetMaxInterval() != nil {
			retryCfg.MaxInterval = retry.GetMaxInterval().AsDuration()
		}
		if retry.GetMaxElapsedTime() != nil {
			retryCfg.MaxElapsedTime = retry.GetMaxElapsedTime().AsDuration()
		}
		opts = append(opts, otlptracehttp.WithRetry(retryCfg))
	}

	// Configure insecure connection
	if cfg.GetInsecure() {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	// Configure headers
	if headers := cfg.GetHeaders(); len(headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(headers))
	}

	// Configure compression
	if normalizedCompression(cfg) == "gzip" {
		opts = append(opts, otlptracehttp.WithCompression(otlptracehttp.GzipCompression))
	}

	return otlptracehttp.New(ctx, opts...)
}
