package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"unicode"

	"drama-server/internal/biz/operations"
	"gorm.io/gorm/schema"
)

// deploymentColumnComments 读取最终字段清单，并要求每个实际列都有中文业务说明。
func deploymentColumnComments(t *testing.T) map[string]map[string]string {
	t.Helper()
	result := map[string]map[string]string{}
	tables := regexp.MustCompile("(?s)CREATE TABLE `(\\w+)` \\((.*?)\\) ENGINE=")
	columns := regexp.MustCompile("(?m)^\\s*`(\\w+)` [^\\n]* COMMENT '([^']+)'")
	allColumns := regexp.MustCompile("(?m)^\\s*`(\\w+)` ")
	alterTables := regexp.MustCompile("(?s)ALTER TABLE `(\\w+)`\\s+(.*?);")
	changedColumns := regexp.MustCompile("(?:ADD|MODIFY) COLUMN `(\\w+)` [^\\n]* COMMENT '([^']+)'")
	droppedColumns := regexp.MustCompile("DROP COLUMN `(\\w+)`")
	droppedTables := regexp.MustCompile("DROP TABLE `(\\w+)`")
	for _, statement := range strings.Split(string(readDeploymentSchema(t)), ";") {
		statement += ";"
		for _, table := range tables.FindAllStringSubmatch(statement, -1) {
			result[table[1]] = map[string]string{}
			for _, column := range columns.FindAllStringSubmatch(table[2], -1) {
				if !strings.ContainsFunc(column[2], func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
					t.Errorf("%s.%s 缺少中文业务注释", table[1], column[1])
				}
				result[table[1]][column[1]] = column[2]
			}
			if len(result[table[1]]) != len(allColumns.FindAllStringSubmatch(table[2], -1)) {
				t.Errorf("%s 存在没有字段注释的列", table[1])
			}
		}
		for _, alteration := range alterTables.FindAllStringSubmatch(statement, -1) {
			for _, column := range changedColumns.FindAllStringSubmatch(alteration[2], -1) {
				if result[alteration[1]] == nil {
					t.Fatalf("增量引用未声明的表 %s", alteration[1])
				}
				if !strings.ContainsFunc(column[2], func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
					t.Errorf("%s.%s 缺少中文业务注释", alteration[1], column[1])
				}
				result[alteration[1]][column[1]] = column[2]
			}
			for _, column := range droppedColumns.FindAllStringSubmatch(alteration[2], -1) {
				if _, ok := result[alteration[1]][column[1]]; !ok {
					t.Fatalf("增量删除未声明的列 %s.%s", alteration[1], column[1])
				}
				delete(result[alteration[1]], column[1])
			}
		}
		for _, table := range droppedTables.FindAllStringSubmatch(statement, -1) {
			delete(result, table[1])
		}
	}

	if len(result) == 0 {
		t.Fatal("最终 Schema 没有业务表")
	}
	return result
}

// TestSchemaColumnsMatchPersistenceModels 防止 Schema 和实际 GORM 读写模型单边新增或遗留字段。
func TestSchemaColumnsMatchPersistenceModels(t *testing.T) {
	expected := deploymentColumnComments(t)
	models := []any{
		&subjectModel{}, &tiktokDramaStateModel{},
		&metricPostbackDetailModel{}, &metricAdEventModel{},
		&applicationPlaybackProfileModel{}, &directPublicationModel{}, &directPublicationVersionModel{}, &directPublicationOperationModel{}, &directHomepageRecommendationModel{}, &directCategoryRankingModel{},
		&idAllocatorModel{},
		&globalConfigModel{}, &homepageRecommendationModel{},
		&metricReceiptModel{}, &metricEventKey{}, &dashboardSnapshotModel{}, &metricSummaryState{},
		&metricActiveUserModel{}, &metricSummaryDayModel{},
		&tikTokTagEnglishNameModel{}, &categoryRankingModel{},
		&applicationModel{}, &strategyBindingModel{}, &unlockStrategyModel{},
		&applicationReviewVersionModel{}, &reviewContentListModel{},
		&consumerUserModel{}, &entitlementGrantModel{}, &entitlementStateModel{}, &entitlementRevocationModel{}, &rewardGrantModel{}, &vipProgressModel{}, &adShowModel{},
		&adminUserModel{}, &adminRoleModel{}, &adminPermissionModel{}, &adminUserRoleModel{}, &adminRolePermissionModel{},
		&auditRecordModel{}, &campaignModel{}, &dramaModel{}, &episodeModel{}, &mediaJobModel{}, &mediaReconciliationCheckpointModel{}, &tiktokPublicationModel{}, &distributionModel{},
	}
	actual := map[string]map[string]bool{}
	for _, model := range models {
		parsed, err := schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatal(err)
		}
		if actual[parsed.Table] == nil {
			actual[parsed.Table] = map[string]bool{}
		}
		for _, name := range parsed.DBNames {
			actual[parsed.Table][name] = true
			if _, ok := expected[parsed.Table][name]; !ok {
				t.Errorf("运行模型使用了 Schema 不存在的列 %s.%s", parsed.Table, name)
			}
		}
		// 用户域模型通过 GORM Table 动态路由；01～15 与模型声明的 00 必须保持同构。
		for _, prefix := range []string{"users_", "reward_grants_", "entitlement_grants_", "user_entitlement_state_", "entitlement_revocations_", "application_vip_progress_", "ad_shows_"} {
			if parsed.Table != prefix+"00" {
				continue
			}
			for shard := 1; shard < 16; shard++ {
				table := fmt.Sprintf("%s%02d", prefix, shard)
				actual[table] = map[string]bool{}
				for _, name := range parsed.DBNames {
					actual[table][name] = true
					if _, ok := expected[table][name]; !ok {
						t.Errorf("运行模型使用了 Schema 不存在的列 %s.%s", table, name)
					}
				}
			}
		}
	}
	// 审核与上线记录共用动态表模型；只比较实际落库字段，关联查询投影不属于表结构。
	publication, err := schema.Parse(&publicationRecordModel{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"tiktok_review_records", "tiktok_release_records"} {
		actual[table] = map[string]bool{}
		for _, name := range publication.DBNames {
			switch name {
			case "actor_name", "task_id", "task_status", "task_stage":
				continue
			}
			actual[table][name] = true
			if _, ok := expected[table][name]; !ok {
				t.Errorf("运行模型使用了 Schema 不存在的列 %s.%s", table, name)
			}
		}
	}
	for table, columns := range expected {
		for name, comment := range columns {
			// 明确标记废弃的原列保留在数据库，运行模型必须排除这些列。
			if strings.HasPrefix(comment, "已废弃") {
				if actual[table][name] {
					t.Errorf("运行模型仍使用废弃列 %s.%s", table, name)
				}
				continue
			}
			// 生成列由 MySQL 维护，唯一索引限制同一分集只能有一个进行中上传。
			if table == "media_jobs" && name == "active_episode_id" || table == "subjects" && (name == "publisher_key" || name == "publisher_app_id" || name == "vod_space_key") {
				continue
			}
			if !actual[table][name] {
				t.Errorf("Schema 保留了运行模型未使用的列 %s.%s", table, name)
			}
		}
	}
}

// assertAllColumnsMatchComments 比较真实 MySQL 的列集合和注释，覆盖生成列及窄投影合并后的完整表。
func assertAllColumnsMatchComments(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	expected := deploymentColumnComments(t)
	comments := deploymentColumnComments(t)
	rows, err := db.QueryContext(ctx, "SELECT TABLE_NAME,COLUMN_NAME,COLUMN_COMMENT FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE()")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, column, comment string
		if err := rows.Scan(&table, &column, &comment); err != nil {
			t.Fatal(err)
		}
		template := table
		if metricDayPattern.MatchString(table) {
			template = "metric_events_template"
		}
		if metricMonthPattern.MatchString(table) {
			template = "dashboard_daily_template"
		}
		want, exists := comments[template][column]
		if !exists || comment != want {
			t.Errorf("实际列或注释与当前业务 Schema 不一致：%s.%s", table, column)
		}
		delete(expected[table], column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for table, columns := range expected {
		for column := range columns {
			t.Errorf("实际数据库缺少 %s.%s", table, column)
		}
	}
}

// TestCurrentSchemaApplicationContractsIntegration 从真实后台入口验证最新结构的应用合同与持久化。
func TestCurrentSchemaApplicationContractsIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := context.Background()
	assertAllColumnsMatchComments(t, ctx, f.store.data.pool)
	// 从后台真实 HTTP 入口创建应用，再从持久化读回，确认合同不再要求遗留字段。
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	var login struct {
		// Token 仅用于本次隔离测试请求，不输出到日志。
		Token string `json:"token"`
	}
	if err := json.Unmarshal(iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200), &login); err != nil {
		t.Fatal(err)
	}
	const officialID = "0090071992547409917"
	response := iaaRequest(t, server.URL, "POST", "/admin/v1/applications", login.Token, "", operations.ApplicationInput{SubjectID: "1", TikTokAppID: officialID, ClientKey: "schema-client-key", ClientSecret: "schema-test-secret", Name: "清理后的应用", MonetizationMode: "IAA"}, 201)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(response, &fields); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"brand", "brandJson", "launchUrl", "byteplusAccountId", "byteplusSpaceName"} {
		if _, exists := fields[removed]; exists {
			t.Errorf("应用响应仍暴露废弃字段 %s", removed)
		}
	}
	created, err := f.store.FindApplicationByTikTokAppID(ctx, officialID)
	if err != nil || created.Name != "清理后的应用" || created.TikTokAppID != officialID || created.ClientKey != "schema-client-key" {
		t.Fatal("HTTP 创建结果未按官方字符串身份持久化", err)
	}
}
