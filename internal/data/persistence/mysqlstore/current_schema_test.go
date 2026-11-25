package mysqlstore

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDeploymentSQLInventory 固定结构增量统一放入 migrations，顶层只保留明确列出的初始化及物理分表维护入口。
func TestDeploymentSQLInventory(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(deploymentSQLDirectory(t), "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 || filepath.Base(files[0]) != "feat_veplayer_tables.sql" || filepath.Base(files[1]) != "init.sql" || filepath.Base(files[2]) != "upgrade-metric-behavior-index.sql" || filepath.Base(files[3]) != "upgrade-non-uid-auto-increment.sql" {
		t.Fatalf("sql 顶层只允许初始化、旧非 UID 主键升级、指标日表索引维护和独立 VePlayer 建表脚本，实际为 %v", files)
	}
	migrations, err := filepath.Glob(filepath.Join(deploymentSQLDirectory(t), "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) == 0 {
		t.Fatal("当前版本缺少迭代增量脚本")
	}
	pattern := regexp.MustCompile(`^[0-9]{8}_[0-9]{3}_[a-z][a-z0-9_]*\.sql$`)
	for _, file := range migrations {
		if !pattern.MatchString(filepath.Base(file)) {
			t.Errorf("增量脚本须按日期、当日序号及用途命名：%s", file)
		}
	}
}

// TestNonUIDAutoIncrementUpgradeCoverage 防止已有库的升级脚本遗漏某张分表或误改用户 UID。
func TestNonUIDAutoIncrementUpgradeCoverage(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(deploymentSQLDirectory(t), "upgrade-non-uid-auto-increment.sql"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	if count := strings.Count(script, "ALTER TABLE `"); count != 80 {
		t.Fatalf("非 UID 主键升级语句数量=%d，期望=80", count)
	}
	for _, prefix := range []string{"entitlement_grants_", "user_entitlement_state_", "entitlement_revocations_", "reward_grants_", "application_vip_progress_"} {
		for shard := 0; shard < 16; shard++ {
			table := fmt.Sprintf("%s%02d", prefix, shard)
			if count := strings.Count(script, "ALTER TABLE `"+table+"`"); count != 1 {
				t.Errorf("%s 的升级语句数量=%d，期望=1", table, count)
			}
		}
	}
	if strings.Contains(script, "ALTER TABLE `users_") || strings.Contains(script, "ALTER TABLE `id_allocators`") {
		t.Error("非 UID 主键升级不得修改用户 UID 或号段水位")
	}
	if count := strings.Count(script, "MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL"); count != 16 {
		t.Errorf("权益投影关联字段升级语句数量=%d，期望=16", count)
	}
}

// TestCurrentSchemaConsumerIDGeneration 保证只有用户 UID 使用号段，内部记录由各物理表生成主键。
func TestCurrentSchemaConsumerIDGeneration(t *testing.T) {
	for shard := 0; shard < 16; shard++ {
		userTable := fmt.Sprintf("users_%02d", shard)
		userDDL := readDeploymentTableSQL(t, userTable)
		if strings.Contains(userDDL, "AUTO_INCREMENT") || !strings.Contains(userDDL, "`id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId'") {
			t.Errorf("%s 必须保留现有非自增 UID 主键", userTable)
		}
		for _, prefix := range []string{"entitlement_grants_", "user_entitlement_state_", "entitlement_revocations_", "reward_grants_", "application_vip_progress_", "ad_shows_"} {
			table := fmt.Sprintf("%s%02d", prefix, shard)
			ddl := readDeploymentTableSQL(t, table)
			if !strings.Contains(ddl, "`id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的") {
				t.Errorf("%s 的内部主键必须由所属物理表自增生成，并注明唯一性范围", table)
			}
			if prefix == "user_entitlement_state_" && !strings.Contains(ddl, "`grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id'") {
				t.Errorf("%s.grant_id 必须保留无外键的同类型关联，并注明用户分片范围", table)
			}
		}
	}
	if !strings.Contains(string(readDeploymentSchema(t)), "VALUES ('consumer_domain', 1, 100000, UTC_TIMESTAMP(6));") {
		t.Error("消费者用户 UID 号段的初始化种子不得改变")
	}
}

// TestCurrentSchemaInitializationInventory 固定最终基础表、分片表及历史权限 INSERT 声明数量；声明数不代表删除旧菜单后的实际节点存量。
func TestCurrentSchemaInitializationInventory(t *testing.T) {
	schema := string(readDeploymentSchema(t))
	if count := len(deploymentColumnComments(t)); count != 152 {
		t.Fatalf("最终基础表数量=%d，期望=152", count)
	}
	sharded := 0
	for _, prefix := range []string{"users_", "reward_grants_", "entitlement_grants_", "user_entitlement_state_", "entitlement_revocations_", "application_vip_progress_", "ad_shows_"} {
		for _, suffix := range []string{"00", "01", "02", "03", "04", "05", "06", "07", "08", "09", "10", "11", "12", "13", "14", "15"} {
			name := prefix + suffix
			if !strings.Contains(schema, "CREATE TABLE `"+name+"`") {
				t.Fatalf("初始化脚本缺少分片表 %s", name)
			}
			sharded++
		}
	}
	if sharded != 112 {
		t.Fatalf("初始化分片表数量=%d，期望=112", sharded)
	}
	normalized := strings.ReplaceAll(schema, "`", "")
	if count := strings.Count(normalized, "INSERT INTO admin_permissions "); count != 112 {
		t.Fatalf("初始化权限声明数量=%d，期望=112", count)
	}
}

func TestCurrentSchemaContainsOnlyRuntimeMonetizationModel(t *testing.T) {
	contents := readDeploymentSchema(t)
	schema := string(contents)
	for _, required := range []string{
		"SET NAMES utf8mb4;",
		"INSERT INTO `admin_permissions`",
		"'rbac.permission.read'",
		"CREATE TABLE `applications`",
		"CREATE TABLE `users_00`",
		"`auth_version` bigint unsigned NOT NULL DEFAULT '1'",
		"UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`)",
		"`platform_album_id` varchar(255)",
		"`platform_episode_id` varchar(255)",
		"CREATE TABLE `tiktok_publications`",
		"`episode_ids_json` json NOT NULL",
		"`lease_expires_at` datetime(6) NOT NULL",
		"`ad_units_json` json NOT NULL",

		"CREATE TABLE `reward_grants_00`", "CREATE TABLE `entitlement_grants_00`", "CREATE TABLE `user_entitlement_state_00`",
		"CREATE TABLE `unlock_strategies`", "`application_id` bigint unsigned NOT NULL COMMENT '策略唯一所属的应用主键'", "UNIQUE KEY `uq_unlock_strategy_application` (`application_id`)", "`rules_json` json NOT NULL", "`unlock_strategy_id`", "`unlock_bindings_json`", "`source_item`", "`revoked_at`", "`data` json NOT NULL",

		"CREATE TABLE `application_vip_progress_00`",
		"CREATE TABLE `media_jobs`", "CREATE TABLE `media_reconciliation_checkpoint`", "`cover_mid`", "`media_space_name`", "`kind`", "`drama_id`",
	} {
		if !strings.Contains(schema, required) {
			t.Fatalf("最终 schema 缺少运行期结构 %q", required)
		}
	}
	for _, removed := range []string{
		"`monetization_revision`", "`configuration_revision`",
		"`brand_json`", "`launch_url`", "`byteplus_account_id`", "`byteplus_space_name`", "`source_url`", "`upload_job_id`",
		"`monetization_status`", "`strategy_type` varchar(64)", "`vip_strong_watch_ad_count`", "`vip_required_ad_count`", "`episode_reward_ad_unit_code`", "`vip_ad_unit_code`",

		"CREATE TABLE `media_assets`", "CREATE TABLE `media_asset_references`", "CREATE TABLE `media_deletion_jobs`", "CREATE TABLE `vod_event_inbox`",
		"CREATE TABLE `schema_metadata`", "CREATE TABLE `admin_sessions`",
		"CREATE TABLE `visitors`", "CREATE TABLE `tiktok_user_identities`",
		"CREATE TABLE `access_entitlements`", "`open_id_ciphertext`", "`access_token_ciphertext`", "`refresh_token_ciphertext`", "episode_subtitle",
		"CREATE TABLE `application_monetization_configs`",
		"CREATE TABLE `drama_translations`", "CREATE TABLE `episode_translations`",
		"CREATE TABLE `tiktok_tag_translations`", "CREATE TABLE `application_monetization_config_translations`",
		"`default_locale`", "`supported_locales_json`", "`subtitle_workflow_run_id`",
		"`subtitle_generation_status`", "`subtitle_generation_error`", "`subtitle_generation_updated_at`",
		"`subtitle_bindings_json`",
		"CREATE TABLE `playback_sessions`",
		"forensic_watermark",
		"CREATE TABLE `visitor_sessions`",
		"CREATE TABLE `visitor_follows`",
		"CREATE TABLE `ad_attempts`",
		"CREATE TABLE `consumer_events`",
		"CREATE TABLE `playback_progress`",
		"CREATE TABLE `visitor_vip_activation_events`",
		"CREATE TABLE `maintenance_runs`",
		"CREATE TABLE `ad_chains`",
		"CREATE TABLE `application_ad_chains`",
		"CREATE TABLE `monetization_plans`",
		"CREATE TABLE `application_daily_pass_progress`",
		"`plan_id`",
		"`plan_version`",
		"`vip_required_count`",
		"`attempt_ttl_seconds`",
		"`default_ad_unit_id`",
		"`ad_unit_ids_json`",
		"CREATE TABLE `application_ad_placements`",
		"applications.ad_placement.read",
		"applications.ad_placement.update",
		"'/v1/admin/",
		"rewarded_daily_pass",
		"`application_id` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT ''",
		"CREATE TABLE `media_cleanup_jobs`",
		"CREATE TABLE `global_media_config`",
		"permission.menu.system.media-config",
		"media_config.read",
		"media_config.update",
		"`owner_type`",
		"`owner_id`",
		"`usage_status`",
		"供运营辨识的策略名称",
		"NULL 表示全局共享策略",
	} {
		if strings.Contains(schema, removed) {
			t.Fatalf("最终 schema 仍包含已移除兼容结构 %q", removed)
		}
	}
}

// TestCurrentSchemaTablesHaveBusinessComments 保证运维人员通过数据库管理工具查看表时，
// 不需要反查 Go 类型或历史迁移才能理解表的业务用途。
func TestCurrentSchemaTablesHaveBusinessComments(t *testing.T) {
	contents := readDeploymentSchema(t)
	count := 0
	for _, statement := range strings.Split(string(contents), ";") {
		if !strings.Contains(statement, "CREATE TABLE `") {
			continue
		}
		count++
		if !strings.Contains(statement, " COMMENT='") {
			name := strings.SplitN(statement, "`", 3)
			if len(name) > 1 {
				t.Errorf("数据表 %s 缺少业务注释", name[1])
			} else {
				t.Errorf("建表语句缺少业务注释：%s", strings.TrimSpace(statement))
			}
		}
	}
	if count == 0 {
		t.Fatal("最终 schema 未声明任何业务数据表")
	}
}

func TestSuperAdministratorIsInitializedByParameterizedSQL(t *testing.T) {
	script := string(readSuperAdminInitializationSQL(t))
	for _, required := range []string{
		"INSERT INTO `admin_users`",
		"@drama_admin_username",
		"@drama_admin_password_hash",
		"CHAR_LENGTH(TRIM(@drama_admin_password_hash)) = 60",
		"`is_super`",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("超级管理员初始化 SQL 缺少 %q", required)
		}
	}
	for _, forbidden := range []string{"123456", "ON DUPLICATE KEY UPDATE", "password ="} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("超级管理员初始化 SQL 包含不安全内容 %q", forbidden)
		}
	}
}

func TestRBACSchemaUsesDatabaseGeneratedIDs(t *testing.T) {
	schema := string(readDeploymentSchema(t))
	for _, table := range []string{"admin_users", "admin_roles", "admin_permissions"} {
		start := strings.Index(schema, "CREATE TABLE `"+table+"` (")
		if start < 0 {
			t.Fatalf("缺少 RBAC 表 %s", table)
		}
		block := strings.SplitN(schema[start:], ") ENGINE=", 2)[0]
		if !strings.Contains(block, "`id` bigint unsigned NOT NULL AUTO_INCREMENT") {
			t.Errorf("%s 主键不是数据库自增整数", table)
		}
	}
	for _, part := range []struct{ table, column string }{{"admin_user_roles", "user_id"}, {"admin_user_roles", "role_id"}, {"admin_role_permissions", "role_id"}, {"admin_role_permissions", "permission_id"}} {
		start := strings.Index(schema, "CREATE TABLE `"+part.table+"` (")
		if start < 0 {
			t.Fatalf("缺少关联表 %s", part.table)
		}
		block := strings.SplitN(schema[start:], ") ENGINE=", 2)[0]
		if !strings.Contains(block, "`"+part.column+"` bigint unsigned NOT NULL") {
			t.Errorf("%s.%s 关联字段类型不一致", part.table, part.column)
		}
	}
	if strings.Contains(schema, "INSERT INTO `admin_permissions` (`id`,") || strings.Contains(schema, "'permission.rbac.") {
		t.Error("权限初始化仍指定业务字符串主键")
	}
	script := string(readSuperAdminInitializationSQL(t))
	if strings.Contains(script, "UUID()") || strings.Contains(script, "(`id`,") {
		t.Error("超级管理员初始化仍指定主键")
	}
}

// TestAdShowSchemaIsSceneIndependent 防止通用展示表重新加入策略门槛、奖励 Offer 或任意 JSON 载荷。
func TestAdShowSchemaIsSceneIndependent(t *testing.T) {
	for shard := 0; shard < 16; shard++ {
		ddl := readDeploymentTableSQL(t, fmt.Sprintf("ad_shows_%02d", shard))
		for _, forbidden := range []string{"`context_json`", "`progress_key`", "`offer_id`", "`required_ad_count`", "`count_by`", "`scope`"} {
			if strings.Contains(ddl, forbidden) {
				t.Errorf("展示事实表不应包含 %s", forbidden)
			}
		}
		for _, required := range []string{"`scene`", "`event_id`", "`ad_unit_id`", "`drama_id` bigint unsigned DEFAULT NULL", "`episode_id` bigint unsigned DEFAULT NULL"} {
			if !strings.Contains(ddl, required) {
				t.Errorf("通用展示字段缺失 %s", required)
			}
		}
	}
}
