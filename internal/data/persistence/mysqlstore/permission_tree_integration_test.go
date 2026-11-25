package mysqlstore

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"drama-server/internal/biz/adminrbac"
)

// assertPermissionTreePersistence 验证真实 MySQL 中的树约束、并发移动、角色引用和已有 JWT 撤权。
func assertPermissionTreePersistence(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	service := adminrbac.NewService(store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")})
	makeNode := func(input adminrbac.PermissionInput) adminrbac.Permission {
		node, err := service.CreatePermission(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		return node
	}
	rootInput := adminrbac.PermissionInput{Name: "集成目录", Code: "tree.integration", Type: "directory", Status: "active"}
	root := makeNode(rootInput)
	actionInput := adminrbac.PermissionInput{ParentID: root.ID, Name: "集成自定义权限", Code: "tree.custom.export", Type: "action", Status: "active", SortOrder: 8}
	action := makeNode(actionInput)
	persisted, err := store.FindAdminPermissionByID(ctx, action.ID)
	if err != nil || persisted.ParentID != root.ID || persisted.SortOrder != 8 {
		t.Fatalf("节点资料未持久化: %+v %v", persisted, err)
	}
	if err := service.DeletePermission(ctx, root.ID); !errors.Is(err, adminrbac.ErrConflict) {
		t.Fatal("父节点引用未保护")
	}
	if _, err := service.CreatePermission(ctx, actionInput); !errors.Is(err, adminrbac.ErrConflict) {
		t.Fatal("重复编码未保护")
	}
	role, err := service.CreateRole(ctx, adminrbac.RoleInput{Name: "权限树集成角色", Code: "tree.integration.role", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := service.CreateUser(ctx, adminrbac.UserCreateInput{Username: "tree-integration-user", Password: "tree-integration-password", Nickname: "测试账号", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceAdminUserRoles(ctx, user.ID, []string{role.ID}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetRolePermissions(ctx, role.ID, []string{action.ID}); err != nil {
		t.Fatal(err)
	}
	if err := service.DeletePermission(ctx, action.ID); !errors.Is(err, adminrbac.ErrConflict) {
		t.Fatal("数据库未保护角色引用")
	}
	login, err := service.Login(ctx, user.Username, "tree-integration-password")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(login.Permissions, action.Code) {
		t.Fatal("自定义权限未参与真实鉴权")
	}
	rootInput.Status = "disabled"
	if _, err := service.UpdatePermission(ctx, root.ID, rootInput); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authorize(ctx, login.Token, action.Code); !errors.Is(err, adminrbac.ErrForbidden) {
		t.Fatal("停用父节点未撤销已有 JWT 权限")
	}
	rootInput.Status = "active"
	if _, err := service.UpdatePermission(ctx, root.ID, rootInput); err != nil {
		t.Fatal(err)
	}

	// 两次相反移动同时发生时，只有一次可提交，另一事务必须看到提交后的树并拒绝循环。
	aInput := adminrbac.PermissionInput{Name: "并发目录 A", Code: "tree.concurrent.a", Type: "directory", Status: "active"}
	bInput := adminrbac.PermissionInput{Name: "并发目录 B", Code: "tree.concurrent.b", Type: "directory", Status: "active"}
	a, b := makeNode(aInput), makeNode(bInput)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, move := range []struct {
		id, parent string
		input      adminrbac.PermissionInput
	}{{a.ID, b.ID, aInput}, {b.ID, a.ID, bInput}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			input := move.input
			input.ParentID = move.parent
			_, err := service.UpdatePermission(ctx, move.id, input)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	success, rejected := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, adminrbac.ErrInvalidInput) {
			rejected++
		} else {
			t.Fatalf("并发移动错误: %v", err)
		}
	}
	if success != 1 || rejected != 1 {
		t.Fatalf("并发移动未保护环: success=%d rejected=%d", success, rejected)
	}
	if _, err := service.UpdatePermission(ctx, a.ID, aInput); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdatePermission(ctx, b.ID, bInput); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a.ID, b.ID} {
		if err := service.DeletePermission(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.SetRolePermissions(ctx, role.ID, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{action.ID, root.ID} {
		if err := service.DeletePermission(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
}
