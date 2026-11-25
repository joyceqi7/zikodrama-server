package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	styxobserve "gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
	mysqldriver "github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/sharding"
	"drama-server/internal/biz/visitor"
)

// TestCurrentSchemaIntegration 模拟部署流程：先对空数据库执行部署 Schema，再创建
// Styx/GORM 数据源并验证 Repository 依赖的最终结构。服务启动过程不参与建表。
func TestCurrentSchemaIntegration(t *testing.T) {
	requireUIDIntegration(t)
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(application.WithSubject(context.Background(), "1"), 45*time.Second)
	defer cancel()

	config, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ParseTime = true
	config.Loc = time.UTC
	config.DBName = ""
	serverDB, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverDB.Close() })

	databaseName := fmt.Sprintf("drama_schema_%d", time.Now().UnixNano())
	if _, err := serverDB.ExecContext(ctx, "CREATE DATABASE `"+databaseName+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = serverDB.ExecContext(application.WithSubject(context.Background(), "1"), "DROP DATABASE IF EXISTS `"+databaseName+"`")
	})
	config.DBName = databaseName
	schemaDB, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = schemaDB.Close() })
	for _, statement := range strings.Split(string(readDeploymentSchema(t)), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := schemaDB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("执行部署 Schema: %v", err)
		}
	}

	assertNoDatabaseForeignKeys(t, ctx, schemaDB)

	mysqlData, err := OpenData(ctx, &configv1.MySQLConfig{Master: config.FormatDSN()}, newMySQLIntegrationRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	uidClient := newUIDIntegrationClient(t)
	store := NewStore(mysqlData, newUIDIntegrationCache(t, uidClient))
	cleanupUIDIntegrationState(t, store, uidClient)
	// Schema 就绪后通过 Styx GORM 执行 Repository 查询，新访客计数应为零。
	count, err := store.CountRewardGrants(sharding.WithBucketID(ctx, 0), "integration-visitor", "vip", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("通过 Styx GORM 执行 Repository 查询失败：%v", err)
	}
	if count != 0 {
		t.Fatalf("新集成数据库中的奖励计数=%d，期望=0", count)
	}
	assertGORMRewardAndVisitorFlow(t, ctx, store)
	assertGORMPublicationIsolation(t, ctx, store)
	assertGORMDramaMediaJobs(t, ctx, store)
	assertEntryPreviewMediaJobs(t, ctx, store)
	assertGORMAdminJWTFlow(t, ctx, store)
	assertGORMRBACAutoIncrement(t, ctx, store)
	assertColumnsAbsent(t, ctx, store.data.pool, databaseName, "media_asset_references", []string{"language"})
	assertMergedApplicationMonetization(t, ctx, store)
	assertMergedConsumerUsers(t, ctx, store)
	assertPaginatedDramaQueries(t, ctx, store)
	// 精简后的真实库不应重新引入会话表、结构版本表或旧变现表。
	var removedTables int
	if err := store.data.pool.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name IN ('admin_sessions', 'schema_metadata', 'application_monetization_configs', 'visitors', 'tiktok_user_identities', 'access_entitlements')", databaseName).Scan(&removedTables); err != nil {
		t.Fatal(err)
	}
	if removedTables != 0 {
		t.Fatalf("已移除的表被重新创建: %d", removedTables)
	}
	assertInitialAdminPermissions(t, ctx, store.data.pool)
	assertPermissionTreePersistence(t, ctx, store)
	assertColumnsExist(t, ctx, store.data.pool, databaseName, "admin_permissions", []string{"type", "parent_id", "route", "icon", "sort_order", "status", "remark"})
	assertAllTablesHaveComments(t, ctx, store.data.pool, databaseName)
	assertAllColumnsMatchComments(t, ctx, store.data.pool)

	assertColumnsExist(t, ctx, store.data.pool, databaseName, "applications", []string{
		"unlock_strategy_id", "unlock_bindings_json",
	})
	assertColumnsExist(t, ctx, store.data.pool, databaseName, "reward_grants_00", []string{
		"entitlements_json", "rule_snapshot_json", "progress_key", "claim_id", "reward_required_count", "period_key", "granted_at",
	})
	assertColumnsExist(t, ctx, store.data.pool, databaseName, "entitlement_grants_00", []string{"type", "data", "source_item", "valid_from", "valid_until", "revoked_at"})
	assertColumnsExist(t, ctx, store.data.pool, databaseName, "user_entitlement_state_00", []string{"grant_id", "target_key", "valid_from", "valid_until"})
	assertColumnsExist(t, ctx, store.data.pool, databaseName, "media_jobs", []string{
		"kind", "drama_id", "processing_started_at", "processing_deadline_at", "lease_owner", "lease_expires_at",
	})
	assertColumnsExist(t, ctx, store.data.pool, databaseName, "dramas", []string{"platform_album_id", "published_snapshot"})
	assertColumnsExist(t, ctx, store.data.pool, databaseName, "tiktok_drama_states", []string{"subject_id", "drama_id", "platform_album_id", "published_snapshot"})
	assertColumnsAbsent(t, ctx, store.data.pool, databaseName, "applications", []string{"brand_json", "launch_url", "byteplus_account_id", "byteplus_space_name"})
	assertColumnsAbsent(t, ctx, store.data.pool, databaseName, "episodes", []string{"source_url", "upload_job_id"})
	assertColumnsExist(t, ctx, store.data.pool, databaseName, "episodes", []string{
		"platform_episode_id",
	})
	assertColumnsExist(t, ctx, store.data.pool, databaseName, "tiktok_publications", []string{
		"workflow_json", "revision",
		"drama_id", "owner_application_id",
		"status", "stage", "platform_cover_id", "platform_album_id", "platform_version", "review_id",
		"review_status", "platform_publish_status", "episode_ids_json", "error_code", "error_message",
		"attempts", "next_attempt_at", "lease_owner", "lease_expires_at", "submitted_at", "online_at",
	})
	assertColumnsAbsent(t, ctx, store.data.pool, databaseName, "admin_permissions", []string{
		"path", "method", "visible",
	})
	assertColumnsAbsent(t, ctx, store.data.pool, databaseName, "media_assets", []string{"owner_type", "owner_id", "usage_status"})

	assertColumnsAbsent(t, ctx, store.data.pool, databaseName, "applications", []string{
		"mode", "rewarded_placement_id", "vip_organic_placement_id", "vip_campaign_placement_id",
		"daily_pass_launch_prompt_enabled", "daily_pass_force_watch_count", "attempt_ttl_seconds",
	})
	assertColumnsAbsent(t, ctx, store.data.pool, databaseName, "applications", []string{"default_ad_unit_id", "default_locale", "supported_locales_json"})
	assertColumnsAbsent(t, ctx, store.data.pool, databaseName, "episodes", []string{"subtitle_workflow_run_id", "subtitle_generation_status", "subtitle_generation_error", "subtitle_generation_updated_at"})
	assertColumnsAbsent(t, ctx, store.data.pool, databaseName, "tiktok_publications", []string{"subtitle_bindings_json"})
	assertColumnsAbsent(t, ctx, store.data.pool, databaseName, "reward_grants_00", []string{"ad_unit_ids_json", "placement_id"})
	assertColumnsAbsent(t, ctx, store.data.pool, databaseName, "entitlement_grants_00", []string{"plan_id", "plan_version"})
	assertColumnsAbsent(t, ctx, store.data.pool, databaseName, "dramas", []string{
		"platform_cover_id", "review_status", "platform_version", "review_id",
	})
	assertColumnsAbsent(t, ctx, store.data.pool, databaseName, "episodes", []string{
		"platform_cover_id",
	})

	for _, table := range []string{
		"application_categories",
		"playback_sessions", "visitor_sessions", "visitor_follows", "ad_attempts", "consumer_events", "playback_progress", "visitor_vip_activation_events", "maintenance_runs",
		"application_monetization_configs", "application_ad_chains", "ad_chains", "application_monetization_bindings",
		"monetization_plans", "monetization_plan_versions", "reward_progress",
		"application_reward_progress", "application_monetization_chain_items", "visitor_vip_states",
		"application_ad_placements",
	} {
		var count int
		if err := store.data.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema = ? AND table_name = ?`, databaseName, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("legacy table %s still exists", table)
		}
	}
}

// assertInitialAdminPermissions 验证新库自带可编辑的目录、菜单和操作节点，
// 不依赖服务启动、管理员初始化命令或外部配置文件补写数据。
func assertInitialAdminPermissions(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	var count int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin_permissions").Scan(&count); err != nil {
		t.Fatalf("统计系统初始权限：%v", err)
	}
	// 当前增量删除四个重复/废弃菜单与操作节点，新库只保留可用的默认权限。
	expectedCount := strings.Count(strings.ReplaceAll(string(readDeploymentSchema(t)), "`", ""), "INSERT INTO admin_permissions ") - 4
	if count != expectedCount {
		t.Fatalf("系统初始权限不完整：%d", count)
	}
	for _, code := range []string{"menu.tiktoktags", "content.tiktok_tag.read", "content.tiktok_tag.update", "content.drama.read", "operations.carousel.read", "operations.carousel.update", "applications.update", "rbac.permission.read"} {
		var stored string
		if err := database.QueryRowContext(ctx, "SELECT code FROM admin_permissions WHERE code = ?", code).Scan(&stored); err != nil {
			t.Fatalf("权限表缺少系统权限 %s：%v", code, err)
		}
	}
}

// assertGORMAdminJWTFlow 验证管理员初始化和登录使用 JWT 和共享账号认证版本，
// 原始 Token 只在登录响应中出现，并可由任意实例立即撤销。
func assertGORMAdminJWTFlow(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	now := time.Date(2026, time.August, 5, 9, 0, 0, 0, time.UTC)
	service := adminrbac.NewService(store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes"),
		Now: func() time.Time { return now }, SessionTTL: time.Hour,
	})
	hash, err := bcrypt.GenerateFromPassword([]byte("integration-admin-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := store.data.pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx,
		"SET @drama_admin_username = ?, @drama_admin_password_hash = ?, @drama_admin_nickname = ?",
		"integration-admin", string(hash), "集成测试管理员",
	); err != nil {
		t.Fatalf("设置超级管理员 SQL 参数: %v", err)
	}
	for _, statement := range strings.Split(string(readSuperAdminInitializationSQL(t)), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := connection.ExecContext(ctx, statement); err != nil {
			t.Fatalf("执行超级管理员初始化 SQL: %v", err)
		}
	}
	login, err := service.Login(ctx, "integration-admin", "integration-admin-password")
	if err != nil {
		t.Fatalf("管理员登录: %v", err)
	}
	if _, err := service.Authenticate(ctx, login.Token); err != nil {
		t.Fatalf("读取共享管理员会话: %v", err)
	}
	if err := service.Logout(ctx, login.Token); err != nil {
		t.Fatalf("撤销管理员会话: %v", err)
	}
	if _, err := service.Authenticate(ctx, login.Token); !errors.Is(err, adminrbac.ErrUnauthorized) {
		t.Fatalf("已撤销管理员会话仍然有效: %v", err)
	}
	fresh, err := service.Login(ctx, "integration-admin", "integration-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, fresh.Token); err != nil {
		t.Fatalf("重复退出撤销了新 JWT: %v", err)
	}

}

// assertGORMRewardAndVisitorFlow 在真实 MySQL 上覆盖本次 GORM 重写中风险最高的链路：
// 聚合保存、奖励结算幂等、逐集权益、VIP 进度，以及匿名访客合并。
func assertGORMRewardAndVisitorFlow(t *testing.T, baseContext context.Context, store *Store) {
	t.Helper()
	// 首次仪表盘查询按真实当前时间汇总最近 90 个 UTC 自然日，固定历史日期会随时间移出窗口。
	// 使用前一个完整 UTC 日，确保全部账本在统计窗口内，且分钟偏移不会产生未来记录或跨日。
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	now := day.Add(10 * time.Hour)
	applicationID := "1012"
	if err := store.SaveApplication(baseContext, application.Application{SubjectID: "1",
		ID: applicationID, TikTokAppID: applicationID, Name: "GORM Integration", RegionCode: "GLOBAL",
		TimeZone: "UTC",
		AdUnits:  application.AdUnits{RewardedVideo: []string{"episode-ad"}, Interstitial: []string{"interstitial-ad"}},
		Status:   application.StatusActive, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("保存集成测试应用: %v", err)
	}
	monetizationConfig := monetization.ApplicationMonetizationConfig{
		ApplicationID: applicationID, Status: monetization.MonetizationEnabled,
		Strategy: monetization.MonetizationStrategyEpisodeUnlock, EpisodeRewardAdUnitCode: "episode-ad",
		VIPAdUnitCode: "episode-ad", FreeEpisodeCount: 0,
		EpisodeReward: monetization.EpisodeRewardPolicy{UnlockEpisodeCount: 1, DailyRewardLimit: 5},
		VIP:           monetization.ApplicationVIPPolicy{Enabled: true, RequiredAdCount: 1},
		CreatedAt:     now, UpdatedAt: now,
	}
	if err := store.SaveApplicationMonetization(baseContext, monetizationConfig); err != nil {
		t.Fatalf("保存集成测试变现配置: %v", err)
	}
	monetizationConfig.UpdatedAt = now.Add(time.Second)
	if err := store.SaveApplicationMonetization(baseContext, monetizationConfig); err != nil {
		t.Fatalf("按上一修订号更新变现配置: %v", err)
	}
	if err := store.SaveApplicationMonetization(baseContext, monetizationConfig); err != nil {
		t.Fatalf("相同配置重复保存失败: %v", err)
	}
	if err := store.SaveDrama(baseContext, domain.Drama{
		ID: "1010", Title: "GORM Drama", Description: "integration", Status: domain.DramaStatusCompleted,
		Language: "en", PublishStatus: "published",
	}); err != nil {
		t.Fatalf("保存集成测试短剧: %v", err)
	}
	if err := store.SaveEpisode(baseContext, domain.Episode{
		ID: "1011", DramaID: "1010", Number: 1, Title: "Episode 1",
		VID: "gorm-vid", MediaStatus: "ready",
	}); err != nil {
		t.Fatalf("保存集成测试分集: %v", err)
	}
	if err := store.data.DB().WithContext(baseContext).Table("users_00").Create(&consumerUserModel{ID: "1031", BucketID: 0, ApplicationID: applicationID}).Error; err != nil {
		t.Fatalf("创建源 Visitor: %v", err)
	}
	ctx := application.WithContext(baseContext, applicationID)
	ctx = sharding.WithBucketID(ctx, 0)
	period := monetization.RewardPeriodFor(now, time.UTC)
	episodeGrant := monetization.RewardGrant{
		ClaimID: "gorm-episode-claim", ApplicationID: applicationID,
		VisitorID: "1031", DramaID: "1010", EpisodeID: "1011",
		UnlockEpisodeIDs: []string{"1011"}, AdUnitID: "episode-ad",
		Scene: monetization.AdSceneEpisodeUnlock, RewardType: monetization.RewardTypeEpisodeUnlock,
		PeriodKey:      period.Key,
		PeriodStartsAt: period.StartsAt, PeriodExpiresAt: period.ExpiresAt,
		DailyRewardLimit: 5, EntitlementScope: monetization.EntitlementScopeEpisode,
		GrantedAt: now.Add(time.Minute),
	}
	claim, err := store.GrantReward(ctx, episodeGrant, episodeGrant.GrantedAt)
	if err != nil || claim.Status != monetization.ClaimStatusGranted || !claim.Unlocked {
		t.Fatalf("领取逐集权益失败：result=%+v err=%v", claim, err)
	}
	claim, err = store.GrantReward(ctx, episodeGrant, episodeGrant.GrantedAt)
	if err != nil || claim.Status != monetization.ClaimStatusAlreadyGranted {
		t.Fatalf("重复 ClaimID 没有幂等恢复：result=%+v err=%v", claim, err)
	}

	vipGrant := monetization.RewardGrant{
		ClaimID: "gorm-vip-claim-0001", ApplicationID: applicationID,
		VisitorID: "1031", AdUnitID: "episode-ad",
		Scene: monetization.AdSceneVIPReward, RewardType: monetization.RewardTypeVIPProgress,
		PeriodKey:      period.Key,
		PeriodStartsAt: period.StartsAt, PeriodExpiresAt: period.ExpiresAt, RequiredCount: 1,
		EntitlementScope: monetization.EntitlementScopeCatalog, GrantedAt: now.Add(3 * time.Minute),
	}
	claim, err = store.GrantReward(ctx, vipGrant, vipGrant.GrantedAt)
	if err != nil || claim.VIPProgress == nil || claim.VIPProgress.Active {
		t.Fatalf("VIP 广告达标后不能自动领取权益：result=%+v err=%v", claim, err)
	}
	vip, err := store.HasCatalogEntitlement(ctx, "1031", vipGrant.GrantedAt)
	if err != nil || vip {
		t.Fatalf("手动领取前不应有 VIP 权益：active=%t err=%v", vip, err)
	}
	claimed, err := store.ReconcileVIPProgress(ctx, "1031", monetizationConfig.Policy(), period, vipGrant.GrantedAt)
	if err != nil || !claimed.Active {
		t.Fatalf("达标后手动领取权益失败：state=%+v err=%v", claimed, err)
	}

	unlocked, err := store.HasEntitlement(ctx, "1031", "1010", "1011", episodeGrant.GrantedAt)
	if err != nil || !unlocked {
		t.Fatalf("逐集权益缺失：unlocked=%t err=%v", unlocked, err)
	}
	vip, err = store.HasCatalogEntitlement(ctx, "1031", vipGrant.GrantedAt)
	if err != nil || !vip {
		t.Fatalf("VIP 权益缺失：active=%t err=%v", vip, err)
	}
	mergedGrant, found, err := store.FindRewardGrant(ctx, "1031", episodeGrant.ClaimID)
	if err != nil || !found || mergedGrant.VisitorID != "1031" {
		t.Fatalf("奖励流水没有归属领取用户：grant=%+v found=%t err=%v", mergedGrant, found, err)
	}
	metrics, err := store.DashboardDays(ctx, day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("从奖励和权益账本聚合仪表盘指标失败：%v", err)
	}
	if len(metrics) != 1 || metrics[0].RewardCompletions != 2 || metrics[0].EpisodeUnlocks != 1 || metrics[0].VIPActivations != 1 {
		t.Fatalf("仪表盘指标没有从权威账本正确聚合：%+v", metrics)
	}

	// 入剧广告只在奖励流水里累计，达标后仍保留完整观看事实，不写分集或 VIP 权益。
	entryGrant := monetization.RewardGrant{
		ClaimID: "gorm-entry-claim-1", ApplicationID: applicationID, VisitorID: "1031", AdUnitID: "episode-ad",
		Scene: monetization.AdSceneEntryReward, RewardType: monetization.RewardTypeEntryProgress,
		ProgressKey: "strategy-1:entry_ad", PeriodKey: period.Key,
		PeriodStartsAt: period.StartsAt, PeriodExpiresAt: period.ExpiresAt,
		RequiredCount: 2, GrantedAt: now.Add(5 * time.Minute),
	}
	for index := 1; index <= 2; index++ {
		entryGrant.ClaimID = fmt.Sprintf("gorm-entry-claim-%d", index)
		entryGrant.GrantedAt = now.Add(time.Duration(index+4) * time.Minute)
		claim, err = store.GrantReward(ctx, entryGrant, entryGrant.GrantedAt)
		if err != nil || claim.Status != monetization.ClaimStatusGranted || claim.Unlocked || claim.RewardType != monetization.RewardTypeEntryProgress {
			t.Fatalf("入剧广告结算不应授予内容权益：result=%+v err=%v", claim, err)
		}
	}
	entryCount, err := store.CountRewardGrants(ctx, "1031", monetization.RewardTypeEntryProgress, period.StartsAt, entryGrant.ProgressKey)
	if err != nil || entryCount != 2 {
		t.Fatalf("当天入剧广告进度错误：count=%d err=%v", entryCount, err)
	}
	entryGrant.ClaimID = "gorm-entry-claim-3"
	if extra, err := store.GrantReward(ctx, entryGrant, now.Add(7*time.Minute)); err != nil || extra.Status != monetization.ClaimStatusGranted {
		t.Fatalf("入剧广告达标后仍须幂等保存：%+v %v", extra, err)
	}
	entryGrant.ClaimID = "gorm-entry-claim-1"
	claim, err = store.GrantReward(ctx, entryGrant, now.Add(7*time.Minute))
	if err != nil || claim.Status != monetization.ClaimStatusAlreadyGranted {
		t.Fatalf("入剧广告重复 ClaimID 没有幂等恢复：result=%+v err=%v", claim, err)
	}
	nextPeriod := monetization.RewardPeriodFor(now.Add(24*time.Hour), time.UTC)
	entryCount, err = store.CountRewardGrants(ctx, "1031", monetization.RewardTypeEntryProgress, nextPeriod.StartsAt, entryGrant.ProgressKey)
	if err != nil || entryCount != 0 {
		t.Fatalf("次日入剧广告进度未清零：count=%d err=%v", entryCount, err)
	}

	// 两个实例可能同时拿到同一 OpenID 的首次登录码。数据库 Upsert 必须让它们都成功，
	// 并统一解析到第一个完成绑定的 Visitor，不能把重复键错误暴露给消费者。
	start := make(chan struct{})
	results := make([]string, 2)
	errorsByCall := make([]error, 2)
	var wait sync.WaitGroup
	for index := range 2 {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errorsByCall[index] = store.ResolveTikTokIdentity(baseContext, visitor.TikTokIdentityRecord{
				ApplicationID: applicationID, OpenIDHash: "concurrent-open-id-hash",
			})
		}(index)
	}
	close(start)
	wait.Wait()
	if errorsByCall[0] != nil || errorsByCall[1] != nil || results[0] == "" || results[0] != results[1] {
		t.Fatalf("并发 OpenID 绑定结果不一致：visitor=%v errors=%v", results, errorsByCall)
	}
}

// assertAllTablesHaveComments 检查业务表的真实 MySQL TABLE_COMMENT，避免 SQL 文本
// 看似带注释但被数据库语法位置或执行方式忽略。
func assertAllTablesHaveComments(t *testing.T, ctx context.Context, db *sql.DB, databaseName string) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = ? AND table_type = 'BASE TABLE' AND TRIM(table_comment) = ''
		ORDER BY table_name`, databaseName)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(tables) > 0 {
		t.Fatalf("以下数据表缺少 TABLE_COMMENT：%v", tables)
	}
}

func assertColumnsExist(t *testing.T, ctx context.Context, db *sql.DB, databaseName, table string, columns []string) {
	t.Helper()
	for _, column := range columns {
		if columnCount(t, ctx, db, databaseName, table, column) != 1 {
			t.Fatalf("expected %s.%s to exist", table, column)
		}
	}
}

func assertColumnsAbsent(t *testing.T, ctx context.Context, db *sql.DB, databaseName, table string, columns []string) {
	t.Helper()
	for _, column := range columns {
		if columnCount(t, ctx, db, databaseName, table, column) != 0 {
			t.Fatalf("legacy column %s.%s still exists", table, column)
		}
	}
}

func columnCount(t *testing.T, ctx context.Context, db *sql.DB, databaseName, table, column string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema = ? AND table_name = ? AND column_name = ?`, databaseName, table, column).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func newMySQLIntegrationRuntime(t *testing.T) *styxruntime.Runtime {
	t.Helper()
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity:     styxruntime.Identity{Name: "mysql-integration-test", Version: "test"},
		Dependencies: styxobserve.NoopDependencies(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(application.WithSubject(context.Background(), "1")) })
	return runtime
}
