package mysqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
)

// TestDashboardBehaviorDailyAggregationIntegration 对照原查询验证跨月完播、身份隔离、时间边界及全部行为计数，并检查主键匹配计划。
func TestDashboardBehaviorDailyAggregationIntegration(t *testing.T) {
	f := newIAAFixture(t)
	db := f.store.data.DB()
	ctx := context.Background()
	from := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 15)
	byTable := map[string][]metricReceiptModel{}
	keys := []metricEventKey{}
	add := func(app, user, id, kind string, at time.Time) {
		table := metricDayTable(at)
		byTable[table] = append(byTable[table], metricReceiptModel{
			ApplicationID: app, UserID: user, EventID: id, Kind: kind,
			OccurredAt: at, ReceivedAt: at.Add(time.Minute),
		})
		keys = append(keys, metricEventKey{ApplicationID: app, UserID: user, EventID: id, Kind: kind, EventDate: at.Format(time.DateOnly)})
	}
	kinds := []string{"active", "launch", "play_start", "play_complete", "rewarded_show", "rewarded_complete", "interstitial_show",
		"tt_video_request_attempt", "tt_episode_play_attempt", "tt_ad_show_attempt",
		"tt_video_request_success", "tt_episode_play_success", "tt_ad_show_success",
		"tt_video_request_failure", "tt_episode_play_failure", "tt_ad_show_failure"}
	for day := 0; day < 15; day++ {
		for _, app := range []string{"101", "102"} {
			// 末日日表只含另一应用，不能为目标应用制造空计数行。
			if day == 14 && app == "101" {
				continue
			}
			for user := 1001; user <= 1070; user++ {
				for _, kind := range kinds {
					add(app, fmt.Sprint(user), fmt.Sprintf("daily-%02d", day), kind, from.AddDate(0, 0, day).Add(12*time.Hour))
				}
			}
		}
	}
	start := time.Date(2026, 9, 30, 23, 59, 0, 0, time.UTC)
	finish := start.Add(2 * time.Minute)
	for _, pair := range []struct{ id, begin, end string }{
		{"cross-play", "play_start", "play_complete"}, {"cross-ad", "rewarded_show", "rewarded_complete"},
	} {
		add("101", "1001", pair.id, pair.begin, start)
		add("101", "1001", pair.id, pair.end, finish)
	}
	add("101", "1001", "orphan", "play_complete", start)
	add("101", "1001", "wrong-kind", "play_start", start)
	add("101", "1001", "wrong-kind", "rewarded_complete", finish)
	add("101", "1001", "wrong-user", "play_start", start)
	add("101", "1002", "wrong-user", "play_complete", finish)
	add("101", "1001", "wrong-app", "play_start", start)
	add("102", "1001", "wrong-app", "play_complete", finish)
	add("101", "1001", "after-window", "play_start", start)
	add("101", "1001", "after-window", "play_complete", to)
	add("101", "1001", "before-window", "play_complete", from.Add(-time.Minute))
	add("101", "1001", "before-window", "play_start", finish)
	for table, rows := range byTable {
		if err := f.store.ensureMetricTable(ctx, table, "metric_events_template"); err != nil {
			t.Fatal(err)
		}
		if err := db.Table(table).CreateInBatches(&rows, 500).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.CreateInBatches(&keys, 500).Error; err != nil {
		t.Fatal(err)
	}
	tables, err := f.store.metricTables(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	// 保留优化前的查询作为语义与耗时对照，不给旧查询加筛选或改写连接。
	events := db.Table("("+metricEventUnion(tables, from, to)+") AS raw_events").Where("application_id = ?", "101").Select("*")
	var oldRows []struct {
		operations.DashboardDay
		// FirstReceivedAt 用来对照原始事实的最早接收时间。
		FirstReceivedAt *time.Time
	}
	started := time.Now()
	if err := db.Table("(?) AS m", events).Joins(`LEFT JOIN (?) AS c ON c.application_id=m.application_id AND c.user_id=m.user_id AND c.event_id=m.event_id
 AND ((m.kind='play_start' AND c.kind='play_complete') OR (m.kind='rewarded_show' AND c.kind='rewarded_complete'))`, events).Select(`DATE_FORMAT(m.occurred_at,'%Y-%m-%d') AS date, COUNT(DISTINCT m.user_id) AS active_users,
 SUM(m.kind='launch') AS launches, SUM(m.kind='play_start') AS play_starts,
 SUM(m.kind='play_start' AND c.event_id IS NOT NULL) AS play_completions,
 SUM(m.kind='rewarded_show') AS rewarded_shows,
 SUM(m.kind='rewarded_show' AND c.event_id IS NOT NULL) AS ad_completions,
 SUM(m.kind='interstitial_show') AS interstitial_shows,
 SUM(m.kind IN ('tt_video_request_attempt','tt_episode_play_attempt','tt_ad_show_attempt')) AS tiktok_postback_attempts,
 SUM(m.kind IN ('tt_video_request_success','tt_episode_play_success','tt_ad_show_success')) AS tiktok_postback_successes,
 SUM(m.kind IN ('tt_video_request_failure','tt_episode_play_failure','tt_ad_show_failure')) AS tiktok_postback_failures,
 MIN(m.received_at) AS first_received_at`).Group("date").Scan(&oldRows).Error; err != nil {
		t.Fatal(err)
	}
	oldDuration := time.Since(started)
	oldDays := map[string]operations.DashboardDay{}
	var oldFirst *time.Time
	for _, row := range oldRows {
		oldDays[row.Date] = row.DashboardDay
		if oldFirst == nil || row.FirstReceivedAt.Before(*oldFirst) {
			oldFirst = row.FirstReceivedAt
		}
	}
	query, args := dashboardBehaviorQuery(tables, from, to, "101")
	var newRows []struct {
		operations.DashboardDay
		// FirstReceivedAt 与每日计数一起读取，避免额外明细扫描。
		FirstReceivedAt *time.Time
	}
	started = time.Now()
	if err := db.Table("(?) AS behaviors", gorm.Expr(query, args...)).Select("*").Scan(&newRows).Error; err != nil {
		t.Fatal(err)
	}
	newDuration := time.Since(started)
	newDays := map[string]operations.DashboardDay{}
	var newFirst *time.Time
	for _, row := range newRows {
		newDays[row.Date] = row.DashboardDay
		if newFirst == nil || row.FirstReceivedAt.Before(*newFirst) {
			newFirst = row.FirstReceivedAt
		}
	}
	if !reflect.DeepEqual(newDays, oldDays) || newFirst == nil || oldFirst == nil || !newFirst.Equal(*oldFirst) {
		t.Fatalf("按日聚合改变统计结果或首次接收时间: new=%+v old=%+v newFirst=%v oldFirst=%v", newDays, oldDays, newFirst, oldFirst)
	}
	day := newDays[start.Format(time.DateOnly)]
	if len(newDays) != 14 || day.ActiveUsers != 70 || day.PlayStarts != 75 || day.PlayCompletions != 71 || day.RewardedShows != 71 || day.AdCompletions != 71 {
		t.Fatalf("跨日匹配、身份隔离或空应用日期错误: days=%d startDay=%+v", len(newDays), day)
	}
	t.Logf("15 日日表 / %d 条事实，行为查询耗时: 原查询=%s 按日汇总=%s", len(keys), oldDuration, newDuration)

	assertDashboardBehaviorAccessPlan(t, db, query, args)

	// 验证实际快照入口仍组合相同计数、MAU 和首次接收时间，空窗口也返回零 MAU。
	days, mau, first, err := dashboardBehaviorSnapshot(db, "101", tables, from, to)
	if err != nil || !reflect.DeepEqual(days, newDays) || first == nil || !first.Equal(*newFirst) || mau[to.AddDate(0, 0, -1).Format(time.DateOnly)] != 70 {
		t.Fatalf("快照入口与行为汇总不一致: days=%+v mau=%+v first=%v err=%v", days, mau, first, err)
	}
	days, mau, first, err = dashboardBehaviorSnapshot(db, "101", nil, from, to)
	if err != nil || len(days) != 0 || first != nil || len(mau) != 15 {
		t.Fatalf("空窗口快照错误: days=%+v mau=%+v first=%v err=%v", days, mau, first, err)
	}
	for date, active := range mau {
		if active != 0 {
			t.Fatalf("空窗口 %s MAU=%d", date, active)
		}
	}
}

// assertDashboardBehaviorAccessPlan 验证明细只走覆盖索引，完成统计按类型范围读取后使用完整主键查找。
func assertDashboardBehaviorAccessPlan(t *testing.T, db *gorm.DB, query string, args []any) {
	t.Helper()
	var planText string
	if err := db.Raw("EXPLAIN FORMAT=JSON SELECT * FROM ("+query+") AS behaviors", args...).Scan(&planText).Error; err != nil {
		t.Fatal(err)
	}
	var plan any
	if err := json.Unmarshal([]byte(planText), &plan); err != nil {
		t.Fatal(err)
	}
	lookups, coveringScans, kindRanges := 0, 0, 0
	var inspect func(any)
	inspect = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			if value["table_name"] == "m" {
				coveringScans++
				if value["key"] != "idx_metrics_app_kind_received" || value["using_index"] != true {
					t.Fatalf("行为明细未使用覆盖索引: %+v", value)
				}
				if parts, ok := value["used_key_parts"].([]any); ok && value["access_type"] == "range" {
					for _, part := range parts {
						if part == "kind" {
							kindRanges++
						}
					}
				}
			}
			if value["table_name"] == "c" {
				lookups++
				if value["key"] != "PRIMARY" || value["access_type"] != "eq_ref" {
					t.Fatalf("完成事件未按唯一主键查找: %+v", value)
				}
			}
			for _, child := range value {
				inspect(child)
			}
		case []any:
			for _, child := range value {
				inspect(child)
			}
		}
	}
	inspect(plan)
	if lookups == 0 || coveringScans == 0 || kindRanges == 0 {
		t.Fatalf("执行计划缺少主键匹配、覆盖读取或类型范围过滤: lookups=%d coveringScans=%d kindRanges=%d", lookups, coveringScans, kindRanges)
	}
}

// TestDashboardBehaviorSparseKindsIntegration 验证只含活跃／质量信号、孤立完成或未完成开始的日期仍保留原计数和首次时间。
func TestDashboardBehaviorSparseKindsIntegration(t *testing.T) {
	f := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	db := f.store.data.DB()
	ctx := context.Background()
	from := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 5)
	user := "9007199254740993"
	first := from.AddDate(0, 0, 1).Add(time.Minute)
	events := []struct {
		day      int
		app      string
		user     string
		id       string
		kind     string
		received time.Duration
	}{
		{0, "102", user, "other-application", "active", time.Minute},
		{1, "101", user, "active-only", "active", 3 * time.Minute},
		{1, "101", "9007199254740994", "quality-first", "player_error", time.Minute},
		{2, "101", user, "no-completion", "play_start", time.Minute},
		{3, "101", user, "orphan-completion", "play_complete", time.Minute},
		{4, "101", user, strings.Repeat("x", 64), "rewarded_show", time.Minute},
		{4, "101", user, strings.Repeat("x", 64), "rewarded_complete", 2 * time.Minute},
	}
	tables := []string{}
	for at := from; at.Before(to); at = at.AddDate(0, 0, 1) {
		table := metricDayTable(at)
		tables = append(tables, table)
		if err := f.store.ensureMetricTable(ctx, table, "metric_events_template"); err != nil {
			t.Fatal(err)
		}
	}
	for _, event := range events {
		at := from.AddDate(0, 0, event.day)
		row := metricReceiptModel{BucketID: 7, ApplicationID: event.app, UserID: event.user, EventID: event.id, Kind: event.kind, OccurredAt: at, ReceivedAt: at.Add(event.received)}
		if err := db.Table(metricDayTable(at)).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&metricEventKey{ApplicationID: event.app, UserID: event.user, EventID: event.id, Kind: event.kind, EventDate: at.Format(time.DateOnly)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, dailyActivity := range []bool{false, true} {
		query, args := dashboardBehaviorQuery(tables, from, to, "101", dailyActivity)
		var rows []struct {
			// DashboardDay 检查零类型次数不能令存在事实的日期被丢弃。
			operations.DashboardDay
			// FirstReceivedAt 检查未计数的质量信号仍参与最早接收时间。
			FirstReceivedAt *time.Time
		}
		if err := db.Raw(query, args...).Scan(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if len(rows) != 4 || rows[0].Date != "2026-10-21" || rows[0].FirstReceivedAt == nil || !rows[0].FirstReceivedAt.Equal(first) {
			t.Fatalf("空应用日期或质量信号首次时间错误: %+v", rows)
		}
		for i, row := range rows {
			want := operations.DashboardDay{Date: from.AddDate(0, 0, i+1).Format(time.DateOnly)}
			if !dailyActivity {
				want.ActiveUsers = 1
				if i == 0 {
					want.ActiveUsers = 2
				}
			}
			if i == 1 {
				want.PlayStarts = 1
			}
			if i == 3 {
				want.RewardedShows, want.AdCompletions = 1, 1
			}
			if !reflect.DeepEqual(row.DashboardDay, want) {
				t.Fatalf("稀疏事件计数改变: dailyActivity=%v got=%+v want=%+v", dailyActivity, row.DashboardDay, want)
			}
		}
	}
}
