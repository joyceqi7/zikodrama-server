package data

import (
	"context"
	"os"
	"testing"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/data/redisstore"
	"github.com/redis/go-redis/v9"
)

// subjectCacheCatalog 只替换目录回源，真实 Redis 键和读写逻辑保持生产实现。
type subjectCacheCatalog struct {
	consumer.Store
	lists, details int
}

// DiscoverDramas 用主体区别回源结果，便于检出同一缓存分区中的跨主体串用。
func (s *subjectCacheCatalog) DiscoverDramas(ctx context.Context, _ consumer.DiscoveryQuery) (consumer.DiscoveryPage, error) {
	s.lists++
	return consumer.DiscoveryPage{Items: []consumer.DramaSummary{{ID: "201", Title: application.SubjectIDFromContext(ctx)}}}, nil
}

// FindDramaDetail 返回当前主体的快照标记，不将凭据写入测试数据。
func (s *subjectCacheCatalog) FindDramaDetail(ctx context.Context, id string) (*consumer.Drama, error) {
	s.details++
	return &consumer.Drama{ID: id, Title: application.SubjectIDFromContext(ctx)}, nil
}

// TestSubjectCatalogCacheIsolationIntegration 验证目录、详情、首页结果都包含主体，旧共享命名空间不会被继续使用。
func TestSubjectCatalogCacheIsolationIntegration(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("未配置独立测试 Redis")
	}
	ctx := application.WithPlayback(application.WithContext(t.Context(), "subject-cache-test"), "byteplus_web")
	a, b := application.WithSubject(ctx, "1"), application.WithSubject(ctx, "2")
	client := redis.NewClient(&redis.Options{Addr: address, DB: 14})
	defer client.Close()
	cache := redisstore.NewCatalogCache(client)
	if err := cache.Invalidate(ctx); err != nil {
		t.Fatal(err)
	}
	defer cache.Invalidate(ctx)
	backing := &subjectCacheCatalog{}
	reader := &cachedConsumerStore{Store: backing, cache: cache}
	// 提前写入旧共享缓存，升级后的请求必须重新回源，不能返回旧数据。
	oldKey := redisstore.CatalogField("direct-list-v1", struct {
		App   string
		Query consumer.DiscoveryQuery
	}{application.IDFromContext(ctx), consumer.DiscoveryQuery{Limit: 20}})
	version, _, err := cache.Bucket("list").Read(ctx, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Bucket("list").Fill(ctx, version, oldKey, []byte(`{"items":[{"id":"201","title":"旧共享结果"}]}`)); err != nil {
		t.Fatal(err)
	}
	home := &homepageResultCache{cache: cache}
	homeLoads := 0
	for _, scope := range []context.Context{a, b, a, b} {
		expected := application.SubjectIDFromContext(scope)
		list, err := reader.DiscoverDramas(scope, consumer.DiscoveryQuery{Limit: 20})
		if err != nil || len(list.Items) != 1 || list.Items[0].Title != expected {
			t.Fatal("目录缓存串主体或使用旧共享结果", err)
		}
		detail, err := reader.FindDramaDetail(scope, "201")
		if err != nil || detail.Title != expected {
			t.Fatal("详情缓存串主体", err)
		}
		result, err := home.Read(scope, "carousel", func() ([]consumer.DramaSummary, error) {
			homeLoads++
			return []consumer.DramaSummary{{ID: "201", Title: expected}}, nil
		})
		if err != nil || len(result) != 1 || result[0].Title != expected {
			t.Fatal("首页缓存串主体", err)
		}
	}
	if backing.lists != 2 || backing.details != 2 || homeLoads != 2 {
		t.Fatal("主体隔离后未复用已有缓存，增加了回源压力")
	}
}
