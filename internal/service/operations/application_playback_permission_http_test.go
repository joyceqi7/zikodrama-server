package operations_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
)

// TestApplicationPlaybackSwitchUsesApplicationUpdatePermission 经真实登录和路由鉴权验证播放器归应用编辑，旧播放权限不能代替编辑权限。
func TestApplicationPlaybackSwitchUsesApplicationUpdatePermission(t *testing.T) {
	for _, test := range []struct {
		name, permission string
		status           int
	}{
		{"应用编辑允许切换", adminrbac.PermissionApplicationUpdate, http.StatusOK},
		{"应用只读拒绝切换", adminrbac.PermissionApplicationRead, http.StatusForbidden},
		{"仅历史播放权限拒绝切换", "application.playback.update", http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			store := memory.NewStore(memory.Seed{})
			app := application.Application{SubjectID: "1", ID: "701", Name: "权限切换测试", TikTokAppID: "000123", ClientKey: "fixture-client", Status: "disabled", MonetizationMode: "IAP"}
			if err := store.SaveApplication(ctx, app); err != nil {
				t.Fatal(err)
			}
			rbac := newRBACService(t, store)
			permissions, err := rbac.ListPermissions(ctx)
			if err != nil {
				t.Fatal(err)
			}
			permissionID, menuID := "", ""
			for _, permission := range permissions {
				if permission.Code == test.permission {
					permissionID = permission.ID
				}
				if permission.Code == "menu.applications" {
					menuID = permission.ID
				}
			}
			if test.permission == "application.playback.update" {
				legacy, err := rbac.CreatePermission(ctx, adminrbac.PermissionInput{ParentID: menuID, Name: "历史播放配置", Code: test.permission, Type: "action", Status: "active"})
				if err != nil {
					t.Fatal(err)
				}
				permissionID = legacy.ID
			}
			if permissionID == "" {
				t.Fatal("未找到测试权限")
			}
			role, err := rbac.CreateRole(ctx, adminrbac.RoleInput{Name: "测试角色", Code: "playback_editor", Status: "active"})
			if err != nil {
				t.Fatal(err)
			}
			if err := rbac.SetRolePermissions(ctx, role.ID, []string{permissionID}); err != nil {
				t.Fatal(err)
			}
			user, err := rbac.CreateUser(ctx, adminrbac.UserCreateInput{Username: "editor", Password: "fixture-editor-password", Nickname: "普通管理员", Status: "active"})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.ReplaceAdminUserRoles(ctx, user.ID, []string{role.ID}); err != nil {
				t.Fatal(err)
			}
			login, err := rbac.Login(ctx, user.Username, "fixture-editor-password")
			if err != nil || login.User.IsSuper {
				t.Fatalf("普通管理员登录失败：%v", err)
			}
			handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), rbac, operationshttp.Config{Direct: directplay.NewService(nil, nil)})
			revision := int64(0)
			input := operations.ApplicationInput{SubjectID: "1", Name: app.Name, TikTokAppID: app.TikTokAppID, ClientKey: app.ClientKey, Status: "disabled", MonetizationMode: "IAP", PlaybackProvider: "byteplus_web", PlaybackRevision: &revision}
			body, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			response := request(t, handler, http.MethodPut, "/admin/v1/applications/701", "application/json", login.Token, bytes.NewReader(body))
			if response.Code != test.status {
				t.Fatalf("状态错误：%d，预期：%d", response.Code, test.status)
			}
			persisted, err := store.FindApplication(ctx, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if test.status == http.StatusOK {
				if persisted.PlaybackProvider != "byteplus_web" || persisted.PlaybackRevision != 1 || persisted.TikTokAppID != app.TikTokAppID || persisted.ClientKey != app.ClientKey {
					t.Fatal("切换未保存正确通道、修订或应用身份")
				}
			} else if persisted.Normalized().PlaybackProvider != "tiktok_minis" || persisted.PlaybackRevision != 0 {
				t.Fatal("权限拒绝仍改变了应用")
			}
		})
	}
}
