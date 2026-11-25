package mysqlstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
)

// metricEventTables 只保留日期合法且位于刷新窗口内的事件日表。
func metricEventTables(tables []string, from, to time.Time) []string {
	selected := make([]string, 0, len(tables))
	for _, table := range tables {
		if !metricDayPattern.MatchString(table) {
			continue
		}
		at, err := time.Parse("20060102", strings.TrimPrefix(table, "metric_events_"))
		if err == nil && !at.Before(from) && at.Before(to) {
			selected = append(selected, table)
		}
	}
	return selected
}

// metricEventUnion 只扫描保留窗口内的实际日表；空窗口使用同构空模板。
func metricEventUnion(tables []string, from, to time.Time) string {
	parts := []string{}
	for _, table := range metricEventTables(tables, from, to) {
		parts = append(parts, "SELECT * FROM `"+table+"`")
	}
	if len(parts) == 0 {
		return "SELECT * FROM metric_events_template WHERE 1=0"
	}
	return strings.Join(parts, " UNION ALL ")
}

// dashboardAudienceQuery 先按日去重用户，再将连续活跃期转换为增减量，避免每条活跃记录匹配 30 个日历日。
// 事件按 occurred_at 的 UTC 日期写入独立日表，因此各分支的用户和日期不会相互重复。
func dashboardAudienceQuery(tables []string, from, to time.Time, app string) (string, []any) {
	parts := []string{}
	args := []any{from.Format(time.DateOnly), to.Format(time.DateOnly)}
	for _, table := range metricEventTables(tables, from, to) {
		at, _ := time.Parse("20060102", strings.TrimPrefix(table, "metric_events_"))
		parts = append(parts, "SELECT DISTINCT DATE(?) AS day,user_id FROM `"+table+"` WHERE application_id = ?")
		args = append(args, at.Format(time.DateOnly), app)
	}
	activity := strings.Join(parts, " UNION ALL ")
	if activity == "" {
		activity = "SELECT DATE(occurred_at) AS day,user_id FROM metric_events_template WHERE 1=0"
	}
	query := fmt.Sprintf(`WITH RECURSIVE calendar AS (
 SELECT DATE(?) AS day
 UNION ALL SELECT DATE_ADD(day,INTERVAL 1 DAY) FROM calendar WHERE DATE_ADD(day,INTERVAL 1 DAY)<DATE(?)
), activity AS (%s), ordered AS (
 SELECT day,user_id,
 LAG(day) OVER (PARTITION BY user_id ORDER BY day) AS previous_day,
 LEAD(day) OVER (PARTITION BY user_id ORDER BY day) AS next_day
 FROM activity
), changes AS (
 SELECT day,1 AS delta FROM ordered WHERE previous_day IS NULL OR previous_day<DATE_SUB(day,INTERVAL 29 DAY)
 UNION ALL
 SELECT DATE_ADD(day,INTERVAL 30 DAY) AS day,-1 AS delta FROM ordered
 WHERE next_day IS NULL OR next_day>DATE_ADD(day,INTERVAL 29 DAY)
), daily_changes AS (
 SELECT day,SUM(delta) AS delta FROM changes GROUP BY day
)
SELECT DATE_FORMAT(calendar.day,'%%Y-%%m-%%d') AS date,
 SUM(COALESCE(daily_changes.delta,0)) OVER (ORDER BY calendar.day ROWS UNBOUNDED PRECEDING) AS users
FROM calendar LEFT JOIN daily_changes ON daily_changes.day=calendar.day
ORDER BY calendar.day`, activity)
	return query, args
}

// dashboardBehaviorQuery 按应用和类型顺序扫描覆盖索引，先把明细收敛为类型计数，再合并每日结果。
// 完成统计只读取开始和展示事件并按去重键完整主键匹配，避免其他类型参与逐行联表。
// 日活可由精确集合提供；直接查询时仍按整日日表去重，跨日完成、空应用和首次接收时间口径保持一致。
func dashboardBehaviorQuery(tables []string, from, to time.Time, app string, dailyActivity ...bool) (string, []any) {
	parts := []string{}
	args := []any{}
	for _, table := range metricEventTables(tables, from, to) {
		at, _ := time.Parse("20060102", strings.TrimPrefix(table, "metric_events_"))
		activeUsers := "0"
		args = append(args, at.Format(time.DateOnly))
		if len(dailyActivity) == 0 || !dailyActivity[0] {
			// 不能累加各类型的去重人数，同一用户可能上报多种事件；正式刷新直接使用日活集合。
			activeUsers = "(SELECT COUNT(DISTINCT a.user_id) FROM `" + table + "` AS a FORCE INDEX (idx_metrics_app_kind_received) WHERE a.application_id=?)"
			args = append(args, app)
		}
		parts = append(parts, `SELECT ? AS date, `+activeUsers+` AS active_users,
 SUM(IF(g.kind='launch',g.event_count,0)) AS launches,
 SUM(IF(g.kind='play_start',g.event_count,0)) AS play_starts,
 MAX(completed.play_completions) AS play_completions,
 SUM(IF(g.kind='rewarded_show',g.event_count,0)) AS rewarded_shows,
 MAX(completed.ad_completions) AS ad_completions,
 SUM(IF(g.kind='interstitial_show',g.event_count,0)) AS interstitial_shows,
 SUM(IF(g.kind IN ('tt_video_request_attempt','tt_episode_play_attempt','tt_ad_show_attempt'),g.event_count,0)) AS tiktok_postback_attempts,
 SUM(IF(g.kind IN ('tt_video_request_success','tt_episode_play_success','tt_ad_show_success'),g.event_count,0)) AS tiktok_postback_successes,
 SUM(IF(g.kind IN ('tt_video_request_failure','tt_episode_play_failure','tt_ad_show_failure'),g.event_count,0)) AS tiktok_postback_failures,
 MIN(g.first_received_at) AS first_received_at
 FROM (SELECT m.kind,COUNT(*) AS event_count,MIN(m.received_at) AS first_received_at
 FROM `+"`"+table+"`"+` AS m FORCE INDEX (idx_metrics_app_kind_received)
 WHERE m.application_id=? GROUP BY m.kind) AS g
 CROSS JOIN (SELECT COALESCE(SUM(m.kind='play_start'),0) AS play_completions,
 COALESCE(SUM(m.kind='rewarded_show'),0) AS ad_completions
 FROM `+"`"+table+"`"+` AS m FORCE INDEX (idx_metrics_app_kind_received)
 JOIN metric_event_keys AS c
 ON c.application_id=m.application_id AND c.user_id=m.user_id AND c.event_id=m.event_id
 AND c.kind=CASE m.kind WHEN 'play_start' THEN 'play_complete' WHEN 'rewarded_show' THEN 'rewarded_complete' END
 AND c.event_date>=? AND c.event_date<?
 WHERE m.application_id=? AND m.kind IN ('play_start','rewarded_show')) AS completed
 HAVING COUNT(*)>0`)
		args = append(args, app, from.Format(time.DateOnly), to.Format(time.DateOnly), app)
	}
	return strings.Join(parts, " UNION ALL "), args
}

// dashboardBehaviorSnapshot 按日汇总行为，完成归属开始日；MAU 由窗口用户去重得到。
func dashboardBehaviorSnapshot(tx *gorm.DB, app string, tables []string, from, to time.Time) (map[string]operations.DashboardDay, map[string]int64, *time.Time, error) {
	return dashboardSelectedBehaviorSnapshot(tx, app, tables, tables, from, to, from)
}

// dashboardSelectedBehaviorSnapshot 分开限定行为日表和 MAU 活跃窗口，历史完播回补不触发整窗 MAU 重算。
func dashboardSelectedBehaviorSnapshot(tx *gorm.DB, app string, tables, audienceTables []string, from, to, audienceFrom time.Time) (map[string]operations.DashboardDay, map[string]int64, *time.Time, error) {
	var rows []struct {
		// DashboardDay 包含单个 UTC 日的行为计数。
		operations.DashboardDay
		// FirstReceivedAt 与每日计数一起读取，避免为首次接收时间再次扫描整窗明细。
		FirstReceivedAt *time.Time
	}
	query, args := dashboardBehaviorQuery(tables, from, to, app)
	if query != "" {
		if err := tx.Table("(?) AS behaviors", gorm.Expr(query, args...)).Select("*").Scan(&rows).Error; err != nil {
			return nil, nil, nil, err
		}
	}
	days := map[string]operations.DashboardDay{}
	var first *time.Time
	for _, row := range rows {
		days[row.Date] = row.DashboardDay
		if row.FirstReceivedAt != nil && (first == nil || row.FirstReceivedAt.Before(*first)) {
			first = row.FirstReceivedAt
		}
	}
	var audience []struct {
		Date  string
		Users int64
	}
	query, args = dashboardAudienceQuery(audienceTables, audienceFrom, to, app)
	if err := tx.Table("(?) AS audience", gorm.Expr(query, args...)).Select("date,users").Scan(&audience).Error; err != nil {
		return nil, nil, nil, err
	}
	mau := map[string]int64{}
	for _, row := range audience {
		mau[row.Date] = row.Users
	}
	return days, mau, first, nil
}

// DashboardAudience 从末日所在月表读取 MAU 快照，不重新扫描已过期明细。
func (s *Store) DashboardAudience(ctx context.Context, to time.Time) (operations.DashboardAudience, error) {
	result := operations.DashboardAudience{}
	rows, err := s.readDashboardSnapshots(ctx, to.AddDate(0, 0, -1), to)
	if err != nil {
		return result, err
	}
	if len(rows) > 0 {
		result.MonthlyActiveUsers = rows[0].MonthlyActiveUsers
	}
	var state metricSummaryState
	if err := s.data.DB().WithContext(ctx).Where("application_id = ?", application.IDFromContext(ctx)).Find(&state).Error; err != nil {
		return result, err
	}
	result.FirstReceivedAt = state.FirstReceivedAt
	return result, nil
}
