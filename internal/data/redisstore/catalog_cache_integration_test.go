package redisstore

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestCatalogCacheRedisIntegration 验证删除、过期、跨实例与防止旧请求回填，绝不操作业务 Redis。
func TestCatalogCacheRedisIntegration(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: address, DB: 14})
	defer client.Close()
	cache := NewCatalogCache(client)
	other := NewCatalogCache(client)
	if err := cache.Invalidate(ctx); err != nil {
		t.Fatal(err)
	}
	defer cache.Invalidate(ctx)
	field := CatalogField("list", []string{"5001", "page-1"})
	version, value, err := cache.Read(ctx, field)
	if err != nil || len(value) != 0 {
		t.Fatalf("read miss: %v", err)
	}
	if err := cache.Fill(ctx, version, field, []byte(`{"items":[]}`)); err != nil {
		t.Fatal(err)
	}
	_, value, err = other.Read(ctx, field)
	if err != nil || len(value) == 0 {
		t.Fatalf("cross-instance hit: %v", err)
	}
	ttl, err := client.TTL(ctx, catalogEntriesKey).Result()
	if err != nil || ttl <= 0 || ttl > catalogTTL {
		t.Fatalf("TTL: %v %v", ttl, err)
	}
	if err := other.Invalidate(ctx); err != nil {
		t.Fatal(err)
	}
	if n := client.Exists(ctx, catalogEntriesKey).Val(); n != 0 {
		t.Fatal("invalidation did not delete cache")
	}
	if err := cache.Fill(ctx, version, field, []byte(`{"stale":true}`)); err != nil {
		t.Fatal(err)
	}
	_, value, err = cache.Read(ctx, field)
	if err != nil || len(value) != 0 {
		t.Fatal("old reader resurrected stale cache")
	}
	// 内容缓存清空或版本键丢失后，新随机版本也不能接受之前的回填。
	client.Del(ctx, catalogVersionKey)
	fresh, _, err := cache.Read(ctx, field)
	if err != nil || fresh == version {
		t.Fatal("version reuse after reset")
	}
	if err := cache.Fill(ctx, fresh, field, []byte(`{"fresh":true}`)); err != nil {
		t.Fatal(err)
	}
	client.PExpire(ctx, catalogEntriesKey, time.Millisecond)
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		_, value, err = cache.Read(ctx, field)
		if err != nil {
			t.Fatal(err)
		}
		if len(value) == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("expired content remained cached")
}

// TestResultBucketsRedisIntegration 验证各分区 TTL、统一失效及旧版本回填保护。
func TestResultBucketsRedisIntegration(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("未配置独立测试 Redis")
	}
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: address, DB: 11})
	defer client.Close()
	cache := NewCatalogCache(client)
	if err := cache.Invalidate(ctx); err != nil {
		t.Fatal(err)
	}
	defer cache.Invalidate(ctx)
	for _, item := range []struct {
		kind string
		max  time.Duration
	}{{"list", time.Minute}, {"hot", time.Minute}, {"carousel", 5 * time.Minute}} {
		bucket := cache.Bucket(item.kind)
		version, _, err := bucket.Read(ctx, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		if err := bucket.Fill(ctx, version, "fixture", []byte(`[]`)); err != nil {
			t.Fatal(err)
		}
		ttl := client.PTTL(ctx, bucket.entries).Val()
		if ttl <= 0 || ttl > item.max {
			t.Fatalf("%s TTL 错误：%v", item.kind, ttl)
		}
		// 失效来自原有内容写入分区，必须同时清理推荐与分页。
		if err := cache.Invalidate(ctx); err != nil {
			t.Fatal(err)
		}
		if err := bucket.Fill(ctx, version, "fixture", []byte(`[]`)); err != nil {
			t.Fatal(err)
		}
		_, value, err := bucket.Read(ctx, "fixture")
		if err != nil || len(value) > 0 {
			t.Fatal("失效后旧查询复活了结果", err)
		}
	}
}
