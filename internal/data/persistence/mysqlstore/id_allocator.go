package mysqlstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/plugin/dbresolver"

	"drama-server/internal/data/idgen"
)

const (
	consumerIDNamespace = "consumer_domain"
	maxIDSegmentSize    = 10_000_000
	idRefillTimeout     = 5 * time.Second
	idRefillLease       = 15 * time.Second
	idRefillBackoff     = time.Second
	idCacheTimeout      = time.Second
	idWaitInterval      = 25 * time.Millisecond
)

// idAllocatorModel 保存业务命名空间的永久预留水位，缓存丢失也不回退。
type idAllocatorModel struct {
	// Namespace 隔离不同用途的全局号段。
	Namespace string `gorm:"column:namespace;primaryKey"`
	// NextID 是下一次尚未预留的 UID，不是 Redis 中的逐个发号游标。
	NextID uint64 `gorm:"column:next_id"`
	// SegmentSize 是每次事务预留的容量。
	SegmentSize uint32 `gorm:"column:segment_size"`
	// UpdatedAt 记录最近一次成功预留的数据库时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 返回号段分配器的固定表名。
func (idAllocatorModel) TableName() string { return "id_allocators" }

// segmentIDAllocator 用 MySQL 永久预留号段，所有进程通过同一个 Redis 游标发号。
// 本地仅保存缓存命名空间，不保存当前段或备用段；实例重启不丢弃共享余量。
type segmentIDAllocator struct {
	data         *Data
	namespace    string
	cache        idgen.Cache
	scope        atomic.Pointer[string]
	scopeMu      sync.Mutex
	scopeLoading chan struct{}
}

// newSegmentIDAllocator 创建惰性分配器；应用启动不申请号段或清理共享缓存。
func newSegmentIDAllocator(data *Data, namespace string, cache idgen.Cache) *segmentIDAllocator {
	return &segmentIDAllocator{data: data, namespace: namespace, cache: cache}
}

// NextID 原子消费共享号段并在 80% 时异步预取；缓存不可用时返回错误，不启用本地降级。
// 唯一性以 Redis 游标不回滚为前提，故障恢复必须先丢弃旧缓存再开放发号。
func (a *segmentIDAllocator) NextID(ctx context.Context) (uint64, error) {
	if a == nil || a.cache == nil {
		return 0, fmt.Errorf("allocate consumer ID: shared Redis cache is not configured")
	}
	key, err := a.cacheKey(ctx)
	if err != nil {
		return 0, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		// 每轮缓存调用使用新租约值，结果不确定时不重发本轮操作。
		// 本轮补段若已超时，下轮不得把失效 token 再次变成有效租约。
		token := rand.Text()
		cacheCtx, cancel := context.WithTimeout(ctx, idCacheTimeout)
		allocation, err := a.cache.Next(cacheCtx, key, token, idRefillLease)
		cancel()
		if err != nil {
			return 0, fmt.Errorf("consume shared consumer ID segment: %w", err)
		}
		if allocation.Available {
			start, err := strconv.ParseUint(allocation.Start, 10, 64)
			if err != nil || start == 0 || start > math.MaxInt64 || uint64(allocation.Offset) > math.MaxInt64-start {
				return 0, fmt.Errorf("consume shared consumer ID segment: invalid cached ID range")
			}
			if allocation.Refill {
				// 保留诊断上下文，但请求结束不能取消已经取得租约的后台预取。
				go func() { _ = a.refill(context.WithoutCancel(ctx), key, token) }()
			}
			return start + uint64(allocation.Offset), nil
		}
		if allocation.Refill {
			if err := a.refill(ctx, key, token); err != nil {
				return 0, err
			}
			continue
		}
		// 等待其他实例补段或退避结束；短轮询使租约持有者成功后请求可以及时续发。
		wait := min(allocation.RetryAfter, idWaitInterval)
		if wait <= 0 {
			wait = idWaitInterval
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, ctx.Err()
		case <-timer.C:
		}
	}
}

// cacheKey 用主库实例、数据库名和业务用途隔离共享 Redis 中的不同发号器。
// 同一数据库的应用发布保持 key 稳定；查询失败不会缓存结果，之后允许重试。
func (a *segmentIDAllocator) cacheKey(ctx context.Context) (string, error) {
	for {
		if scope := a.scope.Load(); scope != nil {
			return *scope, nil
		}
		a.scopeMu.Lock()
		if scope := a.scope.Load(); scope != nil {
			a.scopeMu.Unlock()
			return *scope, nil
		}
		if loading := a.scopeLoading; loading != nil {
			a.scopeMu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-loading:
				continue
			}
		}
		a.scopeLoading = make(chan struct{})
		a.scopeMu.Unlock()
		key, err := a.resolveCacheKey(ctx)
		a.scopeMu.Lock()
		if err == nil {
			a.scope.Store(&key)
		}
		close(a.scopeLoading)
		a.scopeLoading = nil
		a.scopeMu.Unlock()
		return key, err
	}
}

// resolveCacheKey 查询主库身份；首次加载单独限时，其他等待者可以自行取消。
func (a *segmentIDAllocator) resolveCacheKey(parent context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(parent, idRefillTimeout)
	defer cancel()
	if a.data == nil || a.data.DB() == nil {
		return "", fmt.Errorf("resolve consumer ID cache scope: mysql store is not configured")
	}
	var identity struct {
		// DatabaseName 是实际连接所选数据库，不使用可能包含凭据的 DSN。
		DatabaseName string
		// ServerUUID 区分共用 Redis 且数据库同名的不同 MySQL 主实例。
		ServerUUID string
	}
	if err := a.data.DB().WithContext(ctx).Clauses(dbresolver.Write).
		Table("id_allocators").Select("DATABASE() AS database_name, @@server_uuid AS server_uuid").
		Where("namespace = ?", a.namespace).Take(&identity).Error; err != nil {
		return "", fmt.Errorf("resolve consumer ID cache scope: %w", err)
	}
	if identity.DatabaseName == "" || identity.ServerUUID == "" {
		return "", fmt.Errorf("resolve consumer ID cache scope: empty database identity")
	}
	digest := sha256.Sum256([]byte(identity.DatabaseName + "\x00" + identity.ServerUUID + "\x00" + a.namespace))
	key := fmt.Sprintf("drama:uid:{%x}", digest)
	return key, nil
}

// refill 提交权威号段后再按租约发布；发布失败或租约丢失都丢弃该段，永不回收。
// 当前段仍可消费时，所有失败仅影响后台补段；日志保留实际数据库或缓存错误。
func (a *segmentIDAllocator) refill(parent context.Context, key, token string) error {
	ctx, cancel := context.WithTimeout(parent, idRefillTimeout)
	defer cancel()
	segment, err := a.reserveSegment(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "reserve consumer ID segment failed", "error", err)
		a.backoff(parent, key, token)
		return err
	}
	accepted, err := a.cache.Publish(ctx, key, token, segment)
	if err != nil {
		slog.ErrorContext(ctx, "publish consumer ID segment failed", "error", err)
		a.backoff(parent, key, token)
		return fmt.Errorf("publish consumer ID segment: %w", err)
	}
	if !accepted {
		// 这是并发接管或缓存整体丢失，不得安装到新的发号状态中。
		slog.WarnContext(ctx, "discard reserved consumer ID segment after refill lease loss")
	}
	return nil
}

// backoff 对仍由本次预取持有的租约设置共享退避，避免数据库故障时逐请求补段。
func (a *segmentIDAllocator) backoff(parent context.Context, key, token string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), idCacheTimeout)
	defer cancel()
	if err := a.cache.Backoff(ctx, key, token, idRefillBackoff); err != nil {
		slog.ErrorContext(ctx, "back off consumer ID segment refill failed", "error", err)
	}
}

// reserveSegment 在主库事务中锁定命名空间行并永久推进水位，已保留号段不会再次分配。
func (a *segmentIDAllocator) reserveSegment(ctx context.Context) (idgen.Segment, error) {
	if a.data == nil || a.data.DB() == nil {
		return idgen.Segment{}, fmt.Errorf("reserve ID segment: mysql store is not configured")
	}
	var reserved idgen.Segment
	err := a.data.DB().WithContext(ctx).Clauses(dbresolver.Write).Transaction(func(tx *gorm.DB) error {
		var allocator idAllocatorModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&allocator, "namespace = ?", a.namespace).Error; err != nil {
			return fmt.Errorf("lock ID allocator %q: %w", a.namespace, err)
		}
		if allocator.NextID == 0 || allocator.SegmentSize == 0 || allocator.SegmentSize > maxIDSegmentSize {
			return fmt.Errorf("invalid ID allocator %q: next_id and segment_size are out of range", a.namespace)
		}
		end := allocator.NextID + uint64(allocator.SegmentSize)
		if end < allocator.NextID || end-1 > math.MaxInt64 {
			return fmt.Errorf("ID allocator %q exhausted signed BIGINT range", a.namespace)
		}
		if err := tx.Model(&idAllocatorModel{}).
			Where("namespace = ?", a.namespace).
			Updates(map[string]any{"next_id": strconv.FormatUint(end, 10), "updated_at": gorm.Expr("UTC_TIMESTAMP(6)")}).Error; err != nil {
			return fmt.Errorf("advance ID allocator %q: %w", a.namespace, err)
		}
		reserved = idgen.Segment{Start: allocator.NextID, Size: allocator.SegmentSize}
		return nil
	})
	if err != nil {
		return idgen.Segment{}, err
	}
	return reserved, nil
}
