package operations_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
)

// TestPermissionTreeHTTP 验证树节点完整 CRUD、循环保护和独立写权限。
func TestPermissionTreeHTTP(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	service := newRBACService(t, store)
	handler := operationshttp.NewHandler(nil, service, operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string           `json:"token"`
		Menus []adminrbac.Menu `json:"menus"`
	}
	decodeData(t, login, &credentials)
	if login.Code != 200 || len(credentials.Menus) == 0 {
		t.Fatal("登录未返回权限菜单树")
	}
	create := func(body string) adminrbac.Permission {
		response := request(t, handler, http.MethodPost, "/admin/v1/permissions", "application/json", credentials.Token, strings.NewReader(body))
		if response.Code != 201 {
			t.Fatalf("创建节点失败: %d %s", response.Code, response.Body.String())
		}
		var item adminrbac.Permission
		decodeData(t, response, &item)
		return item
	}
	root := create(`{"name":"自定义业务","code":"custom.root","type":"directory","status":"active","sortOrder":1}`)
	child := create(`{"parentId":"` + root.ID + `","name":"自定义子目录","code":"custom.child","type":"directory","status":"active"}`)
	cycle := request(t, handler, http.MethodPut, "/admin/v1/permissions/"+root.ID, "application/json", credentials.Token, strings.NewReader(`{"parentId":"`+child.ID+`","name":"自定义业务","code":"custom.root","type":"directory","status":"active"}`))
	if cycle.Code != 400 {
		t.Fatalf("循环移动应拒绝: %d", cycle.Code)
	}
	if response := request(t, handler, http.MethodDelete, "/admin/v1/permissions/"+root.ID, "", credentials.Token, nil); response.Code != 409 {
		t.Fatalf("不应删除非叶节点: %d", response.Code)
	}
	rename := request(t, handler, http.MethodPut, "/admin/v1/permissions/"+child.ID, "application/json", credentials.Token, strings.NewReader(`{"parentId":"`+root.ID+`","name":"已改名","code":"custom.child","type":"directory","status":"disabled","sortOrder":7}`))
	if rename.Code != 200 {
		t.Fatalf("编辑失败: %s", rename.Body.String())
	}
	read := request(t, handler, http.MethodGet, "/admin/v1/permissions", "", credentials.Token, nil)
	if read.Code != 200 || !strings.Contains(read.Body.String(), `"name":"已改名"`) || !strings.Contains(read.Body.String(), `"sortOrder":7`) {
		t.Fatal("编辑未持久化")
	}
	for _, body := range []string{
		`{"name":"页面","code":"custom.menu","type":"menu","route":"https://outside.test","status":"active"}`,
		`{"name":"无效","code":"*","type":"action","status":"active"}`,
		`{"name":"缺类型","code":"custom.invalid","status":"active"}`,
	} {
		if response := request(t, handler, http.MethodPost, "/admin/v1/permissions", "application/json", credentials.Token, strings.NewReader(body)); response.Code != 400 {
			t.Fatalf("无效字段应拒绝: %d", response.Code)
		}
	}
	if response := request(t, handler, http.MethodDelete, "/admin/v1/permissions/"+child.ID, "", credentials.Token, nil); response.Code != 200 {
		t.Fatal("删除叶节点失败")
	}
	if response := request(t, handler, http.MethodGet, "/admin/v1/permissions", "", "", nil); response.Code != 401 {
		t.Fatal("权限树不得绕过身份认证")
	}
	user, err := service.CreateUser(context.Background(), adminrbac.UserCreateInput{Username: "permission-reader", Password: "permission-reader-password", Nickname: "只读账号", Status: adminrbac.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := service.Login(context.Background(), user.Username, "permission-reader-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		path := "/admin/v1/permissions"
		if method != http.MethodPost {
			path += "/" + root.ID
		}
		response := request(t, handler, method, path, "application/json", viewer.Token, strings.NewReader(`{}`))
		if response.Code != 403 {
			t.Fatalf("无写权限调用 %s 应拒绝: %d", method, response.Code)
		}
	}
}

// TestPlaybackForensicsRouteIsRemoved 验证管理端不再提供自研播放取证查询。
func TestPlaybackForensicsRouteIsRemoved(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), newRBACService(t, store), operationshttp.Config{})
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		response := request(t, handler, method, "/admin/v1/playback-sessions/lookup", "application/json", "", strings.NewReader(`{"forensicWatermarkCode":"removed"}`))
		if response.Code != http.StatusNotFound {
			t.Fatalf("播放取证查询路由未移除：%s %d", method, response.Code)
		}
	}
}
