// Package rocketmq provides RocketMQ 5.x integration for Kratos Wire.
package rocketmq

import (
	"context"
	"fmt"
	"strings"

	rmq "github.com/apache/rocketmq-clients/golang/v5"
	"github.com/apache/rocketmq-clients/golang/v5/credentials"
	"go.opentelemetry.io/otel/propagation"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/internal/lifecycle"
	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

const (
	defaultMaxAttempts = 3
)

// Producer wraps rmq.Producer with automatic trace context propagation.
type Producer struct {
	name         string
	inner        producerClient
	dependencies observe.Dependencies
}

type producerClient interface {
	Send(context.Context, *rmq.Message) ([]*rmq.SendReceipt, error)
	SendAsync(context.Context, *rmq.Message, func(context.Context, []*rmq.SendReceipt, error))
	SendWithTransaction(context.Context, *rmq.Message, rmq.Transaction) ([]*rmq.SendReceipt, error)
	BeginTransaction() rmq.Transaction
	GracefulStop() error
}

// Send sends a message with automatic trace context injection.
func (p *Producer) Send(ctx context.Context, msg *rmq.Message) ([]*rmq.SendReceipt, error) {
	if ctx == nil {
		return nil, fmt.Errorf("rocketmq: send context is required")
	}
	if msg == nil {
		return nil, fmt.Errorf("rocketmq: message is required")
	}
	p.dependencies.Propagator().Inject(ctx, propagation.MapCarrier(msg.GetProperties()))
	return p.inner.Send(ctx, msg)
}

// SendAsync sends a message asynchronously with automatic trace context injection.
func (p *Producer) SendAsync(ctx context.Context, msg *rmq.Message, callback func(context.Context, []*rmq.SendReceipt, error)) {
	if ctx == nil {
		if callback != nil {
			callback(context.Background(), nil, fmt.Errorf("rocketmq: send context is required"))
		}
		return
	}
	if msg == nil {
		if callback != nil {
			callback(ctx, nil, fmt.Errorf("rocketmq: message is required"))
		}
		return
	}
	if callback == nil {
		// RocketMQ SDK 会无条件调用 callback；空回调使用 no-op，避免异步 goroutine panic。
		callback = func(context.Context, []*rmq.SendReceipt, error) {}
	}
	p.dependencies.Propagator().Inject(ctx, propagation.MapCarrier(msg.GetProperties()))
	p.inner.SendAsync(ctx, msg, callback)
}

// SendWithTransaction sends a transactional message with automatic trace context injection.
func (p *Producer) SendWithTransaction(ctx context.Context, msg *rmq.Message, tx rmq.Transaction) ([]*rmq.SendReceipt, error) {
	if ctx == nil {
		return nil, fmt.Errorf("rocketmq: send context is required")
	}
	if msg == nil {
		return nil, fmt.Errorf("rocketmq: message is required")
	}
	p.dependencies.Propagator().Inject(ctx, propagation.MapCarrier(msg.GetProperties()))
	return p.inner.SendWithTransaction(ctx, msg, tx)
}

// BeginTransaction begins a new transaction.
func (p *Producer) BeginTransaction() rmq.Transaction {
	return p.inner.BeginTransaction()
}

// GracefulStop gracefully stops the producer.
func (p *Producer) GracefulStop() error {
	return p.inner.GracefulStop()
}

// NewProducer 创建自动传播 Trace Context 的 RocketMQ Producer，并交给 Runtime 关闭。
func NewProducer(ctx context.Context, name string, c *configv1.RocketMQConfig, runtime *styxruntime.Runtime) (*Producer, error) {
	if ctx == nil {
		return nil, fmt.Errorf("rocketmq: producer context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("rocketmq: producer context: %w", err)
	}
	if runtime == nil {
		return nil, fmt.Errorf("rocketmq: producer runtime is required")
	}
	dependencies := runtime.Dependencies()
	if err := dependencies.Validate(); err != nil {
		return nil, fmt.Errorf("rocketmq: producer runtime dependencies: %w", err)
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("rocketmq: producer name is required")
	}
	if c == nil {
		return nil, fmt.Errorf("rocketmq: config is nil")
	}
	if strings.TrimSpace(c.Endpoint) == "" {
		return nil, fmt.Errorf("rocketmq: endpoint is required")
	}
	if len(c.Topics) == 0 {
		return nil, fmt.Errorf("rocketmq: topics are required")
	}
	for _, topic := range c.Topics {
		if strings.TrimSpace(topic) == "" {
			return nil, fmt.Errorf("rocketmq: topic must not be empty")
		}
	}
	if c.MaxAttempts < 0 {
		return nil, fmt.Errorf("rocketmq: max_attempts must not be negative")
	}
	if err := validateCredentials(c.AccessKey, c.SecretKey); err != nil {
		return nil, err
	}

	// Build RocketMQ config
	cfg := &rmq.Config{
		Endpoint: c.Endpoint,
	}

	// Set namespace if provided
	if c.Namespace != "" {
		cfg.NameSpace = c.Namespace
	}

	// Set credentials if provided
	if c.AccessKey != "" && c.SecretKey != "" {
		cfg.Credentials = &credentials.SessionCredentials{
			AccessKey:    c.AccessKey,
			AccessSecret: c.SecretKey,
		}
	}

	// Build producer options
	var opts []rmq.ProducerOption
	if len(c.Topics) > 0 {
		opts = append(opts, rmq.WithTopics(c.Topics...))
	}

	// Max attempts
	maxAttempts := c.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	opts = append(opts, rmq.WithMaxAttempts(maxAttempts))

	// Create producer
	inner, err := rmq.NewProducer(cfg, opts...)
	if err != nil {
		return nil, fmt.Errorf("rocketmq: create producer failed: %w", err)
	}

	// RocketMQ SDK 的 Start 不接收 Context，因此通过有界等待保证调用方可取消。
	if err := startProducerResource(ctx, runtime, name, inner); err != nil {
		return nil, fmt.Errorf("rocketmq: start producer failed: %w", err)
	}

	dependencies.Logger().Info("rocketmq: producer started", "name", name, "topic_count", len(c.Topics))

	p := &Producer{name: name, inner: inner, dependencies: dependencies}

	shutdown := styxruntime.BoundedClose(inner.GracefulStop)
	if err := runtime.Own("rocketmq producer "+name, shutdown); err != nil {
		return nil, lifecycle.Rollback("rocketmq producer "+name, shutdown, err)
	}
	return p, nil
}

type startableProducer interface {
	Start() error
	GracefulStop() error
}

func startProducerResource(ctx context.Context, runtime *styxruntime.Runtime, name string, inner startableProducer) error {
	cleanupDeferred := false
	if err := runtime.StartResource(ctx, "rocketmq producer "+name, func(startContext context.Context) error {
		var startErr error
		startErr, cleanupDeferred = startWithContext(startContext, inner.Start, inner.GracefulStop)
		return startErr
	}); err != nil {
		if !cleanupDeferred {
			_ = inner.GracefulStop()
		}
		return err
	}
	return nil
}

func startWithContext(ctx context.Context, start func() error, stop func() error) (error, bool) {
	result := make(chan error, 1)
	go func() {
		result <- start()
	}()
	select {
	case err := <-result:
		return err, false
	case <-ctx.Done():
		go func() {
			<-result
			_ = stop()
		}()
		return ctx.Err(), true
	}
}

func validateCredentials(accessKey, secretKey string) error {
	if (accessKey == "") != (secretKey == "") {
		return fmt.Errorf("rocketmq: access_key and secret_key must be configured together")
	}
	return nil
}
