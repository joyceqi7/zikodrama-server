package mysqlstore

import (
	"strings"
	"time"

	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
)

// dashboardRefreshFrom 决定账本与 MAU 的重算窗口：首次及每日首次校正全窗，同日覆盖昨日与今日。
// 行为统计独立按日修订选择原始表，不能用此窗口触发跨日全量扫描。
func dashboardRefreshFrom(now time.Time, refreshed *time.Time, cutoff time.Time) time.Time {
	if refreshed == nil || !refreshed.UTC().Truncate(24*time.Hour).Equal(now.UTC().Truncate(24*time.Hour)) {
		return cutoff
	}
	from := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	if from.Before(cutoff) {
		return cutoff
	}
	return from
}

// dashboardIncrementalBehavior 按完整事实键回补旧开始日的完播，并只扫描近三十日所需的活跃窗口。
// 不以接收时间作为严格水位，避免跨刷新事务提交的事件永久漏算；每日全窗校正保留旧数据修复能力。
func dashboardIncrementalBehavior(tx *gorm.DB, app string, tables []string, cutoff, to, refreshFrom time.Time) (map[string]operations.DashboardDay, map[string]int64, *time.Time, error) {
	if refreshFrom.Equal(cutoff) {
		return dashboardBehaviorSnapshot(tx, app, tables, cutoff, to)
	}
	selected := map[string]bool{}
	for _, table := range metricEventTables(tables, refreshFrom, to) {
		selected[table] = true
	}
	var affected []string
	recent := metricEventUnion(tables, refreshFrom, to)
	if err := tx.Table("("+recent+") AS recent").
		Joins(`JOIN metric_event_keys origin ON origin.application_id=recent.application_id AND origin.user_id=recent.user_id AND origin.event_id=recent.event_id
 AND origin.kind=CASE recent.kind WHEN 'play_complete' THEN 'play_start' WHEN 'rewarded_complete' THEN 'rewarded_show' END`).
		Where("recent.application_id=? AND recent.kind IN ? AND origin.event_date>=? AND origin.event_date<?", app,
			[]string{"play_complete", "rewarded_complete"}, cutoff.Format(time.DateOnly), refreshFrom.Format(time.DateOnly)).
		Distinct("origin.event_date").Pluck("origin.event_date", &affected).Error; err != nil {
		return nil, nil, nil, err
	}
	for _, date := range affected {
		selected["metric_events_"+strings.ReplaceAll(date, "-", "")] = true
	}
	behaviorTables := []string{}
	for _, table := range tables {
		if selected[table] {
			behaviorTables = append(behaviorTables, table)
		}
	}
	audienceFrom := refreshFrom.AddDate(0, 0, -29)
	if audienceFrom.Before(cutoff) {
		audienceFrom = cutoff
	}
	return dashboardSelectedBehaviorSnapshot(tx, app, behaviorTables, tables, cutoff, to, audienceFrom)
}
