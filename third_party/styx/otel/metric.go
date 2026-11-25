package otel

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

// Default metric export interval.
const defaultMetricInterval = 15 * time.Second

// newMeterProvider creates a MeterProvider with OTLP exporter.
// Returns nil if metric is not enabled.
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
// The metric reader is configured based on MetricConfig:
//   - Interval: Export interval (default 15s)
func newMeterProvider(ctx context.Context, cfg *configv1.OTelConfig, res *resource.Resource) (*metric.MeterProvider, error) {
	// Check if metric is enabled
	metricCfg := cfg.GetMetric()
	if metricCfg == nil || !metricCfg.GetEnabled() {
		return nil, nil // Metric not enabled, return nil
	}

	// Validate endpoint
	if normalizedEndpoint(cfg) == "" {
		return nil, fmt.Errorf("otel: endpoint is required for metric exporter")
	}

	// Get timeout with default
	timeout := defaultTimeout
	if cfg.GetTimeout() != nil {
		timeout = cfg.GetTimeout().AsDuration()
	}

	// Get metric interval with default
	interval := defaultMetricInterval
	if metricCfg.GetInterval() != nil {
		interval = metricCfg.GetInterval().AsDuration()
	}

	// Build exporter based on protocol
	var exporter metric.Exporter
	var err error

	switch normalizedProtocol(cfg) {
	case "http":
		exporter, err = newHTTPMetricExporter(ctx, cfg, timeout)
		if err != nil {
			return nil, fmt.Errorf("otel: failed to create HTTP metric exporter: %w", err)
		}
	default: // grpc is the default
		exporter, err = newGRPCMetricExporter(ctx, cfg, timeout)
		if err != nil {
			return nil, fmt.Errorf("otel: failed to create gRPC metric exporter: %w", err)
		}
	}

	// Create MeterProvider with PeriodicReader
	mp := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(exporter,
			metric.WithInterval(interval),
		)),
	)

	return mp, nil
}

// newGRPCMetricExporter creates a gRPC-based OTLP metric exporter.
func newGRPCMetricExporter(ctx context.Context, cfg *configv1.OTelConfig, timeout time.Duration) (metric.Exporter, error) {
	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(normalizedEndpoint(cfg)),
		otlpmetricgrpc.WithTimeout(timeout),
	}

	// Configure retry
	if retry := cfg.GetRetry(); retry != nil && retry.GetEnabled() {
		retryCfg := otlpmetricgrpc.RetryConfig{
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
		opts = append(opts, otlpmetricgrpc.WithRetry(retryCfg))
	}

	// Configure insecure connection
	if cfg.GetInsecure() {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}

	// Configure headers
	if headers := cfg.GetHeaders(); len(headers) > 0 {
		opts = append(opts, otlpmetricgrpc.WithHeaders(headers))
	}

	// Configure compression
	if normalizedCompression(cfg) == "gzip" {
		opts = append(opts, otlpmetricgrpc.WithCompressor("gzip"))
	}

	return otlpmetricgrpc.New(ctx, opts...)
}

// newHTTPMetricExporter creates an HTTP-based OTLP metric exporter.
func newHTTPMetricExporter(ctx context.Context, cfg *configv1.OTelConfig, timeout time.Duration) (metric.Exporter, error) {
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(normalizedEndpoint(cfg)),
		otlpmetrichttp.WithTimeout(timeout),
	}

	// Configure retry
	if retry := cfg.GetRetry(); retry != nil && retry.GetEnabled() {
		retryCfg := otlpmetrichttp.RetryConfig{
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
		opts = append(opts, otlpmetrichttp.WithRetry(retryCfg))
	}

	// Configure insecure connection
	if cfg.GetInsecure() {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}

	// Configure headers
	if headers := cfg.GetHeaders(); len(headers) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(headers))
	}

	// Configure compression
	if normalizedCompression(cfg) == "gzip" {
		opts = append(opts, otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression))
	}

	return otlpmetrichttp.New(ctx, opts...)
}
