package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

type fakeMessageReader struct {
	commits int
}

func (reader *fakeMessageReader) FetchMessage(context.Context) (kafkago.Message, error) {
	return kafkago.Message{}, context.Canceled
}
func (reader *fakeMessageReader) CommitMessages(context.Context, ...kafkago.Message) error {
	reader.commits++
	return nil
}
func (reader *fakeMessageReader) Close() error { return nil }

func TestKafkaServerRejectsIncompleteEntries(t *testing.T) {
	handler := func(ctx context.Context, key, value []byte) error { return nil }
	for _, entry := range []ConsumerEntry{
		{Name: "nil-config", Config: nil, Handler: handler},
		{Name: "nil-handler", Config: &configv1.KafkaConsumerConfig{}, Handler: nil},
	} {
		server, err := NewKafkaServer(newKafkaRuntime(t), entry)
		if err != nil {
			t.Fatalf("NewKafkaServer() error = %v", err)
		}
		if err := server.Start(context.Background()); err == nil {
			t.Fatalf("不完整 Kafka 消费者配置必须失败：%s", entry.Name)
		}
	}
}

func TestKafkaServer_StartStop_Empty(t *testing.T) {
	s, err := NewKafkaServer(newKafkaRuntime(t))
	if err != nil {
		t.Fatalf("NewKafkaServer() error = %v", err)
	}
	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
}

func TestKafkaServerRejectsStartAfterRuntimeShutdown(t *testing.T) {
	runtime := newKafkaRuntime(t)
	server, err := NewKafkaServer(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := server.Start(context.Background()); err == nil {
		t.Fatal("Runtime 关闭后不得启动 Kafka Server")
	}
}

func TestProcessMessageDoesNotCommitHandlerFailure(t *testing.T) {
	server, err := NewKafkaServer(newKafkaRuntime(t))
	if err != nil {
		t.Fatalf("NewKafkaServer() error = %v", err)
	}
	server.ctx = context.Background()
	reader := &fakeMessageReader{}
	processed := server.processMessage("audit", reader, func(context.Context, []byte, []byte) error {
		return errors.New("handler failed")
	}, kafkago.Message{Topic: "audit", Offset: 1})
	if processed {
		t.Fatal("处理失败的消息不应标记成功")
	}
	if reader.commits != 0 {
		t.Fatal("处理失败的消息不得提交位点")
	}
}

func TestProcessMessageCommitsAfterSuccess(t *testing.T) {
	server, err := NewKafkaServer(newKafkaRuntime(t))
	if err != nil {
		t.Fatalf("NewKafkaServer() error = %v", err)
	}
	server.ctx = context.Background()
	reader := &fakeMessageReader{}
	processed := server.processMessage("audit", reader, func(context.Context, []byte, []byte) error {
		return nil
	}, kafkago.Message{Topic: "audit", Offset: 1})
	if !processed || reader.commits != 1 {
		t.Fatalf("处理成功后应提交一次位点，processed=%v commits=%d", processed, reader.commits)
	}
}

func TestValidateConsumerConfigRejectsUnusedSASLSettings(t *testing.T) {
	err := validateConsumerConfig(&configv1.KafkaConsumerConfig{
		Brokers:      []string{"127.0.0.1:9092"},
		GroupId:      "audit",
		Topics:       []string{"audit"},
		SaslUser:     "user",
		SaslPassword: "secret",
	})
	if err == nil {
		t.Fatal("未启用 SASL 时配置凭据必须失败")
	}
}

func TestKafkaServerRejectsRestartUntilTimedOutStopActuallyFinishes(t *testing.T) {
	server, err := NewKafkaServer(newKafkaRuntime(t))
	if err != nil {
		t.Fatalf("NewKafkaServer() error = %v", err)
	}
	server.running = true
	server.ctx, server.cancel = context.WithCancel(context.Background())
	server.wg.Add(1)
	stopContext, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := server.Stop(stopContext); err == nil {
		t.Fatal("后台消费循环未退出时 Stop 必须遵守截止时间")
	}
	if err := server.Start(context.Background()); err == nil {
		t.Fatal("旧消费循环未退出前不能重新启动")
	}
	server.wg.Done()
	if err := server.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
