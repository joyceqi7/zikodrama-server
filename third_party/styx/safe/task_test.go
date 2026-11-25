package safe

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewTaskRejectsInvalidInput(t *testing.T) {
	if _, err := NewTask(testRunner(), "invalid", 0, func(context.Context) {}); err == nil {
		t.Fatal("非正任务间隔必须失败")
	}
	if _, err := NewTask(testRunner(), "invalid", time.Second, nil); err == nil {
		t.Fatal("空任务函数必须失败")
	}
}

func TestTaskStopBeforeStartReturns(t *testing.T) {
	task, err := NewTask(testRunner(), "audit", time.Second, func(context.Context) {})
	if err != nil {
		t.Fatalf("创建任务：%v", err)
	}
	if err := task.Stop(context.Background()); err != nil {
		t.Fatalf("启动前停止任务：%v", err)
	}
}

func TestTaskRejectsStartAfterRuntimeShutdown(t *testing.T) {
	runner := testRunner()
	task, err := NewTask(runner, "late", time.Second, func(context.Context) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := task.Start(context.Background()); err == nil {
		t.Fatal("Runtime 关闭后不得启动周期任务")
	}
}

func TestTaskStopCancelsCurrentExecution(t *testing.T) {
	started := make(chan struct{})
	task, _ := NewTask(testRunner(), "cancel", time.Hour, func(ctx context.Context) {
		close(started)
		<-ctx.Done()
	})
	if err := task.Start(context.Background()); err != nil {
		t.Fatalf("启动任务：%v", err)
	}
	<-started
	stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := task.Stop(stopContext); err != nil {
		t.Fatalf("停止任务：%v", err)
	}
}

func TestTaskCanRestartAfterStop(t *testing.T) {
	var count atomic.Int32
	task, _ := NewTask(testRunner(), "restart", time.Hour, func(context.Context) { count.Add(1) })
	for iteration := int32(1); iteration <= 2; iteration++ {
		if err := task.Start(context.Background()); err != nil {
			t.Fatalf("启动任务：%v", err)
		}
		deadline := time.Now().Add(time.Second)
		for count.Load() < iteration && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if err := task.Stop(context.Background()); err != nil {
			t.Fatalf("停止任务：%v", err)
		}
	}
	if count.Load() != 2 {
		t.Fatalf("任务重启后执行次数=%d，预期 2", count.Load())
	}
}

func TestTaskStopHonorsDeadline(t *testing.T) {
	block := make(chan struct{})
	task, _ := NewTask(testRunner(), "blocked", time.Hour, func(context.Context) { <-block })
	_ = task.Start(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := task.Stop(ctx)
	close(block)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("停止超时错误=%v", err)
	}
	_ = task.Stop(context.Background())
}

func TestTaskGroupRollbackHonorsStartupDeadline(t *testing.T) {
	block := make(chan struct{})
	first, _ := NewTask(testRunner(), "first", time.Hour, func(context.Context) { <-block })
	second, _ := NewTask(testRunner(), "second", time.Hour, func(ctx context.Context) { <-ctx.Done() })
	if err := second.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	group := &TaskGroup{runner: testRunner(), tasks: []*Task{first, second}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := group.Start(ctx); err == nil {
		t.Fatal("已有任务运行时 TaskGroup 启动必须失败")
	}
	if time.Since(started) > time.Second {
		t.Fatal("TaskGroup 回滚未遵守启动截止时间")
	}
	close(block)
	_ = first.Stop(context.Background())
	_ = second.Stop(context.Background())
}
