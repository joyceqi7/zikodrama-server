package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMemoryEnforcesConcurrentLimit 验证并发计数及不同 scope 的隔离。
func TestMemoryEnforcesConcurrentLimit(t *testing.T) {
	limiter := NewMemory(100)
	var allowed atomic.Int32
	var group sync.WaitGroup
	for range 100 {
		group.Go(func() {
			result, err := limiter.Allow(context.Background(), "login", "subject", 10, time.Minute)
			if err != nil {
				t.Error(err)
			}
			if result.Allowed {
				allowed.Add(1)
			}
		})
	}
	group.Wait()
	if allowed.Load() != 10 {
		t.Fatalf("allowed=%d want=10", allowed.Load())
	}
	other, err := limiter.Allow(context.Background(), "bootstrap", "subject", 1, time.Minute)
	if err != nil || !other.Allowed {
		t.Fatalf("不同范围不可共用额度：%+v %v", other, err)
	}
}

// TestMemoryExpiresWithoutEvictingLiveCounters 验证内存上限不能通过驱逐有效计数绕过。
func TestMemoryExpiresWithoutEvictingLiveCounters(t *testing.T) {
	limiter := NewMemory(1)
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	first, _ := limiter.Allow(context.Background(), "login", "one", 1, time.Minute)
	full, _ := limiter.Allow(context.Background(), "login", "two", 1, time.Minute)
	repeat, _ := limiter.Allow(context.Background(), "login", "one", 1, time.Minute)
	if !first.Allowed || full.Allowed || repeat.Allowed {
		t.Fatal("有效窗口被驱逐或配额失效")
	}
	now = now.Add(time.Minute)
	expired, _ := limiter.Allow(context.Background(), "login", "two", 1, time.Minute)
	if !expired.Allowed || len(limiter.windows) != 1 {
		t.Fatal("过期窗口未释放")
	}
}
