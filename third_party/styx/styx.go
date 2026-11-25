// Package styx 提供 Kratos 应用的受治理运行时和基础设施适配器。
package styx

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/internal/lifecycle"
	"gitee.com/linpbin/styx/observe"
	styxotel "gitee.com/linpbin/styx/otel"
	styxnacos "gitee.com/linpbin/styx/registry/nacos"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

// Runtime 是 Styx 深运行时的公开类型。
type Runtime = styxruntime.Runtime

// Config 声明 Styx 内建的服务身份、OpenTelemetry 和 Nacos 配置。
// New 会读取并复制配置，不保留调用方可变指针。
type Config struct {
	App      *configv1.AppInfo
	OTel     *configv1.OTelConfig
	Registry *configv1.NacosConfig
}

// New 创建一个默认不修改进程全局状态的 Runtime。
func New(ctx context.Context, config Config) (*Runtime, error) {
	if ctx == nil {
		return nil, styxruntime.NewError("runtime", "create", styxruntime.KindInvalidConfig, false, errors.New("context is required"))
	}
	if err := ctx.Err(); err != nil {
		return nil, styxruntime.NewError("runtime", "create", styxruntime.KindLifecycle, true, err)
	}
	identity, appSnapshot, err := snapshotApp(config.App)
	if err != nil {
		return nil, err
	}

	provider, err := styxotel.New(ctx, config.OTel, appSnapshot)
	if err != nil {
		return nil, styxruntime.NewError("otel", "create", styxruntime.KindDependency, true, err)
	}
	logOptions := []LogOption{WithOTelProvider(identity.Name, provider.LoggerProvider())}
	var fileOutput *RotatingFileWriter
	if logConfig := config.OTel.GetLog(); logConfig != nil && logConfig.GetFile() != nil {
		fileConfig, parseErr := ParseLogFileConfig(logConfig.GetFile())
		if parseErr != nil {
			return nil, lifecycle.Rollback("otel", provider.Shutdown, parseErr)
		}
		fileOutput, err = NewRotatingFileWriter(*fileConfig)
		if err != nil {
			return nil, lifecycle.Rollback("otel", provider.Shutdown, err)
		}
		logOptions = append(logOptions, WithFileOutput(fileOutput))
	}
	logger := NewLogger(getLogConfig(config.OTel), logOptions...)
	dependencies, err := newRuntimeDependencies(logger, provider)
	if err != nil {
		if fileOutput != nil {
			err = errors.Join(err, fileOutput.Close())
		}
		return nil, lifecycle.Rollback("otel", provider.Shutdown, err)
	}

	runtime, err := styxruntime.New(styxruntime.Config{
		Identity:     identity,
		Dependencies: dependencies,
	})
	if err != nil {
		if fileOutput != nil {
			err = errors.Join(err, fileOutput.Close())
		}
		return nil, lifecycle.Rollback("otel", provider.Shutdown, err)
	}
	if fileOutput != nil {
		if err := runtime.Own("log file", func(context.Context) error { return fileOutput.Close() }); err != nil {
			return nil, errors.Join(err, fileOutput.Close(), provider.Shutdown(ctx))
		}
	}
	if err := runtime.Own("otel", provider.Shutdown); err != nil {
		return nil, lifecycle.Rollback("otel", provider.Shutdown, errors.Join(err, runtime.Shutdown(ctx)))
	}
	if config.Registry != nil && len(config.Registry.Servers) > 0 {
		if _, registryErr := styxnacos.NewRegistry(ctx, config.Registry, runtime); registryErr != nil {
			createErr := styxruntime.NewError("nacos", "create", styxruntime.KindDependency, true, registryErr)
			return nil, lifecycle.Rollback("runtime", runtime.Shutdown, createErr)
		}
	}

	logger.Debug("styx initialized",
		"name", runtime.Name(),
		"version", runtime.Version(),
		"registry", runtime.Registrar() != nil,
	)
	return runtime, nil
}

func snapshotApp(app *configv1.AppInfo) (styxruntime.Identity, *configv1.AppInfo, error) {
	if app == nil || strings.TrimSpace(app.GetName()) == "" {
		return styxruntime.Identity{}, nil, styxruntime.NewError("runtime", "snapshot identity", styxruntime.KindInvalidConfig, false, errors.New("service name is required"))
	}
	snapshot := &configv1.AppInfo{
		Name:       strings.TrimSpace(app.GetName()),
		Version:    strings.TrimSpace(app.GetVersion()),
		Env:        strings.TrimSpace(app.GetEnv()),
		Namespace:  strings.TrimSpace(app.GetNamespace()),
		InstanceId: strings.TrimSpace(app.GetInstanceId()),
	}
	return styxruntime.Identity{
		Name:        snapshot.Name,
		Version:     snapshot.Version,
		Environment: snapshot.Env,
		Namespace:   snapshot.Namespace,
		InstanceID:  snapshot.InstanceId,
	}, snapshot, nil
}

func newRuntimeDependencies(logger *slog.Logger, provider *styxotel.Provider) (observe.Dependencies, error) {
	var tracerProvider trace.TracerProvider = tracenoop.NewTracerProvider()
	if provider.TracerProvider() != nil {
		tracerProvider = provider.TracerProvider()
	}
	var meterProvider metric.MeterProvider = metricnoop.NewMeterProvider()
	if provider.MeterProvider() != nil {
		meterProvider = provider.MeterProvider()
	}
	dependencies, err := observe.NewDependencies(observe.Config{
		Logger:         logger,
		TracerProvider: tracerProvider,
		MeterProvider:  meterProvider,
		Propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	})
	if err != nil {
		return observe.Dependencies{}, styxruntime.NewError("runtime", "create dependencies", styxruntime.KindInvalidConfig, false, err)
	}
	return dependencies, nil
}

func getLogConfig(cfg *configv1.OTelConfig) *configv1.LogConfig {
	if cfg == nil || cfg.Log == nil {
		return &configv1.LogConfig{Level: "info", Console: true, Enabled: false}
	}
	logConfig := &configv1.LogConfig{
		Enabled: cfg.Log.GetEnabled(),
		Level:   cfg.Log.GetLevel(),
		Console: cfg.Log.GetConsole(),
	}
	if logConfig.Level == "" {
		logConfig.Level = "info"
	}
	if cfg.Endpoint == "" || (!logConfig.Console && !logConfig.Enabled) {
		logConfig.Console = true
	}
	return logConfig
}
