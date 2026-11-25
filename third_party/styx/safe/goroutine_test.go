package safe

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	metricdata "go.opentelemetry.io/otel/sdk/metric/metricdata"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func testRunner() *Runner {
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity:     styxruntime.Identity{Name: "safe-test"},
		Dependencies: observe.NoopDependencies(),
	})
	if err != nil {
		panic(err)
	}
	runner, err := NewRunner(runtime)
	if err != nil {
		panic(err)
	}
	return runner
}

func TestNewRunnerRejectsZeroDependencies(t *testing.T) {
	if _, err := NewRunner(nil); err == nil {
		t.Fatal("Runner 必须拒绝空 Runtime")
	}
}

func TestNewRunnerDoesNotCreateTaskMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })
	dependencies, err := observe.NewDependencies(observe.Config{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		TracerProvider: tracenoop.NewTracerProvider(),
		MeterProvider:  meterProvider,
		Propagator:     propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity: styxruntime.Identity{Name: "safe-no-metrics-test"}, Dependencies: dependencies,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunner(runtime); err != nil {
		t.Fatal(err)
	}
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	if len(metrics.ScopeMetrics) != 0 {
		t.Fatalf("safe Runner 不应创建任务指标：%+v", metrics.ScopeMetrics)
	}
}

func TestRunnerGoExecutesAndRecoversPanic(t *testing.T) {
	runner := testRunner()
	done := make(chan struct{})
	runner.Go(context.Background(), "panic", func() {
		defer close(done)
		panic("secret")
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("异步函数未完成")
	}
}

func TestRunnerGoWaitAlwaysReleasesWaitGroup(t *testing.T) {
	runner := testRunner()
	var group sync.WaitGroup
	for range 10 {
		runner.GoWait(context.Background(), "counter", &group, func() {})
	}
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WaitGroup 未释放")
	}
}

func TestRunnerGoWithSpanWaitExecutes(t *testing.T) {
	runner := testRunner()
	var group sync.WaitGroup
	var count atomic.Int32
	runner.GoWithSpanWait(context.Background(), "linked", &group, func(context.Context) { count.Add(1) })
	group.Wait()
	if count.Load() != 1 {
		t.Fatal("带 Span 的异步函数未执行")
	}
}
