package safe

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestCronRejectsInvalidSchedule(t *testing.T) {
	scheduler, err := NewCron(testRunner())
	if err != nil {
		t.Fatalf("创建 Cron：%v", err)
	}
	if err := scheduler.Add("invalid", "not a cron", func(context.Context) {}); err == nil {
		t.Fatal("无效 Cron 表达式必须失败")
	}
}

func TestCronStartStopLifecycle(t *testing.T) {
	scheduler, _ := NewCron(testRunner())
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("启动 Cron：%v", err)
	}
	if err := scheduler.Start(context.Background()); err == nil {
		t.Fatal("重复启动 Cron 必须失败")
	}
	if err := scheduler.Stop(context.Background()); err != nil {
		t.Fatalf("停止 Cron：%v", err)
	}
	if err := scheduler.Stop(context.Background()); err != nil {
		t.Fatalf("重复停止 Cron：%v", err)
	}
}

func TestCronRejectsStartAfterRuntimeShutdown(t *testing.T) {
	runner := testRunner()
	scheduler, err := NewCron(runner)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err == nil {
		t.Fatal("Runtime 关闭后不得启动 Cron")
	}
}

func TestCronExecuteRecoversPanic(t *testing.T) {
	scheduler, _ := NewCron(testRunner())
	var calls atomic.Int32
	scheduler.execute("panic", func(context.Context) {
		calls.Add(1)
		panic("secret")
	})
	if calls.Load() != 1 {
		t.Fatal("Cron 任务未执行")
	}
}

func TestCronRunsScheduledJob(t *testing.T) {
	scheduler, _ := NewCron(testRunner())
	done := make(chan struct{})
	if err := scheduler.Add("run", "* * * * * *", func(context.Context) {
		select {
		case <-done:
		default:
			close(done)
		}
	}); err != nil {
		t.Fatalf("添加 Cron：%v", err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("启动 Cron：%v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Cron 任务未按计划执行")
	}
	_ = scheduler.Stop(context.Background())
}

func TestCronCannotRestartWhileStopIsWaiting(t *testing.T) {
	scheduler, _ := NewCron(testRunner())
	started := make(chan struct{})
	release := make(chan struct{})
	if err := scheduler.Add("blocked", "* * * * * *", func(context.Context) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
	}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("Cron 任务未启动")
	}
	stopContext, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := scheduler.Stop(stopContext); err == nil {
		t.Fatal("仍有任务执行时 Stop 应受 Context 截止控制")
	}
	if err := scheduler.Start(context.Background()); err == nil {
		t.Fatal("Stop 尚未完成时不能重新启动 Cron")
	}
	close(release)
	if err := scheduler.Stop(context.Background()); err != nil {
		t.Fatalf("等待 Cron 完成停止：%v", err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("Cron 完成停止后应允许重启：%v", err)
	}
	_ = scheduler.Stop(context.Background())
}
