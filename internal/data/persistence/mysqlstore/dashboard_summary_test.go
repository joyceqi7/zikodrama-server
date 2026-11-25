package mysqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"github.com/DATA-DOG/go-sqlmock"
)

// TestDashboardFreshSummaryUsesOnlyRead 验证已有新鲜汇总时只查询主键行，不开启事务、建表或执行重复插入。
func TestDashboardFreshSummaryUsesOnlyRead(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := newTestStore(db)
	now := time.Date(2026, 10, 6, 1, 18, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT .* FROM `metric_summary_state` WHERE application_id = .* LIMIT").WithArgs("101", 1).
		WillReturnRows(sqlmock.NewRows([]string{"application_id", "refreshed_at", "first_received_at"}).AddRow("101", now.Add(-10*time.Second), nil))
	if err := store.refreshDashboard(application.WithContext(context.Background(), "101"), now); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestDashboardRefreshRechecksAfterLock 验证领取资格的短事务在其他实例已刷新或持有租约时不执行重统计。
func TestDashboardRefreshRechecksAfterLock(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "已有状态不重复插入", true: "并发首次创建使用主键收敛"}[missing], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			store := newTestStore(db)
			// 分表已准备好，测试集中核对状态行及事务行为。
			store.metricTableState.tables = map[string]struct{}{"dashboard_daily_202607": {}, "dashboard_daily_202608": {}, "dashboard_daily_202609": {}, "dashboard_daily_202610": {}}
			now := time.Date(2026, 10, 6, 1, 18, 0, 0, time.UTC)
			rows := sqlmock.NewRows([]string{"application_id", "refreshed_at", "first_received_at"})
			if !missing {
				rows.AddRow("101", now.Add(-2*time.Minute), nil)
			}
			mock.ExpectQuery("SELECT .* FROM `metric_summary_state` WHERE application_id = .* LIMIT").WithArgs("101", 1).WillReturnRows(rows)
			mock.ExpectBegin()
			if missing {
				mock.ExpectExec("INSERT INTO \x60metric_summary_state\x60").WillReturnResult(sqlmock.NewResult(0, 0))
			}
			mock.ExpectExec("UPDATE \x60metric_summary_state\x60").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectCommit()
			if err := store.refreshDashboard(application.WithContext(context.Background(), "101"), now); !errors.Is(err, errDashboardRefreshBusy) {
				t.Fatalf("其他实例已领取时不应启动重统计: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestDashboardRefreshStateReadFailure 验证只读检查失败时保留数据库错误，不把故障当成已刷新或缺少状态行。
func TestDashboardRefreshStateReadFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := newTestStore(db)
	failure := errors.New("summary state database unavailable")
	mock.ExpectQuery("SELECT .* FROM `metric_summary_state` WHERE application_id = .* LIMIT").WithArgs("101", 1).WillReturnError(failure)
	if err := store.refreshDashboard(application.WithContext(context.Background(), "101"), time.Now()); !errors.Is(err, failure) {
		t.Fatalf("状态检查错误未原样返回: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
