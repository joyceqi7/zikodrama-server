package rocketmq

import (
	"context"
	"testing"

	rmq "github.com/apache/rocketmq-clients/golang/v5"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

func TestRocketMQServerRejectsIncompleteEntries(t *testing.T) {
	handler := func(ctx context.Context, msg *rmq.MessageView) error { return nil }
	for _, entry := range []ConsumerEntry{
		{Name: "nil-config", Config: nil, Handler: handler},
		{Name: "nil-handler", Config: &configv1.RocketMQConsumerConfig{}, Handler: nil},
	} {
		server, err := NewRocketMQServer(newRocketRuntime(t), entry)
		if err != nil {
			t.Fatalf("NewRocketMQServer() error = %v", err)
		}
		if err := server.Start(context.Background()); err == nil {
			t.Fatalf("不完整 RocketMQ 消费者配置必须失败：%s", entry.Name)
		}
	}
}

func TestRocketMQServer_StartStop_Empty(t *testing.T) {
	s, err := NewRocketMQServer(newRocketRuntime(t))
	if err != nil {
		t.Fatalf("NewRocketMQServer() error = %v", err)
	}
	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
}

func TestRocketMQServerRejectsStartAfterRuntimeShutdown(t *testing.T) {
	runtime := newRocketRuntime(t)
	server, err := NewRocketMQServer(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := server.Start(context.Background()); err == nil {
		t.Fatal("Runtime 关闭后不得启动 RocketMQ Server")
	}
}

func TestValidateCredentialsRequiresPair(t *testing.T) {
	if err := validateCredentials("access", ""); err == nil {
		t.Fatal("RocketMQ 凭据未成对配置时必须失败")
	}
}

func TestValidateConsumerConfigRequiresRoutingFields(t *testing.T) {
	if err := validateConsumerConfig(&configv1.RocketMQConsumerConfig{}); err == nil {
		t.Fatal("RocketMQ 消费者缺少端点、消费组和主题时必须失败")
	}
}

func TestRocketMQProducerRejectsNegativeAttempts(t *testing.T) {
	_, err := NewProducer(context.Background(), "audit", &configv1.RocketMQConfig{
		Endpoint: "127.0.0.1:8081", Topics: []string{"audit"}, MaxAttempts: -1,
	}, newRocketRuntime(t))
	if err == nil {
		t.Fatal("RocketMQ 最大尝试次数不能为负数")
	}
}

func newRocketRuntime(t *testing.T) *styxruntime.Runtime {
	t.Helper()
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity:     styxruntime.Identity{Name: "rocketmq-test", Version: "test"},
		Dependencies: observe.NoopDependencies(),
	})
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}
	return runtime
}
