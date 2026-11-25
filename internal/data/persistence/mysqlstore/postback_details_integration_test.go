package mysqlstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/sharding"
)

// TestPostbackDetailsUpgradeIntegration 验证增量升级、旧 Mini、阶段合并、重复投递、筛选和短期清理。
func TestPostbackDetailsUpgradeIntegration(t *testing.T) {
	migration, err := os.ReadFile(filepath.Join(deploymentSQLDirectory(t), "migrations", "20261020_001_postback_details.sql"))
	if err != nil {
		t.Fatal(err)
	}
	f := newIAAFixtureWithSchema(t, strings.Replace(string(readDeploymentSchema(t)), string(migration), "", 1), false)
	ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), 0)
	now := time.Now().UTC().Truncate(time.Microsecond)
	legacy := domain.MetricEvent{ID: "legacy-postback-123456", Kind: "tt_ad_show_success", OccurredAt: now}
	if err := f.store.RecordMetrics(ctx, "9007199254740993", []domain.MetricEvent{legacy}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range strings.Split(string(migration), ";") {
		if strings.TrimSpace(statement) != "" {
			if err := f.store.data.DB().Exec(statement).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	elapsed := 12000.0
	detail := &domain.PostbackDetails{Scene: "entry_reward", ClientVersion: "build-42", Params: map[string]any{"open_id": "[REDACTED]", "if_last_ep": nil, "minis_drama_id": "9007199254740993"}, Warnings: []string{"missing_previous_episode"}, ElapsedMS: &elapsed, Reason: "callback_timeout"}
	id := "detailed-postback-123456"
	events := []domain.MetricEvent{{ID: id, Kind: "tt_ad_show_attempt", OccurredAt: now, Postback: detail}, {ID: id, Kind: "tt_ad_show_unknown", OccurredAt: now, Postback: detail}}
	for i := 0; i < 2; i++ {
		if err := f.store.RecordMetrics(ctx, "9007199254740993", events); err != nil {
			t.Fatal(err)
		}
	}
	query := operations.TikTokPostbackLogQuery{PageSize: 20, Outcome: "unknown", Scene: "entry_reward", ClientVersion: "build-42", From: now.Format(time.DateOnly), Through: now.Format(time.DateOnly)}
	page, err := f.store.ListTikTokPostbackLogs(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Details == nil || page.Items[0].Details.Params["open_id"] != "[REDACTED]" || page.Items[0].UserID != "9007199254740993" {
		t.Fatalf("明细或精度错误: %+v", page.Items)
	}
	// 迟到 SDK 成功只更新事实当前结果，不增加一条列表记录。
	if err := f.store.RecordMetrics(ctx, "9007199254740993", []domain.MetricEvent{{ID: id, Kind: "tt_ad_show_success", OccurredAt: now, Postback: detail}}); err != nil {
		t.Fatal(err)
	}
	page, err = f.store.ListTikTokPostbackLogs(ctx, query)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("成功后仍为未知: %+v %v", page, err)
	}
	all := operations.TikTokPostbackLogQuery{PageSize: 20}
	page, err = f.store.ListTikTokPostbackLogs(ctx, all)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("旧格式或合并失败: %+v %v", page, err)
	}
	for _, item := range page.Items {
		if item.EventID == legacy.ID && item.Details != nil {
			t.Fatal("旧事实不应伪造参数")
		}
	}
	other := application.WithContext(ctx, "102")
	page, err = f.store.ListTikTokPostbackLogs(other, all)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("应用串数: %+v %v", page, err)
	}
	// 数据库写入失败必须回滚计数去重键，客户端重试才能补齐同一事实。
	if err := f.store.data.DB().Exec("RENAME TABLE metric_postback_details TO metric_postback_details_hidden").Error; err != nil {
		t.Fatal(err)
	}
	failed := domain.MetricEvent{ID: "rollback-postback-12345", Kind: "tt_ad_show_failure", OccurredAt: now, Postback: detail}
	if err := f.store.RecordMetrics(ctx, "777", []domain.MetricEvent{failed}); err == nil {
		t.Fatal("缺失明细表应失败")
	}
	var count int64
	if err := f.store.data.DB().Model(&metricEventKey{}).Where("event_id = ?", failed.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("去重键未回滚 %d %v", count, err)
	}
	if err := f.store.data.DB().Exec("RENAME TABLE metric_postback_details_hidden TO metric_postback_details").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordMetrics(ctx, "777", []domain.MetricEvent{failed}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().Model(&metricPostbackDetailModel{}).Where("application_id = ?", "101").Update("received_at", now.Add(-8*24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	page, err = f.store.ListTikTokPostbackLogs(ctx, all)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.Details != nil {
			t.Fatal("超期明细不得继续返回")
		}
	}
	if err := f.store.MaintainMetrics(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().Model(&metricPostbackDetailModel{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("短期明细未清理 %d %v", count, err)
	}
	page, err = f.store.ListTikTokPostbackLogs(ctx, all)
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("清理损坏计数: %+v %v", page, err)
	}
}
