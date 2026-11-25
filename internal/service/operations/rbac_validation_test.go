package operations

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"drama-server/internal/biz/adminrbac"
)

func TestRBACRejectsInvalidInputBeforeCallingService(t *testing.T) {
	// 不提供 RBAC Service；任何先访问业务层再校验参数的回归都会使测试失败。
	handler := &Handler{}
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	for _, test := range []struct {
		name                  string
		handle                http.HandlerFunc
		pathKey, pathID, body string
	}{
		{"创建用户缺用户名", handler.createUser, "", "", `{"password":"private-password-2026","nickname":"用户","status":"active"}`},
		{"用户名过短", handler.createUser, "", "", `{"username":"ab","password":"private-password-2026","nickname":"用户","status":"active"}`},
		{"用户名格式错误", handler.createUser, "", "", `{"username":"1user","password":"private-password-2026","nickname":"用户","status":"active"}`},
		{"创建用户昵称为空", handler.createUser, "", "", `{"username":"user","password":"private-password-2026","nickname":"  ","status":"active"}`},
		{"创建用户状态非法", handler.createUser, "", "", `{"username":"user","password":"private-password-2026","nickname":"用户","status":"unknown"}`},
		{"创建用户缺密码", handler.createUser, "", "", `{"username":"user","nickname":"用户","status":"active"}`},
		{"创建用户弱密码", handler.createUser, "", "", `{"username":"user","password":"123456","nickname":"用户","status":"active"}`},
		{"创建用户超长密码", handler.createUser, "", "", `{"username":"user","password":"` + strings.Repeat("p", 73) + `","nickname":"用户","status":"active"}`},
		{"更新用户缺昵称", handler.updateUser, "userID", "1", `{"status":"active"}`},
		{"更新用户缺状态", handler.updateUser, "userID", "1", `{"nickname":"用户"}`},
		{"更新用户错误主键", handler.updateUser, "userID", "1abc", `{"nickname":"用户","status":"active"}`},
		{"重置弱密码", handler.resetUserPassword, "userID", "1", `{"password":"123456"}`},
		{"重置超长密码", handler.resetUserPassword, "userID", "1", `{"password":"` + strings.Repeat("p", 73) + `"}`},
		{"重置密码缺主键", handler.resetUserPassword, "userID", "", `{"password":"private-password-2026"}`},
		{"创建角色缺名称", handler.createRole, "", "", `{"code":"reader","status":"active"}`},
		{"创建角色缺编码", handler.createRole, "", "", `{"name":"只读","status":"active"}`},
		{"创建角色编码错误", handler.createRole, "", "", `{"name":"只读","code":"1role","status":"active"}`},
		{"创建角色状态错误", handler.createRole, "", "", `{"name":"只读","code":"reader","status":"unknown"}`},
		{"更新角色先检查字段", handler.updateRole, "roleID", "1", `{"name":"","code":"reader","status":"active"}`},
		{"更新角色错误主键", handler.updateRole, "roleID", "01", `{"name":"只读","code":"reader","status":"active"}`},
		{"删除角色错误主键", handler.deleteRole, "roleID", "-1", ``},
		{"分配角色错误主键", handler.setUserRoles, "userID", "1", `{"roleIds":["bad"]}`},
		{"分配角色空元素", handler.setUserRoles, "userID", "1", `{"roleIds":[""]}`},
		{"分配权限错误主键", handler.setRolePermissions, "roleID", "1", `{"permissionIds":["18446744073709551616"]}`},
		{"分配权限目标缺失", handler.setRolePermissions, "roleID", "", `{"permissionIds":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			if test.pathKey != "" {
				request.SetPathValue(test.pathKey, test.pathID)
			}
			response := httptest.NewRecorder()
			test.handle(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("无效参数应在入口返回400，实际%d %s", response.Code, response.Body.String())
			}
			var record map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &record); err != nil {
				t.Fatalf("缺少单条参数错误日志：%v %s", err, output.String())
			}
			if record["error"] != adminrbac.ErrInvalidInput.Error() {
				t.Fatalf("错误日志没有保留参数错误：%s", output.String())
			}
			if strings.Contains(output.String(), "private-password") {
				t.Fatal("参数日志泄漏密码")
			}
		})
	}
}

func TestRBACInputNormalizationAndBoundaries(t *testing.T) {
	input := adminrbac.UserCreateInput{Username: "  EDITOR  ", Password: "  secret-password  ", Nickname: " 编辑 ", Status: "active"}
	if err := validateUserCreateInput(&input); err != nil || input.Username != "editor" || input.Nickname != "编辑" || input.Password != "  secret-password  " {
		t.Fatalf("创建用户规范化错误：%v", err)
	}
	role := adminrbac.RoleInput{Name: " 只读 ", Code: " READ_ONLY ", Remark: " 备注 ", Status: " ACTIVE "}
	if err := validateRoleInput(&role); err != nil || role.Name != "只读" || role.Code != "read_only" || role.Remark != "备注" || role.Status != "active" {
		t.Fatalf("角色规范化错误：%v", err)
	}
	for _, size := range []int{11, 12, 72, 73} {
		want := size >= 12 && size <= 72
		if got := validateAdminPassword(strings.Repeat("a", size)) == nil; got != want {
			t.Fatalf("密码字节边界%d错误", size)
		}
	}
	if validateAdminPassword(strings.Repeat("密", 24)) != nil || validateAdminPassword(strings.Repeat("密", 25)) == nil {
		t.Fatal("必须按bcrypt字节上限校验多字节密码")
	}
	for _, size := range []int{2, 3, 64, 65} {
		input.Username = strings.Repeat("a", size)
		if got := validateUserCreateInput(&input) == nil; got != (size >= 3 && size <= 64) {
			t.Fatalf("用户名长度边界%d错误", size)
		}
	}
	for _, id := range []string{"1", "9007199254740993", "18446744073709551615"} {
		if err := validateRBACID(id); err != nil {
			t.Fatalf("有效字符串主键被拒绝：%s", id)
		}
	}
	for _, id := range []string{"", "0", "01", "1.0", "1abc", "+1", "-1", "18446744073709551616"} {
		if validateRBACID(id) == nil {
			t.Fatalf("错误主键被接受：%s", id)
		}
	}
	for _, ids := range [][]string{nil, {}, {"9007199254740993", "9007199254740993"}} {
		if validateRBACAssignment("1", ids) != nil {
			t.Fatal("空集合清空与重复主键去重合同必须保留")
		}
	}
	for _, test := range []struct {
		route, icon string
		valid       bool
	}{
		{"/channels/postback-logs", "scroll-text", true},
		{"dramas", "library", true},
		{"/direct/releases", "library", false},
		{"directReleases", "library", false},
		{"/dramas/reviews", "scroll-text", false},
		{"reviewRecords", "scroll-text", false},
		{"/official/publications", "library", true},
		{"/dramas/releases", "library", true},
		{"https://outside.test", "library", false},
		{"/channels//logs", "library", false},
		{"/channels/logs?status=ok", "library", false},
		{"/channels/logs", "<svg>", false},
	} {
		permission := adminrbac.PermissionInput{Name: "回传日志", Code: "menu.postbacklogs", Type: adminrbac.PermissionMenu, Route: test.route, Icon: test.icon, Status: adminrbac.StatusActive}
		if got := validatePermissionInput(&permission) == nil; got != test.valid {
			t.Fatalf("页面路径或图标校验错误 route=%q icon=%q valid=%t", test.route, test.icon, test.valid)
		}
	}
}
