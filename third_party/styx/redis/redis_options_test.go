package redis

import (
	"context"
	"strings"
	"testing"
	"time"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

func TestRedisOptionsPropagateDisableIdentity(t *testing.T) {
	cfg := &configv1.RedisConfig{
		Addrs:           []string{"127.0.0.1:6379"},
		MasterName:      "mymaster",
		DisableIdentity: true,
		ReadOnly:        true,
		RouteByLatency:  true,
		RouteRandomly:   true,
	}

	poolSize := 16
	minIdleConns := 4
	maxIdleConns := 8
	maxActiveConns := 32
	connMaxLifetime := time.Hour
	connMaxIdleTime := 30 * time.Minute
	dialTimeout := 5 * time.Second
	readTimeout := 3 * time.Second
	writeTimeout := 3 * time.Second

	standalone := newStandaloneOptions(
		cfg,
		nil,
		poolSize,
		minIdleConns,
		maxIdleConns,
		maxActiveConns,
		connMaxLifetime,
		connMaxIdleTime,
		dialTimeout,
		readTimeout,
		writeTimeout,
	)
	if !standalone.DisableIdentity {
		t.Fatal("standalone options should propagate DisableIdentity")
	}
	if standalone.Protocol != 2 {
		t.Fatalf("standalone options should default to RESP2, got protocol=%d", standalone.Protocol)
	}

	failover := newFailoverOptions(
		cfg,
		nil,
		poolSize,
		minIdleConns,
		maxIdleConns,
		maxActiveConns,
		connMaxLifetime,
		connMaxIdleTime,
		dialTimeout,
		readTimeout,
		writeTimeout,
	)
	if !failover.DisableIdentity {
		t.Fatal("failover options should propagate DisableIdentity")
	}
	if failover.Protocol != 2 {
		t.Fatalf("failover options should default to RESP2, got protocol=%d", failover.Protocol)
	}

	cluster := newClusterOptions(
		cfg,
		nil,
		poolSize,
		minIdleConns,
		maxIdleConns,
		maxActiveConns,
		connMaxLifetime,
		connMaxIdleTime,
		dialTimeout,
		readTimeout,
		writeTimeout,
	)
	if !cluster.DisableIdentity {
		t.Fatal("cluster options should propagate DisableIdentity")
	}
	if cluster.Protocol != 2 {
		t.Fatalf("cluster options should default to RESP2, got protocol=%d", cluster.Protocol)
	}
}

func TestRedisConfigRejectsInvalidModeSpecificLimits(t *testing.T) {
	for _, config := range []*configv1.RedisConfig{
		{Mode: "standalone", Addrs: []string{"one:6379", "two:6379"}},
		{Mode: "cluster", Addrs: []string{"one:6379"}, Db: 1},
		{Addrs: []string{"one:6379"}, PoolSize: 2, MinIdleConns: 3},
	} {
		if _, err := New(context.Background(), "audit", config, newRedisRuntime(t)); err == nil {
			t.Fatal("无效 Redis 配置必须在连接前失败")
		}
	}
}

func TestOpenURLDoesNotEchoCredentialOnParseFailure(t *testing.T) {
	_, err := OpenURL(context.Background(), "audit", "redis://user:secret password@bad host", newRedisRuntime(t))
	if err == nil {
		t.Fatal("非法 Redis URL 必须失败")
	}
	if strings.Contains(err.Error(), "secret password") {
		t.Fatal("Redis URL 解析错误不能回显凭据")
	}
}

func newRedisRuntime(t *testing.T) *styxruntime.Runtime {
	t.Helper()
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity:     styxruntime.Identity{Name: "redis-test", Version: "test"},
		Dependencies: observe.NoopDependencies(),
	})
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}
	return runtime
}
