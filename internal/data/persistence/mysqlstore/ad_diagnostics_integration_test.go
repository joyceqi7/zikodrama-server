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

// TestAdConflictIntegration 验证两类 11006 合并和原始码保留，不需要诊断列。
func TestAdConflictIntegration(t *testing.T) {
	f := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	db := f.store.data.DB()
	if db.Migrator().HasColumn("metric_ad_events", "diagnostics") {
		t.Fatal("统计库不应存在诊断快照列")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), 0)
	// 写入原始错误形式，核对归类不会改写历史错误码。
	if err := db.Exec(`INSERT INTO metric_ad_events(application_id,user_id,event_id,kind,ad_type,ad_unit_id,scene,client_version,os,tik_tok_version,stage,reason,error_code,sub_error_code,occurred_at,received_at) VALUES(101,777,'legacy-conflict','ad_failure','rewarded','slot','entry_reward','build','ios','46.1','show','sdk_error','104111006','11006',?,?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	event := domain.MetricEvent{ID: "new-conflict", Kind: "ad_failure", OccurredAt: now, Ad: &domain.AdMetricDetails{
		AdType: "rewarded", AdUnitID: "slot", Scene: "entry_reward", ClientVersion: "build", OS: "ios", TikTokVersion: "46.1", Stage: "show", Reason: "ad_active_conflict", ErrorCode: "11006", SubErrorCode: "11006",
	}}
	for i := 0; i < 2; i++ {
		if err := f.store.RecordMetrics(ctx, "777", []domain.MetricEvent{event}); err != nil {
			t.Fatal(err)
		}
	}
	for _, code := range []string{"", "11006", "104111006"} {
		result, err := f.store.ReadAdAnalytics(ctx, operations.AdAnalyticsQuery{From: now.Format(time.DateOnly), To: now.Format(time.DateOnly), PageSize: 20, ErrorCode: code})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Items) != 2 || len(result.Errors) != 1 || result.Errors[0].Count != 2 || result.Errors[0].Reason != "ad_active_conflict" || result.Errors[0].ErrorCode != "11006" {
			t.Fatalf("11006 合并或筛选错误: %+v", result)
		}
		for _, item := range result.Items {
			detail := item.Events[0]
			if item.EventID == "legacy-conflict" {
				if detail.ErrorCode != "104111006" {
					t.Fatal("历史事实被改写")
				}
			} else if detail.ErrorCode != "11006" {
				t.Fatalf("原始诊断丢失: %+v", detail)
			}
		}
	}
	other, err := f.store.ReadAdAnalytics(application.WithContext(ctx, "102"), operations.AdAnalyticsQuery{From: now.Format(time.DateOnly), To: now.Format(time.DateOnly), PageSize: 20})
	if err != nil || len(other.Items) != 0 {
		t.Fatal("诊断跨应用泄漏", err)
	}
}

// TestAdContainerUpgradeIntegration 验证内部码升级保留历史子码，50008 不误合并同主码的其他故障。
func TestAdContainerUpgradeIntegration(t *testing.T) {
	migration, err := os.ReadFile(filepath.Join(deploymentSQLDirectory(t), "migrations", "20261102_002_ad_inner_error.sql"))
	if err != nil {
		t.Fatal(err)
	}
	f := newIAAFixtureWithSchema(t, strings.Replace(string(readDeploymentSchema(t)), string(migration), "", 1), false)
	db := f.store.data.DB()
	if db.Migrator().HasColumn("metric_ad_events", "diagnostics") {
		t.Fatal("统计库不应存在诊断快照列")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), 0)
	for _, row := range [][3]string{{"legacy-container", "50008", "sdk_error"}, {"same-main-other-error", "50010", "sdk_error"}} {
		if err := db.Exec(`INSERT INTO metric_ad_events(application_id,user_id,event_id,kind,ad_type,ad_unit_id,scene,client_version,os,tik_tok_version,stage,reason,error_code,sub_error_code,occurred_at,received_at) VALUES(101,777,?,'ad_failure','rewarded','slot','entry_reward','build','ios','46.1','show',?,'101150000',?,?,?)`, row[0], row[2], row[1], now, now).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(string(migration)).Error; err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"ad_request", "ad_failure"} {
		detail := &domain.AdMetricDetails{AdType: "rewarded", AdUnitID: "slot", Scene: "entry_reward", ClientVersion: "build", OS: "ios", TikTokVersion: "46.1", Stage: "show"}
		if kind == "ad_failure" {
			detail.ErrorCode, detail.SubErrorCode, detail.InnerErrorCode, detail.Reason = "101150000", "50000", "50008", "container_unavailable"
		}
		if err := f.store.RecordMetrics(ctx, "777", []domain.MetricEvent{{ID: "new-container", Kind: kind, OccurredAt: now, Ad: detail}}); err != nil {
			t.Fatal(err)
		}
	}
	query := operations.AdAnalyticsQuery{From: now.Format(time.DateOnly), To: now.Format(time.DateOnly), PageSize: 20, ErrorCode: "50008"}
	result, err := f.store.ReadAdAnalytics(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 || result.Summary.Requests != 1 || len(result.Errors) != 1 || result.Errors[0].Count != 2 || result.Errors[0].ErrorCode != "50008" || result.Errors[0].Reason != "container_unavailable" {
		t.Fatalf("50008 归并或分母错误: %+v", result)
	}
	for _, item := range result.Items {
		for _, event := range item.Events {
			if event.Kind != "ad_failure" {
				continue
			}
			if item.EventID == "legacy-container" {
				if event.ErrorCode != "101150000" || event.SubErrorCode != "50008" || event.InnerErrorCode != "" {
					t.Fatal("升级改写历史码")
				}
			} else if event.SubErrorCode != "50000" || event.InnerErrorCode != "50008" {
				t.Fatal("内部码丢失")
			}
		}
	}
	query.ErrorCode = "101150000"
	all, err := f.store.ReadAdAnalytics(ctx, query)
	if err != nil || len(all.Items) != 3 || len(all.Errors) != 2 {
		t.Fatalf("通用主码查询错误: %+v %v", all, err)
	}
	query.ErrorCode = "50010"
	other, err := f.store.ReadAdAnalytics(ctx, query)
	if err != nil || len(other.Items) != 1 || other.Items[0].EventID != "same-main-other-error" {
		t.Fatalf("其他子码查询错误: %+v %v", other, err)
	}
}
