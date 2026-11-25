package mysqlstore

import (
	"context"
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

// TestDramaReportIntegration 验证分页、去重、VIP 排除、日期边界、空指标和独立接口权限。
func TestDramaReportIntegration(t *testing.T) {
	f := newIAAFixture(t)
	db := f.store.data.DB()
	ctx := application.WithSubject(context.Background(), "1")
	// 重复执行菜单增量必须保留运营已自定义的名称，且不能插入重复权限节点。
	if err := db.Table("admin_permissions").Where("code=?", "menu.drama_report").Update("name", "自定义剧目报表").Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := db.Exec(string(readDeploymentSQL(t, "migrations/20261001_001_drama_report_menu.sql"))).Error; err != nil {
			t.Fatal(err)
		}
	}
	var menu struct{ Name string }
	if err := db.Table("admin_permissions").Select("name").Where("code=?", "menu.drama_report").Scan(&menu).Error; err != nil || menu.Name != "自定义剧目报表" {
		t.Fatalf("增量覆盖自定义名称: %+v %v", menu, err)
	}
	var nodes int64
	if err := db.Table("admin_permissions").Where("code IN ?", []string{"menu.drama_report", "operations.drama_report.read"}).Count(&nodes).Error; err != nil || nodes != 2 {
		t.Fatalf("重复权限节点: %d %v", nodes, err)
	}
	from := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	for i := 0; i < 3; i++ {
		if err := f.store.SaveDrama(ctx, domain.Drama{ID: fmt.Sprint(9901 + i), Title: fmt.Sprintf("报表剧%d", i), Status: "updating", PublishStatus: "draft", TagIDs: []string{}}); err != nil {
			t.Fatal(err)
		}
	}
	for i, row := range []struct {
		app, user, drama, kind string
		at                     time.Time
	}{
		{"101", "11001", "9901", "episode_unlock", from},
		{"101", "11001", "9901", "episode_unlock", from.Add(time.Hour)},
		{"102", "11001", "9901", "episode_unlock", from},
		{"101", "11002", "9902", "episode_unlock", from},
		{"101", "11001", "9902", "episode_unlock", from.Add(time.Hour)},
		{"101", "11003", "9901", "vip_progress", from},
		{"101", "11004", "9901", "episode_unlock", from.AddDate(0, 0, 2)},
		{"101", "11005", "9901", "episode_unlock", from.Add(-time.Second)},
	} {
		if err := db.Exec("INSERT INTO "+userTable("reward_grants", uint16(i))+" (bucket_id,claim_id,application_id,visitor_id,drama_id,reward_type,ad_unit_id,granted_at) VALUES (?,?,?,?,?,?,'fixture',?)", i, fmt.Sprintf("drama-report-%d", i), row.app, row.user, row.drama, row.kind, row.at).Error; err != nil {
			t.Fatal(err)
		}
	}
	input := operations.DramaReportQuery{From: from, To: from.AddDate(0, 0, 2), Query: "报表剧", Page: 1, PageSize: 2, Sort: "unlockUsers", Direction: "desc"}
	result, err := f.store.DramaReport(ctx, input)
	if err != nil || result.Total != 3 || len(result.Items) != 2 {
		t.Fatalf("分页错误: %+v %v", result, err)
	}
	if result.Summary.RewardCompletions != 5 || result.Summary.RewardUsers != 3 || result.Summary.AverageRewards == nil {
		t.Fatalf("跨页汇总错误: %+v", result.Summary)
	}
	input.Sort = "unlockCount"
	ranked, rankErr := f.store.DramaReport(ctx, input)
	if rankErr != nil || ranked.Items[0].DramaID != "9901" {
		t.Fatalf("结算次数排序错误: %+v %v", ranked, rankErr)
	}
	row := result.Items[0]
	if row.DramaID != "9901" || row.UnlockUsers != 2 || row.UnlockCount != 3 || row.AverageUnlocks == nil || *row.AverageUnlocks != 1.5 || row.PlayUsers != nil || row.FirstEpisodeCompletionRate != nil {
		t.Fatalf("指标错误: %+v", row)
	}
	input.Page = 2
	zero, err := f.store.DramaReport(ctx, input)
	if err != nil || len(zero.Items) != 1 || zero.Items[0].DramaID != "9903" || zero.Items[0].AverageUnlocks != nil {
		t.Fatalf("零值剧遗漏: %+v %v", zero, err)
	}
	input.Page = 1
	input.Query = "9901"
	single, err := f.store.DramaReport(application.WithContext(ctx, "101"), input)
	if err != nil || single.Total != 1 || single.Items[0].UnlockUsers != 1 || *single.Items[0].AverageUnlocks != 2 {
		t.Fatalf("应用隔离及 ID 搜索错误: %+v %v", single, err)
	}
	input.Query = "报表剧"
	input.Sort = "averageUnlocks"
	input.Direction = "asc"
	input.PageSize = 20
	average, err := f.store.DramaReport(ctx, input)
	if err != nil || average.Items[0].DramaID != "9902" || average.Items[2].DramaID != "9903" {
		t.Fatalf("空值末尾排序错误: %+v %v", average, err)
	}
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	raw := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var login adminrbac.LoginResult
	if err := json.Unmarshal(raw, &login); err != nil {
		t.Fatal(err)
	}
	q := "?scope=all&from=" + from.Format(time.DateOnly) + "&to=" + from.AddDate(0, 0, 1).Format(time.DateOnly) + "&query=9901"
	raw = iaaRequest(t, server.URL, "GET", "/admin/v1/drama-report"+q, login.Token, "", nil, 200)
	var actual operations.DramaReportPage
	if err := json.Unmarshal(raw, &actual); err != nil || actual.Total != 1 || actual.Items[0].UnlockUsers != 2 {
		t.Fatalf("HTTP 合同错误: %s %v", raw, err)
	}
	iaaRequest(t, server.URL, "GET", "/admin/v1/drama-report/9901/trend"+q, login.Token, "", nil, 200)
	iaaRequest(t, server.URL, "GET", "/admin/v1/drama-report/9901/episodes"+q, login.Token, "", nil, 200)
	iaaRequest(t, server.URL, "GET", "/admin/v1/drama-report/9901/episodes"+q+"&range=all", login.Token, "", nil, 400)
	for _, suffix := range []string{"&sort=invalid", "&direction=invalid", "&page=0", "&pageSize=101", "&page=invalid", "&range=all"} {
		iaaRequest(t, server.URL, "GET", "/admin/v1/drama-report"+q+suffix, login.Token, "", nil, 400)
	}
	iaaRequest(t, server.URL, "GET", "/admin/v1/drama-report"+q, "", "", nil, 401)
	if err := db.Table("admin_users").Where("username=?", "iaa-admin").Update("is_super", false).Error; err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, "GET", "/admin/v1/drama-report"+q, login.Token, "", nil, 403)
}
