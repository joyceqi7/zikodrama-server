package mysqlstore

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestEnsureMetricTableCachesSuccessfulDDL 验证同一进程只为同一指标分表执行一次幂等 DDL。
func TestEnsureMetricTableCachesSuccessfulDDL(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := newTestStore(db)
	statement := "CREATE TABLE IF NOT EXISTS `metric_events_20260920` LIKE `metric_events_template`"
	mock.ExpectExec(regexp.QuoteMeta(statement)).WillReturnResult(sqlmock.NewResult(0, 0))
	for range 2 {
		if err := store.ensureMetricTable(context.Background(), "metric_events_20260920", "metric_events_template"); err != nil {
			t.Fatal(err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
