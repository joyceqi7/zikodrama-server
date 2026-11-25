package mongodb

import (
	"context"
	"testing"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

func TestPoolMetricsCollectorCloseIsIdempotent(t *testing.T) {
	collector := NewPoolMetricsCollector("audit", observe.NoopDependencies())
	if err := collector.Register(); err != nil {
		t.Fatalf("注册 MongoDB 指标：%v", err)
	}
	collector.Close()
	collector.Close()
	if !collector.closed {
		t.Fatal("关闭后必须标记指标收集器状态")
	}
}

func TestNewMongoDBRejectsInvalidPoolRange(t *testing.T) {
	_, err := NewMongoDB(context.Background(), "audit", &configv1.MongoConfig{
		Uri:         "mongodb://127.0.0.1:27017",
		Database:    "audit",
		MinPoolSize: 10,
		MaxPoolSize: 1,
	}, newMongoRuntime(t))
	if err == nil {
		t.Fatal("MongoDB 最小连接数大于最大连接数时必须失败")
	}
}

func TestNewMongoDBRejectsNegativePoolLimitsBeforeConversion(t *testing.T) {
	_, err := NewMongoDB(context.Background(), "audit", &configv1.MongoConfig{
		Uri: "mongodb://127.0.0.1:27017", Database: "audit", MinPoolSize: -1, MaxPoolSize: -1,
	}, newMongoRuntime(t))
	if err == nil {
		t.Fatal("MongoDB 负连接池配置不能转换为超大无符号值")
	}
}

func newMongoRuntime(t *testing.T) *styxruntime.Runtime {
	t.Helper()
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity:     styxruntime.Identity{Name: "mongo-test", Version: "test"},
		Dependencies: observe.NoopDependencies(),
	})
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}
	return runtime
}
