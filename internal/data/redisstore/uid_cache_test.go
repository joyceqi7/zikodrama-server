package redisstore

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"drama-server/internal/data/idgen"

	"github.com/redis/go-redis/v9"
)

// TestUIDCacheUnavailable 验证未配置缓存时所有发号操作明确失败。
func TestUIDCacheUnavailable(t *testing.T) {
	for _, cache := range []*UIDCache{nil, NewUIDCache(nil)} {
		if _, err := cache.Next(context.Background(), "key", "token", time.Second); err == nil || !strings.Contains(err.Error(), "unavailable") {
			t.Fatalf("缺少 Redis 时 Next 必须明确失败，err=%v", err)
		}
		if _, err := cache.Publish(context.Background(), "key", "token", idgen.Segment{Start: 1, Size: 10}); err == nil {
			t.Fatal("缺少 Redis 时 Publish 不得成功")
		}
		if err := cache.Backoff(context.Background(), "key", "token", time.Second); err == nil {
			t.Fatal("缺少 Redis 时 Backoff 不得成功")
		}
	}
}

// TestUIDCacheRejectsInvalidArguments 验证非法租约与号段在访问 Redis 前被拒绝。
func TestUIDCacheRejectsInvalidArguments(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewUIDCache(client)
	t.Cleanup(func() { _ = cache.Close() })
	for _, input := range []struct {
		key   string
		token string
		lease time.Duration
	}{
		{"", "token", time.Second}, {"key", "", time.Second},
		{"key", "token", 0}, {"key", "token", time.Microsecond},
		{"key", "token", uidMaxDelay + time.Second},
	} {
		if _, err := cache.Next(context.Background(), input.key, input.token, input.lease); err == nil || !strings.Contains(err.Error(), "invalid UID") {
			t.Fatalf("无效参数必须在连接 Redis 前失败，err=%v", err)
		}
	}
	for _, segment := range []idgen.Segment{
		{Start: 0, Size: 10}, {Start: 1, Size: 0}, {Start: 1, Size: uidMaxSegmentSize + 1},
		{Start: math.MaxUint64, Size: 1}, {Start: math.MaxInt64, Size: 2},
	} {
		if _, err := cache.Publish(context.Background(), "key", "token", segment); err == nil || !strings.Contains(err.Error(), "invalid UID segment") {
			t.Fatalf("无效号段必须在连接 Redis 前失败，segment=%+v err=%v", segment, err)
		}
	}
	for _, delay := range []time.Duration{-time.Second, uidMaxDelay + time.Second} {
		if err := cache.Backoff(context.Background(), "key", "token", delay); err == nil || !strings.Contains(err.Error(), "invalid UID refill backoff") {
			t.Fatalf("无效退避必须在连接 Redis 前失败，err=%v", err)
		}
	}
}

// TestUIDCacheOwnsClientWithoutChangingSharedOptions 验证专用池禁止重试且不修改共享客户端配置。
func TestUIDCacheOwnsClientWithoutChangingSharedOptions(t *testing.T) {
	shared := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: 7, ReadTimeout: -2, WriteTimeout: -2})
	t.Cleanup(func() { _ = shared.Close() })
	cache := NewUIDCache(shared)
	t.Cleanup(func() { _ = cache.Close() })
	owned, ok := cache.client.(*redis.Client)
	if !ok || owned == shared {
		t.Fatal("UID 必须持有独立客户端")
	}
	if shared.Options().MaxRetries != 7 || shared.Options().ContextTimeoutEnabled || shared.Options().ReadTimeout != -1 || shared.Options().WriteTimeout != -1 {
		t.Fatal("创建 UID 客户端修改了共享配置")
	}
	if owned.Options().MaxRetries != 0 || !owned.Options().ContextTimeoutEnabled || owned.Options().ReadTimeout <= 0 || owned.Options().WriteTimeout <= 0 {
		t.Fatal("UID 专用池必须禁用命令重发，并始终允许上下文设置读写期限")
	}
	if owned.Options().MaintNotificationsConfig == shared.Options().MaintNotificationsConfig {
		t.Fatal("UID 专用池不应共享会在连接初始化中变更的通知配置")
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("重复关闭缓存应幂等：%v", err)
	}
}

// TestUIDCacheOwnsClusterWithoutRetries 验证集群节点与路由两层均关闭网络重试。
func TestUIDCacheOwnsClusterWithoutRetries(t *testing.T) {
	shared := redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{"127.0.0.1:1"}, MaxRetries: 7, MaxRedirects: 5})
	t.Cleanup(func() { _ = shared.Close() })
	cache := NewUIDCache(shared)
	t.Cleanup(func() { _ = cache.Close() })
	owned, ok := cache.client.(*redis.ClusterClient)
	if !ok || owned == shared {
		t.Fatal("UID 必须持有独立集群客户端")
	}
	if shared.Options().MaxRetries != 7 || shared.Options().MaxRedirects != 5 || shared.Options().ContextTimeoutEnabled {
		t.Fatal("创建 UID 集群客户端修改了共享配置")
	}
	if owned.Options().MaxRetries != -1 || owned.Options().MaxRedirects != 0 || !owned.Options().ContextTimeoutEnabled {
		t.Fatal("UID 集群必须同时禁用节点重试和外层网络重试")
	}
	owned.Options().Addrs[0] = "127.0.0.1:2"
	if shared.Options().Addrs[0] != "127.0.0.1:1" {
		t.Fatal("集群地址切片仍与共享客户端共用")
	}
}
