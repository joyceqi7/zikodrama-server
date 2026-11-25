package rocketmq

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	rmq "github.com/apache/rocketmq-clients/golang/v5"
	"github.com/apache/rocketmq-clients/golang/v5/credentials"
	"go.opentelemetry.io/otel/propagation"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

const (
	defaultAwaitDuration        = 20 * time.Second
	defaultMaxCacheMessageCount = int32(64)
)

// MessageHandler 是 RocketMQ 消息处理函数。
type MessageHandler func(ctx context.Context, msg *rmq.MessageView) error

// ConsumerEntry 定义一个 RocketMQ 消费者。
type ConsumerEntry struct {
	Name    string
	Config  *configv1.RocketMQConsumerConfig
	Handler MessageHandler
}

// RocketMQServer 实现 Kratos transport.Server。
type RocketMQServer struct {
	entries      []ConsumerEntry
	dependencies observe.Dependencies
	runtime      *styxruntime.Runtime

	mu        sync.Mutex
	running   bool
	consumers []rmq.PushConsumer
	ctx       context.Context
	cancel    context.CancelFunc
	stopping  bool
	stopDone  chan struct{}
	stopErr   error
}

func NewRocketMQServer(runtime *styxruntime.Runtime, entries ...ConsumerEntry) (*RocketMQServer, error) {
	if runtime == nil {
		return nil, fmt.Errorf("rocketmq: server runtime is required")
	}
	server := &RocketMQServer{
		entries:      append([]ConsumerEntry(nil), entries...),
		dependencies: runtime.Dependencies(),
		runtime:      runtime,
	}
	if err := runtime.Own("rocketmq server", server.Stop); err != nil {
		return nil, err
	}
	return server, nil
}

func (s *RocketMQServer) Start(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("rocketmq: context is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("rocketmq: context: %w", err)
	}
	if err := s.dependencies.Validate(); err != nil {
		return fmt.Errorf("rocketmq: runtime dependencies: %w", err)
	}
	return s.runtime.StartResource(ctx, "rocketmq server", s.start)
}

func (s *RocketMQServer) start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return fmt.Errorf("rocketmq: server is already running")
	}
	if len(s.entries) == 0 {
		s.dependencies.Logger().Info("rocketmq: no consumers configured, skipping")
		return nil
	}
	for _, entry := range s.entries {
		if strings.TrimSpace(entry.Name) == "" {
			return fmt.Errorf("rocketmq: consumer name is required")
		}
		if entry.Config == nil {
			return fmt.Errorf("rocketmq: consumer %q config is required", entry.Name)
		}
		if entry.Handler == nil {
			return fmt.Errorf("rocketmq: consumer %q handler is required", entry.Name)
		}
		if err := validateConsumerConfig(entry.Config); err != nil {
			return fmt.Errorf("rocketmq: consumer %q: %w", entry.Name, err)
		}
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.running = true
	s.stopping = false
	s.stopDone = nil
	s.stopErr = nil
	for _, entry := range s.entries {
		consumer, err := s.createConsumer(entry)
		if err != nil {
			s.cancel()
			stopErr := s.stopConsumersLocked()
			s.running = false
			return errors.Join(err, stopErr)
		}
		startErr, cleanupDeferred := startWithContext(ctx, consumer.Start, consumer.GracefulStop)
		if startErr != nil {
			if !cleanupDeferred {
				_ = consumer.GracefulStop()
			}
			s.cancel()
			stopErr := s.stopConsumersLocked()
			s.running = false
			return errors.Join(fmt.Errorf("rocketmq: start consumer %q: %w", entry.Name, startErr), stopErr)
		}
		s.consumers = append(s.consumers, consumer)
		s.dependencies.Logger().Info("rocketmq: consumer started", "name", entry.Name)
	}
	s.dependencies.Logger().Info("rocketmq: all consumers started", "count", len(s.entries))
	return nil
}

func (s *RocketMQServer) Stop(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("rocketmq: context is required")
	}
	s.mu.Lock()
	if !s.running {
		stopErr := s.stopErr
		s.mu.Unlock()
		return stopErr
	}
	if !s.stopping {
		s.stopping = true
		s.stopDone = make(chan struct{})
		if s.cancel != nil {
			s.cancel()
		}
		consumers := append([]rmq.PushConsumer(nil), s.consumers...)
		s.consumers = nil
		done := s.stopDone
		go func() {
			stopErr := stopConsumers(consumers)
			s.mu.Lock()
			s.stopErr = stopErr
			s.running = false
			s.stopping = false
			s.ctx = nil
			s.cancel = nil
			close(done)
			s.mu.Unlock()
		}()
	}
	done := s.stopDone
	s.mu.Unlock()

	select {
	case <-done:
		s.mu.Lock()
		stopErr := s.stopErr
		s.mu.Unlock()
		s.dependencies.Logger().Info("rocketmq: all consumers stopped")
		return stopErr
	case <-ctx.Done():
		return fmt.Errorf("rocketmq: stop: %w", ctx.Err())
	}
}

func (s *RocketMQServer) createConsumer(entry ConsumerEntry) (rmq.PushConsumer, error) {
	c := entry.Config
	config := &rmq.Config{Endpoint: c.Endpoint, ConsumerGroup: c.ConsumerGroup}
	if c.Namespace != "" {
		config.NameSpace = c.Namespace
	}
	if c.AccessKey != "" {
		config.Credentials = &credentials.SessionCredentials{
			AccessKey:    c.AccessKey,
			AccessSecret: c.SecretKey,
		}
	}

	filterExpression := rmq.SUB_ALL
	if c.FilterExpression != "" && c.FilterExpression != "*" {
		filterExpression = rmq.NewFilterExpression(c.FilterExpression)
	}
	options := []rmq.PushConsumerOption{rmq.WithPushSubscriptionExpressions(map[string]*rmq.FilterExpression{
		c.Topic: filterExpression,
	})}
	awaitDuration := defaultAwaitDuration
	if c.AwaitDuration != nil {
		awaitDuration = c.AwaitDuration.AsDuration()
	}
	options = append(options, rmq.WithPushAwaitDuration(awaitDuration))
	maxCacheMessageCount := defaultMaxCacheMessageCount
	if c.MaxCacheMessageCount > 0 {
		maxCacheMessageCount = c.MaxCacheMessageCount
	}
	options = append(options, rmq.WithPushMaxCacheMessageCount(maxCacheMessageCount))
	if c.MaxCacheMessageSizeInBytes > 0 {
		options = append(options, rmq.WithPushMaxCacheMessageSizeInBytes(int64(c.MaxCacheMessageSizeInBytes)))
	}

	handler := entry.Handler
	options = append(options, rmq.WithPushMessageListener(&rmq.FuncMessageListener{
		Consume: func(message *rmq.MessageView) rmq.ConsumerResult {
			carrier := propagation.MapCarrier(message.GetProperties())
			ctx := s.dependencies.Propagator().Extract(s.ctx, carrier)
			if err := handler(ctx, message); err != nil {
				s.dependencies.Logger().ErrorContext(ctx, "rocketmq: handler failed",
					"name", entry.Name,
					"topic", c.Topic,
					"message_id", message.GetMessageId(),
					"error_type", fmt.Sprintf("%T", err),
				)
				return rmq.FAILURE
			}
			return rmq.SUCCESS
		},
	}))
	s.dependencies.Logger().Info("rocketmq: creating consumer", "name", entry.Name)
	return rmq.NewPushConsumer(config, options...)
}

func (s *RocketMQServer) stopConsumersLocked() error {
	err := stopConsumers(s.consumers)
	s.consumers = nil
	return err
}

func stopConsumers(consumers []rmq.PushConsumer) error {
	var stopErrors []error
	for _, consumer := range consumers {
		if err := consumer.GracefulStop(); err != nil {
			stopErrors = append(stopErrors, err)
		}
	}
	return errors.Join(stopErrors...)
}

func validateConsumerConfig(config *configv1.RocketMQConsumerConfig) error {
	if strings.TrimSpace(config.GetEndpoint()) == "" {
		return fmt.Errorf("endpoint is required")
	}
	if strings.TrimSpace(config.GetConsumerGroup()) == "" {
		return fmt.Errorf("consumer_group is required")
	}
	if strings.TrimSpace(config.GetTopic()) == "" {
		return fmt.Errorf("topic is required")
	}
	if err := validateCredentials(config.GetAccessKey(), config.GetSecretKey()); err != nil {
		return err
	}
	if config.GetAwaitDuration() != nil && config.GetAwaitDuration().AsDuration() <= 0 {
		return fmt.Errorf("await_duration must be positive")
	}
	if config.GetMaxCacheMessageCount() < 0 || config.GetMaxCacheMessageSizeInBytes() < 0 {
		return fmt.Errorf("cache limits must not be negative")
	}
	return nil
}
