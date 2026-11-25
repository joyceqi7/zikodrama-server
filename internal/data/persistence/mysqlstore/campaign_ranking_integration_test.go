package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"fmt"
	"testing"
	"time"
)

// TestCampaignRewardRankingIntegration 验证跨分表累计、用户去重、应用隔离、时间边界和停用历史。
func TestCampaignRewardRankingIntegration(t *testing.T) {
	f := newIAAFixture(t)
	db := f.store.data.DB()
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	for _, row := range []struct{ id, app, name, status string }{{"901", "101", "最多奖励", "disabled"}, {"902", "101", "同分靠前", "active"}, {"903", "101", "同分靠后", "active"}, {"904", "102", "另一应用", "active"}} {
		if err := db.Exec("INSERT INTO application_campaigns (id,application_id,code,name,landing_url,minis_path,drama_id,episode_id,status,created_at,updated_at) VALUES (?,?,?,?,'https://example.test/link','/',5008,5014,?,?,?)", row.id, row.app, "rank_"+row.id, row.name, row.status, from, from).Error; err != nil {
			t.Fatal(err)
		}
	}
	rows := []struct {
		shard               uint16
		app, campaign, user string
		at                  time.Time
	}{
		{0, "101", "901", "1001", from}, {15, "101", "901", "1002", from}, {0, "101", "901", "1001", from},
		{1, "101", "902", "1003", from}, {2, "101", "903", "1004", from},
		{3, "102", "904", "1005", from}, {4, "102", "901", "1006", from},
		{5, "101", "902", "1007", to}, {6, "101", "902", "1008", from.Add(-time.Second)},
	}
	for i, row := range rows {
		if err := db.Exec("INSERT INTO "+userTable("reward_grants", row.shard)+" (bucket_id,claim_id,application_id,campaign_id,visitor_id,ad_unit_id,granted_at) VALUES (?,?,?,?,?,'test',?)", row.shard, fmt.Sprintf("rank-%d", i), row.app, row.campaign, row.user, row.at).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("INSERT INTO reward_grants_00 (bucket_id,claim_id,application_id,visitor_id,ad_unit_id,granted_at) VALUES (0,'natural',101,1009,'test',?)", from).Error; err != nil {
		t.Fatal(err)
	}
	ctx := application.WithContext(context.Background(), "101")
	result, err := f.store.CampaignRewardRanking(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 3 || result[0].CampaignID != "901" || result[0].RewardCompletions != 3 || result[0].RewardUsers != 2 || result[1].CampaignID != "902" || result[1].RewardCompletions != 1 || result[2].CampaignID != "903" {
		t.Fatalf("排行错误：%+v", result)
	}
	all, err := f.store.CampaignRewardRanking(ctx, from.AddDate(-1, 0, 0), to.AddDate(0, 0, 1))
	if err != nil || len(all) != 3 || all[0].RewardCompletions != 3 || all[1].RewardCompletions != 3 {
		t.Fatalf("历史排行错误：%+v %v", all, err)
	}
	other, err := f.store.CampaignRewardRanking(application.WithContext(context.Background(), "102"), from, to)
	if err != nil || len(other) != 1 || other[0].CampaignID != "904" {
		t.Fatalf("应用隔离错误：%+v %v", other, err)
	}
}
