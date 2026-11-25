package mysqlstore

import (
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/biz/subject"
	"encoding/json"
	"gorm.io/gorm"
	"reflect"
	"strings"
	"testing"
)

// TestSharedVODSpaceUpgradeIntegration 验证共享空间升级保留旧值、解除独占限制，旧主体表单不能改写废弃空间。
func TestSharedVODSpaceUpgradeIntegration(t *testing.T) {
	for _, present := range []bool{true, false} {
		name := "不从废弃主体空间兜底"
		if present {
			name = "保留已有全局空间"
		}
		t.Run(name, func(t *testing.T) {
			migration := string(readDeploymentSQL(t, "migrations/20261106_001_shared_vod_space.sql"))
			oldSchema := strings.Replace(string(readDeploymentSchema(t)), migration, "", 1)
			f := newIAAFixtureWithSchema(t, oldSchema, false)
			db := f.store.data.DB()
			ctx := t.Context()
			service := subject.NewService(f.store)
			other, err := service.Save(ctx, "", subject.Input{Name: "共用空间的新主体"})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("UPDATE subjects SET vod_space_name='retained-space',updated_at=updated_at WHERE id=1").Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("UPDATE subjects SET vod_space_name='second-space',updated_at=updated_at WHERE id=?", other.ID).Error; err != nil {
				t.Fatal(err)
			}
			var cols []string
			if err := db.Raw("SELECT column_name FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='subjects' ORDER BY ordinal_position").Scan(&cols).Error; err != nil {
				t.Fatal(err)
			}
			before := subjectUpgradeRowDigests(t, f, "subjects", cols)
			values := map[string]any{"access_key_id": "fixture-ak", "secret_access_key": "fixture-sk", "vod_app_id": "001", "region": "ap-southeast-1", "max_file_bytes": 1073741824, "upload_token_ttl_seconds": 600, "cover_domain": "https://fixture.test", "processing_timeout_seconds": 900}
			expectedSpace := ""
			if present {
				expectedSpace = "global-shared-space"
				values["space_name"] = expectedSpace
			}
			raw, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.GlobalConfigs().Put(ctx, globalconfig.Entry{Key: "media", Value: raw}); err != nil {
				t.Fatal(err)
			}
			mediaBefore, err := f.store.GlobalConfigs().Get(ctx, "media")
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(migration).Error; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, subjectUpgradeRowDigests(t, f, "subjects", cols)) {
				t.Fatal("共享空间升级改写了原主体字段")
			}
			mediaAfter, err := f.store.GlobalConfigs().Get(ctx, "media")
			if err != nil {
				t.Fatal(err)
			}
			if !mediaAfter.UpdatedAt.Equal(*mediaBefore.UpdatedAt) {
				t.Fatal("迁移改写配置更新时间")
			}
			var saved map[string]any
			if err := json.Unmarshal(mediaAfter.Value, &saved); err != nil {
				t.Fatal(err)
			}
			expectedJSON, marshalErr := json.Marshal(values)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			actualJSON, marshalErr := json.Marshal(saved)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if string(expectedJSON) != string(actualJSON) {
				t.Fatal("共享空间升级丢失原全局配置字段")
			}
			current, err := globalconfig.NewService(f.store.GlobalConfigs()).Media(ctx)
			if err != nil || current.SpaceName != expectedSpace {
				t.Fatal("共享空间未进入运行配置", err)
			}
			if err := db.Exec("UPDATE subjects SET vod_space_name='retained-space',updated_at=updated_at WHERE id=?", other.ID).Error; err != nil {
				t.Fatal("数据库仍要求不同主体独占空间", err)
			}
			if _, err := service.Save(ctx, other.ID, subject.Input{Name: other.Name, VODSpaceName: "ignored-new-space"}); err != nil {
				t.Fatal(err)
			}
			var retained string
			if err := db.Raw("SELECT vod_space_name FROM subjects WHERE id=?", other.ID).Scan(&retained).Error; err != nil || retained != "retained-space" {
				t.Fatal("主体保存改写了废弃空间", err)
			}
			var model subjectModel
			statement := f.store.subjectDB(ctx).Session(&gorm.Session{DryRun: true}).Find(&model).Statement.SQL.String()
			if strings.Contains(statement, "vod_space") || strings.Contains(statement, "SELECT *") {
				t.Fatal("运行时仍读取废弃主体空间", statement)
			}
		})
	}
}
