// Package idgen 定义 MySQL 预留号段与共享发号缓存之间的协议。
package idgen

import (
	"context"
	"time"
)

// Segment 是 MySQL 已提交且永久划出的左闭右开号段，不能回收复用。
type Segment struct {
	// Start 是号段第一个 UID；跨 Redis Lua 边界时必须使用十进制字符串。
	Start uint64
	// Size 是号段容量，只对这个小整数进行 Lua 数值运算。
	Size uint32
}

// Allocation 保存一次原子发号及预取租约竞争的结果。
type Allocation struct {
	// Start 是当前号段起点的十进制字符串，避免 Lua 大整数精度损失。
	Start string
	// Offset 是本次已消费位置，从零开始。
	Offset uint32
	// Available 表示本次已消费一个 UID；响应丢失允许跳号，不得回退游标。
	Available bool
	// Refill 表示调用方取得了与请求 token 对应的补段租约。
	Refill bool
	// RetryAfter 是无可用 UID 时的重试参考间隔，等待必须受调用上下文约束。
	RetryAfter time.Duration
}

// Cache 保存可整体丢弃的当前段、备用段及补段状态；MySQL 始终是号段权威。
type Cache interface {
	// Next 原子消费一个 UID，必要时切换备用段，并在消耗到 80% 后竞争补段租约。
	// key 隔离数据库及业务命名空间；token 必须是不可复用的随机值，lease 限制持有时间。
	// 命令结果不确定时必须返回错误，禁止网络重发，以免迟到请求重新激活旧租约。
	Next(ctx context.Context, key, token string, lease time.Duration) (Allocation, error)
	// Publish 仅当 key 中尚存同一个有效租约时安装已提交号段；失去租约返回 false。
	// 结果不确定时丢弃该次结果且不重发旧段；重复发布或 key 被删除后不得重建旧游标。
	Publish(ctx context.Context, key, token string, segment Segment) (bool, error)
	// Backoff 仅对相同 token 的补段失败设置短暂退避，保留尚可消费的号段。
	Backoff(ctx context.Context, key, token string, delay time.Duration) error
}
