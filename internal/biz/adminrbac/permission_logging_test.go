package adminrbac_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"drama-server/internal/biz/adminrbac"
)

type permissionFailureRepository struct {
	adminrbac.Repository
	findFailure bool
	failure     error
}

func (r permissionFailureRepository) FindAdminPermissionByID(context.Context, string) (adminrbac.Permission, error) {
	if r.findFailure {
		return adminrbac.Permission{}, r.failure
	}
	return adminrbac.Permission{ID: "1"}, nil
}
func (r permissionFailureRepository) CreateAdminPermission(context.Context, *adminrbac.Permission) error {
	return r.failure
}
func (r permissionFailureRepository) UpdateAdminPermission(context.Context, adminrbac.Permission) error {
	return r.failure
}
func (r permissionFailureRepository) DeleteAdminPermission(context.Context, string) error {
	return r.failure
}

// TestPermissionWriteFailureLogging 验证新增写入口保留实际错误和稳定动作，不记录提交资料。
func TestPermissionWriteFailureLogging(t *testing.T) {
	failure := errors.New("permission storage unavailable")
	for _, item := range []struct {
		name, message string
		find          bool
		run           func(*adminrbac.Service) error
	}{
		{"create", "create admin permission failed", false, func(s *adminrbac.Service) error {
			_, err := s.CreatePermission(context.Background(), adminrbac.PermissionInput{Name: "private-permission-form"})
			return err
		}},
		{"find", "find admin permission failed", true, func(s *adminrbac.Service) error {
			_, err := s.UpdatePermission(context.Background(), "1", adminrbac.PermissionInput{})
			return err
		}},
		{"update", "update admin permission failed", false, func(s *adminrbac.Service) error {
			_, err := s.UpdatePermission(context.Background(), "1", adminrbac.PermissionInput{Name: "private-permission-form"})
			return err
		}},
		{"delete", "delete admin permission failed", false, func(s *adminrbac.Service) error { return s.DeletePermission(context.Background(), "1") }},
	} {
		t.Run(item.name, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			defer slog.SetDefault(previous)
			service := adminrbac.NewService(permissionFailureRepository{findFailure: item.find, failure: failure}, adminrbac.Config{})
			if err := item.run(service); !errors.Is(err, failure) {
				t.Fatal("错误链改变")
			}
			if !strings.Contains(output.String(), item.message) || !strings.Contains(output.String(), failure.Error()) || strings.Contains(output.String(), "private-permission-form") {
				t.Fatalf("日志合同错误: %s", output.String())
			}
		})
	}
}
