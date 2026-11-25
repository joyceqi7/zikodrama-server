package mysqlstore

import (
	"context"
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRewardAnalysisIntegration 覆盖所有应用共享剧合并、自然流量、VIP 拆分、目标集及范围去重。
func TestRewardAnalysisIntegration(t *testing.T) {
	f := newIAAFixture(t)
	db := f.store.data.DB()
	from := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	to := from.AddDate(0, 0, 2)
	for _, row := range []struct{ id, app string }{{"9801", "101"}, {"9802", "102"}} {
		if err := db.Exec("INSERT INTO application_campaigns (id,application_id,code,name,landing_url,minis_path,drama_id,episode_id,status) VALUES (?,?,?,?,'https://example.test/link','/',201,301,'disabled')", row.id, row.app, "report_"+row.id, "测试链接"+row.id).Error; err != nil {
			t.Fatal(err)
		}
	}
	rows := []struct {
		shard          uint16
		app            string
		campaign       any
		user           string
		drama, episode any
		kind           string
		at             time.Time
	}{
		{0, "101", "9801", "11001", "201", "301", "episode_unlock", from},
		{0, "101", "9801", "11001", "201", "302", "episode_unlock", from.AddDate(0, 0, 1)},
		{15, "102", "9802", "11001", "201", "301", "episode_unlock", from},
		{1, "101", nil, "11003", "201", "301", "episode_unlock", from},
		{2, "101", "9801", "11004", nil, nil, "vip_progress", from},
		{3, "102", "9802", "11005", nil, nil, "vip_progress", from},
		{4, "101", nil, "11006", "201", "301", "episode_unlock", to},
		{5, "101", nil, "11007", "201", "301", "episode_unlock", from.Add(-time.Second)},
		{6, "101", nil, "11008", "201", nil, "vip_progress", from},
	}
	for i, row := range rows {
		if err := db.Exec("INSERT INTO "+userTable("reward_grants", row.shard)+" (bucket_id,claim_id,application_id,campaign_id,visitor_id,drama_id,episode_id,reward_type,ad_unit_id,granted_at) VALUES (?,?,?,?,?,?,?,?,'test',?)", row.shard, fmt.Sprintf("analysis-%d", i), row.app, row.campaign, row.user, row.drama, row.episode, row.kind, row.at).Error; err != nil {
			t.Fatal(err)
		}
	}
	result, err := f.store.RewardAnalysis(application.WithSubject(context.Background(), "1"), from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Dramas) != 1 || result.Dramas[0].DramaID != "201" || result.Dramas[0].RewardCompletions != 4 || result.Dramas[0].RewardUsers != 3 {
		t.Fatalf("共享剧合并错误：%+v", result)
	}
	if len(result.Campaigns) != 2 || result.Campaigns[0].ApplicationID != "101" || result.Campaigns[0].EpisodeRewards != 2 || result.Campaigns[0].VIPRewards != 1 || result.Campaigns[0].RewardUsers != 2 || result.UnassignedVIP.RewardCompletions != 2 || result.UnassignedVIP.RewardUsers != 2 {
		t.Fatalf("链接及 VIP 归属错误：%+v", result)
	}
	single, err := f.store.RewardAnalysis(application.WithContext(application.WithSubject(context.Background(), "1"), "102"), from, to)
	if err != nil || len(single.Dramas) != 1 || single.Dramas[0].RewardCompletions != 1 || len(single.Campaigns) != 1 || single.Campaigns[0].ApplicationID != "102" {
		t.Fatalf("单应用隔离错误：%+v %v", single, err)
	}
	episodes, err := f.store.RewardEpisodes(application.WithSubject(context.Background(), "1"), "201", from, to)
	if err != nil || len(episodes) != 2 || episodes[0].EpisodeNumber != 1 || episodes[0].RewardCompletions != 3 || episodes[1].EpisodeNumber != 2 {
		t.Fatalf("分集明细错误：%+v %v", episodes, err)
	}
	trend, err := f.store.RewardTrend(application.WithSubject(context.Background(), "1"), "drama", "201", from, to)
	if err != nil || len(trend.Days) != 2 || trend.Total.RewardCompletions != 4 || trend.Total.RewardUsers != 3 || trend.Days[0].RewardUsers+trend.Days[1].RewardUsers != 4 {
		t.Fatalf("趋势不能累加日去重人数：%+v %v", trend, err)
	}
	campaignTrend, err := f.store.RewardTrend(application.WithContext(application.WithSubject(context.Background(), "1"), "101"), "campaign", "9802", from, to)
	if err != nil || campaignTrend.Total.RewardCompletions != 0 {
		t.Fatalf("趋势跨应用泄漏：%+v %v", campaignTrend, err)
	}
	// 归档集序只是避让值，不能把绝对值伪装成原集数。
	if err := db.Model(&episodeModel{}).Where("id=?", "301").Update("episode_number", -10).Error; err != nil {
		t.Fatal(err)
	}
	archived, err := f.store.RewardEpisodes(application.WithSubject(context.Background(), "1"), "201", from, to)
	if err != nil || len(archived) != 2 || archived[0].EpisodeNumber != 0 || archived[0].RewardCompletions != 3 {
		t.Fatalf("历史集序被错误还原：%+v %v", archived, err)
	}
	if err := db.Model(&episodeModel{}).Where("id=?", "301").Update("episode_number", 1).Error; err != nil {
		t.Fatal(err)
	}
	// 使用真实认证中间件确认所有应用不需要应用头，普通应用资源仍要求应用头。
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	raw := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var login adminrbac.LoginResult
	if err := json.Unmarshal(raw, &login); err != nil {
		t.Fatal(err)
	}
	query := "?scope=all&range=range&from=" + from.Format(time.DateOnly) + "&to=" + to.AddDate(0, 0, -1).Format(time.DateOnly)
	raw = iaaRequest(t, server.URL, "GET", "/admin/v1/reward-analysis"+query, login.Token, "", nil, 200)
	var actual operations.RewardAnalysis
	if err := json.Unmarshal(raw, &actual); err != nil || len(actual.Campaigns) != 2 {
		t.Fatalf("所有应用 HTTP 合同错误：%s %v", raw, err)
	}
	iaaRequest(t, server.URL, "GET", "/admin/v1/reward-analysis"+query, "", "", nil, 401)
	iaaRequest(t, server.URL, "GET", "/admin/v1/reward-analysis?scope=application&from="+from.Format(time.DateOnly)+"&to="+from.Format(time.DateOnly), login.Token, "", nil, 400)
	iaaRequest(t, server.URL, "GET", "/admin/v1/dashboard?from="+from.Format(time.DateOnly)+"&to="+from.Format(time.DateOnly), login.Token, "", nil, 400)
	// 登录有效但没有运营数据操作权限时，所有小程序报表也必须拒绝。
	if err := db.Table("admin_users").Where("username=?", "iaa-admin").Update("is_super", false).Error; err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, "GET", "/admin/v1/reward-analysis"+query, login.Token, "", nil, 403)

}
