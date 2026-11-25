package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"drama-server/internal/biz/adminrbac"
)

// assertGORMRBACAutoIncrement 验证真实 MySQL 的自增回填、大整数精度、关联和会话生命周期。
func assertGORMRBACAutoIncrement(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	const highID = "9007199254740993"
	for _, table := range []string{"admin_users", "admin_roles", "admin_permissions"} {
		var columnType, extra string
		err := store.data.pool.QueryRowContext(ctx, "SELECT column_type, extra FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = 'id'", table).Scan(&columnType, &extra)
		if err != nil || columnType != "bigint unsigned" || extra != "auto_increment" {
			t.Fatalf("%s 主键结构不符：%s %s %v", table, columnType, extra, err)
		}
		if _, err := store.data.pool.ExecContext(ctx, "ALTER TABLE `"+table+"` AUTO_INCREMENT = "+highID); err != nil {
			t.Fatal(err)
		}
	}
	service := adminrbac.NewService(store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes"), Now: func() time.Time { return time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC) }})
	super, err := store.FindAdminUserByUsername(ctx, "integration-admin")
	if err != nil {
		t.Fatal(err)
	}
	user, err := service.CreateUser(ctx, adminrbac.UserCreateInput{Username: "numeric-user", Password: "numeric-test-password", Nickname: "自增用户", Status: adminrbac.StatusActive})
	if err != nil || user.ID != highID {
		t.Fatalf("管理员主键未精确回填：%s %v", user.ID, err)
	}
	role, err := service.CreateRole(ctx, adminrbac.RoleInput{Code: "numeric-role", Name: "自增角色", Status: adminrbac.StatusActive})
	if err != nil || role.ID != highID {
		t.Fatalf("角色主键未精确回填：%s %v", role.ID, err)
	}
	// 唯一约束失败不能回填一个未成功落库的主键。
	duplicateUser := user
	duplicateUser.ID = ""
	if err := store.CreateAdminUser(ctx, &duplicateUser); !errors.Is(err, adminrbac.ErrConflict) || duplicateUser.ID != "" {
		t.Fatalf("重复账号错误或回填行为不正确：%s %v", duplicateUser.ID, err)
	}
	duplicateRole := role
	duplicateRole.ID = ""
	if err := store.CreateAdminRole(ctx, &duplicateRole); !errors.Is(err, adminrbac.ErrConflict) || duplicateRole.ID != "" {
		t.Fatalf("重复角色错误或回填行为不正确：%s %v", duplicateRole.ID, err)
	}
	result, err := store.data.pool.ExecContext(ctx, "INSERT INTO admin_permissions (name,code,created_at,updated_at) VALUES ('集成测试权限','integration.numeric.read',NOW(6),NOW(6))")
	if err != nil {
		t.Fatal(err)
	}
	permissionNumber, err := result.LastInsertId()
	if err != nil || strconv.FormatInt(permissionNumber, 10) != highID {
		t.Fatalf("权限主键未自动生成：%d %v", permissionNumber, err)
	}
	permission, err := store.FindAdminPermissionByID(ctx, highID)
	if err != nil || permission.ID != highID {
		t.Fatalf("权限主键丢失精度：%+v %v", permission, err)
	}
	for name, entity := range map[string]any{"管理员": user, "角色": role, "权限": permission} {
		data, err := json.Marshal(entity)
		if err != nil {
			t.Fatal(err)
		}
		var response map[string]any
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		if response["id"] != highID {
			t.Fatalf("%s 的 JSON ID 必须是精确字符串：%v", name, response["id"])
		}
	}
	if err := service.SetRolePermissions(ctx, role.ID, []string{permission.ID}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetUserRolesAs(ctx, super.ID, user.ID, []string{role.ID}); err != nil {
		t.Fatal(err)
	}
	roles, err := store.ListAdminUserRoleIDs(ctx, user.ID)
	if err != nil || !slices.Equal(roles, []string{highID}) {
		t.Fatalf("账号角色关联错误：%v %v", roles, err)
	}
	permissions, err := store.ListAdminRolePermissionIDs(ctx, role.ID)
	if err != nil || !slices.Equal(permissions, []string{highID}) {
		t.Fatalf("角色权限关联错误：%v %v", permissions, err)
	}
	if err := service.DeleteRole(ctx, role.ID); !errors.Is(err, adminrbac.ErrConflict) {
		t.Fatalf("已分配角色仍可删除：%v", err)
	}
	login, err := service.Login(ctx, user.Username, "numeric-test-password")
	if err != nil || login.User.ID != highID || !slices.Contains(login.Permissions, permission.Code) {
		t.Fatalf("大整数主键登录或权限解析失败：%v", err)
	}
	other := adminrbac.NewService(store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes"), Now: func() time.Time { return time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC) }})
	if session, err := other.Authenticate(ctx, login.Token); err != nil || session.User.ID != highID {
		t.Fatalf("另一实例读取会话主键失败：%v", err)
	}
	for _, id := range []string{highID + "abc", "0" + highID, highID + ".0"} {
		if _, err := store.FindAdminUserByID(ctx, id); !errors.Is(err, adminrbac.ErrNotFound) {
			t.Fatalf("非规范 ID 命中了账号：%s %v", id, err)
		}
	}
	if err := store.ReplaceAdminUserRoles(ctx, user.ID, []string{role.ID + "abc"}); !errors.Is(err, adminrbac.ErrNotFound) {
		t.Fatalf("非法角色 ID 未被拒绝：%v", err)
	}
	if err := store.ReplaceAdminRolePermissions(ctx, role.ID, []string{permission.ID + "abc"}); !errors.Is(err, adminrbac.ErrNotFound) {
		t.Fatalf("非法权限 ID 未被拒绝：%v", err)
	}
	if session, err := service.Authenticate(ctx, login.Token); err != nil || !slices.Contains(session.Permissions, permission.Code) {
		t.Fatalf("非法关联修改破坏了已有授权：%v", err)
	}
	if err := service.ResetUserPasswordAs(ctx, super.ID, user.ID, "new-numeric-test-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Authenticate(ctx, login.Token); !errors.Is(err, adminrbac.ErrUnauthorized) {
		t.Fatalf("重置密码未使旧会话失效：%v", err)
	}
	login, err = service.Login(ctx, user.Username, "new-numeric-test-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateUserAs(ctx, super.ID, user.ID, adminrbac.UserUpdateInput{Nickname: user.Nickname, Status: adminrbac.StatusDisabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, login.Token); !errors.Is(err, adminrbac.ErrUnauthorized) {
		t.Fatalf("停用账号后旧会话仍有效：%v", err)
	}
	if _, err := service.UpdateUserAs(ctx, super.ID, super.ID, adminrbac.UserUpdateInput{Nickname: super.Nickname, Status: adminrbac.StatusDisabled}); !errors.Is(err, adminrbac.ErrLastSuperAdmin) {
		t.Fatalf("最后一位超级管理员未受保护：%v", err)
	}
	// 并发实例分别创建角色，数据库必须分配互不重复的主键。
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := other.CreateRole(ctx, adminrbac.RoleInput{Name: fmt.Sprintf("并发角色%d", i), Code: fmt.Sprintf("parallel-%d", i), Status: adminrbac.StatusActive})
			if err != nil {
				failures <- err
				return
			}
			ids <- r.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	seen := map[string]bool{role.ID: true}
	count := 0
	for id := range ids {
		if seen[id] {
			t.Errorf("并发创建主键重复：%s", id)
		}
		seen[id] = true
		count++
	}
	if count != 8 {
		t.Fatalf("并发角色创建不足：%d", count)
	}
	// 清除本测试额外的权限样本，保持系统固定清单仍为 27 条。
	if err := service.SetUserRolesAs(ctx, super.ID, user.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteRole(ctx, role.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.data.pool.ExecContext(ctx, "DELETE FROM admin_permissions WHERE id = ?", permissionNumber); err != nil {
		t.Fatal(err)
	}
}
