package redisstore

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"drama-server/internal/data/idgen"

	"github.com/redis/go-redis/v9"
)

type uidResponseFault struct {
	dropNext atomic.Bool
	stall    atomic.Bool
	commands atomic.Int64
	dropped  atomic.Int64
}

type uidFaultConn struct {
	// Conn 保留真实连接，仅拦截测试指定的脚本响应。
	net.Conn
	fault *uidResponseFault
	drop  atomic.Bool
}

// Write 统计实际发出的脚本并标记待丢弃的响应。
func (c *uidFaultConn) Write(value []byte) (int, error) {
	command := bytes.ToLower(value)
	if bytes.Contains(command, []byte("\r\nevalsha\r\n")) || bytes.Contains(command, []byte("\r\neval\r\n")) {
		c.fault.commands.Add(1)
		if c.fault.dropNext.Swap(false) {
			c.drop.Store(true)
		}
	}
	return c.Conn.Write(value)
}

// Read 在服务端执行完成后模拟丢响应或阻塞读取，不改写服务端状态。
func (c *uidFaultConn) Read(value []byte) (int, error) {
	count, err := c.Conn.Read(value)
	if count > 0 && c.drop.Swap(false) {
		// 真实服务端已经完成脚本并发送响应，此处丢弃响应模拟结果未知的断链。
		c.fault.dropped.Add(1)
		if c.fault.stall.Load() {
			// 继续等待服务端不会再次发送的响应，真实 socket 应受调用上下文期限约束。
			return c.Conn.Read(value)
		}
		return 0, io.ErrUnexpectedEOF
	}
	return count, err
}

func newUIDFaultCache(t *testing.T, shared *redis.Client) (*UIDCache, *uidResponseFault) {
	t.Helper()
	fault := &uidResponseFault{}
	options := *shared.Options()
	options.MaxRetries = 3
	options.PoolSize = 1
	options.Dialer = func(ctx context.Context, network, address string) (net.Conn, error) {
		connection, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &uidFaultConn{Conn: connection, fault: fault}, nil
	}
	source := redis.NewClient(&options)
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Errorf("关闭故障注入来源客户端失败：%v", err)
		}
	})
	return newUIDOwnedCache(t, source), fault
}

// TestUIDCacheLostResponsesAreNotRetriedIntegration 验证写入成功但响应丢失时不自动重发命令。
func TestUIDCacheLostResponsesAreNotRetriedIntegration(t *testing.T) {
	for _, operation := range []string{"next", "publish", "backoff"} {
		t.Run(operation, func(t *testing.T) {
			cache, shared, key := newUIDCacheFixture(t)
			faultCache, fault := newUIDFaultCache(t, shared)
			ctx := context.Background()
			// 预先载入脚本，故障必须发生在已执行写操作后，而不是 NOSCRIPT 响应上。
			for _, script := range []*redis.Script{nextUID, publishUID, backoffUID} {
				if err := script.Load(ctx, shared).Err(); err != nil {
					t.Fatal(err)
				}
			}
			requireUIDNext(t, cache, key, "initial")
			if operation == "next" {
				requireUIDPublish(t, cache, key, "initial", idgen.Segment{Start: 1, Size: 10}, true)
			}
			fault.dropNext.Store(true)
			var err error
			switch operation {
			case "next":
				_, err = faultCache.Next(ctx, key, "lost-next", time.Second)
			case "publish":
				_, err = faultCache.Publish(ctx, key, "initial", idgen.Segment{Start: 1, Size: 10})
			case "backoff":
				err = faultCache.Backoff(ctx, key, "initial", time.Minute)
			}
			if err == nil || fault.dropped.Load() != 1 || fault.commands.Load() != 1 {
				t.Fatalf("响应丢失必须返回错误且只发出一次脚本：err=%v dropped=%d commands=%d", err, fault.dropped.Load(), fault.commands.Load())
			}
			switch operation {
			case "next":
				allocation := requireUIDNext(t, cache, key, "new-request")
				if !allocation.Available || allocation.Start != "1" || allocation.Offset != 1 {
					t.Fatalf("Next 响应丢失只能跳过已消费位置：%+v", allocation)
				}
			case "publish":
				// 发布结果未知后仅使用新请求取号，不能把同一范围重新送入 Publish。
				allocation := requireUIDNext(t, cache, key, "new-request")
				if !allocation.Available || allocation.Start != "1" || allocation.Offset != 0 {
					t.Fatalf("真实已发布号段未保留：%+v", allocation)
				}
			case "backoff":
				allocation := requireUIDNext(t, cache, key, "new-request")
				if allocation.Refill || allocation.Available || allocation.RetryAfter <= 0 {
					t.Fatalf("真实已执行退避未保留：%+v", allocation)
				}
			}
		})
	}
}

// TestUIDCacheOldLeaseCannotPublishOldRangeIntegration 复现旧租约复活并验证旧范围仍不能重新安装。
func TestUIDCacheOldLeaseCannotPublishOldRangeIntegration(t *testing.T) {
	cache, _, key := newUIDCacheFixture(t)
	requireUIDNext(t, cache, key, "old-token")
	segment := idgen.Segment{Start: 1, Size: 2}
	requireUIDPublish(t, cache, key, "old-token", segment, true)
	if allocation := requireUIDNext(t, cache, key, "first"); !allocation.Available || allocation.Offset != 0 {
		t.Fatalf("第一个 UID 错误：%+v", allocation)
	}
	// 即使调用方错误复用旧 token，旧范围也必须被段起点单调性校验拒绝。
	if allocation := requireUIDNext(t, cache, key, "old-token"); !allocation.Refill || !allocation.Available || allocation.Offset != 1 {
		t.Fatalf("复现旧 token 重新获租约失败：%+v", allocation)
	}
	requireUIDPublish(t, cache, key, "old-token", segment, false)
	if allocation := requireUIDNext(t, cache, key, "third"); allocation.Available {
		t.Fatalf("旧范围重新安装并重复发号：%+v", allocation)
	}
}

// TestUIDCacheCloseLeavesSharedClientUsableIntegration 验证关闭 UID 专用池不影响共享客户端。
func TestUIDCacheCloseLeavesSharedClientUsableIntegration(t *testing.T) {
	cache, shared, _ := newUIDCacheFixture(t)
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if err := shared.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("关闭 UID 缓存影响了共享 Redis 客户端：%v", err)
	}
}

// TestUIDCacheIOHonorsContextDeadlineIntegration 验证真实 socket 读超时服从调用期限且消费进度不回退。
func TestUIDCacheIOHonorsContextDeadlineIntegration(t *testing.T) {
	cache, shared, key := newUIDCacheFixture(t)
	if err := nextUID.Load(context.Background(), shared).Err(); err != nil {
		t.Fatal(err)
	}
	requireUIDNext(t, cache, key, "initial")
	requireUIDPublish(t, cache, key, "initial", idgen.Segment{Start: 1, Size: 10}, true)
	faultCache, fault := newUIDFaultCache(t, shared)
	fault.stall.Store(true)
	fault.dropNext.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := faultCache.Next(ctx, key, "delayed-response", time.Second)
	elapsed := time.Since(started)
	if err == nil || elapsed > time.Second || fault.commands.Load() != 1 || fault.dropped.Load() != 1 {
		t.Fatalf("UID socket 未遵守短上下文期限或自动重发了请求：elapsed=%v err=%v commands=%d dropped=%d", elapsed, err, fault.commands.Load(), fault.dropped.Load())
	}
	if allocation := requireUIDNext(t, cache, key, "new-request"); !allocation.Available || allocation.Offset != 1 {
		t.Fatalf("读超时后已消费位置被回退：%+v", allocation)
	}
}
