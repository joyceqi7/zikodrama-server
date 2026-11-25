package ratelimit

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"time"
)

// Memory 是单实例的轻量固定窗口保护，不保存业务状态，不承担跨实例全局配额。
// 总入口限流由网关配置；进程重启可以丢弃这些短时计数。
type Memory struct {
	// mu 保证并发计数不会丢失。
	mu sync.Mutex
	// windows 只以摘要作为键，不保留 IP、账号或 Token 原文。
	windows map[[32]byte]memoryWindow
	// capacity 限制内存占用，满载时不驱逐仍有效的窗口来放宽限额。
	capacity int
	// nextSweep 限制过期清扫频率，避免大量新主体造成反复全表扫描。
	nextSweep time.Time
	// now 便于确定性测试窗口到期。
	now func() time.Time
}

// memoryWindow 保存一个主体在当前窗口中的计数。
type memoryWindow struct {
	// count 是已占用请求数，不超过当前上限。
	count int
	// expiresAt 是窗口到期时间。
	expiresAt time.Time
}

// NewMemory 创建容量有界的应用内限流器。
func NewMemory(capacity int) *Memory {
	if capacity <= 0 {
		capacity = 50_000
	}
	return &Memory{windows: make(map[[32]byte]memoryWindow), capacity: capacity, now: time.Now}
}

// Allow 在本实例内原子判断额度；不把限流结果用于奖励、权限或发布状态判定。
func (limiter *Memory) Allow(ctx context.Context, scope, subject string, limit int, window time.Duration) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	if limiter == nil || strings.TrimSpace(scope) == "" || strings.TrimSpace(subject) == "" || limit <= 0 || window <= 0 {
		return Decision{}, errors.New("invalid rate limit policy")
	}
	key := sha256.Sum256([]byte(scope + "\x00" + subject))
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.now()
	if !limiter.nextSweep.After(now) {
		for key, item := range limiter.windows {
			if !item.expiresAt.After(now) {
				delete(limiter.windows, key)
			}
		}
		limiter.nextSweep = now.Add(time.Second)
	}
	item, exists := limiter.windows[key]
	if !exists || !item.expiresAt.After(now) {
		if !exists && len(limiter.windows) >= limiter.capacity {
			return Decision{Allowed: false, RetryAfter: time.Second}, nil
		}
		item = memoryWindow{expiresAt: now.Add(window)}
	}
	allowed := item.count < limit
	if allowed {
		item.count++
	}
	limiter.windows[key] = item
	return Decision{Allowed: allowed, RetryAfter: item.expiresAt.Sub(now)}, nil
}

// Ping 保留限流端口的可用性检查语义，不访问任何外部服务。
func (limiter *Memory) Ping(ctx context.Context) error {
	if limiter == nil {
		return errors.New("rate limiter is unavailable")
	}
	return ctx.Err()
}
