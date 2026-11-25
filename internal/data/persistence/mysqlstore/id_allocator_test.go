package mysqlstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"drama-server/internal/biz/visitor"
	"drama-server/internal/data/idgen"
	"drama-server/internal/data/redisstore"
	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"github.com/redis/go-redis/v9"
)

// requireUIDIntegration 只为实际创建 UID 的用例要求两项依赖，纯 MySQL 仓储测试仍能独立运行。
func requireUIDIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_MYSQL_DSN") == "" || os.Getenv("TEST_REDIS_ADDR") == "" {
		t.Skip("UID 集成测试需要同时配置 TEST_MYSQL_DSN 和 TEST_REDIS_ADDR")
	}
}

// newUIDIntegrationClient 为每个实例创建独立连接，不清空共享 Redis 数据库。
func newUIDIntegrationClient(t *testing.T) *redis.Client {
	t.Helper()
	requireUIDIntegration(t)
	database := 15
	if value := os.Getenv("TEST_REDIS_DB"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			t.Fatal("TEST_REDIS_DB 必须是非负整数")
		}
		database = parsed
	}
	client := redis.NewClient(&redis.Options{
		Addr: os.Getenv("TEST_REDIS_ADDR"), Username: os.Getenv("TEST_REDIS_USERNAME"),
		Password: os.Getenv("TEST_REDIS_PASSWORD"), DB: database,
	})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("连接隔离 UID 测试 Redis 失败：%v", err)
	}
	return client
}

// newUIDIntegrationCache 为每个发号实例管理专用连接池，退出时只关闭连接，不删除共享游标。
func newUIDIntegrationCache(t *testing.T, client *redis.Client) *redisstore.UIDCache {
	t.Helper()
	cache := redisstore.NewUIDCache(client)
	t.Cleanup(func() { _ = cache.Close() })
	return cache
}

// cleanupUIDIntegrationState 只由拥有隔离数据库的父测试清理号段；子进程退出必须保留共享游标。
func cleanupUIDIntegrationState(t *testing.T, store *Store, client *redis.Client) {
	t.Helper()
	if client == nil {
		return
	}
	key, err := store.ids.cacheKey(context.Background())
	if err != nil {
		t.Fatalf("读取 UID 测试缓存命名空间失败：%v", err)
	}
	t.Cleanup(func() {
		if err := client.Del(context.Background(), key).Err(); err != nil {
			t.Errorf("清理本测试 UID 缓存失败：%v", err)
		}
	})
}

// uidObservedCache 在真实缓存协议之外记录成功发布，用于等待异步预取完成而不依赖 Redis 内部字段。
type uidObservedCache struct {
	// Cache 继续执行真实 Redis 原子协议。
	idgen.Cache
	published chan idgen.Segment
	backedOff chan struct{}
}

// Publish 保持真实租约检查和安装语义，只记录已安装的号段。
func (c *uidObservedCache) Publish(ctx context.Context, key, token string, segment idgen.Segment) (bool, error) {
	installed, err := c.Cache.Publish(ctx, key, token, segment)
	if installed && err == nil {
		c.published <- segment
	}
	return installed, err
}

// Backoff 等待真实共享退避写入完成，让故障用例不依赖竞态睡眠。
func (c *uidObservedCache) Backoff(ctx context.Context, key, token string, delay time.Duration) error {
	err := c.Cache.Backoff(ctx, key, token, delay)
	if err == nil {
		c.backedOff <- struct{}{}
	}
	return err
}

func observedUIDStore(t *testing.T, data *Data, client *redis.Client) (*Store, *uidObservedCache) {
	cache := &uidObservedCache{Cache: newUIDIntegrationCache(t, client), published: make(chan idgen.Segment, 128), backedOff: make(chan struct{}, 128)}
	return NewStore(data, cache), cache
}

func waitUIDPublication(t *testing.T, cache *uidObservedCache, start uint64) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case segment := <-cache.published:
			if segment.Start == start {
				return
			}
		case <-timer.C:
			t.Fatalf("未在耗尽前发布起点 %d 的备用号段", start)
		}
	}
}

func setUIDSegment(t *testing.T, fixture *iaaFixture, start uint64, size uint32) {
	t.Helper()
	if err := fixture.store.data.DB().Model(&idAllocatorModel{}).Where("namespace = ?", consumerIDNamespace).
		Updates(map[string]any{"next_id": start, "segment_size": size}).Error; err != nil {
		t.Fatalf("配置隔离 UID 号段失败：%v", err)
	}
}

func uidWatermark(t *testing.T, fixture *iaaFixture) uint64 {
	t.Helper()
	var model idAllocatorModel
	if err := fixture.store.data.DB().Where("namespace = ?", consumerIDNamespace).Take(&model).Error; err != nil {
		t.Fatalf("读取 MySQL UID 水位失败：%v", err)
	}
	return model.NextID
}

func nextUID(t *testing.T, store *Store, want uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := store.ids.NextID(ctx)
	if err != nil || id != want {
		t.Fatalf("共享发号结果=%d，期望=%d，错误=%v", id, want, err)
	}
}

// TestIDSegmentAllocatorTwoInstancesIntegration 验证独立数据库连接池与 Redis 客户端跨多个小号段并发发号不重复。
func TestIDSegmentAllocatorTwoInstancesIntegration(t *testing.T) {
	requireUIDIntegration(t)
	fixture := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	setUIDSegment(t, fixture, 1, 32)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	secondData, err := OpenData(ctx, &configv1.MySQLConfig{Master: fixture.dsn}, newMySQLIntegrationRuntime(t))
	if err != nil {
		t.Fatalf("创建第二个实例的独立 MySQL 连接池：%v", err)
	}
	stores := []*Store{fixture.store, NewStore(secondData, newUIDIntegrationCache(t, newUIDIntegrationClient(t)))}
	firstKey, err := stores[0].ids.cacheKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secondKey, err := stores[1].ids.cacheKey(ctx)
	if err != nil || firstKey != secondKey {
		t.Fatalf("同库独立实例未共享命名空间：%v", err)
	}
	const workersPerInstance, idsPerWorker = 4, 125
	start := make(chan struct{})
	failures := make(chan error, len(stores)*workersPerInstance)
	var generated sync.Map
	var duplicate atomic.Uint64
	var count atomic.Uint64
	var workers sync.WaitGroup
	for _, store := range stores {
		for range workersPerInstance {
			workers.Add(1)
			go func(allocator *segmentIDAllocator) {
				defer workers.Done()
				<-start
				for range idsPerWorker {
					id, err := allocator.NextID(ctx)
					if err != nil {
						failures <- err
						return
					}
					if _, loaded := generated.LoadOrStore(id, struct{}{}); loaded {
						duplicate.Add(1)
					}
					count.Add(1)
				}
			}(store.ids)
		}
	}
	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Errorf("双实例并发发号失败：%v", err)
	}
	want := uint64(len(stores) * workersPerInstance * idsPerWorker)
	if count.Load() != want || duplicate.Load() != 0 {
		t.Fatalf("实际生成 %d 个标识，期望 %d，重复 %d 个", count.Load(), want, duplicate.Load())
	}
	if watermark := uidWatermark(t, fixture); watermark < want+1 || watermark > want+65 {
		t.Fatalf("共享号段水位异常，可能每实例重复预取：%d", watermark)
	}
}

// TestIDSegmentAllocatorRestartAndPrefetchIntegration 验证 80% 时已共享备用段，重建实例继续当前段及备用段。
func TestIDSegmentAllocatorRestartAndPrefetchIntegration(t *testing.T) {
	requireUIDIntegration(t)
	fixture := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	setUIDSegment(t, fixture, 1, 10)
	for id := uint64(1); id <= 7; id++ {
		nextUID(t, fixture.store, id)
	}
	if got := uidWatermark(t, fixture); got != 11 {
		t.Fatalf("达到阈值之前不应申请备用段，MySQL 水位=%d", got)
	}
	restarted, observed := observedUIDStore(t, fixture.store.data, newUIDIntegrationClient(t))
	nextUID(t, restarted, 8)
	waitUIDPublication(t, observed, 11)
	if got := uidWatermark(t, fixture); got != 21 {
		t.Fatalf("消耗至 80%% 时应完成预取，MySQL 水位=%d", got)
	}
	// 发布应用产生的新实例不初始化游标，也不能丢弃已在共享缓存中的备用段。
	restartedAgain := NewStore(fixture.store.data, newUIDIntegrationCache(t, newUIDIntegrationClient(t)))
	for id := uint64(9); id <= 11; id++ {
		nextUID(t, restartedAgain, id)
	}
	if got := uidWatermark(t, fixture); got != 21 {
		t.Fatalf("实例重建或切段额外申请了号段，MySQL 水位=%d", got)
	}
}

// TestIDSegmentAllocatorPrefetchDoesNotBlockIntegration 验证预取被数据库锁阻塞时剩余 UID 仍可用，等待耗尽可取消。
func TestIDSegmentAllocatorPrefetchDoesNotBlockIntegration(t *testing.T) {
	requireUIDIntegration(t)
	fixture := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	setUIDSegment(t, fixture, 1, 10)
	store, observed := observedUIDStore(t, fixture.store.data, fixture.uidClient)
	for id := uint64(1); id <= 7; id++ {
		nextUID(t, store, id)
	}
	tx, err := fixture.store.data.pool.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var locked uint64
	if err := tx.QueryRow("SELECT next_id FROM id_allocators WHERE namespace = ? FOR UPDATE", consumerIDNamespace).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	for id := uint64(8); id <= 10; id++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		got, err := store.ids.NextID(ctx)
		cancel()
		if err != nil || got != id {
			t.Fatalf("预取锁等待阻塞了当前段，id=%d，错误=%v", got, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, err = store.ids.NextID(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("号段耗尽后等待未遵守请求截止时间：%v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	waitUIDPublication(t, observed, 11)
	nextUID(t, store, 11)
}

// TestIDSegmentAllocatorCacheDiscardIntegration 验证丢弃当前及备用段后只从 MySQL 新水位申请，旧段不回收。
func TestIDSegmentAllocatorCacheDiscardIntegration(t *testing.T) {
	requireUIDIntegration(t)
	fixture := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	setUIDSegment(t, fixture, 1, 10)
	store, observed := observedUIDStore(t, fixture.store.data, fixture.uidClient)
	for id := uint64(1); id <= 8; id++ {
		nextUID(t, store, id)
	}
	waitUIDPublication(t, observed, 11)
	key, err := store.ids.cacheKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.uidClient.Del(context.Background(), key).Err(); err != nil {
		t.Fatal(err)
	}
	nextUID(t, store, 21)
	if got := uidWatermark(t, fixture); got != 31 {
		t.Fatalf("缓存重建没有保留永久预留水位：%d", got)
	}
}

// TestIDSegmentAllocatorLargeUIDIntegration 验证超过 Lua 精确整数范围的 UID 在消费及切段时仍完整保存。
func TestIDSegmentAllocatorLargeUIDIntegration(t *testing.T) {
	requireUIDIntegration(t)
	fixture := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	const start uint64 = 9_007_199_254_740_993
	setUIDSegment(t, fixture, start, 16)
	for offset := uint64(0); offset < 24; offset++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		id, err := fixture.store.nextID(ctx)
		cancel()
		if err != nil || id != strconv.FormatUint(start+offset, 10) {
			t.Fatalf("大整数 UID 在共享发号过程中丢失精度：id=%q，offset=%d，错误=%v", id, offset, err)
		}
	}
}

// TestIDSegmentAllocatorDatabaseIsolationIntegration 验证共用 Redis 的不同数据库拥有独立号段状态。
func TestIDSegmentAllocatorDatabaseIsolationIntegration(t *testing.T) {
	requireUIDIntegration(t)
	first := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	second := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	keyA, err := first.store.ids.cacheKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := second.store.ids.cacheKey(context.Background())
	if err != nil || keyA == keyB {
		t.Fatalf("不同数据库未隔离共享 UID 状态：%v", err)
	}
	nextUID(t, first.store, 1)
	nextUID(t, second.store, 1)
}

// TestIDSegmentAllocatorMySQLRollbackIntegration 验证号段更新失败不推进水位，修复数据库后可在退避后继续发号。
func TestIDSegmentAllocatorMySQLRollbackIntegration(t *testing.T) {
	requireUIDIntegration(t)
	fixture := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	setUIDSegment(t, fixture, 1, 10)
	if err := fixture.store.data.DB().Exec("CREATE TRIGGER reject_uid_segment BEFORE UPDATE ON id_allocators FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'fixture-uid-update-failure'").Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_, err := fixture.store.ids.NextID(ctx)
	cancel()
	if err == nil {
		t.Fatal("MySQL 预留失败仍返回 UID")
	}
	if got := uidWatermark(t, fixture); got != 1 {
		t.Fatalf("MySQL 预留失败仍推进水位：%d", got)
	}
	if err := fixture.store.data.DB().Exec("DROP TRIGGER reject_uid_segment").Error; err != nil {
		t.Fatal(err)
	}
	nextUID(t, fixture.store, 1)
}

// TestUIDCacheFailurePreservesExistingLoginIntegration 验证 Redis 缺失或失败只阻止新用户发号，老用户身份仍从 MySQL 返回。
func TestUIDCacheFailurePreservesExistingLoginIntegration(t *testing.T) {
	requireUIDIntegration(t)
	fixture := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	ctx := context.Background()
	record := visitor.TikTokIdentityRecord{ApplicationID: "101", OpenIDHash: visitor.TikTokOpenIDHash("101", "uid-existing-user")}
	existing, err := fixture.store.ResolveTikTokIdentityRef(ctx, record)
	if err != nil {
		t.Fatalf("创建已有测试身份失败：%v", err)
	}
	closedCache := newUIDIntegrationCache(t, newUIDIntegrationClient(t))
	if err := closedCache.Close(); err != nil {
		t.Fatal(err)
	}
	cases := []*Store{NewStore(fixture.store.data), NewStore(fixture.store.data, closedCache)}
	watermark := uidWatermark(t, fixture)
	for index, store := range cases {
		t.Run(fmt.Sprintf("缓存故障-%d", index), func(t *testing.T) {
			got, err := store.ResolveTikTokIdentityRef(ctx, record)
			if err != nil || got != existing {
				t.Fatalf("缓存故障影响已有用户身份：%v", err)
			}
			fresh := visitor.TikTokIdentityRecord{ApplicationID: "101", OpenIDHash: visitor.TikTokOpenIDHash("101", fmt.Sprintf("uid-fresh-user-%d", index))}
			if got, err := store.ResolveTikTokIdentityRef(ctx, fresh); err == nil || got.ID != "" {
				t.Fatal("缓存故障被静默降级为新用户发号")
			}
		})
	}
	if got := uidWatermark(t, fixture); got != watermark {
		t.Fatalf("Redis 不可用仍预留了 MySQL 号段：%d", got)
	}
}

// TestIDSegmentAllocatorPrefetchFailureRecoveryIntegration 验证失败预取保留剩余号段，共享退避后其他实例可接管补段。
func TestIDSegmentAllocatorPrefetchFailureRecoveryIntegration(t *testing.T) {
	requireUIDIntegration(t)
	fixture := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	setUIDSegment(t, fixture, 1, 10)
	store, observed := observedUIDStore(t, fixture.store.data, fixture.uidClient)
	for id := uint64(1); id <= 7; id++ {
		nextUID(t, store, id)
	}
	if err := fixture.store.data.DB().Exec("CREATE TRIGGER reject_uid_prefetch BEFORE UPDATE ON id_allocators FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'fixture-uid-prefetch-failure'").Error; err != nil {
		t.Fatal(err)
	}
	nextUID(t, store, 8)
	select {
	case <-observed.backedOff:
	case <-time.After(5 * time.Second):
		t.Fatal("失败预取未写入共享退避")
	}
	for id := uint64(9); id <= 10; id++ {
		nextUID(t, store, id)
	}
	if got := uidWatermark(t, fixture); got != 11 {
		t.Fatalf("失败预取改变了 MySQL 水位：%d", got)
	}
	if err := fixture.store.data.DB().Exec("DROP TRIGGER reject_uid_prefetch").Error; err != nil {
		t.Fatal(err)
	}
	other := NewStore(fixture.store.data, newUIDIntegrationCache(t, newUIDIntegrationClient(t)))
	nextUID(t, other, 11)
	if got := uidWatermark(t, fixture); got != 21 {
		t.Fatalf("故障恢复后没有成功接管补段：%d", got)
	}
}
