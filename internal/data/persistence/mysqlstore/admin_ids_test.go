package mysqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"drama-server/internal/biz/adminrbac"
)

func TestAdminIDsRejectMySQLNumericAliases(t *testing.T) {
	// 空仓储确保非法输入在访问数据库前被拒绝，而不是交给 MySQL 隐式转换。
	store := &Store{}
	ctx := context.Background()
	for _, id := range []string{"", "0", "01", "+1", "-1", "1abc", "1.0", "1e0", " 1", "18446744073709551616", "user-1"} {
		t.Run(id, func(t *testing.T) {
			checks := []func() error{
				func() error { _, err := store.FindAdminUserByID(ctx, id); return err },
				func() error { _, err := store.FindAdminRoleByID(ctx, id); return err },
				func() error { _, err := store.FindAdminPermissionByID(ctx, id); return err },
				func() error { return store.UpdateAdminUser(ctx, adminrbac.User{ID: id}) },
				func() error { return store.UpdateAdminRole(ctx, adminrbac.Role{ID: id}) },
				func() error { return store.DeleteAdminRole(ctx, id) },
				func() error { _, err := store.ListAssignedAdminPermissionIDs(ctx, id); return err },
				func() error { return store.ReplaceAdminUserRoles(ctx, "1", []string{id}) },
				func() error { return store.ReplaceAdminRolePermissions(ctx, "1", []string{id}) },
				func() error { return store.RevokeAdminTokens(ctx, id, 1, time.Time{}) },
			}
			for index, check := range checks {
				if err := check(); !errors.Is(err, adminrbac.ErrNotFound) {
					t.Errorf("入口 %d 未拒绝非法 ID：%v", index, err)
				}
			}
		})
	}
	for _, id := range []string{"1", "9007199254740993", "18446744073709551615"} {
		if _, err := parseAdminID(id); err != nil {
			t.Errorf("合法自增 ID %s 被拒绝：%v", id, err)
		}
	}
}
