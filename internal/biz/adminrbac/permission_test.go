package adminrbac_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"drama-server/internal/biz/adminrbac"
)

// TestPermissionTreeRevocation 验证自定义编码可用于精确鉴权，父节点停用立即影响已有 JWT。
func TestPermissionTreeRevocation(t *testing.T) {
	service, store := newService(t)
	createTestSuperAdministrator(t, store, "tree-admin", "tree-admin-password")
	ctx := context.Background()
	root, err := service.CreatePermission(ctx, adminrbac.PermissionInput{Name: "自定义业务", Code: "custom.root", Type: adminrbac.PermissionDirectory, Status: adminrbac.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	input := adminrbac.PermissionInput{ParentID: root.ID, Name: "自定义导出", Code: "custom.export", Type: adminrbac.PermissionAction, Status: adminrbac.StatusActive}
	action, err := service.CreatePermission(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	role, err := service.CreateRole(ctx, adminrbac.RoleInput{Name: "自定义角色", Code: "custom.role", Status: adminrbac.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	user, err := service.CreateUser(ctx, adminrbac.UserCreateInput{Username: "custom-reader", Password: "custom-reader-password", Nickname: "自定义账号", Status: adminrbac.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	// 通过现有超级管理员设置角色。
	users, err := service.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var superID string
	for _, u := range users {
		if u.IsSuper {
			superID = u.ID
		}
	}
	if err := service.SetUserRolesAs(ctx, superID, user.ID, []string{role.ID}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetRolePermissions(ctx, role.ID, []string{root.ID}); err != nil {
		t.Fatal(err)
	}
	login, err := service.Login(ctx, user.Username, "custom-reader-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authorize(ctx, login.Token, action.Code); !errors.Is(err, adminrbac.ErrForbidden) {
		t.Fatal("目录不应隐式授予子操作")
	}
	if err := service.SetRolePermissions(ctx, role.ID, []string{root.ID, action.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authorize(ctx, login.Token, action.Code); err != nil {
		t.Fatalf("自定义权限未生效: %v", err)
	}
	if err := service.DeletePermission(ctx, action.ID); !errors.Is(err, adminrbac.ErrConflict) {
		t.Fatal("已分配节点不应被删除")
	}
	parentInput := adminrbac.PermissionInput{Name: root.Name, Code: root.Code, Type: root.Type, Status: adminrbac.StatusDisabled}
	if _, err := service.UpdatePermission(ctx, root.ID, parentInput); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authorize(ctx, login.Token, action.Code); !errors.Is(err, adminrbac.ErrForbidden) {
		t.Fatal("停用父节点后旧 JWT 仍获得子权限")
	}
	parentInput.Status = adminrbac.StatusActive
	if _, err := service.UpdatePermission(ctx, root.ID, parentInput); err != nil {
		t.Fatal(err)
	}
	// 改成菜单后，即使编码与接口相同，也不能继续授权操作。
	input.Type = adminrbac.PermissionMenu
	input.Route = "/dramas"
	if _, err := service.UpdatePermission(ctx, action.ID, input); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authorize(ctx, login.Token, action.Code); !errors.Is(err, adminrbac.ErrForbidden) {
		t.Fatal("菜单编码不应授权业务操作")
	}
	session, err := service.Authenticate(ctx, login.Token)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.Menus) != 1 || len(session.Menus[0].Children) != 1 || session.Menus[0].Children[0].Route != "/dramas" {
		t.Fatalf("动态菜单错误: %+v", session.Menus)
	}
	if err := service.SetRolePermissions(ctx, role.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.DeletePermission(ctx, action.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeletePermission(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
}

func TestPermissionAccessUsesExplicitSnapshot(t *testing.T) {
	items := []adminrbac.Permission{
		{ID: "1", Name: "目录", Code: "custom.root", Type: "directory", Status: "active"},
		{ID: "2", ParentID: "1", Name: "页面", Code: "custom.menu", Type: "menu", Route: "dramas", Status: "active"},
		{ID: "3", ParentID: "2", Code: "custom.read", Type: "action", Status: "active"},
		{ID: "4", ParentID: "2", Code: "custom.new", Type: "action", Status: "active"},
	}
	codes, menus := adminrbac.PermissionAccess(items, []string{"1", "2", "3"}, false)
	if !slices.Equal(codes, []string{"custom.read"}) || len(menus) != 1 {
		t.Fatalf("新增节点被隐式授权或菜单丢失: %v %v", codes, menus)
	}
	items[0].Status = "disabled"
	codes, menus = adminrbac.PermissionAccess(items, []string{"1", "2", "3"}, false)
	if len(codes) != 0 || len(menus) != 0 {
		t.Fatal("停用父节点未阻断子树")
	}
}

// TestDefaultDashboardMenuIsUnderOperations 验证默认仪表盘是渠道管理下的独立页面，而不是顶级菜单。
func TestDefaultDashboardMenuIsUnderOperations(t *testing.T) {
	service, store := newService(t)
	createTestSuperAdministrator(t, store, "dashboard-admin", "dashboard-admin-password")
	session, err := service.Login(context.Background(), "dashboard-admin", "dashboard-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range session.Menus {
		foundDashboard, foundPostbackLogs := false, false
		for _, menu := range directory.Children {
			if menu.Route == "/channels/dashboard" {
				if directory.Name != "运营数据" || directory.Route != "" {
					t.Fatalf("仪表盘应位于运营数据目录下，实际目录为 %+v", directory)
				}
				foundDashboard = true
			}
			if menu.Route == "/channels/postback-logs" {
				if directory.Name != "运营数据" || directory.Route != "" {
					t.Fatalf("回传日志应位于运营数据目录下，实际目录为 %+v", directory)
				}
				foundPostbackLogs = true
			}
		}
		if foundDashboard && foundPostbackLogs {
			return
		}
	}
	t.Fatalf("默认权限树缺少运营数据目录下的仪表盘或回传日志: %+v", session.Menus)
}

func TestPermissionTreeRejectsCyclesAndInvalidParents(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()
	create := func(parent, code, kind string) adminrbac.Permission {
		item, err := service.CreatePermission(ctx, adminrbac.PermissionInput{ParentID: parent, Name: code, Code: code, Type: kind, Status: "active"})
		if err != nil {
			t.Fatal(err)
		}
		return item
	}
	a := create("", "custom.a", "directory")
	b := create(a.ID, "custom.b", "directory")
	if _, err := service.UpdatePermission(ctx, a.ID, adminrbac.PermissionInput{ParentID: b.ID, Name: a.Name, Code: a.Code, Type: a.Type, Status: "active"}); !errors.Is(err, adminrbac.ErrInvalidInput) {
		t.Fatal("未拒绝循环移动")
	}
	if _, err := service.CreatePermission(ctx, adminrbac.PermissionInput{ParentID: "999999", Name: "孤儿", Code: "custom.orphan", Type: "action", Status: "active"}); !errors.Is(err, adminrbac.ErrNotFound) {
		t.Fatal("未拒绝缺失父节点")
	}
	if _, err := service.CreatePermission(ctx, adminrbac.PermissionInput{Name: "重复", Code: a.Code, Type: "directory", Status: "active"}); !errors.Is(err, adminrbac.ErrConflict) {
		t.Fatal("未拒绝重复编码")
	}
	if err := service.DeletePermission(ctx, a.ID); !errors.Is(err, adminrbac.ErrConflict) {
		t.Fatal("未保护有子节点的目录")
	}
}
