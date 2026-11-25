package data

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/data/redisstore"
)

// homepageResultCache 复用内容变更失效版本，缓存完整展示结果而非只有剧目引用。
type homepageResultCache struct{ cache *redisstore.CatalogCache }

// Read 按可信应用和区域隔离缓存；命中后不读取剧目数据库。
func (c *homepageResultCache) Read(ctx context.Context, section string, load func() ([]consumer.DramaSummary, error)) ([]consumer.DramaSummary, error) {
	prefix := "subject-home-v2:"
	if application.IsDirect(ctx) {
		prefix = "subject-direct-home-v2:"
	}
	return catalogRead(ctx, c.cache.Bucket(section), redisstore.CatalogField(prefix+section, []string{application.SubjectIDFromContext(ctx), application.IDFromContext(ctx)}), load)
}

// Invalidate 沿用已提交写入的重试失效逻辑，清除分页和推荐并阻止旧查询回填。
func (c *homepageResultCache) Invalidate(ctx context.Context) {
	(&invalidatingCatalogStore{cache: c.cache}).invalidate(ctx)
}
