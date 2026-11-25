package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"strings"
	"testing"
)

// TestDirectUpgradeIntegration 验证增量保留旧数据和已有角色，菜单重复执行不覆盖自定义名称。
func TestDirectUpgradeIntegration(t *testing.T) {
	tables := string(readDeploymentSQL(t, "migrations/20261009_001_direct_playback.sql"))
	menus := string(readDeploymentSQL(t, "migrations/20261009_002_direct_menus.sql"))
	removal := string(readDeploymentSQL(t, "migrations/20261010_001_remove_application_playback_profile_key.sql"))
	schema := strings.Replace(strings.Replace(strings.Replace(string(readDeploymentSchema(t)), string(readDeploymentSQL(t, "migrations/20261106_001_shared_vod_space.sql")), "", 1), tables, "", 1), menus, "", 1)
	schema = strings.Replace(schema, removal, "", 1)
	schema = strings.Replace(schema, string(readDeploymentSQL(t, "migrations/20261010_002_content_versions.sql")), "", 1)
	schema = strings.Replace(schema, string(readDeploymentSQL(t, "migrations/20261010_003_channel_publication_versions.sql")), "", 1)
	schema = strings.Replace(schema, string(readDeploymentSQL(t, "migrations/20261011_001_simplify_direct_operations.sql")), "", 1)
	schema = strings.Replace(schema, string(readDeploymentSQL(t, "migrations/20261011_002_remove_official_review_menu.sql")), "", 1)
	schema = strings.Replace(schema, string(readDeploymentSQL(t, "migrations/20261011_003_merge_direct_publication_menu.sql")), "", 1)
	schema = strings.Replace(schema, string(readDeploymentSQL(t, "migrations/20261105_001_subjects.sql")), "", 1)
	f := newIAAFixtureWithSchema(t, schema, false)
	db := f.store.data.DB()
	ctx := application.WithSubject(context.Background(), "1")
	// 当前升级前夹具可能含后续独立菜单；只核对本次直连增量新增的二十五个权限。
	var permissionCountBefore int64
	if err := db.Table("admin_permissions").Count(&permissionCountBefore).Error; err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`INSERT INTO dramas(id,title,description,cover_url,status,tag_ids_json,publish_status,operation_note,published_snapshot) VALUES(201,'Legacy','','','completed','[]','published','', '{"id":"201","title":"Legacy online","platformAlbumId":"legacy-album"}')`,
		`INSERT INTO admin_roles(id,name,code,status,created_at,updated_at) VALUES(500,'Existing role','existing-role','active',NOW(),NOW())`,
		`INSERT INTO admin_role_permissions(role_id,permission_id) SELECT 500,id FROM admin_permissions WHERE code='menu.dramas'`,
		`UPDATE admin_permissions SET name='运营自定义轮播' WHERE code='menu.carousel'`,
	} {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	var before string
	if err := db.Raw("SELECT published_snapshot FROM dramas WHERE id=201").Scan(&before).Error; err != nil {
		t.Fatal(err)
	}
	apply := func(script string) {
		for _, stmt := range strings.Split(script, ";") {
			if strings.TrimSpace(stmt) != "" {
				if err := db.Exec(stmt).Error; err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	apply(tables)
	apply(menus)
	apply(menus)
	apply(removal)
	var after string
	db.Raw("SELECT published_snapshot FROM dramas WHERE id=201").Scan(&after)
	if after != before {
		t.Fatal("迁移覆盖官方快照")
	}
	var count int64
	db.Table("admin_permissions").Count(&count)
	if count != permissionCountBefore+25 {
		t.Fatal("权限清单缺项", count)
	}
	db.Table("admin_role_permissions").Where("role_id=500").Count(&count)
	if count != 1 {
		t.Fatal("已有角色被自动扩权")
	}
	var name string
	db.Table("admin_permissions").Select("name").Where("code='menu.carousel'").Scan(&name)
	if name != "运营自定义轮播" {
		t.Fatal("自定义菜单名称被覆盖")
	}
	var profile applicationPlaybackProfileModel
	err := db.WithContext(ctx).Where("application_id=101").Take(&profile).Error
	if err == nil {
		t.Fatal("迁移猜测了旧应用模式")
	}
	db.Table("admin_permissions child").Joins("JOIN admin_permissions parent ON parent.id=child.parent_id").Where("child.code='application.playback.update' AND parent.code='menu.applications'").Count(&count)
	if count != 1 {
		t.Fatal("应用播放动作没有归属菜单")
	}
}

// TestApplicationPlaybackProfileKeyRemovalIntegration 验证新增量删除引用列后保留模式、修订及官方应用。
func TestApplicationPlaybackProfileKeyRemovalIntegration(t *testing.T) {
	removal := string(readDeploymentSQL(t, "migrations/20261010_001_remove_application_playback_profile_key.sql"))
	before := strings.Replace(strings.Replace(string(readDeploymentSchema(t)), string(readDeploymentSQL(t, "migrations/20261106_001_shared_vod_space.sql")), "", 1), removal, "", 1)
	before = strings.Replace(before, string(readDeploymentSQL(t, "migrations/20261011_003_merge_direct_publication_menu.sql")), "", 1)
	f := newIAAFixtureWithSchema(t, before, true)
	db := f.store.data.DB()
	if err := db.Exec(`INSERT INTO application_playback_profiles(application_id,provider,playback_profile_key,revision) VALUES(102,'byteplus_web','playback.unused',7)`).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Exec(removal).Error; err != nil {
		t.Fatal(err)
	}
	var columns int64
	if err := db.Raw(`SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='application_playback_profiles' AND COLUMN_NAME='playback_profile_key'`).Scan(&columns).Error; err != nil || columns != 0 {
		t.Fatal("增量没有删除引用列", err)
	}
	direct, err := f.store.FindApplication(application.WithSubject(t.Context(), "1"), "102")
	if err != nil || direct.PlaybackProvider != "byteplus_web" || direct.PlaybackRevision != 7 {
		t.Fatal("删除引用列改变了播放模式或修订", err)
	}
	official, err := f.store.FindApplication(application.WithSubject(t.Context(), "1"), "101")
	if err != nil || official.PlaybackProvider != "tiktok_minis" || official.PlaybackRevision != 0 {
		t.Fatal("删除引用列改变了官方应用", err)
	}
}

// TestDirectOperationsSimplificationIntegration 验证只清理重复入口，保留发布历史、配置、自定义节点与原有操作授权。
func TestDirectOperationsSimplificationIntegration(t *testing.T) {
	removal := string(readDeploymentSQL(t, "migrations/20261011_001_simplify_direct_operations.sql"))
	before := strings.Replace(strings.Replace(string(readDeploymentSchema(t)), string(readDeploymentSQL(t, "migrations/20261106_001_shared_vod_space.sql")), "", 1), removal, "", 1)
	before = strings.Replace(before, string(readDeploymentSQL(t, "migrations/20261011_003_merge_direct_publication_menu.sql")), "", 1)
	f := newIAAFixtureWithSchema(t, before, true)
	db := f.store.data.DB()
	repository, ctx := seedDirect(t, f)
	submitDirect(t, repository, "publish", "")
	for _, statement := range []string{
		`INSERT INTO admin_roles(id,name,code,status,created_at,updated_at) VALUES(500,'记录查看','release-reader','active',NOW(),NOW()),(501,'未分配直连','no-direct','active',NOW(),NOW())`,
		`INSERT INTO admin_role_permissions(role_id,permission_id) SELECT 500,id FROM admin_permissions WHERE code IN ('directory.direct','menu.direct_releases','operations.direct_publication.read','operations.direct_home.copy')`,
		`INSERT INTO admin_permissions(parent_id,name,code,type,route,sort_order,status,created_at,updated_at) SELECT id,'自定义查看','custom.direct.read','action','',0,'active',NOW(),NOW() FROM admin_permissions WHERE code='menu.direct_releases'`,
		`UPDATE admin_permissions SET name='自定义直连发布' WHERE code='menu.direct_publications'`,
		`INSERT INTO direct_homepage_recommendations(subject_id,section,revision,config_json) VALUES(1,'carousel',7,'{"dramaIds":["201"],"items":[{"dramaId":"201","visible":false}]}')`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	published, err := repository.Workbench(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot, configuration string
	if err := db.Raw("SELECT published_snapshot FROM tiktok_drama_states WHERE subject_id=1 AND drama_id=201").Scan(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw("SELECT config_json FROM direct_homepage_recommendations WHERE section='carousel'").Scan(&configuration).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.Exec(removal).Error; err != nil {
			t.Fatal(err)
		}
	}
	after, err := repository.Workbench(ctx, "201")
	if err != nil || after.Publication != published.Publication || len(after.Versions) != len(published.Versions) || after.Summary.LastOperation.ID != published.Summary.LastOperation.ID {
		t.Fatal("菜单清理改变发布事实", err)
	}
	var actual string
	if err := db.Raw("SELECT published_snapshot FROM tiktok_drama_states WHERE subject_id=1 AND drama_id=201").Scan(&actual).Error; err != nil || actual != snapshot {
		t.Fatal("官方快照改变", err)
	}
	if err := db.Raw("SELECT config_json FROM direct_homepage_recommendations WHERE section='carousel'").Scan(&actual).Error; err != nil || actual != configuration {
		t.Fatal("直连首页配置改变", err)
	}
	var codes []string
	if err := db.Table("admin_role_permissions assigned").Joins("JOIN admin_permissions p ON p.id=assigned.permission_id").Where("assigned.role_id=500").Order("p.code").Pluck("p.code", &codes).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Join(codes, ",") != "directory.direct,menu.direct_publications,operations.direct_publication.read" {
		t.Fatal("合并丢失原查看入口或扩大操作授权", codes)
	}
	var count int64
	if err := db.Table("admin_role_permissions").Where("role_id=501").Count(&count).Error; err != nil || count != 0 {
		t.Fatal("未分配角色被扩权", err)
	}
	if err := db.Table("admin_permissions").Where("code IN ?", []string{"menu.direct_releases", "operations.direct_home.copy"}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("废弃节点未移除", err)
	}
	if err := db.Table("admin_permissions child").Joins("JOIN admin_permissions parent ON parent.id=child.parent_id").Where("child.code='custom.direct.read' AND parent.code='menu.direct_publications' AND parent.name='自定义直连发布'").Count(&count).Error; err != nil || count != 1 {
		t.Fatal("自定义子节点或菜单名称丢失", err)
	}
	if err := db.Table("admin_role_permissions assigned").Joins("LEFT JOIN admin_permissions p ON p.id=assigned.permission_id").Where("p.id IS NULL").Count(&count).Error; err != nil || count != 0 {
		t.Fatal("遗留孤立角色授权", err)
	}
}
