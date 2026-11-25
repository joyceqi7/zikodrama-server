package data

import (
	"context"
	"drama-server/internal/data/persistence/mysqlstore"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"testing"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/data/redisstore"
	"drama-server/internal/testsupport/memory"
	"github.com/redis/go-redis/v9"
)

type countedCatalog struct {
	*memory.Store
	lists, details, authoritative int
	failWrite                     bool
}

func (s *countedCatalog) DiscoverDramas(ctx context.Context, q domain.DiscoveryQuery) (domain.DiscoveryPage, error) {
	s.lists++
	return s.Store.DiscoverDramas(ctx, q)
}
func (s *countedCatalog) FindDramaDetail(ctx context.Context, id string) (*domain.Drama, error) {
	s.details++
	return s.Store.FindDrama(ctx, id)
}
func (s *countedCatalog) FindDrama(ctx context.Context, id string) (*domain.Drama, error) {
	s.authoritative++
	return s.Store.FindDrama(ctx, id)
}
func (s *countedCatalog) SaveDrama(ctx context.Context, d domain.Drama) error {
	if s.failWrite {
		return errors.New("transaction rolled back")
	}
	return s.Store.SaveDrama(ctx, d)
}

// UpdateTikTokPublication 模拟已提交的发布快照，不调用平台服务。
func (s *countedCatalog) UpdateTikTokPublication(_ context.Context, _ operations.TikTokPublication, drama *domain.Drama) error {
	if s.failWrite {
		return errors.New("publication transaction rolled back")
	}
	if drama != nil {
		s.Store = memory.NewStore(memory.Seed{Dramas: []domain.Drama{*drama}})
	}
	return nil
}

// TestConsumerCatalogCacheIntegration 覆盖应用隔离、内容副本、B 端失效及权威播放读取。
func TestConsumerCatalogCacheIntegration(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	ctx := application.WithContext(context.Background(), "cache-app")
	client := redis.NewClient(&redis.Options{Addr: address, DB: 13})
	defer client.Close()
	cache := redisstore.NewCatalogCache(client)
	if err := cache.Invalidate(ctx); err != nil {
		t.Fatal(err)
	}
	defer cache.Invalidate(ctx)
	backing := &countedCatalog{Store: memory.NewStore(memory.Seed{})}
	published := domain.Drama{ID: "cache-drama", Title: "Before", OwnerApplicationID: "cache-app", PublishStatus: "published", Status: "updating", TagIDs: []string{"21"}, Episodes: []domain.Episode{{ID: "cache-episode", VID: "vid", MediaStatus: "ready"}}}
	draft := published
	draft.Published = &published
	if err := backing.SaveDrama(ctx, draft); err != nil {
		t.Fatal(err)
	}
	reader := &cachedConsumerStore{Store: backing, cache: cache}
	writer := &invalidatingCatalogStore{catalogPersistence: backing, cache: cache}
	query := domain.DiscoveryQuery{Limit: 20}
	for range 2 {
		page, err := reader.DiscoverDramas(ctx, query)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("list: %+v %v", page, err)
		}
	}
	if backing.lists != 1 {
		t.Fatal("list cache missed")
	}
	other, err := reader.DiscoverDramas(application.WithContext(ctx, "different-app"), query)
	if err != nil || len(other.Items) != 0 {
		t.Fatal("application isolation broken")
	}
	if _, err := reader.DiscoverDramas(ctx, domain.DiscoveryQuery{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if backing.lists != 3 {
		t.Fatal("pagination parameters share cache")
	}
	service := domain.NewService(reader, nil)
	for range 2 {
		detail, err := service.FindDrama(ctx, draft.ID)
		if err != nil || detail.Title != "Before" || len(detail.Tags) != 1 {
			t.Fatalf("detail projection: %+v %v", detail, err)
		}
		detail.Title = "client mutation"
	}
	if backing.details != 1 {
		t.Fatal("detail cache missed")
	}
	// 播放/奖励通过 FindDrama 读取数据库；即使详情已缓存也不能使用它作为播放事实。
	for range 2 {
		if _, err := reader.FindDrama(ctx, draft.ID); err != nil {
			t.Fatal(err)
		}
	}
	if backing.authoritative != 2 {
		t.Fatal("authoritative reads cached")
	}
	backing.failWrite = true
	if err := writer.SaveDrama(ctx, draft); err == nil {
		t.Fatal("rollback swallowed")
	}
	if _, err := service.FindDrama(ctx, draft.ID); err != nil {
		t.Fatal(err)
	}
	if backing.details != 1 {
		t.Fatal("failed transaction evicted cache")
	}
	backing.failWrite = false
	published.Title = "After"
	draft.Published = &published
	if err := writer.SaveDrama(ctx, draft); err != nil {
		t.Fatal(err)
	}
	updated, err := service.FindDrama(ctx, draft.ID)
	if err != nil || updated.Title != "Before" || backing.details != 2 {
		t.Fatalf("detail not invalidated: %v", err)
	}
	page, err := reader.DiscoverDramas(ctx, query)
	if err != nil || page.Items[0].Title != "Before" || backing.lists != 4 {
		t.Fatal("list not invalidated")
	}
	// 取消请求后，已提交的写入仍完成缓存删除。
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	writer.invalidate(cancelled)
	if _, err := service.FindDrama(ctx, draft.ID); err != nil {
		t.Fatal(err)
	}
	if backing.details != 3 {
		t.Fatal("cancelled caller skipped invalidation")
	}
	// 发布和下架由 Worker 写入，复用同一失效路径。
	if err := writer.UpdateTikTokPublication(ctx, operations.TikTokPublication{SubjectID: "1"}, &draft); err != nil {
		t.Fatal(err)
	}
	updated, err = service.FindDrama(ctx, draft.ID)
	if err != nil || updated.Title != "After" {
		t.Fatal("publication cache not invalidated")
	}
	draft.Published = nil
	draft.PublishStatus = "draft"
	if err := writer.UpdateTikTokPublication(ctx, operations.TikTokPublication{SubjectID: "1"}, &draft); err != nil {
		t.Fatal(err)
	}
	if _, err := service.FindDrama(ctx, draft.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("unpublished drama remained visible")
	}
	page, err = reader.DiscoverDramas(ctx, query)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("unpublished drama remained in list")
	}
	if err := writer.DeleteDrama(ctx, draft.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.FindDramaDetail(ctx, draft.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("deleted detail remained cached")
	}
	// Redis 断连时回源读取；不缓存错误，不影响 MySQL 写入结果。
	client.Close()
	if _, err := reader.DiscoverDramas(ctx, query); err != nil {
		t.Fatal(err)
	}
	if err := writer.SaveDrama(ctx, draft); err != nil {
		t.Fatal("cache outage changed committed write result")
	}
}

var _ operations.CatalogRepository = (*invalidatingCatalogStore)(nil)
var _ operations.TikTokPublicationRepository = (*invalidatingCatalogStore)(nil)

// TestCatalogInvalidationAfterSQLCommitIntegration 验证 SQL 回滚保留缓存，提交成功才执行失效。
func TestCatalogInvalidationAfterSQLCommitIntegration(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	ctx := application.WithSubject(context.Background(), "1")
	client := redis.NewClient(&redis.Options{Addr: address, DB: 12})
	defer client.Close()
	cache := redisstore.NewCatalogCache(client)
	defer cache.Invalidate(ctx)
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	orm, err := gorm.Open(mysql.New(mysql.Config{Conn: database, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	data, err := mysqlstore.NewData(orm)
	if err != nil {
		t.Fatal(err)
	}
	writer := &invalidatingCatalogStore{catalogPersistence: mysqlstore.NewStore(data), cache: cache}
	for _, fail := range []bool{true, false} {
		if err := cache.Invalidate(ctx); err != nil {
			t.Fatal(err)
		}
		version, _, err := cache.Read(ctx, "sql-commit")
		if err != nil {
			t.Fatal(err)
		}
		if err := cache.Fill(ctx, version, "sql-commit", []byte(`{"cached":true}`)); err != nil {
			t.Fatal(err)
		}
		mock.ExpectBegin()
		statement := mock.ExpectExec("INSERT INTO `dramas`")
		if fail {
			statement.WillReturnError(errors.New("write failed"))
			mock.ExpectRollback()
		} else {
			statement.WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
		}
		err = writer.SaveDrama(ctx, domain.Drama{TagIDs: []string{}})
		if (err != nil) != fail {
			t.Fatalf("unexpected write result: %v", err)
		}
		_, value, err := cache.Read(ctx, "sql-commit")
		if err != nil {
			t.Fatal(err)
		}
		if (len(value) > 0) != fail {
			t.Fatal("cache invalidation did not follow commit outcome")
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
