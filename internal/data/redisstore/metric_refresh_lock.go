package redisstore

import (
	"context"
	"time"

	"drama-server/internal/data/metricrefresh"
	"github.com/redis/go-redis/v9"
)

// metricRefreshLock 复用缓存连接，Redis 不可用时由调用方使用 MySQL 短租约协调。
type metricRefreshLock struct{ client redis.UniversalClient }

// NewMetricRefreshLock 创建统计刷新锁；未初始化 Redis 时不制造一个不可用的假锁。
func NewMetricRefreshLock(client redis.UniversalClient) metricrefresh.Locker {
	if client == nil {
		return nil
	}
	return &metricRefreshLock{client: client}
}

var acquireMetricRefresh = redis.NewScript(`
if redis.call('SET', KEYS[1], ARGV[1], 'NX', 'PX', ARGV[2]) then return 1 end
return 0
`)
var renewMetricRefresh = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
return redis.call('PEXPIRE', KEYS[1], ARGV[2])
`)
var releaseMetricRefresh = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
return redis.call('DEL', KEYS[1])
`)

// Acquire 以随机持有者和有限 TTL 原子申请，不等待其他刷新者完成。
func (l *metricRefreshLock) Acquire(ctx context.Context, key, owner string, ttl time.Duration) (bool, error) {
	result, err := acquireMetricRefresh.Run(ctx, l.client, []string{key}, owner, ttl.Milliseconds()).Int()
	return result == 1, err
}

// Renew 使用同一脚本核对持有者并续期，避免续到已经被别人接管的锁。
func (l *metricRefreshLock) Renew(ctx context.Context, key, owner string, ttl time.Duration) (bool, error) {
	result, err := renewMetricRefresh.Run(ctx, l.client, []string{key}, owner, ttl.Milliseconds()).Int()
	return result == 1, err
}

// Release 不直接 DEL，防止旧任务删除新任务的锁。
func (l *metricRefreshLock) Release(ctx context.Context, key, owner string) error {
	return releaseMetricRefresh.Run(ctx, l.client, []string{key}, owner).Err()
}
