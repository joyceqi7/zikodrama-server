// Package safe 提供带显式可观测依赖的 goroutine 和后台任务运行器。
package safe

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/trace"

	"gitee.com/linpbin/styx/internal/logutil"
	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

var errAsyncPanic = errors.New("asynchronous task panic")

// Runner 统一管理异步执行所需的日志、Trace 和资源生命周期依赖。
type Runner struct {
	runtime      *styxruntime.Runtime
	dependencies observe.Dependencies
}

// NewRunner 创建异步任务运行器。
func NewRunner(runtime *styxruntime.Runtime) (*Runner, error) {
	if runtime == nil {
		return nil, fmt.Errorf("safe: runtime is required")
	}
	dependencies := runtime.Dependencies()
	if err := dependencies.Validate(); err != nil {
		return nil, fmt.Errorf("safe: runner dependencies: %w", err)
	}
	return &Runner{runtime: runtime, dependencies: dependencies}, nil
}

func (runner *Runner) recoverPanic(ctx context.Context, name string) {
	logutil.Error(runner.dependencies.Logger(), ctx, "asynchronous panic recovered", "task", name)
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.RecordError(errAsyncPanic)
	}
}

// Go 启动一个可恢复 panic 的 goroutine。
func (runner *Runner) Go(ctx context.Context, name string, fn func()) {
	go func() {
		defer func() {
			if recover() != nil {
				runner.recoverPanic(contextOrBackground(ctx), name)
			}
		}()
		fn()
	}()
}

// GoWait 启动 goroutine 并纳入 WaitGroup。
func (runner *Runner) GoWait(ctx context.Context, name string, group *sync.WaitGroup, fn func()) {
	group.Add(1)
	go func() {
		defer func() {
			group.Done()
			if recover() != nil {
				runner.recoverPanic(contextOrBackground(ctx), name)
			}
		}()
		fn()
	}()
}

// GoWithSpan 启动一个与父 Span 建立 Link 的异步任务。
func (runner *Runner) GoWithSpan(ctx context.Context, name string, fn func(context.Context)) {
	runner.goWithSpan(ctx, name, nil, fn)
}

// GoWithSpanWait 启动带 Link 的异步任务并纳入 WaitGroup。
func (runner *Runner) GoWithSpanWait(ctx context.Context, name string, group *sync.WaitGroup, fn func(context.Context)) {
	group.Add(1)
	runner.goWithSpan(ctx, name, group, fn)
}

func (runner *Runner) goWithSpan(ctx context.Context, name string, group *sync.WaitGroup, fn func(context.Context)) {
	parentSpanContext := trace.SpanContextFromContext(contextOrBackground(ctx))
	go func() {
		tracer := runner.dependencies.TracerProvider().Tracer("gitee.com/linpbin/styx/safe")
		spanOptions := []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}
		if parentSpanContext.IsValid() {
			spanOptions = append(spanOptions, trace.WithLinks(trace.Link{SpanContext: parentSpanContext}))
		}
		newContext, span := tracer.Start(context.Background(), name, spanOptions...)
		defer func() {
			if group != nil {
				group.Done()
			}
			span.End()
			if recover() != nil {
				runner.recoverPanic(newContext, name)
			}
		}()
		fn(newContext)
	}()
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
