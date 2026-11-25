package mysqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestMetricActiveUsersReadyDaysSkipRawEvents 验证今日、昨日与历史日完成初始化后均不再扫描原始事件。
func TestMetricActiveUsersReadyDaysSkipRawEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := newTestStore(db)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	from := now.Truncate(24*time.Hour).AddDate(0, 0, -2)
	to := from.AddDate(0, 0, 3)
	tables := []string{"metric_events_20261024", "metric_events_20261025", "metric_events_20261026"}
	states := sqlmock.NewRows([]string{"application_id", "date", "activity_ready"})
	for day := from; day.Before(to); day = day.AddDate(0, 0, 1) {
		states.AddRow("101", day.Format(time.DateOnly), true)
	}
	mock.ExpectQuery("SELECT .* FROM `metric_summary_days`").WithArgs("101", "2026-10-24", "2026-10-27").WillReturnRows(states)
	if err := store.prepareMetricActiveUsers(context.Background(), "101", tables, from, to, false); err != nil {
		t.Fatalf("已初始化日期仍查询原始事件: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestMetricActiveUsersBackfillAndRebuild 验证未初始化日期和显式重建仍补齐日活，再标记初始化完成。
func TestMetricActiveUsersBackfillAndRebuild(t *testing.T) {
	for _, rebuild := range []bool{false, true} {
		name := "首次初始化"
		if rebuild {
			name = "显式重建"
		}
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			store := newTestStore(db)
			from := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
			to := from.AddDate(0, 0, 1)
			mock.ExpectQuery("SELECT .* FROM `metric_summary_days`").WillReturnRows(
				sqlmock.NewRows([]string{"application_id", "date", "activity_ready"}).AddRow("101", "2026-10-24", rebuild))
			mock.ExpectQuery("SELECT DISTINCT user_id FROM `metric_events_20261024`").WithArgs("101").WillReturnRows(
				sqlmock.NewRows([]string{"user_id"}).AddRow("70101").AddRow("70102"))
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `metric_active_users_daily`").WithArgs("101", "2026-10-24", "70101", "101", "2026-10-24", "70102").WillReturnResult(sqlmock.NewResult(0, 2))
			mock.ExpectCommit()
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `metric_summary_days`").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			if err := store.prepareMetricActiveUsers(context.Background(), "101", []string{"metric_events_20261024"}, from, to, rebuild); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestMetricActiveUsersFailedBackfillRemainsUnready 验证回源失败保留错误且不提前确认日活，允许下轮重试。
func TestMetricActiveUsersFailedBackfillRemainsUnready(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := newTestStore(db)
	from := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	failure := errors.New("metric activity source unavailable")
	mock.ExpectQuery("SELECT .* FROM `metric_summary_days`").WillReturnRows(sqlmock.NewRows([]string{"application_id", "date", "activity_ready"}))
	mock.ExpectQuery("SELECT DISTINCT user_id FROM `metric_events_20261026`").WithArgs("101").WillReturnError(failure)
	if err := store.prepareMetricActiveUsers(context.Background(), "101", []string{"metric_events_20261026"}, from, to, false); !errors.Is(err, failure) {
		t.Fatalf("回源错误未原样传播: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
