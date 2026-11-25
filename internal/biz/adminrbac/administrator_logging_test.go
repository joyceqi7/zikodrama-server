package adminrbac_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"drama-server/internal/biz/adminrbac"
)

// listUsersLoggingRepository 只替换管理员列表与角色查询，覆盖两处仓储失败。
type listUsersLoggingRepository struct {
	adminrbac.Repository
	listErr  error
	rolesErr error
}

// ListAdminUsers 返回带敏感字段的测试账号，用于验证日志和返回值的脱敏边界。
func (r listUsersLoggingRepository) ListAdminUsers(context.Context) ([]adminrbac.User, error) {
	return []adminrbac.User{{ID: "user-test", Username: "private-username", PasswordHash: "private-password-hash"}}, r.listErr
}

// ListAdminUserRoleIDs 模拟角色查询成功或失败，不访问数据库。
func (r listUsersLoggingRepository) ListAdminUserRoleIDs(context.Context, string) ([]string, error) {
	return []string{"role-test"}, r.rolesErr
}

func TestListUsersLogsRepositoryErrorDetails(t *testing.T) {
	// 捕获进程默认日志，因此此测试及其子测试不能并行执行。
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	failure := errors.New("query failed: SELECT id FROM admin_users: connection refused")
	for _, test := range []struct {
		name    string
		repo    listUsersLoggingRepository
		message string
	}{
		{name: "列表查询失败", repo: listUsersLoggingRepository{listErr: failure}, message: "list admin users failed"},
		{name: "角色查询失败", repo: listUsersLoggingRepository{rolesErr: failure}, message: "list admin user roles failed"},
		{name: "成功不记错误日志", repo: listUsersLoggingRepository{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			service := adminrbac.NewService(test.repo, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")})
			users, err := service.ListUsers(context.Background())

			if test.message == "" {
				if err != nil || len(users) != 1 || users[0].PasswordHash != "" || len(users[0].RoleIDs) != 1 || users[0].RoleIDs[0] != "role-test" || output.Len() != 0 {
					t.Fatal("成功时应返回脱敏账号及角色，且不产生错误日志")
				}
				return
			}
			if err != failure || users != nil {
				t.Fatal("失败时应原样返回仓储错误，不返回部分账号列表")
			}
			for _, secret := range []string{"private-username", "private-password-hash"} {
				if strings.Contains(output.String(), secret) {
					t.Fatal("日志泄漏敏感数据")
				}
			}
			decoder := json.NewDecoder(&output)
			var record map[string]any
			if err := decoder.Decode(&record); err != nil {
				t.Fatalf("缺少有效的结构化错误日志：%v", err)
			}
			for field, want := range map[string]string{
				"level": "ERROR", "msg": test.message, "error": failure.Error(),
			} {
				if record[field] != want {
					t.Errorf("日志字段 %s = %v，期望 %s", field, record[field], want)
				}
			}
			if test.repo.rolesErr != nil && record["user_id"] != "user-test" {
				t.Error("角色查询失败日志缺少目标账号标识")
			}
			if err := decoder.Decode(&record); !errors.Is(err, io.EOF) {
				t.Error("同一次失败应只产生一条业务错误日志")
			}
		})
	}
}
