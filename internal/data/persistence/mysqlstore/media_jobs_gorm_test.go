package mysqlstore

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"drama-server/internal/biz/operations"
)

func TestClaimMediaJobLeaseUsesConditionalGORMUpdate(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := newTestStore(database)
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.MustCompile(`UPDATE .media_jobs. SET .+ WHERE \(id = \? AND status = \? AND updated_at = \?\) AND next_attempt_at <= \?`).String()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	claimed, err := store.ClaimMediaJobLease(context.Background(), operations.MediaJobLeaseClaim{
		ID: "1013", ExpectedStatus: operations.MediaJobQueued, ExpectedUpdatedAt: now.Add(-time.Second),
		LeaseOwner: "worker-a", ClaimedAt: now, LeaseExpiresAt: now.Add(time.Minute),
	})
	if err != nil || !claimed {
		t.Fatalf("lease claim failed: claimed=%v err=%v", claimed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
