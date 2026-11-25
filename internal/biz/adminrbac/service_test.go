package adminrbac_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/testsupport/memory"
)

func TestSQLInitializedSuperAdministratorCanLogin(t *testing.T) {
	service, store := newService(t)
	ctx := context.Background()
	createTestSuperAdministrator(t, store, "admin", "local-admin-password")
	user, err := store.FindAdminUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("find initial administrator: %v", err)
	}
	if user.PasswordHash == "local-admin-password" || user.PasswordHash == "" {
		t.Fatalf("SQL 测试夹具必须写入 bcrypt 摘要，实际为 %q", user.PasswordHash)
	}

	first, err := service.Login(ctx, "admin", "local-admin-password")
	if err != nil {
		t.Fatalf("login with provisioned password: %v", err)
	}
	if !first.Session.User.IsSuper || first.Session.User.Username != "admin" {
		t.Fatalf("unexpected SQL-initialized principal: %#v", first.Session.User)
	}
}

func TestAdminSessionIsSharedRevocableAndExpires(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{})
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	newService := func() *adminrbac.Service {
		return adminrbac.NewService(store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes"),
			Now: func() time.Time { return now }, SessionTTL: time.Hour,
		})
	}
	firstInstance, secondInstance := newService(), newService()
	createTestSuperAdministrator(t, store, "admin", "local-admin-password")
	login, err := firstInstance.Login(context.Background(), "admin", "local-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secondInstance.Authenticate(context.Background(), login.Token); err != nil {
		t.Fatalf("另一实例不能读取共享管理会话：%v", err)
	}
	if err := secondInstance.Logout(context.Background(), login.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := firstInstance.Authenticate(context.Background(), login.Token); !errors.Is(err, adminrbac.ErrUnauthorized) {
		t.Fatalf("退出后会话仍然有效：%v", err)
	}

	login, err = firstInstance.Login(context.Background(), "admin", "local-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if _, err := secondInstance.Authenticate(context.Background(), login.Token); !errors.Is(err, adminrbac.ErrUnauthorized) {
		t.Fatalf("达到过期时间的会话仍然有效：%v", err)
	}
}

func TestRolePermissionsAreReloadedForEveryRequest(t *testing.T) {
	service, store := newService(t)
	ctx := context.Background()
	createTestSuperAdministrator(t, store, "admin", "local-admin-password")
	admin, err := store.FindAdminUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}

	role, err := service.CreateRole(ctx, adminrbac.RoleInput{
		Name: "内容编辑", Code: "content_editor", Status: adminrbac.StatusActive,
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	permission := permissionByCode(t, service, adminrbac.PermissionDramaRead)
	if err := service.SetRolePermissions(ctx, role.ID, []string{permission.ID}); err != nil {
		t.Fatalf("set role permissions: %v", err)
	}
	user, err := service.CreateUser(ctx, adminrbac.UserCreateInput{
		Username: "editor", Password: "editor-password", Nickname: "编辑", Status: adminrbac.StatusActive,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := service.SetUserRolesAs(ctx, admin.ID, user.ID, []string{role.ID}); err != nil {
		t.Fatalf("set user roles: %v", err)
	}
	login, err := service.Login(ctx, "editor", "editor-password")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := service.Authorize(ctx, login.Token, adminrbac.PermissionDramaRead); err != nil {
		t.Fatalf("expected read permission: %v", err)
	}
	if _, err := service.Authorize(ctx, login.Token, adminrbac.PermissionDramaPublish); !errors.Is(err, adminrbac.ErrForbidden) {
		t.Fatalf("expected publish to be forbidden, got %v", err)
	}

	if err := service.SetRolePermissions(ctx, role.ID, nil); err != nil {
		t.Fatalf("revoke role permissions: %v", err)
	}
	if _, err := service.Authorize(ctx, login.Token, adminrbac.PermissionDramaRead); !errors.Is(err, adminrbac.ErrForbidden) {
		t.Fatalf("revocation must affect an existing token, got %v", err)
	}

	_, err = service.UpdateUserAs(ctx, admin.ID, user.ID, adminrbac.UserUpdateInput{Nickname: "编辑", Status: adminrbac.StatusDisabled})
	if err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if _, err := service.Authenticate(ctx, login.Token); !errors.Is(err, adminrbac.ErrUnauthorized) {
		t.Fatalf("disabled user token must be rejected, got %v", err)
	}
	if _, err := service.UpdateUserAs(ctx, admin.ID, user.ID, adminrbac.UserUpdateInput{Nickname: "编辑", Status: adminrbac.StatusActive}); err != nil {
		t.Fatalf("re-enable user: %v", err)
	}
	if _, err := service.Authenticate(ctx, login.Token); !errors.Is(err, adminrbac.ErrUnauthorized) {
		t.Fatalf("an old token must not revive after re-enabling the account, got %v", err)
	}
}

func TestOnlyActiveSuperAdministratorCannotBeDisabled(t *testing.T) {
	service, store := newService(t)
	ctx := context.Background()
	createTestSuperAdministrator(t, store, "admin", "local-admin-password")
	admin, err := store.FindAdminUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.UpdateUserAs(ctx, admin.ID, admin.ID, adminrbac.UserUpdateInput{Nickname: "管理员", Status: adminrbac.StatusDisabled})
	if !errors.Is(err, adminrbac.ErrLastSuperAdmin) {
		t.Fatalf("expected last-super protection, got %v", err)
	}
}

func TestPasswordResetRevokesExistingTokens(t *testing.T) {
	service, store := newService(t)
	ctx := context.Background()
	createTestSuperAdministrator(t, store, "admin", "local-admin-password")
	login, err := service.Login(ctx, "admin", "local-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := store.FindAdminUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ResetUserPasswordAs(ctx, admin.ID, admin.ID, "new-admin-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, login.Token); !errors.Is(err, adminrbac.ErrUnauthorized) {
		t.Fatalf("old token must be revoked after a password reset, got %v", err)
	}
	if _, err := service.Login(ctx, "admin", "local-admin-password"); !errors.Is(err, adminrbac.ErrUnauthorized) {
		t.Fatalf("old password must be rejected, got %v", err)
	}
	if _, err := service.Login(ctx, "admin", "new-admin-password"); err != nil {
		t.Fatalf("new password should work: %v", err)
	}
}

func TestNonSuperAdministratorCannotMutateSuperAdministrator(t *testing.T) {
	service, store := newService(t)
	ctx := context.Background()
	createTestSuperAdministrator(t, store, "admin", "local-admin-password")
	admin, err := store.FindAdminUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	operator, err := service.CreateUser(ctx, adminrbac.UserCreateInput{
		Username: "operator", Password: "operator-password", Nickname: "Operator", Status: adminrbac.StatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.UpdateUserAs(ctx, operator.ID, admin.ID, adminrbac.UserUpdateInput{Nickname: "Taken", Status: adminrbac.StatusActive}); !errors.Is(err, adminrbac.ErrForbidden) {
		t.Fatalf("expected super update to be forbidden, got %v", err)
	}
	if err := service.ResetUserPasswordAs(ctx, operator.ID, admin.ID, "attacker-password"); !errors.Is(err, adminrbac.ErrForbidden) {
		t.Fatalf("expected super password reset to be forbidden, got %v", err)
	}
	if err := service.SetUserRolesAs(ctx, operator.ID, admin.ID, nil); !errors.Is(err, adminrbac.ErrForbidden) {
		t.Fatalf("expected super role update to be forbidden, got %v", err)
	}
}

func newService(t *testing.T) (*adminrbac.Service, *memory.Store) {
	t.Helper()
	store := memory.NewStore(memory.Seed{})
	service := adminrbac.NewService(store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes"),
		Now:        func() time.Time { return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC) },
		SessionTTL: time.Hour,
	})
	return service, store
}

// createTestSuperAdministrator 模拟部署 SQL 已经写入 bcrypt 摘要后的管理员记录。
func createTestSuperAdministrator(t *testing.T, store *memory.Store, username, password string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 10, 11, 0, 0, 0, time.UTC)
	if err := store.CreateAdminUser(context.Background(), &adminrbac.User{
		Username: username, PasswordHash: string(hash), Nickname: "超级管理员",
		Status: adminrbac.StatusActive, IsSuper: true, AuthVersion: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func permissionByCode(t *testing.T, service *adminrbac.Service, code string) adminrbac.Permission {
	t.Helper()
	permissions, err := service.ListPermissions(context.Background())
	if err != nil {
		t.Fatalf("list permissions: %v", err)
	}
	for _, permission := range permissions {
		if permission.Code == code {
			return permission
		}
	}
	t.Fatalf("permission %s not found", code)
	return adminrbac.Permission{}
}
