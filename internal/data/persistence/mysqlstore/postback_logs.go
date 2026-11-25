package mysqlstore

import (
	"context"
	"drama-server/internal/biz/consumer"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
)

var tikTokPostbackKinds = []string{
	"tt_video_request_attempt", "tt_video_request_success", "tt_video_request_failure",
	"tt_episode_play_attempt", "tt_episode_play_success", "tt_episode_play_failure",
	"tt_ad_show_attempt", "tt_ad_show_success", "tt_ad_show_failure",
	"tt_video_request_unknown", "tt_episode_play_unknown", "tt_ad_show_unknown",
	"tt_video_request_skipped", "tt_episode_play_skipped", "tt_ad_show_skipped",
}

// ListTikTokPostbackLogs 跨 90 日明细分表读取当前应用的脱敏回传日志，使用稳定边界游标避免深 OFFSET。
func (s *Store) ListTikTokPostbackLogs(ctx context.Context, query operations.TikTokPostbackLogQuery) (operations.TikTokPostbackLogPage, error) {
	result := operations.TikTokPostbackLogPage{Items: []operations.TikTokPostbackLog{}, PageSize: query.PageSize}
	tables, err := s.metricTables(ctx, false)
	if err != nil {
		return result, err
	}
	union, unionArgs := postbackEventSource(tables, application.IDFromContext(ctx), query)
	eventExpression := `CASE
  WHEN kind LIKE 'tt_video_request_%' THEN 'video_play_request'
  WHEN kind LIKE 'tt_episode_play_%' THEN 'ep_play'
  WHEN kind LIKE 'tt_ad_show_%' THEN 'minis_ad_show'
 END`
	outcomeExpression := `CASE
  WHEN MAX(kind LIKE '%_success')=1 THEN 'success'
  WHEN MAX(kind LIKE '%_failure')=1 THEN 'failure'
  WHEN MAX(kind LIKE '%_unknown')=1 THEN 'unknown'
  WHEN MAX(kind LIKE '%_skipped')=1 THEN 'skipped'
  ELSE 'attempt'
 END`
	events := s.data.DB().WithContext(ctx).Table("("+union+") AS events", unionArgs...).
		Where("application_id = ?", application.IDFromContext(ctx)).
		Where("kind IN ?", postbackKinds(query.Event))
	// 同一事实的 attempt 与终态是不可变审计记录；列表按事实合并，成功优先于失败，
	// 未调用和超时独立保留；迟到结果优先于未知，避免把已成功事实同时展示为“等待回调”。
	aggregated := s.data.DB().WithContext(ctx).Table("(?) AS events", events).
		Select("MIN(user_id) AS internal_user_id,CAST(user_id AS CHAR) AS user_id,event_id," + eventExpression + " AS event," + outcomeExpression + " AS outcome,MIN(occurred_at) AS occurred_at,MIN(received_at) AS received_at").
		Group("user_id,event_id," + eventExpression)
	facts := s.data.DB().WithContext(ctx).Table("(?) AS facts", aggregated).
		Select(`facts.*,CONCAT(CASE event
  WHEN 'video_play_request' THEN 'tt_video_request'
  WHEN 'ep_play' THEN 'tt_episode_play'
  WHEN 'minis_ad_show' THEN 'tt_ad_show'
 END,'_',outcome) AS kind`)
	db := s.data.DB().WithContext(ctx).Table("(?) AS facts", facts).
		Joins("LEFT JOIN metric_postback_details AS detail ON detail.application_id = ? AND detail.user_id = facts.internal_user_id AND detail.event_id = facts.event_id AND detail.kind = facts.kind AND detail.received_at >= ?", application.IDFromContext(ctx), time.Now().UTC().Add(-7*24*time.Hour))
	if query.Scene != "" {
		db = db.Where("detail.scene = ?", query.Scene)
	}
	if query.ClientVersion != "" {
		db = db.Where("detail.client_version = ?", query.ClientVersion)
	}
	if query.ErrorCode != "" {
		db = db.Where("detail.error_code = ?", query.ErrorCode)
	}
	if query.From != "" {
		db = db.Where("facts.occurred_at >= ? AND facts.occurred_at < DATE_ADD(?, INTERVAL 1 DAY)", query.From, query.Through)
	}
	if query.Outcome != "" {
		db = db.Where("facts.outcome = ?", query.Outcome)
	}
	if !query.BeforeReceivedAt.IsZero() {
		db = db.Where(`facts.received_at < ? OR
 (facts.received_at = ? AND facts.user_id < ?) OR
 (facts.received_at = ? AND facts.user_id = ? AND facts.event_id < ?) OR
 (facts.received_at = ? AND facts.user_id = ? AND facts.event_id = ? AND facts.kind < ?)`,
			query.BeforeReceivedAt, query.BeforeReceivedAt, query.BeforeUserID,
			query.BeforeReceivedAt, query.BeforeUserID, query.BeforeEventID,
			query.BeforeReceivedAt, query.BeforeUserID, query.BeforeEventID, query.BeforeKind)
	}
	// 不把 JSON 直接交给 GORM 的领域指针扫描，保持空明细与 JSON null 的区别。
	var rows []struct {
		UserID     string
		EventID    string
		Event      string
		Outcome    string
		OccurredAt time.Time
		ReceivedAt time.Time
		Payload    *string
	}
	err = db.Select("facts.user_id,facts.event_id,facts.event,facts.outcome,facts.occurred_at,facts.received_at,detail.payload").
		Order("facts.received_at DESC,facts.user_id DESC,facts.event_id DESC,facts.kind DESC").Limit(query.PageSize + 1).Scan(&rows).Error
	if err != nil {
		return result, err
	}
	for _, row := range rows {
		item := operations.TikTokPostbackLog{UserID: row.UserID, EventID: row.EventID, Event: row.Event, Outcome: row.Outcome, OccurredAt: row.OccurredAt, ReceivedAt: row.ReceivedAt}
		if row.Payload != nil {
			var detail consumer.PostbackDetails
			if decodeErr := json.Unmarshal([]byte(*row.Payload), &detail); decodeErr != nil {
				return result, decodeErr
			}
			item.Details = &detail
		}
		result.Items = append(result.Items, item)
	}
	if len(result.Items) > query.PageSize {
		result.HasMore = true
		result.Items = result.Items[:query.PageSize]
	}
	if result.HasMore {
		result.NextCursor = operations.TikTokPostbackLogCursor(application.IDFromContext(ctx), query, result.Items[len(result.Items)-1])
	}
	return result, nil
}

// postbackEventSource 直接读取查询日期内的阶段，再按事实键补齐范围外阶段，避免整窗扫描或重复回查常见同日回调。
// 完整阶段必须在日期和结果筛选之前合并，保证跨日迟到成功、最早发生时间及游标排序不改变。
func postbackEventSource(tables []string, app string, query operations.TikTokPostbackLogQuery) (string, []any) {
	cutoff, until := metricCutoffNow(), metricTomorrow()
	from, to := cutoff, until
	if query.From != "" {
		from, _ = time.Parse(time.DateOnly, query.From)
		through, _ := time.Parse(time.DateOnly, query.Through)
		to = through.AddDate(0, 0, 1)
	}
	kinds := postbackKinds(query.Event)
	marks := strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")
	args := []any{}
	candidates := []string{}
	// 查询整个保留期时不需要候选回查，直接在各日表内筛选，避免给历史查询额外增加主键读取。
	if !from.After(cutoff) && !to.Before(until) {
		parts := []string{}
		for _, table := range metricEventTables(tables, cutoff, until) {
			parts = append(parts, "SELECT application_id,user_id,event_id,kind,occurred_at,received_at FROM `"+table+"` WHERE application_id=? AND kind IN ("+marks+")")
			args = append(args, app)
			for _, kind := range kinds {
				args = append(args, kind)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, " UNION ALL "), args
		}
	}
	selected := map[string]bool{}
	for _, table := range metricEventTables(tables, from, to) {
		selected[table] = true
		candidates = append(candidates, "SELECT bucket_id,user_id,event_id FROM `"+table+"` WHERE application_id=? AND kind IN ("+marks+")")
		args = append(args, app)
		for _, kind := range kinds {
			args = append(args, kind)
		}
	}
	if len(candidates) == 0 {
		return "SELECT application_id,user_id,event_id,kind,occurred_at,received_at FROM metric_events_template WHERE 1=0", nil
	}
	keys := `SELECT DISTINCT c.bucket_id,k.user_id,k.event_id,k.kind,k.event_date
 FROM postback_candidates c STRAIGHT_JOIN metric_event_keys k
 ON k.application_id=? AND k.user_id=c.user_id AND k.event_id=c.event_id
 WHERE k.kind IN (` + marks + `) AND k.event_date>=? AND k.event_date<?
 AND (k.event_date<? OR k.event_date>=?)`
	args = append(args, app)
	for _, kind := range kinds {
		args = append(args, kind)
	}
	args = append(args, cutoff.Format(time.DateOnly), until.Format(time.DateOnly), from.Format(time.DateOnly), to.Format(time.DateOnly))
	parts := []string{}
	for _, table := range metricEventTables(tables, cutoff, until) {
		// 范围内的阶段直接读取；范围外通常没有阶段，空键集可跳过全部远日主键读取。
		if selected[table] {
			parts = append(parts, "SELECT application_id,user_id,event_id,kind,occurred_at,received_at FROM `"+table+"` WHERE application_id=? AND kind IN ("+marks+")")
			args = append(args, app)
			for _, kind := range kinds {
				args = append(args, kind)
			}
			continue
		}
		day, _ := time.Parse("20060102", strings.TrimPrefix(table, "metric_events_"))
		parts = append(parts, fmt.Sprintf(`SELECT e.application_id,e.user_id,e.event_id,e.kind,e.occurred_at,e.received_at
 FROM postback_phase_keys k STRAIGHT_JOIN %s e FORCE INDEX (PRIMARY)
 ON e.bucket_id=k.bucket_id AND e.application_id=? AND e.user_id=k.user_id AND e.event_id=k.event_id AND e.kind=k.kind
 WHERE k.event_date=?`, "`"+table+"`"))
		args = append(args, app, day.Format(time.DateOnly))
	}
	return `WITH postback_candidates AS (SELECT DISTINCT bucket_id,user_id,event_id FROM (` + strings.Join(candidates, " UNION ALL ") + `) selected_events),
 postback_phase_keys AS (` + keys + `) ` + strings.Join(parts, " UNION ALL "), args
}

// postbackKinds 把可选事件筛选映射为包含全部阶段的固定内部类型，不把外部输入拼入 SQL。
func postbackKinds(event string) []string {
	prefixes := map[string][]string{
		"":                   {"tt_video_request", "tt_episode_play", "tt_ad_show"},
		"video_play_request": {"tt_video_request"},
		"ep_play":            {"tt_episode_play"},
		"minis_ad_show":      {"tt_ad_show"},
	}[event]
	outcomes := []string{"attempt", "success", "failure", "unknown", "skipped"}
	result := make([]string, 0, len(prefixes)*len(outcomes))
	for _, prefix := range prefixes {
		for _, value := range outcomes {
			result = append(result, strings.Join([]string{prefix, value}, "_"))
		}
	}
	return result
}
