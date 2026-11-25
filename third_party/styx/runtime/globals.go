package runtime

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var processGlobals struct {
	sync.Mutex
	owner *Runtime
}

type globalSnapshot struct {
	logger         *slog.Logger
	tracerProvider trace.TracerProvider
	meterProvider  metric.MeterProvider
	propagator     propagation.TextMapPropagator
}

// InstallGlobals 显式把当前 Runtime 依赖安装为进程全局值。
// 同一时刻只允许一个 Runtime 持有全局状态，Runtime 关闭时自动恢复快照。
func (runtime *Runtime) InstallGlobals() error {
	if runtime == nil {
		return newError("runtime", "install globals", KindInvalidConfig, false, errors.New("runtime is nil"))
	}
	if err := runtime.dependencies.Validate(); err != nil {
		return newError("runtime", "install globals", KindInvalidConfig, false, err)
	}

	processGlobals.Lock()
	defer processGlobals.Unlock()
	if processGlobals.owner != nil {
		return newError("runtime", "install globals", KindConflict, false, errors.New("process globals already have an owner"))
	}
	snapshot := globalSnapshot{
		logger:         slog.Default(),
		tracerProvider: otel.GetTracerProvider(),
		meterProvider:  otel.GetMeterProvider(),
		propagator:     otel.GetTextMapPropagator(),
	}
	processGlobals.owner = runtime
	slog.SetDefault(runtime.dependencies.Logger())
	otel.SetTracerProvider(runtime.dependencies.TracerProvider())
	otel.SetMeterProvider(runtime.dependencies.MeterProvider())
	otel.SetTextMapPropagator(runtime.dependencies.Propagator())

	if err := runtime.Own("process globals", func(_ context.Context) error {
		processGlobals.Lock()
		defer processGlobals.Unlock()
		if processGlobals.owner != runtime {
			return nil
		}
		slog.SetDefault(snapshot.logger)
		otel.SetTracerProvider(snapshot.tracerProvider)
		otel.SetMeterProvider(snapshot.meterProvider)
		otel.SetTextMapPropagator(snapshot.propagator)
		processGlobals.owner = nil
		return nil
	}); err != nil {
		slog.SetDefault(snapshot.logger)
		otel.SetTracerProvider(snapshot.tracerProvider)
		otel.SetMeterProvider(snapshot.meterProvider)
		otel.SetTextMapPropagator(snapshot.propagator)
		processGlobals.owner = nil
		return err
	}
	return nil
}
