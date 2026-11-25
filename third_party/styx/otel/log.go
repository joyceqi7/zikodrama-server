package otel

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

// newLoggerProvider creates a LoggerProvider with OTLP exporter.
// Returns the LoggerProvider for use with slog integration.
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
// Note: Unlike trace and metric, logs are always enabled when this function is called.
// The caller should check if logging should be enabled before calling this function.
func newLoggerProvider(ctx context.Context, cfg *configv1.OTelConfig, res *resource.Resource) (*log.LoggerProvider, error) {
	if cfg.GetLog() == nil || !cfg.GetLog().GetEnabled() {
		return nil, nil
	}
	// Validate endpoint
	if normalizedEndpoint(cfg) == "" {
		return nil, fmt.Errorf("otel: endpoint is required for log exporter")
	}

	// Get timeout with default
	timeout := defaultTimeout
	if cfg.GetTimeout() != nil {
		timeout = cfg.GetTimeout().AsDuration()
	}

	// Build exporter based on protocol
	var exporter log.Exporter
	var err error

	switch normalizedProtocol(cfg) {
	case "http":
		exporter, err = newHTTPLogExporter(ctx, cfg, timeout)
		if err != nil {
			return nil, fmt.Errorf("otel: failed to create HTTP log exporter: %w", err)
		}
	default: // grpc is the default
		exporter, err = newGRPCLogExporter(ctx, cfg, timeout)
		if err != nil {
			return nil, fmt.Errorf("otel: failed to create gRPC log exporter: %w", err)
		}
	}

	// Create LoggerProvider with BatchProcessor
	lp := log.NewLoggerProvider(
		log.WithResource(res),
		log.WithProcessor(log.NewBatchProcessor(exporter)),
	)

	return lp, nil
}

// newGRPCLogExporter creates a gRPC-based OTLP log exporter.
func newGRPCLogExporter(ctx context.Context, cfg *configv1.OTelConfig, timeout time.Duration) (log.Exporter, error) {
	opts := []otlploggrpc.Option{
		otlploggrpc.WithEndpoint(normalizedEndpoint(cfg)),
		otlploggrpc.WithTimeout(timeout),
	}

	// Configure retry
	if retry := cfg.GetRetry(); retry != nil && retry.GetEnabled() {
		retryCfg := otlploggrpc.RetryConfig{
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
		opts = append(opts, otlploggrpc.WithRetry(retryCfg))
	}

	// Configure insecure connection
	if cfg.GetInsecure() {
		opts = append(opts, otlploggrpc.WithInsecure())
	}

	// Configure headers
	if headers := cfg.GetHeaders(); len(headers) > 0 {
		opts = append(opts, otlploggrpc.WithHeaders(headers))
	}

	// Configure compression
	if normalizedCompression(cfg) == "gzip" {
		opts = append(opts, otlploggrpc.WithCompressor("gzip"))
	}

	return otlploggrpc.New(ctx, opts...)
}

// newHTTPLogExporter creates an HTTP-based OTLP log exporter.
func newHTTPLogExporter(ctx context.Context, cfg *configv1.OTelConfig, timeout time.Duration) (log.Exporter, error) {
	opts := []otlploghttp.Option{
		otlploghttp.WithEndpoint(normalizedEndpoint(cfg)),
		otlploghttp.WithTimeout(timeout),
	}

	// Configure retry
	if retry := cfg.GetRetry(); retry != nil && retry.GetEnabled() {
		retryCfg := otlploghttp.RetryConfig{
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
		opts = append(opts, otlploghttp.WithRetry(retryCfg))
	}

	// Configure insecure connection
	if cfg.GetInsecure() {
		opts = append(opts, otlploghttp.WithInsecure())
	}

	// Configure headers
	if headers := cfg.GetHeaders(); len(headers) > 0 {
		opts = append(opts, otlploghttp.WithHeaders(headers))
	}

	// Configure compression
	if normalizedCompression(cfg) == "gzip" {
		opts = append(opts, otlploghttp.WithCompression(otlploghttp.GzipCompression))
	}

	return otlploghttp.New(ctx, opts...)
}
