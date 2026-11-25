package data

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/data/redisstore"
)

// cachedConsumerStore 只缓存列表和详情读取；播放、奖励及权益仍使用嵌入的权威仓储。
type cachedConsumerStore struct {
	domain.Store
	cache *redisstore.CatalogCache
}

type cachedDramaDetail struct {
	// Tags 保存当前草稿的展示目录。
	Tags []domain.TikTokTag `json:"tags"`
	// PublishedTags 保存线上内容的展示目录。
	PublishedTags []domain.TikTokTag `json:"publishedTags"`
	// Drama 保留剧目当前资料，不能作为播放权限依据。
	Drama domain.Drama `json:"drama"`
	// Published 保留线上快照，回填时与草稿一起复制。
	Published *domain.Drama `json:"published"`
}

func catalogRead[T any](ctx context.Context, cache *redisstore.CatalogCache, field string, load func() (T, error)) (T, error) {
	var result T
	if cache == nil {
		loaded, err := load()
		if err != nil {
			slog.ErrorContext(ctx, "load consumer catalog without cache failed", "error", err)
		}
		return loaded, err
	}
	cacheCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	version, value, err := cache.Read(cacheCtx, field)
	cancel()
	if err != nil {
		slog.ErrorContext(ctx, "read consumer catalog cache failed", "error", err)
	}
	if len(value) > 0 {
		if err := json.Unmarshal(value, &result); err == nil {
			return result, nil
		} else {
			slog.ErrorContext(ctx, "decode consumer catalog cache failed", "error", err)
		}
	}
	// 缓存未命中时每个请求独立查库；查询继续使用自己的上下文，不共享取消或错误结果。
	loaded, err := load()
	if err != nil {
		slog.ErrorContext(ctx, "load consumer catalog cache source failed", "error", err)
		return result, err
	}
	encoded, err := json.Marshal(loaded)
	if err != nil {
		slog.ErrorContext(ctx, "encode consumer catalog cache failed", "error", err)
		return result, err
	}
	if version != "" {
		fillCtx, fillCancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer fillCancel()
		if err := cache.Fill(fillCtx, version, field, encoded); err != nil {
			slog.ErrorContext(ctx, "fill consumer catalog cache failed", "error", err)
		}
	}
	return loaded, nil
}

// DiscoverDramas 按应用、筛选和页位置缓存列表摘要，空列表同样缓存。
func (s *cachedConsumerStore) DiscoverDramas(ctx context.Context, input domain.DiscoveryQuery) (domain.DiscoveryPage, error) {
	prefix := "subject-list-v3"
	if application.IsDirect(ctx) {
		prefix = "subject-direct-list-v3"
	}
	field := redisstore.CatalogField(prefix, struct {
		App     string
		Subject string
		Query   domain.DiscoveryQuery
	}{application.IDFromContext(ctx), application.SubjectIDFromContext(ctx), input})
	return catalogRead(ctx, s.cache.Bucket("list"), field, func() (domain.DiscoveryPage, error) { return s.Store.DiscoverDramas(ctx, input) })
}

// FindDramaDetail 缓存完整内容及线上快照；Service 仍检查应用授权，Handler 动态计算个人权益。
func (s *cachedConsumerStore) FindDramaDetail(ctx context.Context, id string) (*domain.Drama, error) {
	prefix := "subject-detail-v4"
	if application.IsDirect(ctx) {
		prefix = "subject-direct-detail-v4"
	}
	field := redisstore.CatalogField(prefix, []string{application.IDFromContext(ctx), application.SubjectIDFromContext(ctx), id})
	detail, err := catalogRead(ctx, s.cache, field, func() (cachedDramaDetail, error) {
		drama, err := s.Store.FindDramaDetail(ctx, id)
		if err != nil {
			return cachedDramaDetail{}, err
		}
		result := cachedDramaDetail{Drama: *drama, Published: drama.Published, Tags: drama.TikTokTags}
		if drama.Published != nil {
			result.PublishedTags = drama.Published.TikTokTags

		}
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	detail.Drama.Published = detail.Published
	detail.Drama.TikTokTags = detail.Tags
	if detail.Drama.Published != nil {
		detail.Drama.Published.TikTokTags = detail.PublishedTags

	}
	return &detail.Drama, nil
}
