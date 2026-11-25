package safe

import (
	"context"
	"testing"
	"time"
)

func TestAuditTaskStopBeforeStartReturns(t *testing.T) {
	task, err := NewTask(testRunner(), "audit", time.Second, func(context.Context) {})
	if err != nil {
		t.Fatalf("创建任务：%v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = task.Stop(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Task 在启动前调用 Stop 会永久阻塞")
	}
}
