package redisstore

import (
	"context"
	"drama-server/internal/biz/globalconfig"
	"encoding/json"
	"errors"
	"github.com/redis/go-redis/v9"
	"log/slog"
	"time"
)

// CachedGlobalConfigRepository 使用 MySQL 持久化、Redis 缓存；不维护配置版本。
type CachedGlobalConfigRepository struct {
	source globalconfig.Repository
	client redis.UniversalClient
}

// NewCachedGlobalConfigRepository 组合权威仓储和可丢弃缓存，Redis 故障时回源 MySQL。
func NewCachedGlobalConfigRepository(source globalconfig.Repository, client redis.UniversalClient) *CachedGlobalConfigRepository {
	return &CachedGlobalConfigRepository{source: source, client: client}
}

func globalConfigCacheKey(key string) string { return "drama:global_config_cache:" + key }

// List 直接读取数据库，管理端不使用缓存列表。
func (r *CachedGlobalConfigRepository) List(ctx context.Context) ([]globalconfig.Entry, error) {
	return r.source.List(ctx)
}

// Get 先读 Redis；未命中、损坏或故障时读 MySQL，成功结果缓存五分钟，缺失和失败不缓存。
func (r *CachedGlobalConfigRepository) Get(ctx context.Context, key string) (globalconfig.Entry, error) {
	cacheKey := globalConfigCacheKey(key)
	if r.client != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		raw, cacheErr := r.client.Get(cacheCtx, cacheKey).Bytes()
		cancel()
		if cacheErr == nil {
			var entry globalconfig.Entry
			decodeErr := json.Unmarshal(raw, &entry)
			if decodeErr == nil && entry.Key != key {
				decodeErr = errors.New("cached configuration key mismatch")
			}
			if decodeErr == nil {
				decodeErr = globalconfig.ValidateInput(entry.Key, entry.Value)
			}
			if decodeErr == nil {
				return entry, nil
			}
			slog.ErrorContext(ctx, "decode global configuration cache failed", "config_key", key, "error", decodeErr)
		} else if !errors.Is(cacheErr, redis.Nil) {
			slog.ErrorContext(ctx, "read global configuration cache failed", "error", cacheErr)
		}
	}
	entry, err := r.source.Get(ctx, key)
	if err != nil {
		return globalconfig.Entry{}, err
	}
	if r.client != nil {
		raw, encodeErr := json.Marshal(entry)
		if encodeErr != nil {
			slog.ErrorContext(ctx, "encode global configuration cache failed", "config_key", key, "error", encodeErr)
			return entry, nil
		}
		cacheCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		cacheErr := r.client.Set(cacheCtx, cacheKey, raw, 5*time.Minute).Err()
		cancel()
		if cacheErr != nil {
			slog.ErrorContext(ctx, "write global configuration cache failed", "error", cacheErr)
		}
	}
	return entry, nil
}

// Put 先提交数据库，成功后删除缓存；数据库失败保持缓存不变。
// 缓存删除失败记录错误并由 TTL 兜底，不把已提交的数据库写入误报为失败。
func (r *CachedGlobalConfigRepository) Put(ctx context.Context, row globalconfig.Entry) error {
	if err := r.source.Put(ctx, row); err != nil {
		return err
	}
	r.invalidate(ctx, row.Key)
	return nil
}

// Delete 先删除数据库记录，再清缓存；记录已不存在时也清理可能遗留的缓存。
func (r *CachedGlobalConfigRepository) Delete(ctx context.Context, key string) error {
	err := r.source.Delete(ctx, key)
	if err == nil || errors.Is(err, globalconfig.ErrNotFound) {
		r.invalidate(ctx, key)
	}
	return err
}

func (r *CachedGlobalConfigRepository) invalidate(ctx context.Context, key string) {
	if r.client == nil {
		return
	}
	// 持久化成功后，即使请求取消也完成短时缓存清理。
	cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 150*time.Millisecond)
	defer cancel()
	if err := r.client.Del(cacheCtx, globalConfigCacheKey(key)).Err(); err != nil {
		slog.ErrorContext(ctx, "delete global configuration cache failed", "error", err)
	}
}
