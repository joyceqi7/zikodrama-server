package kafka

import (
	"context"
	"testing"

	kafkago "github.com/segmentio/kafka-go"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

func TestKafkaHeaderCarrier_GetSetKeys(t *testing.T) {
	headers := []kafkago.Header{
		{Key: "traceparent", Value: []byte("00-abc-def-01")},
	}
	carrier := newKafkaHeaderCarrier(&headers)

	if got := carrier.Get("traceparent"); got != "00-abc-def-01" {
		t.Errorf("Get(traceparent) = %q, want %q", got, "00-abc-def-01")
	}
	if got := carrier.Get("missing"); got != "" {
		t.Errorf("Get(missing) = %q, want empty", got)
	}

	carrier.Set("tracestate", "vendor=abc")
	if got := carrier.Get("tracestate"); got != "vendor=abc" {
		t.Errorf("Set+Get(tracestate) = %q, want %q", got, "vendor=abc")
	}

	carrier.Set("traceparent", "00-xyz-uvw-01")
	if got := carrier.Get("traceparent"); got != "00-xyz-uvw-01" {
		t.Errorf("Set overwrite = %q, want %q", got, "00-xyz-uvw-01")
	}

	keys := carrier.Keys()
	if len(keys) != 2 {
		t.Errorf("Keys() len = %d, want 2", len(keys))
	}
}

func newKafkaRuntime(t *testing.T) *styxruntime.Runtime {
	t.Helper()
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity:     styxruntime.Identity{Name: "kafka-test", Version: "test"},
		Dependencies: observe.NoopDependencies(),
	})
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}
	return runtime
}

func TestCompressionCodec(t *testing.T) {
	tests := []struct {
		name string
		want kafkago.Compression
	}{
		{"gzip", kafkago.Gzip},
		{"snappy", kafkago.Snappy},
		{"lz4", kafkago.Lz4},
		{"zstd", kafkago.Zstd},
		{"none", 0},
		{"", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := compressionCodec(tt.name)
			if err != nil {
				t.Fatalf("compressionCodec(%q): %v", tt.name, err)
			}
			if got != tt.want {
				t.Errorf("compressionCodec(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestCompressionCodecRejectsUnknownValue(t *testing.T) {
	if _, err := compressionCodec("brotli"); err == nil {
		t.Fatal("未知压缩算法必须失败")
	}
}

func TestKafkaProducerRejectsInvalidLimitsBeforeCreatingTransport(t *testing.T) {
	_, err := NewKafkaProducer("audit", &configv1.KafkaProducerConfig{
		Brokers: []string{"127.0.0.1:9092"}, RetryMax: -1,
	}, newKafkaRuntime(t))
	if err == nil {
		t.Fatal("Kafka 生产者负重试次数必须失败")
	}
	producer := &KafkaProducer{dependencies: observe.NoopDependencies()}
	if err := producer.Send(context.Background(), "", "", nil); err == nil {
		t.Fatal("Kafka 消息必须指定 topic")
	}
}
