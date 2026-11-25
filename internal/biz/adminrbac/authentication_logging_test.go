package adminrbac

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// diagnosticAdminRepository 只覆盖账号查询，以区分正常账号不存在和真实数据库错误。
type diagnosticAdminRepository struct {
	Repository
	err error
}

// FindAdminUserByUsername 返回指定查询故障，不保存账号或密码。
func (r diagnosticAdminRepository) FindAdminUserByUsername(context.Context, string) (User, error) {
	return User{}, r.err
}

// TestAdminAuthenticationFailureLevelsPreserveResults 验证账号拒绝与取消不刷成 ERROR，真实存储故障保留实际原因和错误链。
func TestAdminAuthenticationFailureLevelsPreserveResults(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	databaseErr := errors.New("SELECT id FROM admin_users: connection refused")
	for _, tc := range []struct {
		name, level string
		cause, want error
		authorize   bool
	}{
		{name: "账号不存在", cause: ErrNotFound, want: ErrUnauthorized, level: "WARN"},
		{name: "依赖取消", cause: context.Canceled, want: context.Canceled, level: "INFO"},
		{name: "数据库故障", cause: databaseErr, want: databaseErr, level: "ERROR"},
		{name: "缺少会话", want: ErrUnauthorized, level: "WARN", authorize: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			service := NewService(diagnosticAdminRepository{err: tc.cause}, Config{SigningKey: []byte("private-local-admin-signing-key-2026")})
			var err error
			if tc.authorize {
				_, err = service.Authorize(context.Background(), "", "test:read")
			} else {
				_, err = service.Login(context.Background(), "private-account", "private-password")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("认证结果改变: %v", err)
			}
			raw := logs.String()
			decoder := json.NewDecoder(&logs)
			count := 0
			for decoder.More() {
				var entry map[string]any
				if err := decoder.Decode(&entry); err != nil {
					t.Fatal(err)
				}
				count++
				if entry["level"] != tc.level || entry["error"] == nil {
					t.Fatalf("认证级别或诊断错误: %v", entry)
				}
			}
			if count == 0 || tc.cause != nil && !strings.Contains(raw, tc.cause.Error()) {
				t.Fatal("缺少实际故障")
			}
			for _, secret := range []string{"private-account", "private-password", "private-local-admin-signing-key-2026"} {
				if strings.Contains(raw, secret) {
					t.Fatal("认证日志泄漏凭据")
				}
			}
		})
	}
}
