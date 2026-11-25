package mysqlstore

import (
	"context"
	"errors"
	"testing"

	"drama-server/internal/biz/sharding"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/gorm"
)

func TestUserTransactionLocksAuthoritativeRowAndRollsBack(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "提交"
		if fail {
			name = "业务失败回滚"
		}
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store := newTestStore(db)
			ctx := sharding.WithBucketID(context.Background(), 0)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT .* FROM `users_00` .*FOR UPDATE").WithArgs(0, "app", "user", 1).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("user"))
			if fail {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}
			failure := errors.New("业务写入失败")
			called := false
			err = store.userTransaction(ctx, "app", "user", func(*gorm.DB) error {
				called = true
				if fail {
					return failure
				}
				return nil
			})
			if !called || (fail && !errors.Is(err, failure)) || (!fail && err != nil) {
				t.Fatalf("业务回调或事务结果异常: called=%t err=%v", called, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
