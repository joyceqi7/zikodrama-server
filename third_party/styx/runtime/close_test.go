package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoundedCloseTimesOutAndContinuesWaitingWithoutRestart(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	shutdown := BoundedClose(func() error {
		calls.Add(1)
		<-release
		return nil
	})

	firstContext, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := shutdown(firstContext); err == nil {
		t.Fatal("首次等待必须遵守 Context 截止时间")
	}
	close(release)
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("继续等待关闭过程：%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("Close 调用次数 = %d，预期 1", calls.Load())
	}
}

func TestBoundedCloseRejectsInvalidInput(t *testing.T) {
	if err := BoundedClose(nil)(context.Background()); err == nil {
		t.Fatal("必须拒绝空 Close 函数")
	}
	shutdown := BoundedClose(func() error { return nil })
	if err := shutdown(nil); err == nil {
		t.Fatal("必须拒绝空 Context")
	}
}

func TestBoundedShutdownContinuesAfterCallerTimeout(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	shutdown := BoundedShutdown(time.Second, func(context.Context) error {
		calls.Add(1)
		<-release
		return nil
	})

	firstContext, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := shutdown(firstContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("首次等待错误 = %v，预期 DeadlineExceeded", err)
	}
	close(release)
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("继续等待关闭过程：%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("Shutdown 调用次数 = %d，预期 1", calls.Load())
	}
}

func TestBoundedShutdownRejectsInvalidInput(t *testing.T) {
	if err := BoundedShutdown(0, func(context.Context) error { return nil })(context.Background()); err == nil {
		t.Fatal("必须拒绝无效内部超时")
	}
	if err := BoundedShutdown(time.Second, nil)(context.Background()); err == nil {
		t.Fatal("必须拒绝空 Shutdown 函数")
	}
}
