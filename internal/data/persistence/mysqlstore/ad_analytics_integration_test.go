package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/sharding"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAdAnalyticsUpgradeIntegration 验证旧库升级、指标幂等、阶段关联、分位数、游标、权限及清理。
func TestAdAnalyticsUpgradeIntegration(t *testing.T) {
	migration, err := os.ReadFile(filepath.Join(deploymentSQLDirectory(t), "migrations", "20261021_001_ad_analytics.sql"))
	if err != nil {
		t.Fatal(err)
	}
	innerMigration, err := os.ReadFile(filepath.Join(deploymentSQLDirectory(t), "migrations", "20261102_002_ad_inner_error.sql"))
	if err != nil {
		t.Fatal(err)
	}
	schema := strings.Replace(string(readDeploymentSchema(t)), string(innerMigration), "", 1)
	f := newIAAFixtureWithSchema(t, strings.Replace(schema, string(migration), "", 1), false)
	db := f.store.data.DB()
	now := time.Now().UTC().Truncate(time.Microsecond)
	ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), 0)
	// 老客户端在升级前仍可保存纯计数。
	if err := f.store.RecordMetrics(ctx, "777", []domain.MetricEvent{{ID: "legacy-ad-metric-1234", Kind: "rewarded_show", OccurredAt: now}}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range strings.Split(string(migration), ";") {
		if strings.TrimSpace(statement) != "" {
			if err := db.Exec(statement).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.Exec(string(innerMigration)).Error; err != nil {
		t.Fatal(err)
	}
	put := func(id, kind string, ms float64, code string) {
		t.Helper()
		detail := &domain.AdMetricDetails{AdType: "rewarded", AdUnitID: "slot-1", Scene: "episode_unlock", ClientVersion: "build-42", OS: "ios", TikTokVersion: "46.1.0", Stage: "show", ErrorCode: code, ShowElapsedMS: &ms}
		if kind == "ad_shown" {
			detail.Evidence = "show_promise"
		}
		event := domain.MetricEvent{ID: id, Kind: kind, OccurredAt: now, Ad: detail}
		for i := 0; i < 2; i++ {
			if err := f.store.RecordMetrics(ctx, "9007199254740993", []domain.MetricEvent{event}); err != nil {
				t.Fatal(err)
			}
		}
	}
	put("shown-ad-call-123456", "ad_request", 0, "")
	put("shown-ad-call-123456", "ad_shown", 100, "")
	put("shown-ad-call-123456", "ad_failure", 120, "50000")
	put("pending-ad-call-1234", "ad_request", 0, "")
	put("pending-ad-call-1234", "ad_show_unknown", 500, "")
	put("failed-ad-call-12345", "ad_request", 0, "")
	put("failed-ad-call-12345", "ad_failure", 30, "50101")
	q := operations.AdAnalyticsQuery{From: now.Format(time.DateOnly), To: now.Format(time.DateOnly), PageSize: 1}
	result, err := f.store.ReadAdAnalytics(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Requests != 3 || result.Summary.Shown != 1 || result.Summary.MatchedShown != 1 || result.Summary.FailedAfterShow != 1 || result.Summary.FailedBeforeShow != 1 || result.Summary.Unknown != 1 || result.Summary.P50MS == nil || *result.Summary.P50MS != 100 || len(result.Items) != 1 || !result.HasMore {
		t.Fatalf("漏斗或分位数错误 %+v", result)
	}
	var parts []string
	b, _ := base64.RawURLEncoding.DecodeString(result.NextCursor)
	if err := json.Unmarshal(b, &parts); err != nil {
		t.Fatal(err)
	}
	q.BeforeAt, _ = time.Parse(time.RFC3339Nano, parts[1])
	q.BeforeUser = parts[2]
	q.BeforeID = parts[3]
	next, err := f.store.ReadAdAnalytics(ctx, q)
	if err != nil || len(next.Items) != 1 || next.Items[0].EventID == result.Items[0].EventID {
		t.Fatalf("游标错误 %+v %v", next, err)
	}
	q.BeforeAt = time.Time{}
	q.ErrorCode = "50000"
	filtered, err := f.store.ReadAdAnalytics(ctx, q)
	if err != nil || filtered.Summary.Requests != 1 || filtered.Summary.Shown != 1 {
		t.Fatalf("错误筛选丢失请求阶段 %+v %v", filtered, err)
	}
	other, err := f.store.ReadAdAnalytics(application.WithContext(ctx, "102"), q)
	if err != nil || other.Summary.Calls != 0 {
		t.Fatalf("应用串数 %+v %v", other, err)
	}
	// 迟到展示仍更新原请求，没有增加分母。
	put("pending-ad-call-1234", "ad_shown", 800, "")
	q.ErrorCode = ""
	late, err := f.store.ReadAdAnalytics(ctx, q)
	if err != nil || late.Summary.Requests != 3 || late.Summary.Shown != 2 || late.Summary.Unknown != 0 || late.Summary.P95MS == nil || *late.Summary.P95MS != 800 {
		t.Fatalf("迟到结果错误 %+v %v", late, err)
	}
	// 展示与关闭超时分别统计；迟到关闭不增加请求或展示，也不抹去展示后明确故障。
	put("shown-ad-call-123456", "ad_close_unknown", 1000, "")
	put("pending-ad-call-1234", "ad_close_unknown", 1000, "")
	unknown, err := f.store.ReadAdAnalytics(ctx, q)
	if err != nil || unknown.Summary.CloseUnknown != 1 || unknown.Summary.Unknown != 0 {
		t.Fatalf("超时阶段混淆 %+v %v", unknown.Summary, err)
	}
	put("pending-ad-call-1234", "ad_closed", 1200, "")
	resolved, err := f.store.ReadAdAnalytics(ctx, q)
	if err != nil || resolved.Summary.CloseUnknown != 0 || resolved.Summary.Requests != 3 {
		t.Fatalf("迟到结果没有归入原请求 %+v %v", resolved.Summary, err)
	}
	// 完播可补证展示，但不能把整段广告时长当成开始展示的耗时。
	put("completed-ad-call-123", "ad_request", 0, "")
	completionMS := 40000.0
	completion := domain.AdMetricDetails{AdType: "rewarded", AdUnitID: "slot-1", Scene: "episode_unlock", ClientVersion: "build-42", OS: "ios", TikTokVersion: "46.1.0", Stage: "show", ShowElapsedMS: &completionMS, Evidence: "completed_close"}
	if err := f.store.RecordMetrics(ctx, "9007199254740993", []domain.MetricEvent{{ID: "completed-ad-call-123", Kind: "ad_shown", OccurredAt: now, Ad: &completion}}); err != nil {
		t.Fatal(err)
	}
	withCompletion, err := f.store.ReadAdAnalytics(ctx, q)
	if err != nil || withCompletion.Summary.Shown != 3 || withCompletion.Summary.TimingSamples != 2 || withCompletion.Summary.P95MS == nil || *withCompletion.Summary.P95MS != 800 {
		t.Fatalf("完播补证污染展示耗时 %+v %v", withCompletion.Summary, err)
	}
	// 写入侧表失败必须回滚整个计数事务。
	if err := db.Exec("RENAME TABLE metric_ad_events TO hidden_ad_events").Error; err != nil {
		t.Fatal(err)
	}
	bad := domain.MetricEvent{ID: "rollback-ad-call-1234", Kind: "ad_request", OccurredAt: now, Ad: &domain.AdMetricDetails{AdType: "rewarded"}}
	if err := f.store.RecordMetrics(ctx, "777", []domain.MetricEvent{bad}); err == nil {
		t.Fatal("缺表应使事务失败")
	}
	var count int64
	if err := db.Model(&metricEventKey{}).Where("event_id=?", bad.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("幂等键未回滚 %d %v", count, err)
	}
	if err := db.Exec("RENAME TABLE hidden_ad_events TO metric_ad_events").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&metricAdEventModel{}).Where("application_id=?", "101").Update("occurred_at", now.AddDate(0, 0, -91)).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.store.MaintainMetrics(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&metricAdEventModel{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("过期事实未清理 %d %v", count, err)
	}
}

// TestAdAnalyticsDirectShowIntegration 验证直接展示的请求分母、孤立展示和历史加载阶段隔离，不改写原记录。
func TestAdAnalyticsDirectShowIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), 0)
	now := time.Now().UTC().Truncate(time.Microsecond)
	put := func(id, kind, stage, version string) {
		t.Helper()
		ms := 100.0
		detail := &domain.AdMetricDetails{AdType: "rewarded", AdUnitID: "slot-1", Scene: "entry_reward",
			ClientVersion: version, OS: "ios", TikTokVersion: "46.1.0", Stage: stage}
		if kind == "ad_shown" {
			detail.ShowElapsedMS, detail.Evidence = &ms, "show_promise"
		}
		for i := 0; i < 2; i++ {
			if err := f.store.RecordMetrics(ctx, "9007199254740993", []domain.MetricEvent{{ID: id, Kind: kind, OccurredAt: now, Ad: detail}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, event := range [][2]string{{"ad_trigger", "gate"}, {"ad_create", "create"}, {"ad_created", "create"}, {"ad_request", "show"}, {"ad_shown", "show"}, {"ad_closed", "close"}, {"ad_complete", "close"}} {
		put("direct-shown-call-1234", event[0], event[1], "build-direct")
	}
	put("create-failed-call-123", "ad_create", "create", "build-direct")
	put("create-failed-call-123", "ad_failure", "create", "build-direct")
	put("orphan-shown-call-123", "ad_shown", "show", "build-orphan")
	// 直接落库模拟已有历史事实；本次变更不执行删除或将其改写成 show。
	for _, kind := range []string{"ad_load_request", "ad_loaded", "ad_load_unknown", "ad_failure"} {
		put("historical-load-call-123", kind, "load", "build-load")
	}
	put("direct-shown-call-1234", "ad_failure", "load", "build-direct")
	q := operations.AdAnalyticsQuery{From: now.Format(time.DateOnly), To: now.Format(time.DateOnly), PageSize: 20}
	result, err := f.store.ReadAdAnalytics(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	summary := result.Summary
	if summary.Calls != 3 || summary.Requests != 1 || summary.Shown != 2 || summary.MatchedShown != 1 || summary.Completed != 1 || summary.CreateFailures != 1 || summary.FailedAfterShow != 0 || summary.TimingSamples != 1 || summary.P95MS == nil || *summary.P95MS != 100 {
		t.Fatalf("直接展示漏斗、关联分母或耗时样本错误 %+v", summary)
	}
	if len(result.Errors) != 1 || result.Errors[0].Stage != "create" {
		t.Fatalf("历史加载故障混入当前错误分布 %+v", result.Errors)
	}
	for _, item := range result.Items {
		for _, event := range item.Events {
			if event.Stage == "load" || strings.HasPrefix(event.Kind, "ad_load") {
				t.Fatal("当前调用明细不应混入加载阶段")
			}
		}
	}
	q.ClientVersion = "build-direct"
	filtered, err := f.store.ReadAdAnalytics(ctx, q)
	if err != nil || filtered.Summary.Calls != 2 || filtered.Summary.Requests != 1 || filtered.Summary.Shown != 1 {
		t.Fatalf("构建筛选未保留完整直接展示阶段 %+v %v", filtered.Summary, err)
	}
	var count int64
	if err := f.store.data.DB().Model(&metricAdEventModel{}).Where("event_id=?", "historical-load-call-123").Count(&count).Error; err != nil || count != 4 {
		t.Fatalf("历史加载事实被改写或删除，记录数 %d，错误 %v", count, err)
	}
}
