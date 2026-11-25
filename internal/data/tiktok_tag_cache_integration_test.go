package data

import (
	"context"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/data/redisstore"
	"drama-server/internal/testsupport/memory"
	"github.com/redis/go-redis/v9"
	"os"
	"testing"
)

// TestTikTokTagCacheIntegration 覆盖英文翻译在缓存命中、保存失效和清空时保持一致。
func TestTikTokTagCacheIntegration(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("未配置隔离 Redis")
	}
	ctx := application.WithContext(context.Background(), "101")
	client := redis.NewClient(&redis.Options{Addr: address, DB: 13})
	defer client.Close()
	cache := redisstore.NewCatalogCache(client)
	if err := cache.Invalidate(ctx); err != nil {
		t.Fatal(err)
	}
	defer cache.Invalidate(ctx)
	published := domain.Drama{ID: "21", OwnerApplicationID: "101", PublishStatus: "published", TagIDs: []string{"21"}}
	backing := &countedCatalog{Store: memory.NewStore(memory.Seed{PublishedOwnerID: "101", Dramas: []domain.Drama{published}})}
	reader := &cachedConsumerStore{Store: backing, cache: cache}
	writer := &invalidatingCatalogStore{catalogPersistence: backing, cache: cache}
	service := domain.NewService(reader, nil)
	for _, name := range []string{"", "Mystery", "Detective", ""} {
		if err := writer.SaveTikTokTagEnglishName(ctx, "21", name); err != nil {
			t.Fatal(err)
		}
		expected := name
		if expected == "" {
			expected = "悬疑推理"
		}
		for range 2 {
			detail, err := service.FindDrama(ctx, "21")
			if err != nil || len(detail.Tags) != 1 || detail.Tags[0] != expected {
				t.Fatalf("详情缓存翻译不一致 %v %+v", err, detail)
			}
			page, err := reader.DiscoverDramas(ctx, domain.DiscoveryQuery{Limit: 20})
			if err != nil || len(page.Items) != 1 || page.Items[0].Tags[0] != expected {
				t.Fatalf("列表缓存翻译不一致 %v %+v", err, page)
			}
		}
	}
	if backing.details != 4 || backing.lists != 4 {
		t.Fatal("缓存命中或失效次数错误")
	}
}
