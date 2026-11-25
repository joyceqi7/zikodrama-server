package mysqlstore

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestNonUIDModelsReturnAutoIncrementStrings 验证五类分表写入省略主键，并精确回填超过 JavaScript 安全整数范围的字符串。
func TestNonUIDModelsReturnAutoIncrementStrings(t *testing.T) {
	grant := entitlementGrantModel{}
	state := entitlementStateModel{}
	revocation := entitlementRevocationModel{}
	reward := rewardGrantModel{}
	progress := vipProgressModel{}
	cases := []struct {
		table string
		model any
		id    *string
	}{
		{"entitlement_grants_07", &grant, &grant.ID},
		{"user_entitlement_state_07", &state, &state.ID},
		{"entitlement_revocations_07", &revocation, &revocation.ID},
		{"reward_grants_07", &reward, &reward.ID},
		{"application_vip_progress_07", &progress, &progress.ID},
	}
	for _, tc := range cases {
		t.Run(tc.table, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			store := newTestStore(db)
			mock.ExpectBegin()
			// ID 是模型首字段；默认自增插入应直接从 bucket_id 开始，不能把空字符串作为主键写入。
			mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `" + tc.table + "` (`bucket_id`,")).
				WillReturnResult(sqlmock.NewResult(9007199254740993, 1))
			mock.ExpectCommit()
			if err := store.data.DB().Table(tc.table).Create(tc.model).Error; err != nil {
				t.Fatalf("分表自增插入失败: %v", err)
			}
			if *tc.id != "9007199254740993" {
				t.Fatalf("大整数主键未按字符串完整回填: %q", *tc.id)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
