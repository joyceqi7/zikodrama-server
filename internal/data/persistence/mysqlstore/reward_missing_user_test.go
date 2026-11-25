package mysqlstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	consumer "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/sharding"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/gorm"
)

// TestGrantRewardMissingUserIsRetryable 防止用户路由异常被误判为永久奖励拒绝。
func TestGrantRewardMissingUserIsRetryable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := newTestStore(db)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `users_07` WHERE bucket_id = .* LIMIT ").WithArgs(727, "101", "42", 1).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), 727)
	_, err = store.GrantReward(ctx, monetization.RewardGrant{VisitorID: "42", ClaimID: "missing-user-claim"}, time.Now())
	if !errors.Is(err, consumer.ErrServiceUnavailable) || !errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, monetization.ErrInvalidRewardClaim) {
		t.Fatalf("用户记录缺失必须保留可重试故障及原始原因: %v", err)
	}
	if !strings.Contains(err.Error(), "bucket 727") {
		t.Fatal("缺少桶路由诊断")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
