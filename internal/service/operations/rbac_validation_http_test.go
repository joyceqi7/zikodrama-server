package operations_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"drama-server/internal/biz/adminrbac"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
)

func TestRBACHTTPValidationPreservesCredentialsAndBusinessRules(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	handler := operationshttp.NewHandler(nil, newRBACService(t, store), operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)
	if login.Code != 200 || credentials.Token == "" {
		t.Fatal("测试管理员登录失败")
	}

	weak := request(t, handler, http.MethodPost, "/admin/v1/users", "application/json", credentials.Token, strings.NewReader(`{"username":"weak","password":"123456","nickname":"用户","status":"active"}`))
	if weak.Code != 400 {
		t.Fatalf("弱密码创建应返回400，实际%d", weak.Code)
	}
	if _, err := store.FindAdminUserByUsername(context.Background(), "weak"); !errors.Is(err, adminrbac.ErrNotFound) {
		t.Fatal("弱密码请求写入了账号")
	}

	created := request(t, handler, http.MethodPost, "/admin/v1/users", "application/json", credentials.Token, strings.NewReader(`{"username":"  EDITOR  ","password":"  editor-password  ","nickname":" 编辑 ","status":"active"}`))
	if created.Code != 201 {
		t.Fatalf("合法用户创建失败：%d %s", created.Code, created.Body.String())
	}
	var user adminrbac.User
	decodeData(t, created, &user)
	if user.Username != "editor" || user.Nickname != "编辑" {
		t.Fatal("Handler没有在调用Service前规范化参数")
	}
	before, err := store.FindAdminUserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ path, body string }{
		{"/admin/v1/users/" + user.ID, `{"nickname":" ","status":"disabled"}`},
		{"/admin/v1/users/" + user.ID + "/password", `{"password":"123456"}`},
	} {
		response := request(t, handler, http.MethodPut, test.path, "application/json", credentials.Token, strings.NewReader(test.body))
		if response.Code != 400 {
			t.Fatalf("无效更新应返回400，实际%d", response.Code)
		}
	}
	after, err := store.FindAdminUserByID(context.Background(), user.ID)
	if err != nil || after.Nickname != before.Nickname || after.Status != before.Status || after.AuthVersion != before.AuthVersion || after.PasswordHash != before.PasswordHash {
		t.Fatal("无效请求修改了账号或认证版本")
	}
	exactPassword := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"editor","password":"  editor-password  "}`))
	if exactPassword.Code != 200 {
		t.Fatal("密码首尾空格被改写，原始密码无法登录")
	}

	createdRole := request(t, handler, http.MethodPost, "/admin/v1/roles", "application/json", credentials.Token, strings.NewReader(`{"name":" 只读 ","code":" READER ","remark":" 备注 ","status":" ACTIVE "}`))
	if createdRole.Code != 201 {
		t.Fatalf("合法角色创建失败：%d %s", createdRole.Code, createdRole.Body.String())
	}
	var role adminrbac.Role
	decodeData(t, createdRole, &role)
	if role.Name != "只读" || role.Code != "reader" || role.Status != "active" || role.Remark != "备注" {
		t.Fatal("角色参数没有正确规范化")
	}
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{"roleIds":["bad"]}`, 400},
		{`{"roleIds":["99999"]}`, 404},
		{`{"roleIds":["` + role.ID + `","` + role.ID + `"]}`, 200},
	} {
		response := request(t, handler, http.MethodPut, "/admin/v1/users/"+user.ID+"/roles", "application/json", credentials.Token, strings.NewReader(test.body))
		if response.Code != test.status {
			t.Fatalf("格式校验和存在性校验边界错误：期望%d，实际%d %s", test.status, response.Code, response.Body.String())
		}
	}
	referenced := request(t, handler, http.MethodDelete, "/admin/v1/roles/"+role.ID, "", credentials.Token, nil)
	if referenced.Code != 409 {
		t.Fatalf("Service必须继续保护被引用的角色，实际%d", referenced.Code)
	}
	cleared := request(t, handler, http.MethodPut, "/admin/v1/users/"+user.ID+"/roles", "application/json", credentials.Token, strings.NewReader(`{"roleIds":[]}`))
	if cleared.Code != 200 {
		t.Fatal("空角色数组应允许清空分配")
	}
	roles, err := store.ListAdminUserRoleIDs(context.Background(), user.ID)
	if err != nil || len(roles) != 0 {
		t.Fatal("角色分配未被清空")
	}
}
