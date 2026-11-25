package kafka

import (
	"context"
	"fmt"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/internal/lifecycle"
	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

type Message struct {
	Topic string
	Key   []byte
	Value []byte
}

type KafkaProducer struct {
	name         string
	writer       *kafkago.Writer
	dependencies observe.Dependencies
}

func NewKafkaProducer(name string, c *configv1.KafkaProducerConfig, runtime *styxruntime.Runtime) (*KafkaProducer, error) {
	if runtime == nil {
		return nil, fmt.Errorf("kafka: producer runtime is required")
	}
	dependencies := runtime.Dependencies()
	if err := dependencies.Validate(); err != nil {
		return nil, fmt.Errorf("kafka: producer runtime dependencies: %w", err)
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("kafka: producer name is required")
	}
	if c == nil {
		return nil, fmt.Errorf("kafka: producer config is nil")
	}
	if len(c.Brokers) == 0 {
		return nil, fmt.Errorf("kafka: brokers is required")
	}
	for _, broker := range c.Brokers {
		if strings.TrimSpace(broker) == "" {
			return nil, fmt.Errorf("kafka: broker must not be empty")
		}
	}
	if c.MaxMessageBytes < 0 || c.RetryMax < 0 {
		return nil, fmt.Errorf("kafka: producer limits must not be negative")
	}
	if !c.SaslEnabled && (c.SaslMechanism != "" || c.SaslUser != "" || c.SaslPassword != "") {
		return nil, fmt.Errorf("kafka: SASL settings require sasl_enabled=true")
	}
	dialer, err := buildDialer(c.SaslEnabled, c.SaslMechanism, c.SaslUser, c.SaslPassword, c.Tls)
	if err != nil {
		return nil, fmt.Errorf("kafka: dialer creation failed: %w", err)
	}
	writeTimeout := DefaultTimeout
	if c.Timeout != nil {
		writeTimeout = c.Timeout.AsDuration()
		if writeTimeout <= 0 {
			return nil, fmt.Errorf("kafka: producer timeout must be positive")
		}
	}
	maxAttempts := DefaultRetryMax
	if c.RetryMax > 0 {
		maxAttempts = int(c.RetryMax)
	}
	batchBytes := int64(DefaultMaxMessageBytes)
	if c.MaxMessageBytes > 0 {
		batchBytes = int64(c.MaxMessageBytes)
	}
	requiredAcks := kafkago.RequiredAcks(DefaultRequiredAcks)
	switch c.RequiredAcks {
	case configv1.KafkaRequiredAcks_KAFKA_REQUIRED_ACKS_ALL:
		requiredAcks = kafkago.RequireAll
	case configv1.KafkaRequiredAcks_KAFKA_REQUIRED_ACKS_NONE:
		requiredAcks = kafkago.RequireNone
	case configv1.KafkaRequiredAcks_KAFKA_REQUIRED_ACKS_LEADER:
		requiredAcks = kafkago.RequireOne
	default:
		return nil, fmt.Errorf("kafka: unsupported required_acks value %d", c.RequiredAcks)
	}
	compression := DefaultCompression
	if c.Compression != "" {
		compression = c.Compression
	}
	compressionValue, err := compressionCodec(compression)
	if err != nil {
		return nil, err
	}
	batchTimeout := time.Second
	if c.FlushFrequency != nil {
		batchTimeout = c.FlushFrequency.AsDuration()
		if batchTimeout <= 0 {
			return nil, fmt.Errorf("kafka: flush frequency must be positive")
		}
	}
	transport := &kafkago.Transport{
		DialTimeout: dialer.Timeout,
		TLS:         dialer.TLS,
		SASL:        dialer.SASLMechanism,
	}
	w := &kafkago.Writer{
		Addr:         kafkago.TCP(c.Brokers...),
		Balancer:     &kafkago.LeastBytes{},
		WriteTimeout: writeTimeout,
		MaxAttempts:  maxAttempts,
		BatchBytes:   batchBytes,
		BatchTimeout: batchTimeout,
		RequiredAcks: requiredAcks,
		Compression:  compressionValue,
		Transport:    transport,
	}
	dependencies.Logger().Info("kafka: producer created", "name", name, "broker_count", len(c.Brokers))
	p := &KafkaProducer{name: name, writer: w, dependencies: dependencies}
	shutdown := styxruntime.BoundedClose(func() error {
		err := w.Close()
		transport.CloseIdleConnections()
		return err
	})
	if err := runtime.Own("kafka producer "+name, shutdown); err != nil {
		return nil, lifecycle.Rollback("kafka producer "+name, shutdown, err)
	}
	return p, nil
}

func (p *KafkaProducer) Send(ctx context.Context, topic, key string, value []byte) error {
	if ctx == nil {
		return fmt.Errorf("kafka: send context is required")
	}
	if strings.TrimSpace(topic) == "" {
		return fmt.Errorf("kafka: topic is required")
	}
	msg := kafkago.Message{
		Topic: topic,
		Key:   []byte(key),
		Value: value,
	}
	p.dependencies.Propagator().Inject(ctx, newKafkaHeaderCarrier(&msg.Headers))
	if err := p.writer.WriteMessages(ctx, msg); err != nil {
		return fmt.Errorf("kafka: send failed: %w", err)
	}
	return nil
}

func (p *KafkaProducer) SendBatch(ctx context.Context, msgs ...Message) error {
	if ctx == nil {
		return fmt.Errorf("kafka: batch send context is required")
	}
	if len(msgs) == 0 {
		return nil
	}
	kafkaMsgs := make([]kafkago.Message, 0, len(msgs))
	for _, m := range msgs {
		if strings.TrimSpace(m.Topic) == "" {
			return fmt.Errorf("kafka: every batch message requires a topic")
		}
		km := kafkago.Message{
			Topic: m.Topic,
			Key:   m.Key,
			Value: m.Value,
		}
		p.dependencies.Propagator().Inject(ctx, newKafkaHeaderCarrier(&km.Headers))
		kafkaMsgs = append(kafkaMsgs, km)
	}
	if err := p.writer.WriteMessages(ctx, kafkaMsgs...); err != nil {
		return fmt.Errorf("kafka: batch send failed: %w", err)
	}
	return nil
}
