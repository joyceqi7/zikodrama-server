package kafka

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/types/known/durationpb"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

// MessageHandler 是 Kafka 消息处理函数。
type MessageHandler func(ctx context.Context, key, value []byte) error

// ConsumerEntry 定义一个 Kafka 消费者。
type ConsumerEntry struct {
	Name    string
	Config  *configv1.KafkaConsumerConfig
	Handler MessageHandler
}

type messageReader interface {
	FetchMessage(context.Context) (kafkago.Message, error)
	CommitMessages(context.Context, ...kafkago.Message) error
	Close() error
}

// KafkaServer 实现 Kratos transport.Server，并保证处理成功后才提交位点。
type KafkaServer struct {
	entries      []ConsumerEntry
	dependencies observe.Dependencies
	runtime      *styxruntime.Runtime

	mu      sync.Mutex
	running bool
	readers []messageReader
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	stopErr error
}

func NewKafkaServer(runtime *styxruntime.Runtime, entries ...ConsumerEntry) (*KafkaServer, error) {
	if runtime == nil {
		return nil, fmt.Errorf("kafka: server runtime is required")
	}
	server := &KafkaServer{
		entries:      append([]ConsumerEntry(nil), entries...),
		dependencies: runtime.Dependencies(),
		runtime:      runtime,
	}
	if err := runtime.Own("kafka server", server.Stop); err != nil {
		return nil, err
	}
	return server, nil
}

func (s *KafkaServer) Start(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("kafka: context is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("kafka: context: %w", err)
	}
	if err := s.dependencies.Validate(); err != nil {
		return fmt.Errorf("kafka: runtime dependencies: %w", err)
	}
	return s.runtime.StartResource(ctx, "kafka server", s.start)
}

func (s *KafkaServer) start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return fmt.Errorf("kafka: server is already running")
	}
	if len(s.entries) == 0 {
		s.dependencies.Logger().Info("kafka: no consumers configured, skipping")
		return nil
	}

	for _, entry := range s.entries {
		if entry.Name == "" {
			return fmt.Errorf("kafka: consumer name is required")
		}
		if entry.Config == nil {
			return fmt.Errorf("kafka: consumer %q config is required", entry.Name)
		}
		if entry.Handler == nil {
			return fmt.Errorf("kafka: consumer %q handler is required", entry.Name)
		}
		if err := validateConsumerConfig(entry.Config); err != nil {
			return fmt.Errorf("kafka: consumer %q: %w", entry.Name, err)
		}
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.running = true
	s.stopErr = nil
	for _, entry := range s.entries {
		reader, err := s.createReader(entry)
		if err != nil {
			s.cancel()
			closeErr := s.closeReadersLocked()
			s.running = false
			return errors.Join(err, closeErr)
		}
		s.readers = append(s.readers, reader)
	}
	for index, entry := range s.entries {
		s.wg.Add(1)
		go s.consumeLoop(entry.Name, s.readers[index], entry.Handler)
	}
	s.dependencies.Logger().Info("kafka: all consumers started", "count", len(s.entries))
	return nil
}

func (s *KafkaServer) Stop(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("kafka: context is required")
	}
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	readers := append([]messageReader(nil), s.readers...)
	s.readers = nil
	s.mu.Unlock()

	var closeErrors []error
	for _, reader := range readers {
		if err := reader.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	s.mu.Lock()
	s.stopErr = errors.Join(s.stopErr, errors.Join(closeErrors...))
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		s.mu.Lock()
		s.running = false
		s.ctx = nil
		s.cancel = nil
		stopErr := s.stopErr
		s.mu.Unlock()
		s.dependencies.Logger().Info("kafka: all consumers stopped")
		return stopErr
	case <-ctx.Done():
		return fmt.Errorf("kafka: stop: %w", ctx.Err())
	}
}

func (s *KafkaServer) createReader(entry ConsumerEntry) (messageReader, error) {
	c := entry.Config
	dialer, err := buildDialer(c.SaslEnabled, c.SaslMechanism, c.SaslUser, c.SaslPassword, c.Tls)
	if err != nil {
		return nil, err
	}
	startOffset := kafkago.LastOffset
	if c.InitialOffset == -2 {
		startOffset = kafkago.FirstOffset
	}
	sessionTimeout := durationOrDefault(c.SessionTimeout, DefaultSessionTimeout)
	heartbeatInterval := durationOrDefault(c.HeartbeatInterval, DefaultHeartbeatInterval)
	rebalanceTimeout := durationOrDefault(c.RebalanceTimeout, DefaultRebalanceTimeout)
	fetchMaxWait := durationOrDefault(c.FetchMaxWait, DefaultFetchMaxWait)
	fetchMinBytes := int(c.FetchMinBytes)
	if fetchMinBytes <= 0 {
		fetchMinBytes = DefaultFetchMinBytes
	}
	fetchMaxBytes := int(c.FetchMaxBytes)
	if fetchMaxBytes <= 0 {
		fetchMaxBytes = DefaultFetchMaxBytes
	}
	config := kafkago.ReaderConfig{
		Brokers:           c.Brokers,
		GroupID:           c.GroupId,
		GroupTopics:       c.Topics,
		Dialer:            dialer,
		StartOffset:       startOffset,
		SessionTimeout:    sessionTimeout,
		HeartbeatInterval: heartbeatInterval,
		RebalanceTimeout:  rebalanceTimeout,
		MinBytes:          fetchMinBytes,
		MaxBytes:          fetchMaxBytes,
		MaxWait:           fetchMaxWait,
	}
	s.dependencies.Logger().Info("kafka: creating consumer",
		"name", entry.Name,
		"broker_count", len(c.Brokers),
		"topic_count", len(c.Topics),
	)
	return kafkago.NewReader(config), nil
}

func (s *KafkaServer) consumeLoop(name string, reader messageReader, handler MessageHandler) {
	defer s.wg.Done()
	s.dependencies.Logger().Info("kafka: consumer started", "name", name)
	consecutiveErrors := 0
	for {
		message, err := reader.FetchMessage(s.ctx)
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			consecutiveErrors++
			backoff := min(time.Duration(consecutiveErrors)*time.Second, 3*time.Second)
			s.dependencies.Logger().Warn("kafka: fetch message failed",
				"name", name,
				"error_type", fmt.Sprintf("%T", err),
				"consecutive_errors", consecutiveErrors,
			)
			if !waitContext(s.ctx, backoff) {
				return
			}
			continue
		}
		consecutiveErrors = 0
		for !s.processMessage(name, reader, handler, message) {
			if !waitContext(s.ctx, time.Second) {
				return
			}
		}
	}
}

func (s *KafkaServer) processMessage(name string, reader messageReader, handler MessageHandler, message kafkago.Message) bool {
	headers := message.Headers
	ctx := s.dependencies.Propagator().Extract(s.ctx, newKafkaHeaderCarrier(&headers))
	if err := handler(ctx, message.Key, message.Value); err != nil {
		s.dependencies.Logger().ErrorContext(ctx, "kafka: handler failed",
			"name", name,
			"topic", message.Topic,
			"partition", message.Partition,
			"offset", message.Offset,
			"error_type", fmt.Sprintf("%T", err),
		)
		return false
	}
	if err := reader.CommitMessages(s.ctx, message); err != nil {
		if s.ctx.Err() == nil {
			s.dependencies.Logger().Error("kafka: commit failed", "name", name, "error_type", fmt.Sprintf("%T", err))
		}
		return false
	}
	return true
}

func (s *KafkaServer) closeReadersLocked() error {
	var closeErrors []error
	for _, reader := range s.readers {
		if err := reader.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	s.readers = nil
	return errors.Join(closeErrors...)
}

func validateConsumerConfig(config *configv1.KafkaConsumerConfig) error {
	if len(config.GetBrokers()) == 0 {
		return fmt.Errorf("brokers are required")
	}
	for _, broker := range config.GetBrokers() {
		if strings.TrimSpace(broker) == "" {
			return fmt.Errorf("broker must not be empty")
		}
	}
	if strings.TrimSpace(config.GetGroupId()) == "" {
		return fmt.Errorf("group_id is required")
	}
	if len(config.GetTopics()) == 0 {
		return fmt.Errorf("topics are required")
	}
	for _, topic := range config.GetTopics() {
		if strings.TrimSpace(topic) == "" {
			return fmt.Errorf("topic must not be empty")
		}
	}
	if config.GetInitialOffset() != 0 && config.GetInitialOffset() != -1 && config.GetInitialOffset() != -2 {
		return fmt.Errorf("initial_offset must be 0, -1, or -2")
	}
	if config.GetHeartbeatInterval() != nil && config.GetSessionTimeout() != nil &&
		config.GetHeartbeatInterval().AsDuration() >= config.GetSessionTimeout().AsDuration() {
		return fmt.Errorf("heartbeat_interval must be shorter than session_timeout")
	}
	for name, duration := range map[string]*durationpb.Duration{
		"session_timeout":    config.GetSessionTimeout(),
		"heartbeat_interval": config.GetHeartbeatInterval(),
		"rebalance_timeout":  config.GetRebalanceTimeout(),
		"fetch_max_wait":     config.GetFetchMaxWait(),
	} {
		if duration != nil && duration.AsDuration() <= 0 {
			return fmt.Errorf("%s must be positive", name)
		}
	}
	if config.GetFetchMinBytes() < 0 || config.GetFetchMaxBytes() < 0 {
		return fmt.Errorf("fetch byte limits must not be negative")
	}
	if config.GetFetchMinBytes() > 0 && config.GetFetchMaxBytes() > 0 && config.GetFetchMinBytes() > config.GetFetchMaxBytes() {
		return fmt.Errorf("fetch_min_bytes must not exceed fetch_max_bytes")
	}
	if !config.GetSaslEnabled() && (config.GetSaslMechanism() != "" || config.GetSaslUser() != "" || config.GetSaslPassword() != "") {
		return fmt.Errorf("SASL settings require sasl_enabled=true")
	}
	_, err := buildDialer(config.GetSaslEnabled(), config.GetSaslMechanism(), config.GetSaslUser(), config.GetSaslPassword(), config.GetTls())
	return err
}

func durationOrDefault(duration *durationpb.Duration, fallback time.Duration) time.Duration {
	if duration == nil {
		return fallback
	}
	value := duration.AsDuration()
	if value <= 0 {
		return fallback
	}
	return value
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
