package mysqlstore

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/operations"
)

// dashboardBehaviorBaselineQuery 固定本次优化前的逐日全量联表查询，只用于统计口径和执行耗时对照。
func dashboardBehaviorBaselineQuery(tables []string, from, to time.Time, app string) (string, []any) {
	parts := []string{}
	args := []any{}
	for _, table := range metricEventTables(tables, from, to) {
		at, _ := time.Parse("20060102", strings.TrimPrefix(table, "metric_events_"))
		parts = append(parts, `SELECT ? AS date,0 AS active_users,
 SUM(m.kind='launch') AS launches,SUM(m.kind='play_start') AS play_starts,
 SUM(m.kind='play_start' AND c.event_id IS NOT NULL) AS play_completions,
 SUM(m.kind='rewarded_show') AS rewarded_shows,
 SUM(m.kind='rewarded_show' AND c.event_id IS NOT NULL) AS ad_completions,
 SUM(m.kind='interstitial_show') AS interstitial_shows,
 SUM(m.kind IN ('tt_video_request_attempt','tt_episode_play_attempt','tt_ad_show_attempt')) AS tiktok_postback_attempts,
 SUM(m.kind IN ('tt_video_request_success','tt_episode_play_success','tt_ad_show_success')) AS tiktok_postback_successes,
 SUM(m.kind IN ('tt_video_request_failure','tt_episode_play_failure','tt_ad_show_failure')) AS tiktok_postback_failures,
 MIN(m.received_at) AS first_received_at
 FROM `+"`"+table+"`"+` AS m LEFT JOIN metric_event_keys AS c
 ON m.kind IN ('play_start','rewarded_show') AND c.application_id=m.application_id AND c.user_id=m.user_id AND c.event_id=m.event_id
 AND c.kind=CASE m.kind WHEN 'play_start' THEN 'play_complete' WHEN 'rewarded_show' THEN 'rewarded_complete' END
 AND c.event_date>=? AND c.event_date<?
 WHERE m.application_id=? HAVING COUNT(*)>0`)
		args = append(args, at.Format(time.DateOnly), from.Format(time.DateOnly), to.Format(time.DateOnly), app)
	}
	return strings.Join(parts, " UNION ALL "), args
}

// TestDashboardBehaviorGroupedScaleIntegration 在两日日表的三十二万条事实中交替对照原 SQL，避免只测小样本或刷新频率。
func TestDashboardBehaviorGroupedScaleIntegration(t *testing.T) {
	f := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	db := f.store.data.DB()
	ctx := context.Background()
	from := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 2)
	kinds := []string{"active", "launch", "play_start", "play_complete", "rewarded_show", "rewarded_complete", "interstitial_show",
		"tt_video_request_attempt", "tt_episode_play_attempt", "tt_ad_show_attempt", "tt_video_request_success", "tt_episode_play_success", "tt_ad_show_success",
		"tt_video_request_failure", "tt_episode_play_failure", "tt_ad_show_failure", "player_first_frame", "player_switch", "player_stall", "player_error"}
	digits := []string{}
	for n := range 10 {
		digits = append(digits, fmt.Sprintf("SELECT %d AS n", n))
	}
	kindRows := make([]string, len(kinds))
	for i := range kinds {
		kindRows[i] = "SELECT ? AS kind"
	}
	tables := []string{}
	for at := from; at.Before(to); at = at.AddDate(0, 0, 1) {
		table := metricDayTable(at)
		tables = append(tables, table)
		if err := f.store.ensureMetricTable(ctx, table, "metric_events_template"); err != nil {
			t.Fatal(err)
		}
		args := make([]any, 0, len(kinds)+3)
		for _, kind := range kinds {
			args = append(args, kind)
		}
		args = append(args, at.Format(time.DateOnly), at.Format(time.DateOnly), at.Format(time.DateOnly))
		query := "INSERT INTO `" + table + "` (bucket_id,application_id,user_id,event_id,kind,occurred_at,received_at) " +
			"WITH d AS (" + strings.Join(digits, " UNION ALL ") + "), ids AS (SELECT a.n+10*b.n+100*c.n+1000*e.n AS n FROM d a CROSS JOIN d b CROSS JOIN d c CROSS JOIN d e), kinds AS (" + strings.Join(kindRows, " UNION ALL ") + ") " +
			"SELECT MOD(n,16),IF(MOD(n,5)=0,102,101),70000+MOD(n,2000),CONCAT('scale-',?,'-',LPAD(n,8,'0')),kind,TIMESTAMP(?)+INTERVAL MOD(n,86400) SECOND,TIMESTAMP(?)+INTERVAL MOD(n,86400) SECOND FROM ids CROSS JOIN kinds WHERE n<8000"
		if err := db.Exec(query, args...).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO metric_event_keys(application_id,user_id,event_id,kind,event_date) SELECT application_id,user_id,event_id,kind,DATE_FORMAT(occurred_at,'%Y-%m-%d') FROM `" + table + "`").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("ANALYZE TABLE `" + table + "`").Error; err != nil {
			t.Fatal(err)
		}
	}
	baseline, baselineArgs := dashboardBehaviorBaselineQuery(tables, from, to, "101")
	optimized, optimizedArgs := dashboardBehaviorQuery(tables, from, to, "101", true)
	type row struct {
		// DashboardDay 保留正式刷新返回的全部业务计数。
		operations.DashboardDay
		// FirstReceivedAt 同时检查首次接收时间，不能仅对照行为次数。
		FirstReceivedAt *time.Time
	}
	read := func(query string, args []any) ([]row, time.Duration) {
		t.Helper()
		started := time.Now()
		var rows []row
		if err := db.Raw("SELECT * FROM ("+query+") AS behaviors", args...).Scan(&rows).Error; err != nil {
			t.Fatal(err)
		}
		return rows, time.Since(started)
	}
	expected, _ := read(baseline, baselineArgs)
	actual, _ := read(optimized, optimizedArgs)
	if len(expected) != 2 || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("预聚合改变统计结果: expected=%+v actual=%+v", expected, actual)
	}
	for _, day := range actual {
		if day.Launches != 6400 || day.PlayStarts != 6400 || day.PlayCompletions != 6400 || day.RewardedShows != 6400 || day.AdCompletions != 6400 {
			t.Fatalf("大样本计数或应用隔离错误: %+v", day)
		}
	}
	var before, after []time.Duration
	for run := range 5 {
		var oldRows, newRows []row
		var oldTime, newTime time.Duration
		if run%2 == 0 {
			oldRows, oldTime = read(baseline, baselineArgs)
			newRows, newTime = read(optimized, optimizedArgs)
		} else {
			newRows, newTime = read(optimized, optimizedArgs)
			oldRows, oldTime = read(baseline, baselineArgs)
		}
		if !reflect.DeepEqual(oldRows, expected) || !reflect.DeepEqual(newRows, expected) {
			t.Fatal("重复执行的统计结果发生变化")
		}
		before, after = append(before, oldTime), append(after, newTime)
	}
	slices.Sort(before)
	slices.Sort(after)
	t.Logf("2 日日表 / 320000 条事实 / 目标应用 256000 条，交替执行五次中位数：原联表=%s 类型预聚合=%s，耗时降低 %.1f%%", before[2], after[2], (1-float64(after[2])/float64(before[2]))*100)
	// 执行计划是稳定验收条件；不把机器负载造成的偶发耗时波动写成脆弱的测试断言。
	assertDashboardBehaviorAccessPlan(t, db, optimized, optimizedArgs)
}
