package data

import "drama-server/internal/pkg/ratelimit"

// NewRequestRateLimiter 提供有界的实例级保护，不增加 Redis 或其他存储依赖。
func NewRequestRateLimiter() ratelimit.Limiter { return ratelimit.NewMemory(50_000) }
