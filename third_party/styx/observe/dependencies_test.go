package observe

import (
	"io"
	"log/slog"
	"testing"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestNewDependenciesRejectsMissingRuntimeDependency(t *testing.T) {
	valid := Config{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		TracerProvider: tracenoop.NewTracerProvider(),
		MeterProvider:  metricnoop.NewMeterProvider(),
		Propagator:     propagation.TraceContext{},
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "logger", mutate: func(config *Config) { config.Logger = nil }},
		{name: "tracer", mutate: func(config *Config) { config.TracerProvider = nil }},
		{name: "meter", mutate: func(config *Config) { config.MeterProvider = nil }},
		{name: "propagator", mutate: func(config *Config) { config.Propagator = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if _, err := NewDependencies(config); err == nil {
				t.Fatal("缺少运行依赖时必须失败")
			}
		})
	}
}

func TestNoopDependenciesAreComplete(t *testing.T) {
	dependencies := NoopDependencies()
	if err := dependencies.Validate(); err != nil {
		t.Fatalf("显式 no-op 运行依赖校验失败：%v", err)
	}
	if dependencies.Logger() == nil || dependencies.TracerProvider() == nil ||
		dependencies.MeterProvider() == nil || dependencies.Propagator() == nil {
		t.Fatal("显式 no-op 运行依赖必须完整")
	}
}

func TestZeroDependenciesAreRejected(t *testing.T) {
	if err := (Dependencies{}).Validate(); err == nil {
		t.Fatal("零值运行依赖必须被拒绝")
	}
}
