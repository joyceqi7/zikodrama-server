package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"gorm.io/gorm/clause"
)

// TestDashboardFreshReadAvoidsSummaryWriteLockIntegration 验证状态行被另一个事务锁住时，新鲜汇总仍可直接读取。
func TestDashboardFreshReadAvoidsSummaryWriteLockIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithContext(context.Background(), "101")
	now := time.Now().UTC().Truncate(time.Microsecond)
	first := now.Add(-24 * time.Hour)
	db := f.store.data.DB()
	if err := db.Create(&metricSummaryState{ApplicationID: "101", RefreshedAt: &now, FirstReceivedAt: &first}).Error; err != nil {
		t.Fatal(err)
	}
	table := metricMonthTable(now)
	if err := f.store.ensureMetricTable(ctx, table, "dashboard_daily_template"); err != nil {
		t.Fatal(err)
	}
	date := now.Format(time.DateOnly)
	if err := db.Table(table).Create(&dashboardSnapshotModel{ApplicationID: "101", DashboardCounters: DashboardCounters{Date: date, NewUsers: 7}, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	var locked metricSummaryState
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("application_id = ?", "101").First(&locked).Error; err != nil {
		t.Fatal(err)
	}
	// 不释放写锁，真实读取必须在期限内完成；旧的重复插入会在此锁上等待直到超时。
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	started := time.Now()
	rows, err := f.store.DashboardDays(readCtx, now.Truncate(24*time.Hour), now.Truncate(24*time.Hour).AddDate(0, 0, 1))
	if err != nil || len(rows) != 1 || rows[0].NewUsers != 7 {
		t.Fatalf("状态写锁阻塞了新鲜汇总读取，或返回错误快照: rows=%+v err=%v", rows, err)
	}
	t.Logf("持有汇总状态写锁期间读取已有快照耗时: %s", time.Since(started))
	var state metricSummaryState
	if err := db.Where("application_id = ?", "101").First(&state).Error; err != nil || state.FirstReceivedAt == nil || !state.FirstReceivedAt.Equal(first.Truncate(time.Microsecond)) {
		t.Fatalf("只读路径改变首次采集时间: %+v %v", state, err)
	}
}

// TestDashboardAudienceRollingWindowIntegration 验证跨 30 日空档、同日重复事件和应用隔离后的每日 MAU。
func TestDashboardAudienceRollingWindowIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := context.Background()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 90)
	events := []struct {
		day  int
		app  string
		user string
		id   string
	}{
		{0, "101", "1001", "first"},
		{29, "101", "1001", "again"},
		{29, "101", "1001", "same-day"},
		{30, "101", "1002", "second-user"},
		{60, "101", "1001", "return-after-gap"},
		{29, "102", "2001", "other-app"},
	}
	for _, event := range events {
		at := from.AddDate(0, 0, event.day)
		table := metricDayTable(at)
		if err := f.store.ensureMetricTable(ctx, table, "metric_events_template"); err != nil {
			t.Fatal(err)
		}
		row := metricReceiptModel{ApplicationID: event.app, UserID: event.user, EventID: event.id,
			Kind: "active", OccurredAt: at, ReceivedAt: at}
		if err := f.store.data.DB().Table(table).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	tables, err := f.store.metricTables(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	query, args := dashboardAudienceQuery(tables, from, to, "101")
	var rows []struct {
		Date  string
		Users int64
	}
	if err := f.store.data.DB().Raw(query, args...).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 90 {
		t.Fatalf("MAU 日期数=%d，期望 90", len(rows))
	}
	if err := f.store.prepareMetricActiveUsers(ctx, "101", tables, from, to, false); err != nil {
		t.Fatal(err)
	}
	_, activeMAU, err := dashboardActiveAudience(f.store.data.DB().WithContext(ctx), "101", from, to)
	if err != nil {
		t.Fatal(err)
	}
	for day, row := range rows {
		active := map[string]bool{}
		for _, event := range events {
			if event.app == "101" && event.day >= day-29 && event.day <= day {
				active[event.user] = true
			}
		}
		want := int64(len(active))
		if row.Date != from.AddDate(0, 0, day).Format(time.DateOnly) || row.Users != want {
			t.Fatalf("第 %d 天 MAU=%+v，期望 %d", day, row, want)
		}
		if activeMAU[row.Date] != want {
			t.Fatalf("新日活算法第 %d 天 MAU=%d，期望 %d", day, activeMAU[row.Date], want)
		}
	}
}

// TestDashboardHTTPIntegration 从真实认证 HTTP 入口验证跨分片、应用隔离和日期边界。
func TestDashboardHTTPIntegration(t *testing.T) {
	f := newIAAFixture(t)
	db := f.store.data.DB()
	for _, row := range []consumerUserModel{
		{ID: "70001", ApplicationID: "101", BucketID: 0, CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "70002", ApplicationID: "101", BucketID: 1, CreatedAt: time.Date(2026, 9, 2, 23, 59, 59, 0, time.UTC)},
		{ID: "70003", ApplicationID: "101", BucketID: 1, CreatedAt: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)},
		{ID: "70004", ApplicationID: "102", BucketID: 0, CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
	} {
		if err := db.Table(userTable("users", row.BucketID)).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	// 两个分片分别落账，授予三集不能被误算成一次或一个用户。
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for _, shard := range []uint16{0, 1} {
		reward := rewardGrantModel{ID: fmt.Sprint(71000 + int(shard)), BucketID: shard, ApplicationID: "101", VisitorID: "70001", ClaimID: fmt.Sprint("dashboard-", shard), GrantedAt: at}
		if err := db.Table(userTable("reward_grants", shard)).Create(&reward).Error; err != nil {
			t.Fatal(err)
		}
		grant := entitlementGrantModel{ID: fmt.Sprint(72000 + int(shard)), BucketID: shard, ApplicationID: "101", VisitorID: "70001", Type: "episode_access", Data: []byte(`{"episodeIds":["301","302","303"]}`), ValidFrom: at, SourceType: "reward", SourceID: fmt.Sprint(71000 + int(shard)), GrantedAt: at}
		if err := db.Table(userTable("entitlement_grants", shard)).Create(&grant).Error; err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	raw := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var login struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &login); err != nil {
		t.Fatal(err)
	}
	raw = iaaRequest(t, server.URL, "GET", "/admin/v1/dashboard?from=2026-09-01&to=2026-09-02", login.Token, "101", nil, 200)
	var result operations.AnalyticsDashboard
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Days) != 2 || result.Days[0].NewUsers != 1 || result.Days[1].NewUsers != 1 || result.Days[0].RewardCompletions != 2 || result.Days[0].EpisodeUnlocks != 6 {
		t.Fatalf("日期边界或应用隔离失效：%+v", result.Days)
	}
	for _, query := range []string{"from=x&to=2026-09-02", "from=2026-09-03&to=2026-09-01", "from=2026-01-01&to=2026-09-02", "from=2099-01-01&to=2099-01-02"} {
		iaaRequest(t, server.URL, "GET", "/admin/v1/dashboard?"+query, login.Token, "101", nil, 400)
	}
	iaaRequest(t, server.URL, "GET", "/admin/v1/dashboard?from=2026-09-01&to=2026-09-02", "", "101", nil, 401)
	iaaRequest(t, server.URL, "GET", "/admin/v1/dashboard?from=2026-09-01&to=2026-09-02", login.Token, "", nil, 400)
}
