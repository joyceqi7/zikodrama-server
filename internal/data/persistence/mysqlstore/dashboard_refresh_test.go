package mysqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"github.com/DATA-DOG/go-sqlmock"
)

// dashboardLockStub 注入 Redis 不可用或租约丢失，不替代数据库的发布校验。
type dashboardLockStub struct {
	acquireErr error
	renewOK    bool
	renewErr   error
}

// Acquire 模拟 Redis 故障，验证数据库租约仍能保护刷新。
func (l dashboardLockStub) Acquire(context.Context, string, string, time.Duration) (bool, error) {
	return l.acquireErr == nil, l.acquireErr
}

// Renew 模拟网络错误或持有者已失效。
func (l dashboardLockStub) Renew(context.Context, string, string, time.Duration) (bool, error) {
	return l.renewOK, l.renewErr
}

// Release 不访问外部环境。
func (l dashboardLockStub) Release(context.Context, string, string) error { return nil }

// TestDashboardPublishLostLeaseDoesNotWrite 验证发布资格失败会回滚，不写任何汇总或确认日修订。
func TestDashboardPublishLostLeaseDoesNotWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := newTestStore(db)
	lease := &dashboardRefreshLease{store: store, owner: "old-owner", state: metricSummaryState{ApplicationID: "101", RefreshGeneration: 1}}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `metric_summary_state`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	result := dashboardRefreshResult{months: map[string][]dashboardSnapshotModel{"dashboard_daily_202610": {{ApplicationID: "101"}}}, days: []metricSummaryDayModel{{ApplicationID: "101", Date: "2026-10-25", Revision: 1}}}
	if err := store.publishDashboard(context.Background(), lease, result, time.Now(), time.Now()); !errors.Is(err, errDashboardRefreshLost) {
		t.Fatalf("旧资格未拒绝: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestDashboardStaleReadUsesSnapshot 验证过期快照不触发租约、DDL 或事件扫描，保持页面查询可用。
func TestDashboardStaleReadUsesSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := newTestStore(db)
	now := time.Now().UTC()
	from := now.Truncate(24 * time.Hour)
	mock.ExpectQuery("SELECT .* FROM `metric_summary_state`").WillReturnRows(sqlmock.NewRows([]string{"application_id", "refreshed_at"}).AddRow("101", now.Add(-time.Hour)))
	mock.ExpectQuery("SELECT .* FROM `information_schema`.`tables`").WithArgs("dashboard_daily_%").WillReturnRows(sqlmock.NewRows([]string{"table_name"}).AddRow(metricMonthTable(now)))
	mock.ExpectQuery("SELECT .* FROM `" + metricMonthTable(now) + "`").WillReturnRows(sqlmock.NewRows([]string{"application_id", "date", "launches"}).AddRow("101", from.Format(time.DateOnly), 7))
	rows, err := store.DashboardDays(application.WithContext(context.Background(), "101"), from, from.AddDate(0, 0, 1))
	if err != nil || len(rows) != 1 || rows[0].Launches != 7 {
		t.Fatalf("过期快照查询失败: %+v %v", rows, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestDashboardRedisRenewalLossStopsBeforeDatabase 验证失去 Redis 持有者后不再续期数据库资格。
func TestDashboardRedisRenewalLossStopsBeforeDatabase(t *testing.T) {
	for _, failure := range []error{nil, errors.New("redis renewal unavailable")} {
		store := &Store{metricRefreshLock: dashboardLockStub{renewErr: failure}}
		lease := &dashboardRefreshLease{store: store, redisHeld: true, key: "test", owner: "owner"}
		err := lease.renew(context.Background())
		if failure == nil && !errors.Is(err, errDashboardRefreshLost) {
			t.Fatalf("Redis 丢锁未停止: %v", err)
		}
		if failure != nil && !errors.Is(err, failure) {
			t.Fatalf("Redis 错误丢失: %v", err)
		}
	}
}

// TestDashboardUnchangedDaysSkipRawEvents 验证修订未变化时不扫描原始事件，仅从精确日活集合计算人数。
func TestDashboardUnchangedDaysSkipRawEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := newTestStore(db)
	from := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	mock.ExpectQuery("SELECT .* FROM `metric_summary_days`").WillReturnRows(sqlmock.NewRows([]string{"application_id", "date", "revision", "published_revision", "behavior_ready"}).AddRow("101", "2026-10-25", 2, 2, true))
	mock.ExpectQuery("SELECT activity_date,user_id FROM `metric_active_users_daily`").WillReturnRows(sqlmock.NewRows([]string{"activity_date", "user_id"}).AddRow("2026-10-25", "70101").AddRow("2026-10-25", "70102"))
	days, mau, _, ack, err := dashboardRefreshBehavior(store.data.DB().WithContext(context.Background()), "101", []string{"metric_events_20261025"}, from, to, from, from, false)
	if err != nil || len(days) != 0 || len(ack) != 0 || mau["2026-10-25"] != 2 {
		t.Fatalf("未变化日期仍重算或人数错误: days=%v mau=%v ack=%v err=%v", days, mau, ack, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestDashboardExpiredCompletionLookupFailure 验证过期完成关联失败时保留具体错误，不返回可发布的部分汇总。
func TestDashboardExpiredCompletionLookupFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := newTestStore(db)
	from := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	failure := errors.New("expired metric completion lookup unavailable")
	mock.ExpectQuery("SELECT .* FROM `metric_summary_days`").WillReturnRows(sqlmock.NewRows([]string{"application_id", "date", "revision", "published_revision", "behavior_ready"}).AddRow("101", "2026-10-25", 2, 2, true))
	mock.ExpectQuery("SELECT DISTINCT origin.event_date FROM .*expired").
		WithArgs("101", "play_complete", "rewarded_complete", "2026-10-24", "2026-10-25", "2026-10-25", "2026-10-26").WillReturnError(failure)
	days, mau, first, ack, err := dashboardRefreshBehavior(store.data.DB().WithContext(context.Background()), "101", []string{"metric_events_20261025"}, from, to, from, from.AddDate(0, 0, -1), false)
	if !errors.Is(err, failure) || days != nil || mau != nil || first != nil || ack != nil {
		t.Fatalf("过期完成关联错误丢失或返回了部分结果: days=%v mau=%v first=%v ack=%v err=%v", days, mau, first, ack, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestDashboardLeaseUsesCoordinationPool 验证续期不占用已经被重统计事务使用的连接池。
func TestDashboardLeaseUsesCoordinationPool(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coord := newTestStore(db)
	store := &Store{metricCoordData: coord.data}
	lease := &dashboardRefreshLease{store: store, owner: "owner", state: metricSummaryState{ApplicationID: "101", RefreshGeneration: 1}}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `metric_summary_state`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := lease.renew(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
