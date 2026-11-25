package mysqlstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/sharding"
)

// TestAdShowFactsUpgradeIntegration 验证旧库升级保留奖励，展示幂等、主体隔离、剧目范围和当地日期。
func TestAdShowFactsUpgradeIntegration(t *testing.T) {
	migration, err := os.ReadFile(filepath.Join(deploymentSQLDirectory(t), "migrations", "20261008_001_ad_shows.sql"))
	if err != nil {
		t.Fatal(err)
	}
	oldSchema := strings.Replace(string(readDeploymentSchema(t)), string(migration), "", 1)
	f := newIAAFixtureWithSchema(t, oldSchema, false)
	now := time.Date(2026, 10, 8, 15, 59, 0, 0, time.UTC)
	db := f.store.data.DB()
	if err := db.Exec("INSERT INTO reward_grants_07 (bucket_id,claim_id,application_id,visitor_id,drama_id,reward_type,progress_key,ad_unit_id,granted_at) VALUES (7,'existing-entry',101,107,201,'entry_progress','entry-rule','ad',?)", now).Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range strings.Split(string(migration), ";") {
		if strings.TrimSpace(statement) != "" {
			if err := db.Exec(statement).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.Exec("INSERT INTO users_07 (bucket_id,id,application_id) VALUES (7,107,101)").Error; err != nil {
		t.Fatal(err)
	}
	ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), 7)
	show := monetization.AdShow{EventID: "same-display", Scene: monetization.AdSceneEntryReward, AdUnitID: "ad", DramaID: "201", OccurredAt: now}
	var wait sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() { defer wait.Done(); failures <- f.store.RecordAdShow(ctx, "107", show) }()
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	conflicting := show
	conflicting.AdUnitID = "another-ad"
	if err := f.store.RecordAdShow(ctx, "107", conflicting); !errors.Is(err, monetization.ErrInvalidAdShow) {
		t.Fatalf("同键改写广告位未拒绝: %v", err)
	}
	generic := show
	generic.EventID, generic.Scene, generic.DramaID = "generic-event", "home_entry", ""
	if err := f.store.RecordAdShow(ctx, "107", generic); err != nil {
		t.Fatal(err)
	}
	var missingContent int64
	if err := db.Table("ad_shows_07").Where("event_id = ? AND drama_id IS NULL AND episode_id IS NULL", generic.EventID).Count(&missingContent).Error; err != nil || missingContent != 1 {
		t.Fatal("首页场景应无内容目标", missingContent, err)
	}
	generic.EventID = "Generic-event"
	if err := f.store.RecordAdShow(ctx, "107", generic); err != nil {
		t.Fatal(err)
	}
	homeCount, err := f.store.CountAdShows(ctx, "107", monetization.AdShowQuery{Scene: "home_entry", From: now.Add(-time.Hour), Until: now.Add(time.Hour)})
	if err != nil || homeCount != 2 {
		t.Fatalf("场景或大小写幂等键混用: %d %v", homeCount, err)
	}
	show.EventID, show.DramaID = "other-drama", "202"
	if err := f.store.RecordAdShow(ctx, "107", show); err != nil {
		t.Fatal(err)
	}
	period := monetization.RewardPeriodFor(now, application.LocationFromContext(application.WithTimeZone(ctx, "101", "Asia/Shanghai")))
	for _, test := range []struct {
		drama            string
		shown, completed int
	}{{"", 2, 1}, {"201", 1, 1}, {"202", 1, 0}} {
		shown, err := f.store.CountAdShows(ctx, "107", monetization.AdShowQuery{Scene: monetization.AdSceneEntryReward, DramaID: test.drama, From: period.StartsAt, Until: period.ExpiresAt})
		if err != nil || shown != test.shown {
			t.Fatalf("剧目 %s 展示错误: %d %v", test.drama, shown, err)
		}
		completed, err := f.store.CountEntryAdCompletions(ctx, "107", "entry-rule", test.drama, period.StartsAt, period.ExpiresAt)
		if err != nil || completed != test.completed {
			t.Fatalf("剧目 %s 完播错误: %d %v", test.drama, completed, err)
		}
	}
	show.EventID, show.DramaID = "same-display", "202"
	if err := f.store.RecordAdShow(ctx, "107", show); !errors.Is(err, monetization.ErrInvalidAdShow) {
		t.Fatalf("同键不能改写来源剧: %v", err)
	}
	for _, test := range []struct {
		ctx         context.Context
		user, scene string
	}{{ctx, "108", monetization.AdSceneEntryReward}, {application.WithContext(ctx, "102"), "107", monetization.AdSceneEntryReward}, {ctx, "107", "another_scene"}} {
		shown, err := f.store.CountAdShows(test.ctx, test.user, monetization.AdShowQuery{Scene: test.scene, From: period.StartsAt, Until: period.ExpiresAt})
		if err != nil || shown != 0 {
			t.Fatal("应用、用户或规则计数串用", shown, err)
		}
	}
	next, err := f.store.CountAdShows(ctx, "107", monetization.AdShowQuery{Scene: monetization.AdSceneEntryReward, From: period.ExpiresAt, Until: period.ExpiresAt.Add(24 * time.Hour)})
	if err != nil || next != 0 {
		t.Fatal("次日没有清零", next, err)
	}
	// 入剧按剧查询事实；既有计数计入当前剧，另一部剧仍独立累计。
	grant := monetization.RewardGrant{ApplicationID: "101", VisitorID: "107", AdUnitID: "ad", DramaID: "201",
		Scene: monetization.AdSceneEntryReward, RewardType: monetization.RewardTypeEntryProgress,
		ProgressKey: "entry-rule", PeriodKey: period.Key, PeriodStartsAt: period.StartsAt, PeriodExpiresAt: period.ExpiresAt,
		RequiredCount: 2, GrantedAt: now}
	for i := 0; i < 4; i++ {
		grant.ClaimID = fmt.Sprintf("modern-entry-%d", i)
		if i >= 2 {
			grant.DramaID = "202"
		}
		claim, err := f.store.GrantReward(ctx, grant, now)
		if err != nil || claim.RewardType != monetization.RewardTypeEntryProgress || claim.Unlocked {
			t.Fatal("现代入剧奖励未保留事实或错误授予内容权益", claim, err)
		}
	}
	for _, test := range []struct {
		drama     string
		completed int
	}{{"", 5}, {"201", 3}, {"202", 2}} {
		counts, err := f.store.CountEntryAdCompletions(ctx, "107", "entry-rule", test.drama, period.StartsAt, period.ExpiresAt)
		if err != nil || counts != test.completed {
			t.Fatal("完整观看事实丢失或未按剧隔离", counts, err)
		}
	}
	for i := 0; i < 16; i++ {
		var count int64
		if err := db.Table(fmt.Sprintf("ad_shows_%02d", i)).Count(&count).Error; err != nil {
			t.Fatal("新分表缺失", err)
		}
	}
	assertNoDatabaseForeignKeys(t, ctx, f.store.data.pool)
}

// TestAdShowCleanupIntegration 验证现有测试清理脚本包括新事实；预览、缺表拒绝与历史数据清理保持一致。
func TestAdShowCleanupIntegration(t *testing.T) {
	f := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	db := f.store.data.DB()
	script, err := os.ReadFile(filepath.Join(deploymentSQLDirectory(t), "..", "docs", "cleanup-test-user-data.sql"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	start, end := strings.Index(text, "CREATE PROCEDURE "), strings.Index(text, "END$$")
	if start < 0 || end < start {
		t.Fatal("测试数据清理过程缺失")
	}
	if err := db.Exec(text[start : end+len("END")]).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO ad_shows_07 (bucket_id,application_id,visitor_id,event_id,scene,ad_unit_id,drama_id,occurred_at) VALUES (7,101,107,'cleanup-entry','entry_reward','ad',201,UTC_TIMESTAMP(6))").Error; err != nil {
		t.Fatal(err)
	}
	var databaseName string
	if err := db.Raw("SELECT DATABASE()").Scan(&databaseName).Error; err != nil {
		t.Fatal(err)
	}
	call := "CALL cleanup_drama_test_user_data_20260923(?,?)"
	if err := db.Exec("RENAME TABLE ad_shows_15 TO ad_shows_partial").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(call, databaseName, 1).Error; err == nil {
		t.Fatal("展示分表不完整时必须拒绝清理")
	}
	if err := db.Exec("RENAME TABLE ad_shows_partial TO ad_shows_15").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(call, databaseName, 0).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Table("ad_shows_07").Count(&count).Error; err != nil || count != 1 {
		t.Fatal("预览误删展示事实", count, err)
	}
	if err := db.Exec(call, databaseName, 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("ad_shows_07").Count(&count).Error; err != nil || count != 0 {
		t.Fatal("确认清理后残留展示事实", count, err)
	}
	if err := db.Table("id_allocators").Where("namespace = ?", "consumer_domain").Count(&count).Error; err != nil || count != 1 {
		t.Fatal("清理误删 UID 水位", count, err)
	}
}
