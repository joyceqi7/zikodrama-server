package kafka

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"
	"go.opentelemetry.io/otel/propagation"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

const (
	DefaultMaxMessageBytes   = 1024 * 1024
	DefaultTimeout           = 10 * time.Second
	DefaultRetryMax          = 3
	DefaultRequiredAcks      = -1
	DefaultCompression       = "snappy"
	DefaultSessionTimeout    = 10 * time.Second
	DefaultHeartbeatInterval = 3 * time.Second
	DefaultRebalanceTimeout  = 60 * time.Second
	DefaultFetchMaxWait      = 100 * time.Millisecond
	DefaultFetchMinBytes     = 1
	DefaultFetchMaxBytes     = 10 * 1024 * 1024
)

// kafkaHeaderCarrier adapts kafka-go headers to OTel TextMapCarrier.
type kafkaHeaderCarrier struct {
	headers *[]kafkago.Header
}

var _ propagation.TextMapCarrier = (*kafkaHeaderCarrier)(nil)

func newKafkaHeaderCarrier(headers *[]kafkago.Header) *kafkaHeaderCarrier {
	return &kafkaHeaderCarrier{headers: headers}
}

func (c *kafkaHeaderCarrier) Get(key string) string {
	for _, h := range *c.headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func (c *kafkaHeaderCarrier) Set(key, val string) {
	for i, h := range *c.headers {
		if h.Key == key {
			(*c.headers)[i].Value = []byte(val)
			return
		}
	}
	*c.headers = append(*c.headers, kafkago.Header{Key: key, Value: []byte(val)})
}

func (c *kafkaHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(*c.headers))
	for _, h := range *c.headers {
		keys = append(keys, h.Key)
	}
	return keys
}

func buildDialer(saslEnabled bool, saslMechanism, saslUser, saslPassword string, tlsCfg *configv1.KafkaTLSConfig) (*kafkago.Dialer, error) {
	dialer := &kafkago.Dialer{
		Timeout:   DefaultTimeout,
		DualStack: true,
	}
	if saslEnabled {
		mechanism, err := buildSASLMechanism(saslMechanism, saslUser, saslPassword)
		if err != nil {
			return nil, err
		}
		dialer.SASLMechanism = mechanism
	}
	if tlsCfg != nil && tlsCfg.Enabled {
		tc, err := buildTLSConfig(tlsCfg)
		if err != nil {
			return nil, err
		}
		dialer.TLS = tc
	}
	return dialer, nil
}

func buildSASLMechanism(mechanism, user, password string) (sasl.Mechanism, error) {
	if user == "" || password == "" {
		return nil, fmt.Errorf("kafka: sasl_user and sasl_password are required when SASL is enabled")
	}
	switch mechanism {
	case "PLAIN":
		return &plain.Mechanism{Username: user, Password: password}, nil
	case "SCRAM-SHA-256":
		m, err := scram.Mechanism(scram.SHA256, user, password)
		if err != nil {
			return nil, fmt.Errorf("kafka: SCRAM-SHA-256 init failed: %w", err)
		}
		return m, nil
	case "SCRAM-SHA-512":
		m, err := scram.Mechanism(scram.SHA512, user, password)
		if err != nil {
			return nil, fmt.Errorf("kafka: SCRAM-SHA-512 init failed: %w", err)
		}
		return m, nil
	default:
		return nil, fmt.Errorf("kafka: unsupported SASL mechanism %q", mechanism)
	}
}

func buildTLSConfig(cfg *configv1.KafkaTLSConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		ServerName:         cfg.ServerName,
		MinVersion:         tls.VersionTLS12,
	}
	if cfg.CaFile != "" {
		caCert, err := os.ReadFile(cfg.CaFile)
		if err != nil {
			return nil, fmt.Errorf("kafka: failed to read CA file: %w", err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("kafka: failed to parse CA certificate")
		}
		tlsConfig.RootCAs = caCertPool
	}
	if (cfg.CertFile == "") != (cfg.KeyFile == "") {
		return nil, fmt.Errorf("kafka: cert_file and key_file must be configured together")
	}
	if cfg.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("kafka: failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	return tlsConfig, nil
}

func compressionCodec(name string) (kafkago.Compression, error) {
	switch name {
	case "", "none":
		return 0, nil
	case "gzip":
		return kafkago.Gzip, nil
	case "snappy":
		return kafkago.Snappy, nil
	case "lz4":
		return kafkago.Lz4, nil
	case "zstd":
		return kafkago.Zstd, nil
	default:
		return 0, fmt.Errorf("kafka: unsupported compression %q", name)
	}
}
