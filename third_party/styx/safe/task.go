package safe

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
)

const taskGroupRollbackTimeout = 5 * time.Second

// Task 表示可重复启动、可取消当前执行的周期任务。
type Task struct {
	runner   *Runner
	name     string
	interval time.Duration
	fn       func(context.Context)

	mu      sync.Mutex
	running bool
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewTask 创建周期任务，拒绝无效间隔和空回调。
func NewTask(runner *Runner, name string, interval time.Duration, fn func(context.Context)) (*Task, error) {
	if runner == nil {
		return nil, fmt.Errorf("safe: runner is required")
	}
	if err := runner.dependencies.Validate(); err != nil {
		return nil, fmt.Errorf("safe: runner dependencies: %w", err)
	}
	if name == "" {
		return nil, fmt.Errorf("safe: task name is required")
	}
	if interval <= 0 {
		return nil, fmt.Errorf("safe: task interval must be positive")
	}
	if fn == nil {
		return nil, fmt.Errorf("safe: task function is required")
	}
	task := &Task{runner: runner, name: name, interval: interval, fn: fn}
	if err := runner.runtime.Own("background task "+name, task.Stop); err != nil {
		return nil, fmt.Errorf("safe: own task %q: %w", name, err)
	}
	return task, nil
}

// Start 启动任务；同一 Task 不能并发启动。
func (task *Task) Start(parent context.Context) error {
	if parent == nil {
		return fmt.Errorf("safe: task context is required")
	}
	if err := parent.Err(); err != nil {
		return fmt.Errorf("safe: task context: %w", err)
	}
	return task.runner.runtime.StartResource(parent, "background task "+task.name, func(ctx context.Context) error {
		task.mu.Lock()
		defer task.mu.Unlock()
		if task.running {
			return fmt.Errorf("safe: task %q is already running", task.name)
		}
		task.ctx, task.cancel = context.WithCancel(ctx)
		task.done = make(chan struct{})
		task.running = true
		go task.run(task.done)
		task.runner.dependencies.Logger().Info("background task started", "task", task.name, "interval", task.interval)
		return nil
	})
}

// Stop 取消当前执行并在调用方 Context 截止前等待退出。
func (task *Task) Stop(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("safe: stop context is required")
	}
	task.mu.Lock()
	if !task.running {
		task.mu.Unlock()
		return nil
	}
	task.cancel()
	done := task.done
	task.mu.Unlock()

	select {
	case <-done:
		task.runner.dependencies.Logger().Info("background task stopped", "task", task.name)
		return nil
	case <-ctx.Done():
		return fmt.Errorf("safe: stop task %q: %w", task.name, ctx.Err())
	}
}

func (task *Task) run(done chan struct{}) {
	defer func() {
		task.mu.Lock()
		if task.done == done {
			task.running = false
		}
		task.mu.Unlock()
		close(done)
	}()
	task.execute()
	ticker := time.NewTicker(task.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			task.execute()
		case <-task.ctx.Done():
			return
		}
	}
}

func (task *Task) execute() {
	tracer := task.runner.dependencies.TracerProvider().Tracer("gitee.com/linpbin/styx/safe")
	ctx, span := tracer.Start(task.ctx, "task/"+task.name, trace.WithSpanKind(trace.SpanKindInternal))
	defer func() {
		span.End()
		if recover() != nil {
			task.runner.recoverPanic(ctx, "task/"+task.name)
		}
	}()
	task.fn(ctx)
}

// TaskGroup 管理一组周期任务。
type TaskGroup struct {
	runner *Runner
	tasks  []*Task
}

func NewTaskGroup(runner *Runner) *TaskGroup {
	return &TaskGroup{runner: runner}
}

func (group *TaskGroup) Add(name string, interval time.Duration, fn func(context.Context)) error {
	task, err := NewTask(group.runner, name, interval, fn)
	if err != nil {
		return err
	}
	group.tasks = append(group.tasks, task)
	return nil
}

func (group *TaskGroup) Start(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("safe: task group context is required")
	}
	started := make([]*Task, 0, len(group.tasks))
	for _, task := range group.tasks {
		if err := task.Start(ctx); err != nil {
			rollbackContext := ctx
			cancel := func() {}
			if _, hasDeadline := ctx.Deadline(); !hasDeadline {
				rollbackContext, cancel = context.WithTimeout(context.Background(), taskGroupRollbackTimeout)
			}
			var rollbackErrors []error
			for _, active := range started {
				if stopErr := active.Stop(rollbackContext); stopErr != nil {
					rollbackErrors = append(rollbackErrors, stopErr)
				}
			}
			cancel()
			return errors.Join(err, errors.Join(rollbackErrors...))
		}
		started = append(started, task)
	}
	return nil
}

func (group *TaskGroup) Stop(ctx context.Context) error {
	errorsList := make([]error, 0, len(group.tasks))
	for _, task := range group.tasks {
		if err := task.Stop(ctx); err != nil {
			errorsList = append(errorsList, err)
		}
	}
	return errors.Join(errorsList...)
}

// RunOnce 启动一个独立 Span 的一次性任务。
func (runner *Runner) RunOnce(name string, fn func(context.Context)) {
	runner.GoWithSpan(context.Background(), "job/"+name, fn)
}

// RunOnceLinked 启动一个与父 Span 建立 Link 的一次性任务。
func (runner *Runner) RunOnceLinked(parent context.Context, name string, fn func(context.Context)) {
	runner.GoWithSpan(parent, "job/"+name, fn)
}
