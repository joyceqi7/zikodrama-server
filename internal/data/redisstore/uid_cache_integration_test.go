package redisstore

import (
	"context"
	"fmt"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"drama-server/internal/data/idgen"

	"github.com/redis/go-redis/v9"
)

func newUIDCacheFixture(t *testing.T) (*UIDCache, *redis.Client, string) {
	t.Helper()
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("未配置 TEST_REDIS_ADDR，跳过独立 Redis 集成测试")
	}
	client := redis.NewClient(&redis.Options{Addr: address, DB: 15})
	nonce, err := catalogNonce()
	if err != nil {
		t.Fatal(err)
	}
	key := "test:uid:{" + nonce + "}"
	t.Cleanup(func() {
		if err := client.Del(context.Background(), key).Err(); err != nil {
			t.Errorf("清理本测试 UID 缓存失败：%v", err)
		}
		if err := client.Close(); err != nil {
			t.Errorf("关闭 Redis 客户端失败：%v", err)
		}
	})
	return newUIDOwnedCache(t, client), client, key
}

func newUIDOwnedCache(t *testing.T, client redis.UniversalClient) *UIDCache {
	t.Helper()
	cache := NewUIDCache(client)
	t.Cleanup(func() {
		if err := cache.Close(); err != nil {
			t.Errorf("关闭 UID 专用连接池失败：%v", err)
		}
	})
	return cache
}

func requireUIDNext(t *testing.T, cache *UIDCache, key, token string) idgen.Allocation {
	t.Helper()
	allocation, err := cache.Next(context.Background(), key, token, time.Second)
	if err != nil {
		t.Fatalf("读取 UID 缓存失败：%v", err)
	}
	return allocation
}

func requireUIDPublish(t *testing.T, cache *UIDCache, key, token string, segment idgen.Segment, want bool) {
	t.Helper()
	installed, err := cache.Publish(context.Background(), key, token, segment)
	if err != nil || installed != want {
		t.Fatalf("发布 UID 号段 installed=%v want=%v err=%v", installed, want, err)
	}
}

// TestUIDCachePrefetchAndSwitchIntegration 验证 80% 预取、备用段切换与重建实例续用进度。
func TestUIDCachePrefetchAndSwitchIntegration(t *testing.T) {
	cache, client, key := newUIDCacheFixture(t)
	first := idgen.Segment{Start: 9007199254740993, Size: 10}
	standby := idgen.Segment{Start: first.Start + uint64(first.Size), Size: 5}
	allocation := requireUIDNext(t, cache, key, "initial")
	if allocation.Available || !allocation.Refill {
		t.Fatalf("空缓存必须竞争初始化租约：%+v", allocation)
	}
	requireUIDPublish(t, cache, key, "initial", first, true)
	for offset := uint32(0); offset < 8; offset++ {
		allocation = requireUIDNext(t, cache, key, fmt.Sprintf("request-%d", offset))
		if !allocation.Available || allocation.Start != strconv.FormatUint(first.Start, 10) || allocation.Offset != offset || allocation.Refill != (offset == 7) {
			t.Fatalf("第 %d 次发号或 80%% 预取错误：%+v", offset+1, allocation)
		}
	}
	requireUIDPublish(t, cache, key, "request-7", standby, true)
	// 重建适配器代表应用发版，当前游标和已预取备用段仍属于共享缓存。
	cache = newUIDOwnedCache(t, client)
	for offset := uint32(8); offset < 10; offset++ {
		allocation = requireUIDNext(t, cache, key, fmt.Sprintf("restarted-%d", offset))
		if !allocation.Available || allocation.Offset != offset || allocation.Start != strconv.FormatUint(first.Start, 10) || allocation.Refill {
			t.Fatalf("应用重启必须接着使用当前段：%+v", allocation)
		}
	}
	allocation = requireUIDNext(t, cache, key, "switch")
	if !allocation.Available || allocation.Start != strconv.FormatUint(standby.Start, 10) || allocation.Offset != 0 || allocation.Refill {
		t.Fatalf("耗尽后必须原子切换共享备用段：%+v", allocation)
	}
	if ttl, err := client.TTL(context.Background(), key).Result(); err != nil || ttl != -1 {
		t.Fatalf("UID 游标不能设置 TTL：ttl=%v err=%v", ttl, err)
	}
}

// TestUIDCachePublishRetryNeverRewindsIntegration 验证重复发布与缓存删除后迟到发布不能回退游标。
func TestUIDCachePublishRetryNeverRewindsIntegration(t *testing.T) {
	cache, client, key := newUIDCacheFixture(t)
	segment := idgen.Segment{Start: 51, Size: 10}
	requireUIDNext(t, cache, key, "initial")
	until, err := client.HGet(context.Background(), key, "pending_until").Result()
	if err != nil {
		t.Fatal(err)
	}
	// 防御误用：即使调用方重复传入 token，也不能延长原租约。
	if allocation := requireUIDNext(t, cache, key, "initial"); allocation.Available || !allocation.Refill {
		t.Fatalf("相同请求的重试必须保留租约：%+v", allocation)
	}
	if actual := client.HGet(context.Background(), key, "pending_until").Val(); actual != until {
		t.Fatalf("请求重试延长了原租约：%s != %s", actual, until)
	}
	requireUIDPublish(t, cache, key, "initial", segment, true)
	if allocation := requireUIDNext(t, cache, key, "consume-first"); allocation.Offset != 0 || !allocation.Available {
		t.Fatalf("首号不正确：%+v", allocation)
	}
	// 防御误用：已成功发布的范围被重复提交时，也不能把 used 重新置零。
	requireUIDPublish(t, cache, key, "initial", segment, false)
	if allocation := requireUIDNext(t, cache, key, "consume-second"); allocation.Offset != 1 || !allocation.Available {
		t.Fatalf("发布重试回退了游标：%+v", allocation)
	}
	// 防御误用：重复传入 token 仍会消费新偏移，不得返回已经消费的位置。
	_ = requireUIDNext(t, cache, key, "lost-next-response")
	if allocation := requireUIDNext(t, cache, key, "lost-next-response"); allocation.Offset != 3 {
		t.Fatalf("发号请求重试回退了游标：%+v", allocation)
	}
	if err := client.Del(context.Background(), key).Err(); err != nil {
		t.Fatal(err)
	}
	requireUIDPublish(t, cache, key, "initial", segment, false)
	if count := client.Exists(context.Background(), key).Val(); count != 0 {
		t.Fatal("键删除后的发布重试重新创建了旧段")
	}
	allocation := requireUIDNext(t, cache, key, "fresh")
	if allocation.Available || !allocation.Refill {
		t.Fatalf("缓存丢弃后必须重新预留号段：%+v", allocation)
	}
	requireUIDPublish(t, cache, key, "initial", segment, false)
	requireUIDPublish(t, cache, key, "fresh", idgen.Segment{Start: 61, Size: 10}, true)
	if allocation := requireUIDNext(t, cache, key, "fresh-first"); allocation.Start != "61" || allocation.Offset != 0 {
		t.Fatalf("重建缓存复用了旧段：%+v", allocation)
	}
}

// TestUIDCacheLeaseTakeoverIntegration 验证过期租约可被接管且旧持有者不能发布。
func TestUIDCacheLeaseTakeoverIntegration(t *testing.T) {
	cache, client, key := newUIDCacheFixture(t)
	requireUIDNext(t, cache, key, "old-owner")
	if allocation := requireUIDNext(t, cache, key, "waiter"); allocation.Refill || allocation.Available || allocation.RetryAfter <= 0 {
		t.Fatalf("有效租约期间其他实例必须等待：%+v", allocation)
	}
	// 直接将服务端租约时间移到过去，避免依赖调度速度制造易抖动的时间测试。
	if err := client.HSet(context.Background(), key, "pending_until", 1).Err(); err != nil {
		t.Fatal(err)
	}
	requireUIDPublish(t, cache, key, "old-owner", idgen.Segment{Start: 1, Size: 10}, false)
	if allocation := requireUIDNext(t, cache, key, "old-owner"); allocation.Refill {
		t.Fatal("过期 token 不得重新复活同一个租约")
	}
	if allocation := requireUIDNext(t, cache, key, "new-owner"); !allocation.Refill {
		t.Fatalf("其他实例必须能接管过期租约：%+v", allocation)
	}
	requireUIDPublish(t, cache, key, "old-owner", idgen.Segment{Start: 1, Size: 10}, false)
	if err := cache.Backoff(context.Background(), key, "old-owner", time.Second); err != nil {
		t.Fatal(err)
	}
	requireUIDPublish(t, cache, key, "new-owner", idgen.Segment{Start: 11, Size: 10}, true)
	if allocation := requireUIDNext(t, cache, key, "issue"); allocation.Start != "11" || allocation.Offset != 0 {
		t.Fatalf("迟到持有者覆盖了新段：%+v", allocation)
	}
}

// TestUIDCacheBackoffKeepsCurrentSegmentIntegration 验证共享退避期间仍可消费当前段并在退避结束后补段。
func TestUIDCacheBackoffKeepsCurrentSegmentIntegration(t *testing.T) {
	cache, client, key := newUIDCacheFixture(t)
	requireUIDNext(t, cache, key, "initial")
	requireUIDPublish(t, cache, key, "initial", idgen.Segment{Start: 1, Size: 10}, true)
	for index := 0; index < 8; index++ {
		requireUIDNext(t, cache, key, fmt.Sprintf("prefetch-%d", index))
	}
	if err := cache.Backoff(context.Background(), key, "prefetch-7", time.Hour); err != nil {
		t.Fatal(err)
	}
	for index := 8; index < 10; index++ {
		allocation := requireUIDNext(t, cache, key, fmt.Sprintf("backoff-%d", index))
		if !allocation.Available || allocation.Offset != uint32(index) || allocation.Refill {
			t.Fatalf("预取失败不能中止剩余当前段或绕过退避：%+v", allocation)
		}
	}
	if allocation := requireUIDNext(t, cache, key, "empty-backoff"); allocation.Available || allocation.Refill || allocation.RetryAfter <= 0 {
		t.Fatalf("共享退避期间不得再次预留号段：%+v", allocation)
	}
	if err := client.HSet(context.Background(), key, "retry_at", 1).Err(); err != nil {
		t.Fatal(err)
	}
	if allocation := requireUIDNext(t, cache, key, "retry"); allocation.Available || !allocation.Refill {
		t.Fatalf("退避结束后必须允许重试：%+v", allocation)
	}
	// 当前段恰好耗尽时，预取结果可以直接成为当前段，不能再发旧段。
	requireUIDPublish(t, cache, key, "retry", idgen.Segment{Start: 11, Size: 10}, true)
	if allocation := requireUIDNext(t, cache, key, "after-retry"); allocation.Start != "11" || allocation.Offset != 0 {
		t.Fatalf("失败重试后没有切换到新段：%+v", allocation)
	}
}

// TestUIDCacheRejectsMalformedStateIntegration 验证残缺或损坏的缓存被拒绝而非重置发号。
func TestUIDCacheRejectsMalformedStateIntegration(t *testing.T) {
	for name, change := range map[string]func(context.Context, *redis.Client, string) error{
		"missing-used": func(ctx context.Context, client *redis.Client, key string) error {
			return client.HDel(ctx, key, "used").Err()
		},
		"invalid-schema": func(ctx context.Context, client *redis.Client, key string) error {
			return client.HSet(ctx, key, "schema", "other").Err()
		},
		"negative-used": func(ctx context.Context, client *redis.Client, key string) error {
			return client.HSet(ctx, key, "used", "-1").Err()
		},
		"fractional-used": func(ctx context.Context, client *redis.Client, key string) error {
			return client.HSet(ctx, key, "used", "0.5").Err()
		},
		"overflow-used": func(ctx context.Context, client *redis.Client, key string) error {
			return client.HSet(ctx, key, "used", "11").Err()
		},
		"overflow-size": func(ctx context.Context, client *redis.Client, key string) error {
			return client.HSet(ctx, key, "size", "10000001").Err()
		},
		"zero-start": func(ctx context.Context, client *redis.Client, key string) error {
			return client.HSet(ctx, key, "start", "0").Err()
		},
		"leading-zero-start": func(ctx context.Context, client *redis.Client, key string) error {
			return client.HSet(ctx, key, "start", "01").Err()
		},
		"overflow-range": func(ctx context.Context, client *redis.Client, key string) error {
			return client.HSet(ctx, key, "start", strconv.FormatInt(math.MaxInt64, 10)).Err()
		},
		"standby-without-size": func(ctx context.Context, client *redis.Client, key string) error {
			return client.HSet(ctx, key, "standby_start", "11").Err()
		},
		"pending-without-expiry": func(ctx context.Context, client *redis.Client, key string) error {
			return client.HSet(ctx, key, "pending_token", "missing-expiry").Err()
		},
	} {
		t.Run(name, func(t *testing.T) {
			cache, client, key := newUIDCacheFixture(t)
			ctx := context.Background()
			requireUIDNext(t, cache, key, "initial")
			requireUIDPublish(t, cache, key, "initial", idgen.Segment{Start: 1, Size: 10}, true)
			if err := change(ctx, client, key); err != nil {
				t.Fatal(err)
			}
			before, err := client.HGetAll(ctx, key).Result()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := cache.Next(ctx, key, "consumer", time.Second); err == nil || !strings.Contains(err.Error(), "invalid UID cache state") {
				t.Fatalf("损坏缓存必须拒绝发号：%v", err)
			}
			if _, err := cache.Publish(ctx, key, "initial", idgen.Segment{Start: 11, Size: 10}); err == nil {
				t.Fatal("损坏缓存不得安装段")
			}
			if err := cache.Backoff(ctx, key, "initial", time.Second); err == nil {
				t.Fatal("损坏缓存不得部分改写租约状态")
			}
			after, err := client.HGetAll(ctx, key).Result()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("损坏状态被悄悄修复或重置：before=%v after=%v err=%v", before, after, err)
			}
		})
	}
	t.Run("wrong-type", func(t *testing.T) {
		cache, client, key := newUIDCacheFixture(t)
		if err := client.Set(context.Background(), key, "wrong", 0).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := cache.Next(context.Background(), key, "token", time.Second); err == nil || !strings.Contains(err.Error(), "invalid UID cache type") {
			t.Fatalf("错误类型必须拒绝发号：%v", err)
		}
		if value := client.Get(context.Background(), key).Val(); value != "wrong" {
			t.Fatal("错误类型被覆盖")
		}
	})
}

// TestUIDCacheConcurrentOffsetsIntegration 验证独立连接池并发消费不重复且保留大整数精度。
func TestUIDCacheConcurrentOffsetsIntegration(t *testing.T) {
	cache, client, key := newUIDCacheFixture(t)
	requireUIDNext(t, cache, key, "initial")
	requireUIDPublish(t, cache, key, "initial", idgen.Segment{Start: math.MaxInt64 - 999, Size: 1000}, true)
	var workers sync.WaitGroup
	results := make(chan idgen.Allocation, 1000)
	errors := make(chan error, 10)
	for worker := 0; worker < 10; worker++ {
		workerCache := newUIDOwnedCache(t, client)
		workers.Go(func() {
			for index := 0; index < 100; index++ {
				allocation, err := workerCache.Next(context.Background(), key, fmt.Sprintf("worker-%d-%d", worker, index), time.Second)
				if err != nil {
					errors <- err
					return
				}
				results <- allocation
			}
		})
	}
	workers.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Errorf("并发发号失败：%v", err)
	}
	seen := make(map[uint32]bool)
	owners := 0
	for allocation := range results {
		if !allocation.Available || seen[allocation.Offset] || allocation.Start != strconv.FormatUint(math.MaxInt64-999, 10) {
			t.Fatalf("并发游标重复、越界或大 UID 精度丢失：%+v", allocation)
		}
		seen[allocation.Offset] = true
		if allocation.Refill {
			owners++
		}
	}
	if len(seen) != 1000 || owners != 1 {
		t.Fatalf("并发消费数量或共享预取所有者异常：issued=%d owners=%d", len(seen), owners)
	}
}
