// Package metricrefresh 定义统计刷新使用的可丢弃互斥端口；最终发布资格由 MySQL 版本校验决定。
package metricrefresh

import (
	"context"
	"time"
)

const (
	// LeaseDuration 限制失联刷新者占用协调状态的时间，持有者必须定期续期。
	LeaseDuration = 30 * time.Second
	// RenewInterval 给网络和数据库短暂抖动保留续期余量。
	RenewInterval = 10 * time.Second
)

// Locker 减少跨实例重复计算，不承担汇总事实或发布版本的权威存储。
type Locker interface {
	// Acquire 原子申请指定应用的临时锁；owner 每次申请都必须重新生成。
	Acquire(context.Context, string, string, time.Duration) (bool, error)
	// Renew 仅延长仍属于 owner 的锁，锁已失效时返回 false。
	Renew(context.Context, string, string, time.Duration) (bool, error)
	// Release 仅删除仍属于 owner 的锁，迟到释放不得影响后来者。
	Release(context.Context, string, string) error
}
