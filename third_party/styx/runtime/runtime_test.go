package runtime

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"gitee.com/linpbin/styx/observe"
	"go.opentelemetry.io/otel"
)

func TestRuntimeSnapshotsIdentity(t *testing.T) {
	identity := Identity{Name: "catalog", Version: "v1", Environment: "test"}
	runtime, err := New(Config{Identity: identity, Dependencies: observe.NoopDependencies()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	identity.Name = "mutated"
	identity.Version = "v2"

	if runtime.Name() != "catalog" || runtime.Version() != "v1" {
		t.Fatalf("runtime identity changed: name=%q version=%q", runtime.Name(), runtime.Version())
	}
}

func TestRuntimeShutdownRunsResourcesInReverseOrderOnce(t *testing.T) {
	runtime := newTestRuntime(t)
	var order []string
	for _, name := range []string{"telemetry", "registry", "database"} {
		resourceName := name
		if err := runtime.Own(resourceName, func(context.Context) error {
			order = append(order, resourceName)
			return nil
		}); err != nil {
			t.Fatalf("Own(%q) error = %v", resourceName, err)
		}
	}

	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}
	if want := []string{"database", "registry", "telemetry"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("shutdown order = %v, want %v", order, want)
	}
}

func TestRuntimeShutdownAggregatesSafeClassifiedErrors(t *testing.T) {
	runtime := newTestRuntime(t)
	first := errors.New("first-secret")
	second := errors.New("second-secret")
	_ = runtime.Own("first", func(context.Context) error { return first })
	_ = runtime.Own("second", func(context.Context) error { return second })

	err := runtime.Shutdown(context.Background())
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("Shutdown() error = %v, want both causes", err)
	}
	var runtimeError *Error
	if !errors.As(err, &runtimeError) || runtimeError.Kind != KindLifecycle {
		t.Fatalf("Shutdown() error = %T %v, want lifecycle Error", err, err)
	}
	if got := err.Error(); got == "" || containsAny(got, "first-secret", "second-secret") {
		t.Fatalf("Shutdown() exposed unsafe cause: %q", got)
	}
}

func TestRuntimeShutdownHonorsAlreadyCancelledContextAndCanRetry(t *testing.T) {
	runtime := newTestRuntime(t)
	calls := 0
	_ = runtime.Own("database", func(context.Context) error {
		calls++
		return nil
	})

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.Shutdown(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown(cancelled) error = %v, want context canceled", err)
	}
	if calls != 0 {
		t.Fatalf("resource called with an already cancelled context: %d", calls)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown(retry) error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("resource calls = %d, want 1", calls)
	}
}

func TestRuntimeRejectsOwnershipAfterShutdownStarts(t *testing.T) {
	runtime := newTestRuntime(t)
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	err := runtime.Own("late", func(context.Context) error { return nil })
	var runtimeError *Error
	if !errors.As(err, &runtimeError) || runtimeError.Kind != KindConflict {
		t.Fatalf("Own() error = %T %v, want conflict Error", err, err)
	}
}

func TestRuntimeRejectsResourceStartAfterShutdown(t *testing.T) {
	runtime := newTestRuntime(t)
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	var called atomic.Bool
	err := runtime.StartResource(context.Background(), "late task", func(context.Context) error {
		called.Store(true)
		return nil
	})
	var runtimeError *Error
	if !errors.As(err, &runtimeError) || runtimeError.Kind != KindConflict {
		t.Fatalf("StartResource() error = %T %v, want conflict", err, err)
	}
	if called.Load() {
		t.Fatal("Runtime 关闭后不得执行资源启动回调")
	}
}

func TestRuntimeShutdownWaitsForResourceStartBeforeClosing(t *testing.T) {
	runtime := newTestRuntime(t)
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	closed := make(chan struct{})
	if err := runtime.Own("worker", func(context.Context) error {
		close(closed)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	startResult := make(chan error, 1)
	go func() {
		startResult <- runtime.StartResource(context.Background(), "worker", func(context.Context) error {
			close(startEntered)
			<-releaseStart
			return nil
		})
	}()
	<-startEntered
	shutdownResult := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		shutdownResult <- runtime.Shutdown(ctx)
	}()
	select {
	case <-closed:
		t.Fatal("资源启动完成前 Runtime 不得执行关闭")
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseStart)
	if err := <-startResult; err != nil {
		t.Fatalf("StartResource() error = %v", err)
	}
	if err := <-shutdownResult; err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case <-closed:
	default:
		t.Fatal("资源启动完成后 Runtime 未执行关闭")
	}
}

func TestRuntimeInstallsGlobalsExplicitlyAndRejectsAnotherOwner(t *testing.T) {
	previousLogger := slog.Default()
	previousTracer := otel.GetTracerProvider()
	previousMeter := otel.GetMeterProvider()
	previousPropagator := otel.GetTextMapPropagator()
	first := newTestRuntime(t)
	second := newTestRuntime(t)

	if err := first.InstallGlobals(); err != nil {
		t.Fatalf("InstallGlobals() error = %v", err)
	}
	if slog.Default() != first.Dependencies().Logger() ||
		otel.GetTracerProvider() != first.Dependencies().TracerProvider() ||
		otel.GetMeterProvider() != first.Dependencies().MeterProvider() ||
		!reflect.DeepEqual(otel.GetTextMapPropagator(), first.Dependencies().Propagator()) {
		t.Fatal("InstallGlobals() did not install runtime dependencies")
	}
	err := second.InstallGlobals()
	var runtimeError *Error
	if !errors.As(err, &runtimeError) || runtimeError.Kind != KindConflict {
		t.Fatalf("second InstallGlobals() error = %T %v, want conflict", err, err)
	}

	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatalf("first Shutdown() error = %v", err)
	}
	if slog.Default() != previousLogger || otel.GetTracerProvider() != previousTracer ||
		otel.GetMeterProvider() != previousMeter || !reflect.DeepEqual(otel.GetTextMapPropagator(), previousPropagator) {
		t.Fatal("Shutdown() did not restore previous globals")
	}
	if err := second.InstallGlobals(); err != nil {
		t.Fatalf("InstallGlobals() after restore error = %v", err)
	}
	if err := second.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}
}

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	runtime, err := New(Config{
		Identity:     Identity{Name: "test", Version: "dev"},
		Dependencies: observe.NoopDependencies(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return runtime
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if candidate != "" && len(value) >= len(candidate) {
			for index := 0; index+len(candidate) <= len(value); index++ {
				if value[index:index+len(candidate)] == candidate {
					return true
				}
			}
		}
	}
	return false
}
