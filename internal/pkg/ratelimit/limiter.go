// Package ratelimit 定义跨实例请求限流的最小业务无关合同。
package ratelimit

import (
	"context"
	"time"
)

// Decision 描述当前请求是否仍在指定窗口的额度内。
type Decision struct {
	// Allowed 表示请求是否可以继续进入业务处理。
	Allowed bool
	// RetryAfter 表示额度耗尽后距离窗口释放的大致时间。
	RetryAfter time.Duration
}

// Limiter 只保存可丢弃的短期计数，不得用于权益、发布状态或 RBAC 判定。
type Limiter interface {
	// Allow 在跨实例共享窗口中原子增加计数并返回限流结果。
	Allow(context.Context, string, string, int, time.Duration) (Decision, error)
	// Ping 验证限流依赖是否可用，供就绪检查使用。
	Ping(context.Context) error
}
