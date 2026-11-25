package rocketmq

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	rmq "github.com/apache/rocketmq-clients/golang/v5"

	"gitee.com/linpbin/styx/observe"
)

func TestSendAsyncReplacesNilCallback(t *testing.T) {
	inner := &fakeProducerClient{}
	producer := &Producer{inner: inner, dependencies: observe.NoopDependencies()}
	producer.SendAsync(context.Background(), &rmq.Message{Topic: "topic"}, nil)
	if !inner.callbackConfigured {
		t.Fatal("SendAsync 必须为 RocketMQ SDK 提供非空回调")
	}
}

func TestStartWithContextStopsClientAfterLateStart(t *testing.T) {
	release := make(chan struct{})
	stopped := make(chan struct{})
	var starts atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err, cleanupDeferred := startWithContext(ctx, func() error {
		starts.Add(1)
		<-release
		return nil
	}, func() error {
		close(stopped)
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start 错误 = %v，预期 DeadlineExceeded", err)
	}
	if !cleanupDeferred {
		t.Fatal("超时后应把 Stop 所有权交给迟到启动清理")
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("超时后完成启动的 RocketMQ Client 未被停止")
	}
	if starts.Load() != 1 {
		t.Fatalf("Start 调用次数 = %d，预期 1", starts.Load())
	}
}

func TestProducerTimeoutStopsOnlyAfterStartReturnsAndOnlyOnce(t *testing.T) {
	runtime := newRocketRuntime(t)
	inner := &blockingStartProducer{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := startProducerResource(ctx, runtime, "late", inner)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("启动 Producer 错误 = %v，预期 DeadlineExceeded", err)
	}
	stopsBeforeStartReturned := inner.stopCalls.Load()
	close(inner.release)
	wantStopsAfterLateStart := stopsBeforeStartReturned + 1
	deadline := time.Now().Add(time.Second)
	for inner.stopCalls.Load() < wantStopsAfterLateStart && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if stopsBeforeStartReturned != 0 {
		t.Fatalf("Producer Start 返回前已调用 %d 次 Stop", stopsBeforeStartReturned)
	}
	if got := inner.concurrentStops.Load(); got != 0 {
		t.Fatalf("Producer Start 返回前发生 %d 次并发 Stop", got)
	}
	if got := inner.stopCalls.Load(); got != 1 {
		t.Fatalf("Producer Stop 调用次数 = %d，预期 1", got)
	}
}

type blockingStartProducer struct {
	started         chan struct{}
	release         chan struct{}
	startFinished   atomic.Bool
	stopCalls       atomic.Int32
	concurrentStops atomic.Int32
}

func (producer *blockingStartProducer) Start() error {
	close(producer.started)
	<-producer.release
	producer.startFinished.Store(true)
	return nil
}

func (producer *blockingStartProducer) GracefulStop() error {
	if !producer.startFinished.Load() {
		producer.concurrentStops.Add(1)
	}
	producer.stopCalls.Add(1)
	return nil
}

type fakeProducerClient struct {
	callbackConfigured bool
}

func (*fakeProducerClient) Send(context.Context, *rmq.Message) ([]*rmq.SendReceipt, error) {
	return nil, nil
}

func (producer *fakeProducerClient) SendAsync(_ context.Context, _ *rmq.Message, callback func(context.Context, []*rmq.SendReceipt, error)) {
	producer.callbackConfigured = callback != nil
}

func (*fakeProducerClient) SendWithTransaction(context.Context, *rmq.Message, rmq.Transaction) ([]*rmq.SendReceipt, error) {
	return nil, nil
}

func (*fakeProducerClient) BeginTransaction() rmq.Transaction { return nil }
func (*fakeProducerClient) GracefulStop() error               { return nil }
