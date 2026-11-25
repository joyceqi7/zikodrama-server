package otel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

const providerShutdownTimeout = 10 * time.Second

// Provider holds all OTel providers.
type Provider struct {
	tracerProvider *trace.TracerProvider
	meterProvider  *metric.MeterProvider
	loggerProvider *log.LoggerProvider
	shutdown       styxruntime.ShutdownFunc
}

// New 根据配置创建 OTel Provider，不修改进程级全局 Provider。
func New(ctx context.Context, cfg *configv1.OTelConfig, app *configv1.AppInfo) (*Provider, error) {
	if ctx == nil {
		return nil, fmt.Errorf("otel: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("otel: initialization context: %w", err)
	}
	if cfg == nil {
		cfg = &configv1.OTelConfig{}
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	// Create Resource from AppInfo
	res, err := NewResource(ctx, app)
	if err != nil {
		return nil, fmt.Errorf("otel: failed to create resource: %w", err)
	}

	// Create TracerProvider (may return nil if not enabled)
	tp, err := newTracerProvider(ctx, cfg, res)
	if err != nil {
		return nil, fmt.Errorf("otel: failed to create tracer provider: %w", err)
	}

	// Create MeterProvider (may return nil if not enabled)
	mp, err := newMeterProvider(ctx, cfg, res)
	if err != nil {
		// Shutdown tracer provider if it was created
		if tp != nil {
			shutdownProvider(tp.Shutdown)
		}
		return nil, fmt.Errorf("otel: failed to create meter provider: %w", err)
	}

	// Create LoggerProvider
	lp, err := newLoggerProvider(ctx, cfg, res)
	if err != nil {
		// Shutdown previously created providers
		if tp != nil {
			shutdownProvider(tp.Shutdown)
		}
		if mp != nil {
			shutdownProvider(mp.Shutdown)
		}
		return nil, fmt.Errorf("otel: failed to create logger provider: %w", err)
	}

	provider := &Provider{
		tracerProvider: tp,
		meterProvider:  mp,
		loggerProvider: lp,
	}
	provider.shutdown = styxruntime.BoundedShutdown(providerShutdownTimeout, provider.shutdownAll)
	return provider, nil
}

// TracerProvider returns the TracerProvider.
func (p *Provider) TracerProvider() *trace.TracerProvider {
	return p.tracerProvider
}

// MeterProvider returns the MeterProvider.
func (p *Provider) MeterProvider() *metric.MeterProvider {
	return p.meterProvider
}

// LoggerProvider returns the LoggerProvider.
func (p *Provider) LoggerProvider() *log.LoggerProvider {
	return p.loggerProvider
}

// Shutdown gracefully shuts down all providers.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	return p.shutdown(ctx)
}

func (p *Provider) shutdownAll(ctx context.Context) error {
	var shutdownErrors []error
	components := []struct {
		name     string
		provider bool
		shutdown func(context.Context) error
	}{
		{name: "logger provider", provider: p.loggerProvider != nil, shutdown: func(ctx context.Context) error { return p.loggerProvider.Shutdown(ctx) }},
		{name: "meter provider", provider: p.meterProvider != nil, shutdown: func(ctx context.Context) error { return p.meterProvider.Shutdown(ctx) }},
		{name: "tracer provider", provider: p.tracerProvider != nil, shutdown: func(ctx context.Context) error { return p.tracerProvider.Shutdown(ctx) }},
	}
	for _, component := range components {
		if !component.provider {
			continue
		}
		if err := component.shutdown(ctx); err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Errorf("%s: %w", component.name, err))
		}
	}
	return errors.Join(shutdownErrors...)
}

func shutdownProvider(shutdown func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}
