package mysqlstore

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// applyMetricBehaviorIndexUpgrade 按 mysql 客户端的分隔符执行日表索引升级，过程体中的分号不拆为独立语句。
func applyMetricBehaviorIndexUpgrade(t *testing.T, db *gorm.DB) error {
	t.Helper()
	script := string(readDeploymentSQL(t, "upgrade-metric-behavior-index.sql"))
	script = strings.ReplaceAll(script, "DELIMITER $$", "")
	script = strings.ReplaceAll(script, "DELIMITER ;", "")
	for _, statement := range strings.Split(script, "$$") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

// TestMetricBehaviorIndexUpgradeIntegration 验证模板增量、已有日表补索引及重复升级均保留原始事实和去重键。
func TestMetricBehaviorIndexUpgradeIntegration(t *testing.T) {
	migration := string(readDeploymentSQL(t, "migrations/20261026_002_metric_behavior_index.sql"))
	before := strings.Replace(string(readDeploymentSchema(t)), migration, "", 1)
	f := newIAAFixtureWithSchema(t, before, false)
	db := f.store.data.DB()
	ctx := context.Background()
	for _, date := range []string{"2026-10-25", "2026-10-26"} {
		at, _ := time.Parse(time.DateOnly, date)
		table := metricDayTable(at)
		if err := f.store.ensureMetricTable(ctx, table, "metric_events_template"); err != nil {
			t.Fatal(err)
		}
		row := metricReceiptModel{ApplicationID: "101", UserID: "70101", EventID: "before-index-" + date, Kind: "launch", OccurredAt: at, ReceivedAt: at}
		if err := db.Table(table).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&metricEventKey{ApplicationID: row.ApplicationID, UserID: row.UserID, EventID: row.EventID, Kind: row.Kind, EventDate: date}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(migration).Error; err != nil {
		t.Fatal(err)
	}
	// 已有日表不会随模板 ALTER 自动获得索引，发布前必须补齐。
	if db.Migrator().HasIndex("metric_events_20261025", "idx_metrics_app_kind_received") {
		t.Fatal("旧日表在升级前意外包含新索引")
	}
	for range 2 {
		if err := applyMetricBehaviorIndexUpgrade(t, db); err != nil {
			t.Fatal(err)
		}
	}
	newDay := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	if err := f.store.ensureMetricTable(ctx, metricDayTable(newDay), "metric_events_template"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"metric_events_template", "metric_events_20261025", "metric_events_20261026", "metric_events_20261027"} {
		var columns string
		if err := db.Raw("SELECT GROUP_CONCAT(column_name ORDER BY seq_in_index SEPARATOR ',') FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND index_name='idx_metrics_app_kind_received'", table).Scan(&columns).Error; err != nil {
			t.Fatal(err)
		}
		if columns != "application_id,kind,received_at" {
			t.Fatalf("%s 缺少完整覆盖索引: %s", table, columns)
		}
	}
	var keys int64
	if err := db.Model(&metricEventKey{}).Count(&keys).Error; err != nil || keys != 2 {
		t.Fatalf("升级改变去重键: count=%d err=%v", keys, err)
	}
	from := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	query, args := dashboardBehaviorQuery([]string{"metric_events_20261025", "metric_events_20261026"}, from, newDay, "101", true)
	var rows []struct {
		// Launches 校验已有日表的事实仍可按新查询完整汇总。
		Launches int64
	}
	if err := db.Raw(query, args...).Scan(&rows).Error; err != nil || len(rows) != 2 || rows[0].Launches != 1 || rows[1].Launches != 1 {
		t.Fatalf("已有事实在索引升级后无法读取: rows=%+v err=%v", rows, err)
	}
}

// TestMetricBehaviorIndexUpgradeRejectsWrongDefinitionIntegration 验证同名异构索引不会被静默视为已升级。
func TestMetricBehaviorIndexUpgradeRejectsWrongDefinitionIntegration(t *testing.T) {
	f := newIAAFixture(t)
	db := f.store.data.DB()
	if err := db.Exec("CREATE TABLE metric_events_20261025 LIKE metric_events_template").Error; err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, definition string }{
		{"列顺序不同", "INDEX idx_metrics_app_kind_received(application_id,received_at,kind)"},
		{"类型只有前缀", "INDEX idx_metrics_app_kind_received(application_id,kind(8),received_at)"},
		{"错误的唯一索引", "UNIQUE INDEX idx_metrics_app_kind_received(application_id,kind,received_at)"},
		{"索引不可见", "INDEX idx_metrics_app_kind_received(application_id,kind,received_at) INVISIBLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := db.Exec("ALTER TABLE metric_events_20261025 DROP INDEX idx_metrics_app_kind_received, ADD " + test.definition).Error; err != nil {
				t.Fatal(err)
			}
			if err := applyMetricBehaviorIndexUpgrade(t, db); err == nil || !strings.Contains(err.Error(), "Invalid metric index on metric_events_20261025:") || !strings.Contains(err.Error(), "invalid_parts=") {
				t.Fatalf("同名异构索引未明确拒绝: %v", err)
			}
		})
	}
}

// TestMetricBehaviorIndexUpgradeScopeIntegration 验证游标只补精确日表索引，不误改相似表名且保留全部原始字段。
func TestMetricBehaviorIndexUpgradeScopeIntegration(t *testing.T) {
	f := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	db := f.store.data.DB()
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	row := metricReceiptModel{BucketID: 7, ApplicationID: "101", UserID: "9007199254740993", EventID: "scope-preserved-event", Kind: "launch", OccurredAt: at, ReceivedAt: at.Add(time.Minute)}
	tables := []struct {
		name   string
		target bool
	}{
		{"metric_events_20261025", true},
		{"metric_events_20261026", true},
		{"metric_events_20261025_backup", false},
		{"metric_events_archive", false},
		{"metric_events_2026102", false},
		{"unrelated_events", false},
	}
	for _, table := range tables {
		if err := db.Exec("CREATE TABLE `" + table.name + "` LIKE metric_events_template").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("ALTER TABLE `" + table.name + "` DROP INDEX idx_metrics_app_kind_received").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Table(table.name).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := applyMetricBehaviorIndexUpgrade(t, db); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if found := db.Migrator().HasIndex(table.name, "idx_metrics_app_kind_received"); found != table.target {
			t.Errorf("索引修改范围错误: table=%s has_index=%v expected=%v", table.name, found, table.target)
		}
		var actual []metricReceiptModel
		if err := db.Table(table.name).Find(&actual).Error; err != nil {
			t.Fatal(err)
		}
		if len(actual) != 1 || !reflect.DeepEqual(actual[0], row) {
			t.Errorf("表 %s 的事实数量或字段发生变化", table.name)
		}
	}
}
