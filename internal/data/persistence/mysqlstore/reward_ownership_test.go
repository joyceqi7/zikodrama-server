package mysqlstore

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"drama-server/internal/biz/monetization"
	"github.com/DATA-DOG/go-sqlmock"
)

// TestRewardOwnershipReadsOnlyRequestedDrama 验证索引目标范围与整剧、VIP、有效期的既有鉴权语义。
func TestRewardOwnershipReadsOnlyRequestedDrama(t *testing.T) {
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		kind string
		data string
		end  any
		want map[string]bool
	}{
		{"逐集仅返回所需集合", "episode_access", `{"schemaVersion":1,"dramaId":"d1","episodeIds":["e1","e9"]}`, nil, map[string]bool{"e1": true}},
		{"整剧固定范围", "drama_access", `{"schemaVersion":1,"dramaId":"d1","episodeIds":["e1"]}`, nil, map[string]bool{"e1": true}},
		{"整剧包含后续更新", "drama_access", `{"schemaVersion":1,"dramaId":"d1","episodeIds":["old"],"includeFutureEpisodes":true}`, nil, map[string]bool{"e1": true, "e2": true}},
		{"目录VIP", "vip", `{"schemaVersion":1,"scope":"application"}`, nil, map[string]bool{"e1": true, "e2": true}},
		{"其他剧不能借用", "drama_access", `{"schemaVersion":1,"dramaId":"d2","includeFutureEpisodes":true}`, nil, map[string]bool{}},
		{"到期瞬间不可访问", "vip", `{"schemaVersion":1,"scope":"application"}`, now, map[string]bool{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			store := newTestStore(db)
			rows := sqlmock.NewRows([]string{"grant_id", "application_id", "visitor_id", "type", "data", "valid_from", "valid_until", "created_at"}).
				AddRow("11", "app", "user", tc.kind, tc.data, now.Add(-time.Hour), tc.end, now.Add(-time.Hour))
			mock.ExpectQuery("SELECT .* FROM `user_entitlement_state_00` WHERE .*target_key IN").
				WithArgs(0, "app", "user", now, now, "application", "drama:d1", "episode:d1:e1", "episode:d1:e2").WillReturnRows(rows)
			got, err := rewardOwnedEpisodeIDsDB(store.data.DB(), 0, "app", "user", "d1", []string{"e1", "e2", "e1"}, now)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("权益覆盖不符: got=%v want=%v err=%v", got, tc.want, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestRewardOwnershipSkipsEmptyAndPropagatesFailures 验证无需求零查询，查询及 JSON 故障不能变成未拥有。
func TestRewardOwnershipSkipsEmptyAndPropagatesFailures(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := newTestStore(db)
	now := time.Now().UTC()
	if got, err := rewardOwnedEpisodeIDsDB(store.data.DB(), 0, "app", "user", "d1", nil, now); err != nil || len(got) != 0 {
		t.Fatalf("空需求不应读库: %v %v", got, err)
	}
	failure := errors.New("reward ownership database unavailable")
	mock.ExpectQuery("SELECT .* FROM `user_entitlement_state_00`").WillReturnError(failure)
	if _, err := rewardOwnedEpisodeIDsDB(store.data.DB(), 0, "app", "user", "d1", []string{"e1"}, now); !errors.Is(err, failure) {
		t.Fatalf("查询错误未传播: %v", err)
	}
	mock.ExpectQuery("SELECT .* FROM `user_entitlement_state_00`").WillReturnRows(sqlmock.NewRows([]string{"grant_id", "data"}).AddRow("11", "{"))
	if _, err := rewardOwnedEpisodeIDsDB(store.data.DB(), 0, "app", "user", "d1", []string{"e1"}, now); err == nil {
		t.Fatal("损坏数据不能按未拥有处理")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestRewardModelStoresOnlySettledFacts 验证新记录不保存规则条款，历史条款损坏不妨碍实际奖励恢复。
func TestRewardModelStoresOnlySettledFacts(t *testing.T) {
	model, err := newRewardGrantModel(monetization.RewardGrant{
		ClaimID: "claim", ApplicationID: "app", VisitorID: "user", UnlockEpisodeIDs: []string{"e1"},
		BuildRule: &monetization.UnlockRule{Code: "latest"},
	})
	if err != nil || string(model.RuleSnapshotJSON) != "null" {
		t.Fatalf("不应保存本次构造规则: %s %v", model.RuleSnapshotJSON, err)
	}
	model.RuleSnapshotJSON = []byte("historical-unused-rule")
	grant, err := model.domain()
	if err != nil || !reflect.DeepEqual(grant.UnlockEpisodeIDs, []string{"e1"}) || grant.BuildRule != nil {
		t.Fatalf("应恢复实际事实而非历史规则: %+v %v", grant, err)
	}
}
