package mysqlstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/sharding"
	"drama-server/internal/data/redisstore"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TestDashboardRefreshFencingIntegration 验证计算期间没有状态行锁，过期任务无法覆盖接管者发布的快照。
func TestDashboardRefreshFencingIntegration(t *testing.T) {
	f := newIAAFixture(t)
	if f.uidClient != nil {
		f.store.ConfigureMetricRefresh(redisstore.NewMetricRefreshLock(f.uidClient), nil)
	}
	ctx := application.WithContext(context.Background(), "101")
	db := f.store.data.DB()
	now := time.Now().UTC()
	old := now.Add(-time.Hour)
	if err := db.Create(&metricSummaryState{ApplicationID: "101", RefreshedAt: &old, SnapshotAt: &old}).Error; err != nil {
		t.Fatal(err)
	}
	first, err := f.store.acquireDashboardRefresh(ctx, "101", now, true)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release(ctx)
	if _, err := f.store.acquireDashboardRefresh(ctx, "101", now, true); !errors.Is(err, errDashboardRefreshBusy) {
		t.Fatalf("并发领取未拒绝: %v", err)
	}
	// 任务正在计算时，另一个事务应能立即拿到状态行锁；只读计算不持有它。
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	var state metricSummaryState
	lockErr := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "NOWAIT"}).Where("application_id=?", "101").Take(&state).Error
	_ = tx.Rollback().Error
	if lockErr != nil {
		t.Fatal("刷新计算期仍长期持有状态行锁", lockErr)
	}
	if err := db.Model(&metricSummaryState{}).Where("application_id=?", "101").Update("refresh_lease_until", gorm.Expr("TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6))")).Error; err != nil {
		t.Fatal(err)
	}
	if first.redisHeld {
		// 模拟 Redis 过期或丢失，即使两次 Redis 领取均成功，数据库仍须拒绝旧版本。
		if err := f.uidClient.PExpire(ctx, first.key, -time.Second).Err(); err != nil {
			t.Fatal(err)
		}
	}
	second, err := f.store.acquireDashboardRefresh(ctx, "101", now, true)
	if err != nil {
		t.Fatal(err)
	}
	defer second.release(ctx)
	if second.state.RefreshGeneration <= first.state.RefreshGeneration {
		t.Fatal("接管没有递增发布版本")
	}
	table := metricMonthTable(now)
	if err := f.store.ensureMetricTable(ctx, table, "dashboard_daily_template"); err != nil {
		t.Fatal(err)
	}
	makeResult := func(launches int64) dashboardRefreshResult {
		return dashboardRefreshResult{months: map[string][]dashboardSnapshotModel{table: {{ApplicationID: "101", DashboardCounters: DashboardCounters{Date: now.Format(time.DateOnly), Launches: launches}, UpdatedAt: now}}}}
	}
	if err := f.store.publishDashboard(ctx, second, makeResult(22), now, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := f.store.publishDashboard(ctx, first, makeResult(11), now, now.Add(2*time.Second)); !errors.Is(err, errDashboardRefreshLost) {
		t.Fatalf("旧任务仍能发布: %v", err)
	}
	var row dashboardSnapshotModel
	if err := db.Table(table).Where("application_id=? AND date=?", "101", now.Format(time.DateOnly)).Take(&row).Error; err != nil || row.Launches != 22 {
		t.Fatalf("新结果被覆盖: %+v %v", row, err)
	}
}

// TestMetricActivityIdempotencyAndPublishRaceIntegration 验证重复事实不推进修订，计算期间的新事实不会被旧发布吞掉。
func TestMetricActivityIdempotencyAndPublishRaceIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), 0)
	now := time.Now().UTC()
	firstEvent := domain.MetricEvent{ID: "refresh-race-first", Kind: "launch", OccurredAt: now}
	for range 2 {
		if err := f.store.RecordMetrics(ctx, "70101", []domain.MetricEvent{firstEvent}); err != nil {
			t.Fatal(err)
		}
	}
	var day metricSummaryDayModel
	if err := f.store.data.DB().Where("application_id=? AND date=?", "101", now.Format(time.DateOnly)).Take(&day).Error; err != nil || day.Revision != 1 {
		t.Fatalf("重复上报推进修订: %+v %v", day, err)
	}
	var active int64
	if err := f.store.data.DB().Model(&metricActiveUserModel{}).Where("application_id=?", "101").Count(&active).Error; err != nil || active != 1 {
		t.Fatalf("日活重复: %d %v", active, err)
	}
	lease, err := f.store.acquireDashboardRefresh(ctx, "101", now, false)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release(ctx)
	secondEvent := domain.MetricEvent{ID: "refresh-race-second", Kind: "launch", OccurredAt: now}
	if err := f.store.RecordMetrics(ctx, "70101", []domain.MetricEvent{secondEvent}); err != nil {
		t.Fatal(err)
	}
	result := dashboardRefreshResult{months: map[string][]dashboardSnapshotModel{}, days: []metricSummaryDayModel{day}}
	if err := f.store.publishDashboard(ctx, lease, result, now, now); err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().Where("application_id=? AND date=?", "101", day.Date).Take(&day).Error; err != nil || day.Revision != 2 || day.PublishedRevision != 1 {
		t.Fatalf("并发新事实被确认掉: %+v %v", day, err)
	}
}

// TestMetricRefreshUpgradeIntegration 验证既有状态与原始明细保留，历史日活在首次刷新时补齐。
func TestMetricRefreshUpgradeIntegration(t *testing.T) {
	migration := string(readDeploymentSQL(t, "migrations/20261026_001_metric_refresh.sql"))
	before := strings.Replace(string(readDeploymentSchema(t)), migration, "", 1)
	f := newIAAFixtureWithSchema(t, before, false)
	ctx := application.WithContext(context.Background(), "101")
	now := time.Now().UTC()
	old := now.Add(-2 * time.Hour)
	if err := f.store.data.DB().Exec("INSERT INTO metric_summary_state(application_id,refreshed_at,first_received_at) VALUES (101,?,?)", old, old).Error; err != nil {
		t.Fatal(err)
	}
	table := metricDayTable(now)
	if err := f.store.ensureMetricTable(ctx, table, "metric_events_template"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().Table(table).Create(&metricReceiptModel{ApplicationID: "101", UserID: "70101", EventID: "before-upgrade", Kind: "launch", OccurredAt: now, ReceivedAt: old}).Error; err != nil {
		t.Fatal(err)
	}
	for _, stmt := range strings.Split(migration, ";") {
		if strings.TrimSpace(stmt) != "" {
			if err := f.store.data.DB().Exec(stmt).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	var state metricSummaryState
	if err := f.store.data.DB().Where("application_id=?", "101").Take(&state).Error; err != nil || state.SnapshotAt == nil || !state.SnapshotAt.Equal(old.Truncate(time.Microsecond)) {
		t.Fatalf("升级丢失历史窗口: %+v %v", state, err)
	}
	if err := f.store.refreshDashboard(ctx, now); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := f.store.data.DB().Model(&metricActiveUserModel{}).Where("application_id=?", "101").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("历史日活没有补齐: %d %v", count, err)
	}
	if err := f.store.data.DB().Where("application_id=?", "101").Take(&state).Error; err != nil || state.FirstReceivedAt == nil || !state.FirstReceivedAt.Equal(old.Truncate(time.Microsecond)) {
		t.Fatalf("首次采集时间被改写: %+v %v", state, err)
	}
}

// TestDashboardBusyRefreshProtectsRetentionIntegration 验证有刷新仍在读取时不提前清理九十日事实。
func TestDashboardBusyRefreshProtectsRetentionIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithContext(context.Background(), "101")
	now := time.Now().UTC()
	expired := metricDayTable(metricCutoff(now).AddDate(0, 0, -1))
	if err := f.store.ensureMetricTable(ctx, expired, "metric_events_template"); err != nil {
		t.Fatal(err)
	}
	lease, err := f.store.acquireDashboardRefresh(ctx, "101", now, false)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release(ctx)
	if err := f.store.MaintainMetrics(ctx, now); err != nil {
		t.Fatal(err)
	}
	if !f.store.data.DB().Migrator().HasTable(expired) {
		t.Fatal("正在刷新时清理了旧事实")
	}
	lease.release(ctx)
	if err := f.store.MaintainMetrics(ctx, now); err != nil {
		t.Fatal(err)
	}
	if f.store.data.DB().Migrator().HasTable(expired) {
		t.Fatal("完成刷新后未清理过期事实")
	}
}

// TestDashboardRedisFailureFallsBackIntegration 验证 Redis 故障时数据库短租约仍能阻止重复领取。
func TestDashboardRedisFailureFallsBackIntegration(t *testing.T) {
	f := newIAAFixture(t)
	f.store.ConfigureMetricRefresh(dashboardLockStub{acquireErr: errors.New("test Redis unavailable")}, nil)
	ctx := application.WithContext(context.Background(), "101")
	now := time.Now().UTC()
	lease, err := f.store.acquireDashboardRefresh(ctx, "101", now, false)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release(ctx)
	if lease.redisHeld {
		t.Fatal("故障 Redis 被误记为已持锁")
	}
	if _, err := f.store.acquireDashboardRefresh(ctx, "101", now, true); !errors.Is(err, errDashboardRefreshBusy) {
		t.Fatalf("故障降级未阻止重复刷新: %v", err)
	}
}
