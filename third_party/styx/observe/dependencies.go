// Package observe 定义 Styx 模块共享的显式运行依赖。
package observe

import (
	"errors"
	"io"
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

var (
	errMissingLogger         = errors.New("observe: logger is required")
	errMissingTracerProvider = errors.New("observe: tracer provider is required")
	errMissingMeterProvider  = errors.New("observe: meter provider is required")
	errMissingPropagator     = errors.New("observe: propagator is required")
)

// Config 声明框架模块运行所需的可观测依赖。
type Config struct {
	Logger         *slog.Logger
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	Propagator     propagation.TextMapPropagator
}

// Dependencies 是通过完整性校验后的不可变运行依赖。
type Dependencies struct {
	logger         *slog.Logger
	tracerProvider trace.TracerProvider
	meterProvider  metric.MeterProvider
	propagator     propagation.TextMapPropagator
}

// NewDependencies 创建显式运行依赖，不接受隐式全局回退。
func NewDependencies(config Config) (Dependencies, error) {
	dependencies := Dependencies{
		logger:         config.Logger,
		tracerProvider: config.TracerProvider,
		meterProvider:  config.MeterProvider,
		propagator:     config.Propagator,
	}
	if err := dependencies.Validate(); err != nil {
		return Dependencies{}, err
	}
	return dependencies, nil
}

// Validate 检查依赖对象是否完整，可用于拒绝未初始化的零值。
func (dependencies Dependencies) Validate() error {
	if dependencies.logger == nil {
		return errMissingLogger
	}
	if dependencies.tracerProvider == nil {
		return errMissingTracerProvider
	}
	if dependencies.meterProvider == nil {
		return errMissingMeterProvider
	}
	if dependencies.propagator == nil {
		return errMissingPropagator
	}
	return nil
}

// NoopDependencies 为不启用导出的应用和测试提供显式 no-op 依赖。
func NoopDependencies() Dependencies {
	dependencies, _ := NewDependencies(Config{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		TracerProvider: tracenoop.NewTracerProvider(),
		MeterProvider:  metricnoop.NewMeterProvider(),
		Propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	})
	return dependencies
}

// Logger 返回模块共享的结构化日志器。
func (dependencies Dependencies) Logger() *slog.Logger {
	return dependencies.logger
}

// TracerProvider 返回模块共享的链路追踪 Provider。
func (dependencies Dependencies) TracerProvider() trace.TracerProvider {
	return dependencies.tracerProvider
}

// MeterProvider 返回模块共享的指标 Provider。
func (dependencies Dependencies) MeterProvider() metric.MeterProvider {
	return dependencies.meterProvider
}

// Propagator 返回模块共享的上下文传播器。
func (dependencies Dependencies) Propagator() propagation.TextMapPropagator {
	return dependencies.propagator
}
