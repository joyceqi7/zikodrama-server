package mysqlstore

import (
	"strings"
	"testing"
	"time"
)

// TestOfficialReviewMenuRemovalIntegration 验证删除默认菜单、保留审核事实及自定义子树，重复执行不扩展角色权限。
func TestOfficialReviewMenuRemovalIntegration(t *testing.T) {
	removal := string(readDeploymentSQL(t, "migrations/20261011_002_remove_official_review_menu.sql"))
	before := strings.Replace(string(readDeploymentSchema(t)), removal, "", 1)
	before = strings.Replace(before, string(readDeploymentSQL(t, "migrations/20261011_003_merge_direct_publication_menu.sql")), "", 1)
	for _, status := range []string{"no_children", "active", "disabled"} {
		t.Run(status, func(t *testing.T) {
			f := newIAAFixtureWithSchema(t, before, true)
			db := f.store.data.DB()
			// 仅审核菜单在本增量被删除，保留后续独立增量已加入的其他权限节点。
			var permissionCountBefore int64
			if err := db.Table("admin_permissions").Count(&permissionCountBefore).Error; err != nil {
				t.Fatal(err)
			}
			for _, statement := range []string{
				`INSERT INTO admin_roles(id,name,code,status,created_at,updated_at) VALUES(500,'历史查看','review-history','active',NOW(),NOW())`,
				`INSERT INTO admin_role_permissions(role_id,permission_id) SELECT 500,id FROM admin_permissions WHERE code IN ('menu.review_records','content.drama.read')`,
			} {
				if err := db.Exec(statement).Error; err != nil {
					t.Fatal(err)
				}
			}
			if status != "no_children" {
				if err := db.Exec(`UPDATE admin_permissions SET status=? WHERE code='menu.review_records'`, status).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Exec(`INSERT INTO admin_permissions(parent_id,name,code,type,route,sort_order,status,created_at,updated_at) SELECT id,'自定义导出','custom.review.export','action','',3,'active',NOW(),NOW() FROM admin_permissions WHERE code='menu.review_records'`).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Exec(`INSERT INTO admin_role_permissions(role_id,permission_id) SELECT 500,id FROM admin_permissions WHERE code='custom.review.export'`).Error; err != nil {
					t.Fatal(err)
				}
			}
			// 审核记录不依赖菜单存在；迁移不能触及已冻结的版本或审核意见。
			record := publicationRecordModel{SubjectID: "1", ID: "7001", DramaID: "201", Title: "历史送审标题", Status: "rejected", RecordJSON: []byte(`{"platformVersion":2,"reviewFailReasons":["Historical feedback"]}`), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			if err := db.Table("tiktok_review_records").Create(&record).Error; err != nil {
				t.Fatal(err)
			}
			var saved publicationRecordModel
			if err := db.Table("tiktok_review_records").Where("id=?", record.ID).Take(&saved).Error; err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := db.Exec(removal).Error; err != nil {
					t.Fatal(err)
				}
			}
			var after publicationRecordModel
			if err := db.Table("tiktok_review_records").Where("id=?", record.ID).Take(&after).Error; err != nil || after.Title != saved.Title || after.Status != saved.Status || string(after.RecordJSON) != string(saved.RecordJSON) {
				t.Fatal("移除菜单改变审核历史", err)
			}
			var codes []string
			if err := db.Table("admin_role_permissions assigned").Joins("JOIN admin_permissions p ON p.id=assigned.permission_id").Where("assigned.role_id=500").Order("p.code").Pluck("p.code", &codes).Error; err != nil {
				t.Fatal(err)
			}
			want := "content.drama.read"
			if status != "no_children" {
				want += ",custom.review.export"
			}
			if strings.Join(codes, ",") != want {
				t.Fatal("原角色被扩展成官方发布权限或丢失自定义授权", codes)
			}
			var count int64
			if err := db.Table("admin_permissions").Where("code='menu.review_records' AND type='menu'").Count(&count).Error; err != nil || count != 0 {
				t.Fatal("旧审核菜单仍存在", err)
			}
			if status == "no_children" {
				if err := db.Table("admin_permissions").Count(&count).Error; err != nil || count != permissionCountBefore-1 {
					t.Fatal("默认权限节点清单错误", count, err)
				}
			} else if err := db.Table("admin_permissions child").Joins("JOIN admin_permissions parent ON parent.id=child.parent_id").Where("child.code='custom.review.export' AND child.status='active' AND parent.code='menu.review_records' AND parent.type='directory' AND parent.route='' AND parent.status=?", status).Count(&count).Error; err != nil || count != 1 {
				t.Fatal("自定义子树或原父节点启停状态改变", err)
			}
			if err := db.Table("admin_role_permissions assigned").Joins("LEFT JOIN admin_permissions p ON p.id=assigned.permission_id").Where("p.id IS NULL").Count(&count).Error; err != nil || count != 0 {
				t.Fatal("角色关联出现悬空引用", err)
			}
		})
	}
}
