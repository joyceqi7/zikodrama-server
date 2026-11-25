package safe

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/robfig/cron/v3"
	"go.opentelemetry.io/otel/trace"
)

// Cron 包装 robfig/cron，并使用 Runner 统一处理日志、Trace 与 panic。
type Cron struct {
	runner *Runner
	inner  *cron.Cron

	mu       sync.Mutex
	running  bool
	stopping bool
	stopDone chan struct{}
	ctx      context.Context
}

// NewCron 创建支持秒级表达式的调度器。
func NewCron(runner *Runner, options ...cron.Option) (*Cron, error) {
	if runner == nil {
		return nil, fmt.Errorf("safe: runner is required")
	}
	if err := runner.dependencies.Validate(); err != nil {
		return nil, fmt.Errorf("safe: runner dependencies: %w", err)
	}
	defaults := []cron.Option{
		cron.WithSeconds(),
		cron.WithChain(cron.SkipIfStillRunning(&cronLogger{logger: runner.dependencies.Logger()})),
	}
	scheduler := &Cron{runner: runner, inner: cron.New(append(defaults, options...)...)}
	if err := runner.runtime.Own("cron scheduler", scheduler.Stop); err != nil {
		return nil, fmt.Errorf("safe: own cron scheduler: %w", err)
	}
	return scheduler, nil
}

// Add 添加一个具名 Cron 任务。
func (scheduler *Cron) Add(name, schedule string, fn func(context.Context)) error {
	if name == "" {
		return fmt.Errorf("safe: cron job name is required")
	}
	if fn == nil {
		return fmt.Errorf("safe: cron job function is required")
	}
	_, err := scheduler.inner.AddFunc(schedule, func() { scheduler.execute(name, fn) })
	if err != nil {
		return fmt.Errorf("safe: add cron job %q: %w", name, err)
	}
	scheduler.runner.dependencies.Logger().Info("cron job added", "job", name, "schedule", schedule)
	return nil
}

// Start 启动调度器。
func (scheduler *Cron) Start(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("safe: cron context is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("safe: cron context: %w", err)
	}
	return scheduler.runner.runtime.StartResource(ctx, "cron scheduler", func(startContext context.Context) error {
		scheduler.mu.Lock()
		defer scheduler.mu.Unlock()
		if scheduler.running {
			return fmt.Errorf("safe: cron scheduler is already running")
		}
		scheduler.ctx = startContext
		scheduler.running = true
		scheduler.stopping = false
		scheduler.stopDone = nil
		scheduler.inner.Start()
		scheduler.runner.dependencies.Logger().Info("cron scheduler started")
		return nil
	})
}

// Stop 在调用方 Context 截止前等待正在执行的任务结束。
func (scheduler *Cron) Stop(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("safe: stop context is required")
	}
	scheduler.mu.Lock()
	if !scheduler.running {
		scheduler.mu.Unlock()
		return nil
	}
	if !scheduler.stopping {
		scheduler.stopping = true
		scheduler.stopDone = make(chan struct{})
		innerDone := scheduler.inner.Stop()
		done := scheduler.stopDone
		go func() {
			<-innerDone.Done()
			scheduler.mu.Lock()
			scheduler.running = false
			scheduler.stopping = false
			close(done)
			scheduler.mu.Unlock()
		}()
	}
	done := scheduler.stopDone
	scheduler.mu.Unlock()
	select {
	case <-done:
		scheduler.runner.dependencies.Logger().Info("cron scheduler stopped")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("safe: stop cron scheduler: %w", ctx.Err())
	}
}

func (scheduler *Cron) execute(name string, fn func(context.Context)) {
	scheduler.mu.Lock()
	ctx := scheduler.ctx
	scheduler.mu.Unlock()
	tracer := scheduler.runner.dependencies.TracerProvider().Tracer("gitee.com/linpbin/styx/safe")
	ctx, span := tracer.Start(contextOrBackground(ctx), "cron/"+name, trace.WithSpanKind(trace.SpanKindInternal))
	defer func() {
		span.End()
		if recover() != nil {
			scheduler.runner.recoverPanic(ctx, "cron/"+name)
		}
	}()
	fn(ctx)
}

type cronLogger struct {
	logger *slog.Logger
}

func (logger *cronLogger) Info(message string, fields ...any) {
	logger.logger.Info(message, fields...)
}

func (logger *cronLogger) Error(err error, message string, fields ...any) {
	fields = append(fields, "error_type", fmt.Sprintf("%T", err))
	logger.logger.Error(message, fields...)
}
