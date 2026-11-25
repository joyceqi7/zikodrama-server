package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/sharding"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

// TestMetricCollectionIntegration 从认证采集入口到管理仪表盘读回，验证幂等、跨日去重与应用隔离。
func TestMetricCollectionIntegration(t *testing.T) {
	f := newIAAFixture(t)
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	token, user := iaaLogin(t, server.URL, "101", "user-a")
	otherToken, _ := iaaLogin(t, server.URL, "102", "user-a")
	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	events := []domain.MetricEvent{}
	for _, kind := range []string{
		"active", "launch", "play_start", "play_complete", "rewarded_show", "rewarded_complete", "interstitial_show",
		"tt_video_request_attempt", "tt_video_request_success", "tt_video_request_failure",
		"tt_episode_play_attempt", "tt_episode_play_success", "tt_episode_play_failure",
		"tt_ad_show_attempt", "tt_ad_show_success", "tt_ad_show_failure",
	} {
		events = append(events, domain.MetricEvent{ID: "same-fact-123456789", Kind: kind, OccurredAt: now})
	}
	body := map[string]any{"events": events}
	for i := 0; i < 2; i++ {
		iaaRequest(t, server.URL, "POST", "/api/v1/metrics", token, "102", body, 200)
	}
	iaaRequest(t, server.URL, "POST", "/api/v1/metrics", otherToken, "", body, 200)
	// 未匹配展示的结束回调不会把完播数计得比展示数大。
	iaaRequest(t, server.URL, "POST", "/api/v1/metrics", token, "", map[string]any{"events": []domain.MetricEvent{{ID: "orphan-fact-123456789", Kind: "rewarded_complete", OccurredAt: now}}}, 200)
	for _, invalid := range []any{
		map[string]any{"events": []any{}}, map[string]any{"events": events, "userId": "999"},
		map[string]any{"events": []domain.MetricEvent{{ID: "valid-metric-12345678", Kind: "history", OccurredAt: now}}},
		map[string]any{"events": make([]domain.MetricEvent, 51)},
	} {
		iaaRequest(t, server.URL, "POST", "/api/v1/metrics", token, "", invalid, 400)
	}
	iaaRequest(t, server.URL, "POST", "/api/v1/metrics", "", "", body, 401)
	// 合法旧时间不再触发窗口拒绝；同键重试仍只保留首次收到的事实。
	for _, clientAt := range []time.Time{now.AddDate(-1, 0, 0), now.AddDate(1, 0, 0)} {
		iaaRequest(t, server.URL, "POST", "/api/v1/metrics", token, "", map[string]any{"events": []domain.MetricEvent{{ID: "same-fact-123456789", Kind: "launch", OccurredAt: clientAt}}}, 200)
	}
	// 历史夹具只验证 MAU 窗口；线上新采集按服务器接收时间归日。
	var bucket uint16
	for i := uint16(0); i < 16; i++ {
		var rows []consumerUserModel
		if err := f.store.data.DB().Table(userTable("users", i)).Where("id = ? AND application_id = ?", user, "101").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if len(rows) > 0 {
			bucket = rows[0].BucketID
			break
		}
	}
	ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), bucket)
	for _, days := range []int{-1, -29, -30} {
		if err := f.store.RecordMetrics(ctx, user, []domain.MetricEvent{{ID: fmt.Sprintf("old-metric-%016d", -days), Kind: "active", OccurredAt: today.AddDate(0, 0, days)}}); err != nil {
			t.Fatal(err)
		}
	}
	// 结束跨日时归属开始日，不产生次日超过 100% 的完播率。
	if err := f.store.RecordMetrics(ctx, user, []domain.MetricEvent{
		{ID: "cross-day-play-123456", Kind: "play_start", OccurredAt: today.Add(-time.Hour)},
		{ID: "cross-day-play-123456", Kind: "play_complete", OccurredAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	// 另一个固定分片用户只在窗口外活跃，不能计入 MAU。
	outside := sharding.WithBucketID(application.WithContext(context.Background(), "101"), (bucket+1)%1024)
	if err := f.store.RecordMetrics(outside, "999999", []domain.MetricEvent{{ID: "outside-metric-123456", Kind: "active", OccurredAt: today.AddDate(0, 0, -30)}}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordMetrics(outside, "888888", []domain.MetricEvent{{ID: "inside-metric-123456", Kind: "active", OccurredAt: today.AddDate(0, 0, -29)}}); err != nil {
		t.Fatal(err)
	}
	raw := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var login struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &login); err != nil {
		t.Fatal(err)
	}
	raw = iaaRequest(t, server.URL, "GET", "/admin/v1/dashboard?from="+today.AddDate(0, 0, -1).Format("2006-01-02")+"&to="+today.Format("2006-01-02"), login.Token, "101", nil, 200)
	var dashboard operations.AnalyticsDashboard
	if err := json.Unmarshal(raw, &dashboard); err != nil {
		t.Fatal(err)
	}
	day := dashboard.Days[1]
	if day.ActiveUsers != 1 || day.Launches != 1 || day.PlayStarts != 1 || day.PlayCompletions != 1 || day.RewardedShows != 1 || day.AdCompletions != 1 || day.InterstitialShows != 1 || day.TikTokPostbackAttempts != 3 || day.TikTokPostbackSuccesses != 3 || day.TikTokPostbackFailures != 3 || (dashboard.Audience.MonthlyActiveUsers == nil || *dashboard.Audience.MonthlyActiveUsers != 2) || dashboard.Audience.FirstReceivedAt == nil {
		t.Fatalf("计数、幂等或隔离错误：%+v audience=%+v", day, dashboard.Audience)
	}
	if dashboard.Days[0].ActiveUsers != 1 || dashboard.Days[0].PlayStarts != 1 || dashboard.Days[0].PlayCompletions != 1 {
		t.Fatal("同用户跨日未正确计 DAU")
	}
}
