package mysqlstore

import (
	"strings"
	"testing"
)

// TestDirectMenuMergeIntegration 验证入口合并不改发布事实、不扩大操作授权，并保留旧祖先停用语义。
func TestDirectMenuMergeIntegration(t *testing.T) {
	script := string(readDeploymentSQL(t, "migrations/20261011_003_merge_direct_publication_menu.sql"))
	before := strings.Replace(string(readDeploymentSchema(t)), script, "", 1)
	for _, inactive := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "inactive_ancestor"}[inactive], func(t *testing.T) {
			f := newIAAFixtureWithSchema(t, before, true)
			db := f.store.data.DB()
			repository, ctx := seedDirect(t, f)
			submitDirect(t, repository, "publish", "")
			for _, statement := range []string{
				`INSERT INTO admin_roles(id,name,code,status,created_at,updated_at) VALUES(500,'原发布者','old-publisher','active',NOW(),NOW()),(501,'仅原菜单','menu-only','active',NOW(),NOW())`,
				`INSERT INTO admin_role_permissions(role_id,permission_id) SELECT 500,id FROM admin_permissions WHERE code IN ('menu.direct_publications','content.drama.read','operations.direct_publication.read','operations.direct_publication.publish')`,
				`INSERT INTO admin_role_permissions(role_id,permission_id) SELECT 501,id FROM admin_permissions WHERE code='menu.direct_publications'`,
				`INSERT INTO admin_permissions(parent_id,name,code,type,route,sort_order,status,created_at,updated_at) SELECT id,'自定义查看','custom.direct.read','action','',3,'active',NOW(),NOW() FROM admin_permissions WHERE code='menu.direct_publications'`,
				`INSERT INTO admin_role_permissions(role_id,permission_id) SELECT 500,id FROM admin_permissions WHERE code='custom.direct.read'`,
			} {
				if err := db.Exec(statement).Error; err != nil {
					t.Fatal(err)
				}
			}
			if inactive {
				if err := db.Exec(`UPDATE admin_permissions SET status='disabled' WHERE code='directory.direct'`).Error; err != nil {
					t.Fatal(err)
				}
			}
			published, err := repository.Workbench(ctx, "201")
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := db.Exec(script).Error; err != nil {
					t.Fatal(err)
				}
			}
			after, err := repository.Workbench(ctx, "201")
			if err != nil || after.Publication != published.Publication || len(after.Versions) != len(published.Versions) {
				t.Fatal("合并菜单改变发布版本", err)
			}
			var count int64
			if err := db.Table("admin_permissions").Where("code='menu.direct_publications'").Count(&count).Error; err != nil || count != 0 {
				t.Fatal("旧菜单没有删除", err)
			}
			var codes []string
			if err := db.Table("admin_role_permissions g").Joins("JOIN admin_permissions p ON p.id=g.permission_id").Where("g.role_id=500").Order("p.code").Pluck("p.code", &codes).Error; err != nil {
				t.Fatal(err)
			}
			want := "content.drama.read,custom.direct.read,operations.direct_publication.publish,operations.direct_publication.read"
			if !inactive {
				want = "content.drama.read,custom.direct.read,menu.dramas,operations.direct_publication.publish,operations.direct_publication.read"
			}
			if strings.Join(codes, ",") != want {
				t.Fatal("原授权改变或扩权", codes)
			}
			if err := db.Table("admin_role_permissions").Where("role_id=501").Count(&count).Error; err != nil || count != 0 {
				t.Fatal("没有内容读取权限的角色被扩权", err)
			}
			status := "active"
			if inactive {
				status = "disabled"
			}
			if err := db.Table("admin_permissions c").Joins("JOIN admin_permissions p ON p.id=c.parent_id").Where("c.code IN ? AND p.code='menu.dramas' AND c.status=?", []string{"custom.direct.read", "operations.direct_publication.read", "operations.direct_publication.publish"}, status).Count(&count).Error; err != nil || count != 3 {
				t.Fatal("操作节点移位或启停语义丢失", count, err)
			}
			if err := db.Table("admin_role_permissions g").Joins("LEFT JOIN admin_permissions p ON p.id=g.permission_id").Where("p.id IS NULL").Count(&count).Error; err != nil || count != 0 {
				t.Fatal("出现悬空授权", err)
			}
		})
	}
}
