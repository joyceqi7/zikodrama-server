package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"sort"
	"time"
)

// adAnalyticsRow 只扫描标量事实，不把领域阶段数组交给 ORM 关联推断。
type adAnalyticsRow struct {
	// UserID 是内部主体字符串，不是 OpenID。
	UserID string
	// EventID 关联各阶段，不用于授予权益。
	EventID string
	// OccurredAt 固定调用日期。
	OccurredAt time.Time
	// AdType 区分激励与插屏。
	AdType string
	// AdUnitID 标识实际广告位。
	AdUnitID string
	// Scene 标识入口。
	Scene string
	// ClientVersion 标识 Mini 构建。
	ClientVersion string
	// Requested 表示实际进入 show。
	Requested int
	// Shown 表示客户端确认展示。
	Shown int
	// Completed 表示激励完播。
	Completed int
	// Failed 表示任一阶段明确失败，可与 Shown 同时成立。
	Failed int
	// Cancelled 表示激励未完播。
	Cancelled int
	// Unknown 表示曾超时，是否已解决由其他阶段判断。
	Unknown int
	// Closed 表示收到原生关闭。
	Closed int
	// Skipped 表示本地拦截。
	Skipped int
}

// 50008 依据子码或独立内部码归并，通用主码不能证明容器不可用。
const adContainerSQL = "(sub_error_code='50008' OR inner_error_code='50008')"

// 同一 11006 类别只归并已知主码或明确子码，原始明细保持不变。
const adConflictSQL = "(error_code IN ('11006','104111006') OR sub_error_code='11006')"

const adSummarySQL = `COUNT(*) AS calls,SUM(requested) AS requests,SUM(shown) AS shown,SUM(shown=1 AND requested=1) AS matched_shown,SUM(completed) AS completed,
 SUM(failed=1 AND requested=1 AND shown=0) AS failed_before_show,SUM(failed=1 AND shown=1) AS failed_after_show,
 SUM(failed=1 AND requested=0) AS create_failures,SUM(cancelled) AS cancelled,
 SUM(show_unknown=1 AND shown=0 AND failed=0 AND cancelled=0 AND closed=0) AS unknown,
 SUM(requested=1 AND shown=0 AND failed=0 AND cancelled=0 AND closed=0 AND show_unknown=0) AS pending,
 SUM(close_unknown=1 AND closed=0 AND failed=0) AS close_unknown,SUM(skipped) AS skipped`

// 广告诊断只合并当前直接 show 流程的阶段；历史加载记录留在原表，不进入当前漏斗或明细。
var adAnalyticsKinds = []string{"ad_trigger", "ad_create", "ad_created", "ad_request", "ad_shown", "ad_complete", "ad_closed", "ad_cancelled", "ad_failure", "ad_skipped", "ad_show_unknown", "ad_close_unknown"}
var adAnalyticsStages = []string{"capability", "gate", "create", "setup", "show", "callback", "close", "destroy"}

// adAnalyticsFacts 先合并完整调用再筛选错误码，不能筛掉请求阶段导致分母丢失。
func adAnalyticsFacts(ctx context.Context, tx *gorm.DB, q operations.AdAnalyticsQuery) *gorm.DB {
	db := tx.Model(&metricAdEventModel{}).Where("application_id=? AND occurred_at>=? AND occurred_at<DATE_ADD(?,INTERVAL 1 DAY)", application.IDFromContext(ctx), q.From, q.To).
		Where("kind IN ? AND stage IN ?", adAnalyticsKinds, adAnalyticsStages)
	for key, value := range map[string]string{"ad_type": q.AdType, "ad_unit_id": q.AdUnitID, "scene": q.Scene, "client_version": q.ClientVersion, "os": q.OS, "tik_tok_version": q.TikTokVersion} {
		if value != "" {
			db = db.Where(key+"=?", value)
		}
	}
	agg := db.Select(`MIN(user_id) AS internal_user_id,CAST(user_id AS CHAR) AS user_id,event_id,MIN(occurred_at) AS occurred_at,MIN(ad_type) AS ad_type,MIN(ad_unit_id) AS ad_unit_id,MIN(scene) AS scene,MIN(client_version) AS client_version,
 MAX(kind='ad_request') AS requested,MAX(kind='ad_shown') AS shown,MAX(kind='ad_complete') AS completed,
 MAX(kind='ad_failure') AS failed,MAX(kind='ad_cancelled') AS cancelled,MAX(kind='ad_closed') AS closed,
 MAX(kind IN ('ad_show_unknown','ad_close_unknown')) AS unknown,
 MAX(kind='ad_show_unknown') AS show_unknown,MAX(kind='ad_close_unknown') AS close_unknown,MAX(kind='ad_skipped') AS skipped,
 MIN(CASE WHEN kind='ad_shown' AND evidence='show_promise' THEN show_elapsed_ms END) AS show_ms`).Group("user_id,event_id")
	// 只有筛选错误码时才计算是否命中；不为普通汇总构造每次调用的错误码字符串集合。
	// HAVING 在完整调用合并后过滤，保留同次调用的请求和展示分母。
	if q.ErrorCode != "" {
		switch q.ErrorCode {
		case "11006", "104111006":
			agg = agg.Having("MAX(" + adConflictSQL + ")=1")
		case "50008":
			agg = agg.Having("MAX(" + adContainerSQL + ")=1")
		default:
			agg = agg.Having("MAX(error_code=? OR sub_error_code=? OR inner_error_code=?)=1", q.ErrorCode, q.ErrorCode, q.ErrorCode)
		}
	}
	return tx.Table("(?) AS facts", agg)
}

// ReadAdAnalytics 在数据库聚合阶段事实，明细只读取当前页，分位数只计算已展示且关联请求的样本。
func (s *Store) ReadAdAnalytics(ctx context.Context, q operations.AdAnalyticsQuery) (operations.AdAnalytics, error) {
	var result operations.AdAnalytics
	// 漏斗、趋势和明细使用同一只读快照，避免迟到结果恰好在多次查询之间到达而造成口径不一致。
	err := s.readSnapshot(ctx, func(tx *gorm.DB) error {
		var err error
		result, err = readAdAnalytics(ctx, tx, q)
		return err
	})
	return result, err
}

// readAdAnalytics 只在调用方创建的同一数据库快照中查询。
func readAdAnalytics(ctx context.Context, db *gorm.DB, q operations.AdAnalyticsQuery) (operations.AdAnalytics, error) {
	result := operations.AdAnalytics{Daily: []operations.AdDay{}, Errors: []operations.AdErrorCount{}, Items: []operations.AdAnalyticsItem{}, GeneratedAt: time.Now().UTC()}
	facts := adAnalyticsFacts(ctx, db, q)
	if err := db.Table("(?) AS f", facts).Select(adSummarySQL).Scan(&result.Summary).Error; err != nil {
		return result, err
	}
	window := db.Table("(?) AS f", facts).Select("show_ms,ROW_NUMBER() OVER(ORDER BY show_ms) AS rn,COUNT(*) OVER() AS n").Where("requested=1 AND shown=1 AND show_ms IS NOT NULL")
	var timing struct {
		P50     *float64
		P95     *float64
		Samples int64
	}
	if err := db.Table("(?) AS timing", window).Select("MAX(CASE WHEN rn=GREATEST(1,CEIL(n*0.50)) THEN show_ms END) AS p50,MAX(CASE WHEN rn=GREATEST(1,CEIL(n*0.95)) THEN show_ms END) AS p95,COUNT(*) AS samples").Scan(&timing).Error; err != nil {
		return result, err
	}
	result.Summary.P50MS = timing.P50
	result.Summary.P95MS = timing.P95
	result.Summary.TimingSamples = timing.Samples
	if err := db.Table("(?) AS f", facts).Select("DATE_FORMAT(occurred_at,'%Y-%m-%d') AS date," + adSummarySQL).Group("DATE_FORMAT(occurred_at,'%Y-%m-%d')").Order("date").Scan(&result.Daily).Error; err != nil {
		return result, err
	}
	errors := db.Model(&metricAdEventModel{}).Joins("JOIN (?) AS f ON f.internal_user_id=metric_ad_events.user_id AND f.event_id=metric_ad_events.event_id", facts).Where("metric_ad_events.application_id=? AND kind IN ('ad_failure','ad_cancelled')", application.IDFromContext(ctx)).Where("stage IN ?", adAnalyticsStages).Select("stage,CASE WHEN " + adConflictSQL + " THEN '11006' WHEN " + adContainerSQL + " THEN '50008' ELSE error_code END AS error_code,CASE WHEN " + adConflictSQL + " THEN '11006' WHEN " + adContainerSQL + " THEN '50008' ELSE sub_error_code END AS sub_error_code,CASE WHEN " + adConflictSQL + " THEN 'ad_active_conflict' WHEN " + adContainerSQL + " THEN 'container_unavailable' ELSE reason END AS reason")
	errors = db.Table("(?) AS grouped_errors", errors).Select("stage,error_code,sub_error_code,reason,COUNT(*) AS count").Group("stage,error_code,sub_error_code,reason").Order("count DESC,stage,error_code,sub_error_code,reason").Limit(100)
	if err := errors.Scan(&result.Errors).Error; err != nil {
		return result, err
	}
	page := db.Table("(?) AS f", facts)
	if !q.BeforeAt.IsZero() {
		page = page.Where("occurred_at<? OR (occurred_at=? AND user_id<?) OR (occurred_at=? AND user_id=? AND event_id<?)", q.BeforeAt, q.BeforeAt, q.BeforeUser, q.BeforeAt, q.BeforeUser, q.BeforeID)
	}
	var rows []adAnalyticsRow
	if err := page.Select("user_id,event_id,occurred_at,ad_type,ad_unit_id,scene,client_version,requested,shown,completed,failed,cancelled,unknown,closed,skipped").Order("occurred_at DESC,user_id DESC,event_id DESC").Limit(q.PageSize + 1).Scan(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		result.Items = append(result.Items, operations.AdAnalyticsItem{UserID: row.UserID, EventID: row.EventID, OccurredAt: row.OccurredAt, AdType: row.AdType, AdUnitID: row.AdUnitID, Scene: row.Scene, ClientVersion: row.ClientVersion, Requested: row.Requested, Shown: row.Shown, Completed: row.Completed, Failed: row.Failed, Cancelled: row.Cancelled, Unknown: row.Unknown, Closed: row.Closed, Skipped: row.Skipped})
	}

	if len(result.Items) > q.PageSize {
		result.HasMore = true
		result.Items = result.Items[:q.PageSize]
	}
	if result.HasMore {
		result.NextCursor = operations.AdAnalyticsCursor(application.IDFromContext(ctx), q, result.Items[len(result.Items)-1])
	}
	if len(result.Items) > 0 {
		pairs := [][]any{}
		for _, item := range result.Items {
			pairs = append(pairs, []any{item.UserID, item.EventID})
		}
		var events []metricAdEventModel
		if err := db.Where("application_id=? AND (user_id,event_id) IN ?", application.IDFromContext(ctx), pairs).Where("kind IN ? AND stage IN ?", adAnalyticsKinds, adAnalyticsStages).Order("received_at,kind").Find(&events).Error; err != nil {
			return result, err
		}
		for i := range result.Items {
			result.Items[i].Events = []operations.AdEventRecord{}
			for _, e := range events {
				if e.UserID == result.Items[i].UserID && e.EventID == result.Items[i].EventID {
					result.Items[i].Events = append(result.Items[i].Events, operations.AdEventRecord{Kind: e.Kind, AdMetricDetails: e.AdMetricDetails, ReceivedAt: e.ReceivedAt})
				}
			}
			sortAdAnalyticsEvents(result.Items[i].Events)
		}
	}
	return result, nil
}

// sortAdAnalyticsEvents 按同一次调用的单调耗时还原阶段顺序，网络批量接收或补报不改变调用经过。
// 任一阶段缺少耗时时，整次调用回退接收时间，避免混用两个时间基准造成非传递比较。
func sortAdAnalyticsEvents(events []operations.AdEventRecord) {
	allTimed := true
	for _, event := range events {
		if event.ElapsedMS == nil {
			allTimed = false
			break
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if allTimed {
			if *a.ElapsedMS != *b.ElapsedMS {
				return *a.ElapsedMS < *b.ElapsedMS
			}
		} else if !a.ReceivedAt.Equal(b.ReceivedAt) {
			return a.ReceivedAt.Before(b.ReceivedAt)
		}
		if left, right := adAnalyticsEventSequence(a.Kind), adAnalyticsEventSequence(b.Kind); left != right {
			return left < right
		}
		if !a.ReceivedAt.Equal(b.ReceivedAt) {
			return a.ReceivedAt.Before(b.ReceivedAt)
		}
		return a.Kind < b.Kind
	})
}

// adAnalyticsEventSequence 在耗时或接收时间相同时按采集流程展示，不能用事件名的字母顺序推断先后。
func adAnalyticsEventSequence(kind string) int {
	switch kind {
	case "ad_trigger":
		return 0
	case "ad_skipped":
		return 1
	case "ad_create":
		return 2
	case "ad_created":
		return 3
	case "ad_request":
		return 4
	case "ad_show_unknown":
		return 5
	case "ad_shown":
		return 6
	case "ad_close_unknown":
		return 7
	case "ad_closed":
		return 8
	case "ad_complete":
		return 9
	case "ad_cancelled":
		return 10
	case "ad_failure":
		return 11
	default:
		return 12
	}
}
