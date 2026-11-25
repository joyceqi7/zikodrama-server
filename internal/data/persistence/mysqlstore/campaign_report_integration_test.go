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

// TestCampaignReportIntegration 验证奖励去重、跨应用隔离、分页及菜单独立授权。
func TestCampaignReportIntegration(t *testing.T) {
	f := newIAAFixture(t)
	db := f.store.data.DB()
	ctx := application.WithSubject(context.Background(), "1")
	if err := db.Table("admin_permissions").Where("code=?", "menu.campaign_report").Update("name", "自定义链接报表").Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := db.Exec(string(readDeploymentSQL(t, "migrations/20261001_002_campaign_report_menu.sql"))).Error; err != nil {
			t.Fatal(err)
		}
	}
	var menu struct{ Name string }
	if err := db.Table("admin_permissions").Select("name").Where("code=?", "menu.campaign_report").Scan(&menu).Error; err != nil || menu.Name != "自定义链接报表" {
		t.Fatalf("菜单幂等错误: %+v %v", menu, err)
	}
	var nodes int64
	if err := db.Table("admin_permissions").Where("code IN ?", []string{"menu.campaign_report", "operations.campaign_report.read"}).Count(&nodes).Error; err != nil || nodes != 2 {
		t.Fatalf("节点错误: %d %v", nodes, err)
	}
	for i := 0; i < 4; i++ {
		app, status := "101", "active"
		if i == 1 {
			app = "102"
		}
		if i == 2 {
			status = "disabled"
		}
		if err := db.Exec("INSERT INTO application_campaigns (id,application_id,name,code,landing_url,minis_path,drama_id,episode_id,status) VALUES (?,?,?,?,?,'fixture',9901,99011,?)", 8801+i, app, fmt.Sprintf("报表投放%d", i), fmt.Sprintf("report-code-%d", i), "https://example.com/fixture", status).Error; err != nil {
			t.Fatal(err)
		}
	}
	from := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	for i, row := range []struct {
		app, user string
		campaign  any
		kind      string
		at        time.Time
	}{
		{"101", "12001", 8801, "episode_unlock", from},
		{"101", "12001", 8801, "vip_progress", from.Add(time.Hour)},
		{"101", "12002", 8801, "episode_unlock", from},
		{"102", "12001", 8802, "episode_unlock", from},
		{"101", "12001", 8803, "vip_progress", from},
		{"102", "12003", 8801, "episode_unlock", from},
		{"101", "12004", nil, "episode_unlock", from},
		{"101", "12005", 8801, "episode_unlock", from.Add(-time.Second)},
		{"101", "12006", 8801, "episode_unlock", from.AddDate(0, 0, 2)},
	} {
		if err := db.Exec("INSERT INTO "+userTable("reward_grants", uint16(i))+" (bucket_id,claim_id,application_id,campaign_id,visitor_id,drama_id,reward_type,ad_unit_id,granted_at) VALUES (?,?,?,?,?,9901,?,'fixture',?)", i, fmt.Sprintf("campaign-report-%d", i), row.app, row.campaign, row.user, row.kind, row.at).Error; err != nil {
			t.Fatal(err)
		}
	}
	input := operations.CampaignReportQuery{From: from, To: from.AddDate(0, 0, 2), Query: "报表投放", Page: 1, PageSize: 2, Sort: "rewardCompletions", Direction: "desc"}
	result, err := f.store.CampaignReport(ctx, input)
	if err != nil || result.Total != 4 || len(result.Items) != 2 {
		t.Fatalf("分页错误: %+v %v", result, err)
	}
	if result.Summary.RewardCompletions != 5 || result.Summary.RewardUsers != 3 || result.Summary.EpisodeRewards != 3 || result.Summary.VIPRewards != 2 {
		t.Fatalf("跨链接范围汇总没有独立去重: %+v", result.Summary)
	}
	if result.Unattributed.RewardCompletions != 2 || result.Unattributed.RewardUsers != 2 {
		t.Fatalf("未归因奖励遗漏: %+v", result.Unattributed)
	}
	row := result.Items[0]
	if row.CampaignID != "8801" || row.RewardUsers != 2 || row.RewardCompletions != 3 || row.EpisodeRewards != 2 || row.VIPRewards != 1 || row.AverageRewards == nil || *row.AverageRewards != 1.5 {
		t.Fatalf("指标错误: %+v", row)
	}
	input.Page = 2
	second, err := f.store.CampaignReport(ctx, input)
	if err != nil || len(second.Items) != 2 || second.Items[0].CampaignID != "8803" || second.Items[0].RewardCompletions != 1 || second.Items[1].AverageRewards != nil {
		t.Fatalf("停用及零奖励错误: %+v %v", second, err)
	}
	input.Page = 1
	input.PageSize = 20
	input.Query = "REPORT-CODE-0"
	single, err := f.store.CampaignReport(application.WithContext(ctx, "101"), input)
	if err != nil || single.Total != 1 || single.Items[0].RewardUsers != 2 {
		t.Fatalf("搜索错误: %+v %v", single, err)
	}
	isolated, err := f.store.CampaignReport(application.WithContext(ctx, "102"), input)
	if err != nil || isolated.Total != 0 {
		t.Fatalf("应用隔离错误: %+v %v", isolated, err)
	}
	input.Query = "报表投放"
	input.Sort = "averageRewards"
	input.Direction = "asc"
	sorted, err := f.store.CampaignReport(ctx, input)
	if err != nil || sorted.Items[0].CampaignID != "8802" || sorted.Items[3].CampaignID != "8804" {
		t.Fatalf("平均值空值排序错误: %+v %v", sorted, err)
	}
	input.Sort = "rewardUsers"
	input.Direction = "desc"
	sorted, err = f.store.CampaignReport(ctx, input)
	if err != nil || sorted.Items[0].CampaignID != "8801" {
		t.Fatalf("人数排序错误: %+v %v", sorted, err)
	}
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	raw := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var login adminrbac.LoginResult
	if err := json.Unmarshal(raw, &login); err != nil {
		t.Fatal(err)
	}
	q := "?scope=all&from=" + from.Format(time.DateOnly) + "&to=" + from.AddDate(0, 0, 1).Format(time.DateOnly) + "&query=report-code-0"
	raw = iaaRequest(t, server.URL, "GET", "/admin/v1/campaign-report"+q, login.Token, "", nil, 200)
	var actual operations.CampaignReportPage
	if err := json.Unmarshal(raw, &actual); err != nil || actual.Total != 1 || actual.Items[0].RewardUsers != 2 {
		t.Fatalf("HTTP 合同错误: %s %v", raw, err)
	}
	for _, suffix := range []string{"&sort=invalid", "&direction=invalid", "&page=0", "&pageSize=101", "&page=invalid", "&range=all"} {
		iaaRequest(t, server.URL, "GET", "/admin/v1/campaign-report"+q+suffix, login.Token, "", nil, 400)
	}
	iaaRequest(t, server.URL, "GET", "/admin/v1/campaign-report"+q, "", "", nil, 401)
	if err := db.Table("admin_users").Where("username=?", "iaa-admin").Update("is_super", false).Error; err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, "GET", "/admin/v1/campaign-report"+q, login.Token, "", nil, 403)
}
