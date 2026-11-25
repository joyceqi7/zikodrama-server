package mysqlstore

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/sharding"
	"drama-server/internal/biz/subject"
)

// TestSubjectReportsIsolationIntegration 验证全部/单应用报表、汇总、明细和趋势都限制在主体，缺少主体不能回退全局。
func TestSubjectReportsIsolationIntegration(t *testing.T) {
	f := newIAAFixture(t)
	b, err := subject.NewService(f.store).Save(t.Context(), "", subject.Input{Name: "报表主体 B"})
	if err != nil {
		t.Fatal(err)
	}
	app := application.Application{ID: "103", SubjectID: b.ID, TikTokAppID: "000103", Name: "报表应用 B", ClientKey: "report-b", ClientSecret: "report-b-secret", Status: "active", MonetizationMode: "IAP", TimeZone: "UTC"}
	if err := f.store.CreateApplication(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	bCtx := application.WithSubject(t.Context(), b.ID)
	draft := consumer.Drama{Title: "主体 B 报表剧", PublishStatus: "draft", TagIDs: []string{}}
	if err := f.store.SaveDramaAggregate(bCtx, &draft, nil); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	from, to := now.Truncate(24*time.Hour), now.Truncate(24*time.Hour).Add(24*time.Hour)
	for _, row := range []struct {
		app, drama, campaign string
		count                int
	}{{"101", "201", "701", 3}, {app.ID, draft.ID, "702", 2}} {
		if err := f.store.data.DB().Exec("INSERT INTO application_campaigns(id,application_id,code,name,landing_url,minis_path,drama_id,status) VALUES(?,?,?,?,'https://fixture/link','/',?,'disabled')", row.campaign, row.app, "report-"+row.campaign, "投放 "+row.campaign, row.drama).Error; err != nil {
			t.Fatal(err)
		}
		for i := 0; i < row.count; i++ {
			if err := f.store.data.DB().Exec("INSERT INTO reward_grants_00(bucket_id,application_id,visitor_id,claim_id,drama_id,campaign_id,ad_unit_id,reward_type,granted_at) VALUES(0,?,1000,?,?,?,'report-ad','episode_unlock',?)", row.app, fmt.Sprintf("report-%s-%d", row.app, i), row.drama, row.campaign, now).Error; err != nil {
				t.Fatal(err)
			}
		}
		ctx := sharding.WithBucketID(application.WithContext(t.Context(), row.app), 0)
		if err := f.store.RecordMetrics(ctx, "1000", []consumer.MetricEvent{{ID: "subject-report-show-" + row.app, Kind: "rewarded_show", OccurredAt: now}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		subject, app, drama, campaign string
		count                         int64
	}{{"1", "", "201", "701", 3}, {b.ID, "", draft.ID, "702", 2}, {b.ID, app.ID, draft.ID, "702", 2}, {"", "", "", "", 0}} {
		ctx := application.WithContext(application.WithSubject(t.Context(), tc.subject), tc.app)
		page, err := f.store.DramaReport(ctx, operations.DramaReportQuery{From: from, To: to, Page: 1, PageSize: 20, Sort: "unlockCount", Direction: "desc"})
		if err != nil {
			t.Fatal(err)
		}
		wantLen := 1
		if tc.subject == "" {
			wantLen = 0
		}
		if len(page.Items) != wantLen || page.Summary.RewardCompletions != tc.count {
			t.Fatalf("剧目报表串主体: subject=%s total=%d rewards=%d", tc.subject, page.Total, page.Summary.RewardCompletions)
		}
		if wantLen == 1 && page.Items[0].DramaID != tc.drama {
			t.Fatal("报表泄漏其他主体剧名或主键")
		}
		campaigns, err := f.store.CampaignReport(ctx, operations.CampaignReportQuery{From: from, To: to, Page: 1, PageSize: 20, Sort: "rewardCompletions", Direction: "desc"})
		if err != nil || len(campaigns.Items) != wantLen || campaigns.Summary.RewardCompletions != tc.count {
			t.Fatal("链接汇总串主体", err)
		}
		if wantLen == 1 && campaigns.Items[0].CampaignID != tc.campaign {
			t.Fatal("链接报表泄漏其他主体活动")
		}
		analysis, err := f.store.RewardAnalysis(ctx, from, to)
		if err != nil || len(analysis.Dramas) != wantLen || len(analysis.Campaigns) != wantLen {
			t.Fatal("奖励榜串主体", err)
		}
		if wantLen == 1 && (analysis.Dramas[0].DramaID != tc.drama || analysis.Campaigns[0].CampaignID != tc.campaign) {
			t.Fatal("奖励榜返回其他主体内容")
		}
		wantShows := int64(1)
		if tc.subject == "" {
			wantShows = 0
		}
		if page.AdShows.Total != wantShows || campaigns.AdShows.Total != wantShows {
			t.Fatal("展示汇总读取了其他主体事件")
		}
	}
	trend, err := f.store.RewardTrend(bCtx, "drama", "201", from, to)
	if err != nil || trend.Total.RewardCompletions != 0 {
		t.Fatal("趋势可读其他主体", err)
	}
	episodes, err := f.store.RewardEpisodes(bCtx, "201", from, to)
	if err != nil || len(episodes) != 0 {
		t.Fatal("分集明细可读其他主体", err)
	}
	trend, err = f.store.RewardTrend(bCtx, "campaign", "701", from, to)
	if err != nil || trend.Total.RewardCompletions != 0 {
		t.Fatal("链接趋势可读其他主体", err)
	}
	handler := f.handler(t)
	login := subjectAdminHTTP(t, handler, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var admin adminrbac.LoginResult
	if err := json.Unmarshal(login, &admin); err != nil {
		t.Fatal(err)
	}
	query := "?scope=all&from=" + from.Format(time.DateOnly) + "&to=" + from.Format(time.DateOnly)
	for _, path := range []string{"/admin/v1/drama-report", "/admin/v1/campaign-report", "/admin/v1/reward-analysis"} {
		subjectAdminHTTP(t, handler, "GET", path+query, admin.Token, "", nil, 400)
		subjectAdminHTTP(t, handler, "GET", path+query, admin.Token, b.ID, nil, 200)
		// 单应用筛选必须与当前主体一致，不能通过头部覆盖顶部主体。
		r := httptest.NewRequest("GET", path+"?scope=application&from="+from.Format(time.DateOnly)+"&to="+from.Format(time.DateOnly), nil)
		r.Header.Set("Authorization", "Bearer "+admin.Token)
		r.Header.Set("X-Subject-ID", b.ID)
		r.Header.Set("X-Application-ID", "101")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("跨主体单应用报表请求未拒绝: %s HTTP=%d", path, w.Code)
		}
	}
}
