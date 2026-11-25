package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/data/redisstore"
	"github.com/redis/go-redis/v9"
)

type homepageCountedCatalog struct {
	consumer.Store
	reads atomic.Int64
}

// FindVisibleRankingMembers 轮播缓存用例没有分类成员，不应触发最小投影查询。
func (c *homepageCountedCatalog) FindVisibleRankingMembers(context.Context, []string) ([]homepage.RankingMember, error) {
	return nil, errors.New("unexpected ranking member query in cache fixture")
}

// GetRankings 缓存用例没有分类成员，分类 SQL 持久化由独立集成测试覆盖。
func (c *homepageCountedCatalog) GetRankings(context.Context) (map[string]homepage.Ranking, error) {
	return map[string]homepage.Ranking{}, nil
}

// ReplaceRanking 不参与轮播和热播缓存用例。
func (c *homepageCountedCatalog) ReplaceRanking(context.Context, string, homepage.Ranking) (homepage.Ranking, error) {
	return homepage.Ranking{}, errors.New("unexpected category write in cache fixture")
}

// FindVisibleSummaries 返回按需构造的摘要并计数，用于断言缓存命中没有回源。
func (c *homepageCountedCatalog) FindVisibleSummaries(_ context.Context, ids []string) ([]consumer.DramaSummary, error) {
	c.reads.Add(1)
	rows := []consumer.DramaSummary{}
	for _, id := range ids {
		rows = append(rows, consumer.DramaSummary{ID: id, Title: id, FirstEpisodeID: "1"})
	}
	return rows, nil
}

// DiscoverDramas 返回固定分页样本并计数，不触发平台或真实业务数据库。
func (c *homepageCountedCatalog) DiscoverDramas(_ context.Context, _ consumer.DiscoveryQuery) (consumer.DiscoveryPage, error) {
	c.reads.Add(1)
	return consumer.DiscoveryPage{Items: []consumer.DramaSummary{{ID: "1", Title: "目录"}}}, nil
}

// TestHomepageResultCacheIntegration 使用真实 Redis 验证完整结果命中、TTL、配置失效与人工配置保留。
func TestHomepageResultCacheIntegration(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("未配置独立测试 Redis")
	}
	ctx := application.WithSubject(application.WithContext(context.Background(), "cache-home"), "1")
	client := redis.NewClient(&redis.Options{Addr: address, DB: 12})
	defer client.Close()
	cache := redisstore.NewCatalogCache(client)
	clean := func() {
		if err := cache.Invalidate(ctx); err != nil {
			t.Error(err)
		}
		keys, err := client.Keys(ctx, "drama:homepage:{global}:*").Result()
		if err != nil {
			t.Error(err)
			return
		}
		if len(keys) > 0 {
			if err := client.Del(ctx, keys...).Err(); err != nil {
				t.Error(err)
			}
		}
	}
	clean()
	defer clean()
	repo, db := newProductionRewardStore(t)
	// 推荐保存现在核验 MySQL 上线事实；缓存测试也需真实上线夹具，不能仅伪造摘要。
	for _, id := range []string{"1", "2", "3"} {
		online := consumer.Drama{ID: id, Title: id, PublishStatus: "published", PlatformVersion: 1}
		draft := online
		draft.Published = &online
		if err := repo.SaveDrama(ctx, draft); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(online)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Table("tiktok_drama_states").Create(map[string]any{"subject_id": "1", "drama_id": id, "published_snapshot": raw}).Error; err != nil {
			t.Fatal(err)
		}
	}
	catalog := &homepageCountedCatalog{}
	service := homepage.NewService(repo, catalog, catalog, &homepageResultCache{cache: cache})
	if _, err := service.SaveCarousel(ctx, homepage.Carousel{DramaIDs: []string{"1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveHot(ctx, homepage.Carousel{DramaIDs: []string{"1"}}); err != nil {
		t.Fatal(err)
	}
	first, err := readHomepageSections(service, ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := catalog.reads.Load()
	first.Carousel[0].Title = "调用方修改"
	second, err := readHomepageSections(service, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.reads.Load() != count || second.Carousel[0].Title != "1" {
		t.Fatal("结果命中仍然回源或共享了可变数据")
	}
	for _, item := range []struct {
		suffix string
		max    time.Duration
	}{{"carousel", 5 * time.Minute}, {"hot", time.Minute}} {
		ttl := client.PTTL(ctx, "drama:{consumer-catalog}:entries:"+item.suffix).Val()
		if ttl <= 0 || ttl > item.max {
			t.Fatalf("%s TTL 错误：%v", item.suffix, ttl)
		}
	}
	// 删除本测试全部缓存后，MySQL 配置仍可恢复完整首页。
	if err := cache.Invalidate(ctx); err != nil {
		t.Fatal(err)
	}
	restored, err := readHomepageSections(service, ctx)
	if err != nil || len(restored.Carousel) != 1 || len(restored.Hot) != 1 {
		t.Fatal("缓存丢失导致推荐丢失", err)
	}
	for _, section := range []string{"carousel", "hot"} {
		if client.Exists(ctx, "drama:homepage:{global}:"+section).Val() != 0 {
			t.Fatal("运行时仍写入 Redis 权威配置")
		}
	}
	// 先填充另一个小程序的结果；共享编辑必须使两个应用同时失效。
	otherCtx := application.WithContext(ctx, "other-home-app")
	if value, err := readHomepageSections(service, otherCtx); err != nil || value.Hot[0].ID != "1" {
		t.Fatal("第二应用未读取共享配置", err)
	}
	// 保存人工热播后立即失效缓存；到期仅重新读取同一配置，不生成自动排行。
	if _, err := service.SaveHot(ctx, homepage.Carousel{Revision: 1, DramaIDs: []string{"2"}}); err != nil {
		t.Fatal(err)
	}
	updatedHot, err := readHomepageSections(service, ctx)
	if err != nil || updatedHot.Hot[0].ID != "2" {
		t.Fatal("人工热播保存未失效", err)
	}
	if value, err := readHomepageSections(service, otherCtx); err != nil || value.Hot[0].ID != "2" {
		t.Fatal("共享编辑未失效另一个应用的缓存", err)
	}
	if err := client.PExpire(ctx, "drama:{consumer-catalog}:entries:hot", time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	refreshed, err := readHomepageSections(service, ctx)
	if err != nil || refreshed.Hot[0].ID != "2" {
		t.Fatal("过期后人工配置丢失", err)
	}
	if _, err := service.SaveCarousel(ctx, homepage.Carousel{Revision: 1, DramaIDs: []string{"2"}}); err != nil {
		t.Fatal(err)
	}
	updated, err := readHomepageSections(service, ctx)
	if err != nil || updated.Carousel[0].ID != "2" {
		t.Fatal("配置保存没有失效完整结果", err)
	}
	reader := &cachedConsumerStore{Store: catalog, cache: cache}
	query := consumer.DiscoveryQuery{Limit: 2}
	before := catalog.reads.Load()
	for range 2 {
		if _, err := reader.DiscoverDramas(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	if catalog.reads.Load() != before+1 {
		t.Fatal("分页命中仍回源")
	}
	if ttl := client.PTTL(ctx, "drama:{consumer-catalog}:entries:list").Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatal("分页未设置一分钟 TTL")
	}
	if _, err := reader.DiscoverDramas(ctx, consumer.DiscoveryQuery{Limit: 2, Offset: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.DiscoverDramas(application.WithContext(ctx, "another-app"), query); err != nil {
		t.Fatal(err)
	}
	if catalog.reads.Load() != before+3 {
		t.Fatal("分页或应用缓存串用")
	}
	if err := client.PExpire(ctx, "drama:{consumer-catalog}:entries:list", time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := reader.DiscoverDramas(ctx, query); err != nil {
		t.Fatal(err)
	}
	if catalog.reads.Load() != before+4 {
		t.Fatal("分页 TTL 到期没有回源")
	}
	// Redis 断连不能丢失配置，也不能将已提交的 MySQL 保存误报为失败。
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previousLogger)
	failedClient := redis.NewClient(&redis.Options{Addr: address, DB: 12})
	if err := failedClient.Close(); err != nil {
		t.Fatal(err)
	}
	service = homepage.NewService(repo, catalog, catalog, &homepageResultCache{cache: redisstore.NewCatalogCache(failedClient)})
	saved, err := service.SaveHot(ctx, homepage.Carousel{Revision: 2, DramaIDs: []string{"3"}})
	if err != nil || saved.Revision != 3 {
		t.Fatal("Redis 故障阻止 MySQL 保存", err)
	}
	fallback, err := readHomepageSections(service, ctx)
	if err != nil || fallback.Hot[0].ID != "3" {
		t.Fatal("Redis 故障未回源", err)
	}
	if !strings.Contains(logs.String(), "invalidate consumer catalog after committed change failed") || !strings.Contains(logs.String(), "redis: client is closed") {
		t.Fatal("缓存故障缺少操作与实际错误", logs.String())
	}
	uncached := homepage.NewService(repo, catalog, catalog, &homepageResultCache{})
	if value, err := readHomepageSections(uncached, ctx); err != nil || value.Hot[0].ID != "3" {
		t.Fatal("未初始化 Redis 时首页不能读取 MySQL", err)
	}

}

// TestIndependentCatalogMissesRedisIntegration 验证同键并发未命中各自查库、缓存成功结果且不缓存数据库错误。
func TestIndependentCatalogMissesRedisIntegration(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("未配置独立测试 Redis")
	}
	ctx := application.WithContext(context.Background(), "independent-cache-requests")
	client := redis.NewClient(&redis.Options{Addr: address, DB: 12})
	defer client.Close()
	cache := redisstore.NewCatalogCache(client)
	if err := cache.Invalidate(ctx); err != nil {
		t.Fatal(err)
	}
	defer cache.Invalidate(ctx)
	var loads atomic.Int64
	start := make(chan struct{})
	release := make(chan struct{})
	entered := make(chan struct{}, 20)
	var wg sync.WaitGroup
	for index := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			value, err := catalogRead(ctx, cache.Bucket("hot"), "burst", func() ([]int, error) {
				loads.Add(1)
				entered <- struct{}{}
				<-release
				return []int{index}, nil
			})
			if err != nil || len(value) != 1 || value[0] != index {
				t.Errorf("并发请求没有返回自己的查询结果：index=%d value=%v error=%v", index, value, err)
			}
		}()
	}
	close(start)
	wait := time.NewTimer(2 * time.Second)
	defer wait.Stop()
	for range 20 {
		select {
		case <-entered:
		case <-wait.C:
			close(release)
			wg.Wait()
			t.Fatal("并发请求仍合并或等待其他请求查库")
		}
	}
	close(release)
	wg.Wait()
	if loads.Load() != 20 {
		t.Fatalf("并发回源次数：%d", loads.Load())
	}
	if _, value, err := cache.Bucket("hot").Read(ctx, "burst"); err != nil || len(value) == 0 {
		t.Fatal("独立查询成功后没有写入缓存", err)
	}
	_, err := catalogRead(ctx, cache, "failure", func() ([]string, error) { return nil, errors.New("database unavailable") })
	if err == nil {
		t.Fatal("数据库错误被吞掉")
	}
	if _, value, err := cache.Read(ctx, "failure"); err != nil || len(value) != 0 {
		t.Fatal("数据库错误被缓存", err)
	}
}

// readHomepageSections 在集成测试内组合三个公开分区方法，生产服务不再提供聚合首页读取。
func readHomepageSections(service *homepage.Service, ctx context.Context) (homepage.Sections, error) {
	carousel, err := service.HomeCarousel(ctx)
	if err != nil {
		return homepage.Sections{}, err
	}
	hot, err := service.HomeHot(ctx)
	if err != nil {
		return homepage.Sections{}, err
	}
	rankings, err := service.HomeRankings(ctx)
	if err != nil {
		return homepage.Sections{}, err
	}
	return homepage.Sections{Carousel: carousel, Hot: hot, Rankings: rankings}, nil
}

// ListTikTokTags 为分类榜提供固定测试目录。
func (c *homepageCountedCatalog) ListTikTokTags(context.Context) ([]consumer.TikTokTag, error) {
	return consumer.TikTokTagCatalog(), nil
}
