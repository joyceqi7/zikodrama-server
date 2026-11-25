package adminrbac_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/adminrbac"
)

// serviceLoggingRepository 按调用阶段注入故障，检查 Service 是否保留错误和诊断信息。
type serviceLoggingRepository struct {
	adminrbac.Repository
	fail    string
	failure error
	finds   int
}

// FindAdminUserByID 区分操作者、目标账号与更新后回读三个失败位置。
func (r *serviceLoggingRepository) FindAdminUserByID(context.Context, string) (adminrbac.User, error) {
	r.finds++
	stage := []string{"actor", "target", "reload"}[r.finds-1]
	if r.fail == stage {
		return adminrbac.User{}, r.failure
	}
	return adminrbac.User{ID: "1", Status: adminrbac.StatusActive, PasswordHash: "private-password-hash"}, nil
}

// UpdateAdminUser 模拟资料保存失败。
func (r *serviceLoggingRepository) UpdateAdminUser(context.Context, adminrbac.User) error {
	if r.fail == "update" {
		return r.failure
	}
	return nil
}

// ListAdminUserRoleIDs 模拟更新后角色读取失败。
func (r *serviceLoggingRepository) ListAdminUserRoleIDs(context.Context, string) ([]string, error) {
	if r.fail == "roles" {
		return nil, r.failure
	}
	return []string{}, nil
}

// UpdateAdminPassword 模拟密码摘要保存失败，不记录密码参数。
func (r *serviceLoggingRepository) UpdateAdminPassword(context.Context, string, string, time.Time) error {
	return r.failure
}

// FindAdminRoleByID 模拟角色分配前校验失败。
func (r *serviceLoggingRepository) FindAdminRoleByID(context.Context, string) (adminrbac.Role, error) {
	if r.fail == "find_role" {
		return adminrbac.Role{}, r.failure
	}
	return adminrbac.Role{ID: "2"}, nil
}

// ReplaceAdminUserRoles 模拟角色关联整体替换失败。
func (r *serviceLoggingRepository) ReplaceAdminUserRoles(context.Context, string, []string) error {
	return r.failure
}

// ListAdminPermissions 模拟只读权限目录查询失败。
func (r *serviceLoggingRepository) ListAdminPermissions(context.Context) ([]adminrbac.Permission, error) {
	return nil, r.failure
}

// RevokeAdminTokens 模拟注销时认证版本更新失败。
func (r *serviceLoggingRepository) RevokeAdminTokens(context.Context, string, int64, time.Time) error {
	return r.failure
}

// CreateAdminUser 模拟账号写入失败，不输出输入的密码摘要。
func (r *serviceLoggingRepository) CreateAdminUser(context.Context, *adminrbac.User) error {
	return r.failure
}

func TestServiceLogsErrorsBeforeReturning(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	failure := errors.New("query failed: SELECT id FROM admin_users: connection refused")
	ctx := context.Background()
	update := func(s *adminrbac.Service) error {
		_, err := s.UpdateUserAs(ctx, "1", "2", adminrbac.UserUpdateInput{Nickname: "private-nickname", Status: adminrbac.StatusActive})
		return err
	}
	reset := func(s *adminrbac.Service) error { return s.ResetUserPasswordAs(ctx, "1", "2", "private-password-2026") }
	assign := func(s *adminrbac.Service) error { return s.SetUserRolesAs(ctx, "1", "2", []string{"3"}) }
	for _, test := range []struct {
		name, fail, message string
		run                 func(*adminrbac.Service) error
	}{
		{"更新时操作者查询失败", "actor", "authorize admin actor failed", update},
		{"更新时目标查询失败", "target", "find admin user by id failed", update},
		{"账号保存失败", "update", "update admin user failed", update},
		{"账号回读失败", "reload", "find admin user by id failed", update},
		{"账号角色回读失败", "roles", "list admin user roles failed", update},
		{"重置时操作者查询失败", "actor", "authorize admin actor failed", reset},
		{"重置时目标查询失败", "target", "find admin user by id failed", reset},
		{"密码保存失败", "password", "update admin password failed", reset},
		{"分配时操作者查询失败", "actor", "authorize admin actor failed", assign},
		{"分配时目标查询失败", "target", "find admin user by id failed", assign},
		{"分配角色查询失败", "find_role", "find admin role by id failed", assign},
		{"角色关联保存失败", "replace", "replace admin user roles failed", assign},
		{"只读权限查询失败", "permissions", "list admin permissions failed", func(s *adminrbac.Service) error { _, err := s.ListPermissions(ctx); return err }},
		{"创建账号失败", "create", "create admin user failed", func(s *adminrbac.Service) error {
			_, err := s.CreateUser(ctx, adminrbac.UserCreateInput{Username: "private-user", Nickname: "private-nickname", Password: "private-password-2026", Status: adminrbac.StatusActive})
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			repo := &serviceLoggingRepository{fail: test.fail, failure: failure}
			if err := test.run(adminrbac.NewService(repo, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")})); err != failure {
				t.Fatalf("错误传播被改变：%v", err)
			}
			raw := output.String()
			found := false
			for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
				var record map[string]any
				if err := json.Unmarshal([]byte(line), &record); err != nil {
					t.Fatalf("日志不是有效 JSON：%v", err)
				}
				if record["msg"] == test.message && record["error"] == failure.Error() && record["level"] == "ERROR" {
					found = true
				}
			}
			if !found {
				t.Fatalf("缺少操作描述及原始 SQL 错误：%s", raw)
			}
			for _, secret := range []string{"private-password", "private-session-token", "private-user", "private-nickname"} {
				if strings.Contains(raw, secret) {
					t.Fatal("日志泄漏账号或凭据")
				}
			}
		})
	}
	t.Run("更新成功不产生错误日志", func(t *testing.T) {
		var output bytes.Buffer
		slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
		service := adminrbac.NewService(&serviceLoggingRepository{}, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")})
		if err := update(service); err != nil || output.Len() != 0 {
			t.Fatalf("成功路径产生错误或日志：%v %s", err, output.String())
		}
	})
}
