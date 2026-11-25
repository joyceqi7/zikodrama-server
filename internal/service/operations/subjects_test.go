package operations_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/subject"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
)

// TestSubjectHTTPContract 覆盖主体维护、发布方成对校验及空密钥保留，响应不返回密钥。
func TestSubjectHTTPContract(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	rbac := newRBACService(t, store)
	login, err := rbac.Login(t.Context(), "admin", "local-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	h := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), rbac, operationshttp.Config{})
	created := request(t, h, "POST", "/admin/v1/subjects", "application/json", login.Token, strings.NewReader(`{"name":" 主体 B ","tiktokAppId":"000123","clientKey":"publisher-b","clientSecret":"private-subject-secret"}`))
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), "private-subject-secret") || strings.Contains(created.Body.String(), `"clientSecret":`) {
		t.Fatalf("主体创建或脱敏错误：%d", created.Code)
	}
	var row subject.Subject
	decodeData(t, created, &row)
	if row.Name != "主体 B" || row.TikTokAppID != "000123" || !row.ClientSecretConfigured {
		t.Fatal("资料规范化或字符串标识改变")
	}
	updated := request(t, h, "PUT", "/admin/v1/subjects/"+row.ID, "application/json", login.Token, strings.NewReader(`{"name":"主体 B 修改","tiktokAppId":"000123","clientKey":"publisher-b","clientSecret":""}`))
	if updated.Code != 200 {
		t.Fatalf("空密钥更新失败：%d", updated.Code)
	}
	stored, err := store.FindSubject(t.Context(), row.ID)
	if err != nil || stored.ClientSecret != "private-subject-secret" {
		t.Fatal("空密钥覆盖已有发布凭据", err)
	}
	for _, body := range []string{`{"name":" "}`, `{"name":"主体 C","tiktokAppId":"123"}`, `{"name":"主体 C","clientSecret":"only-secret"}`, `{"name":"主体 C","tiktokAppId":"invalid","clientKey":"key"}`} {
		if response := request(t, h, "POST", "/admin/v1/subjects", "application/json", login.Token, strings.NewReader(body)); response.Code != 400 {
			t.Fatal("未拒绝无效主体输入")
		}
	}
	// 即使具有发布权限，也不能不选主体或指定不存在的主体读取其他主体的最新任务。
	for _, id := range []string{"", "999999", "invalid"} {
		response := requestWithHeaders(t, h, "GET", "/admin/v1/dramas/1/publication", "", login.Token, nil, map[string]string{"X-Subject-ID": id})
		if response.Code != 400 {
			t.Fatalf("无效主体仍可查询官方任务：%q %d", id, response.Code)
		}
	}
}

// TestSubjectOptionsUseSessionOnly 验证普通账号可切换安全选项，但主体配置与密钥仍受统一 RBAC 控制。
func TestSubjectOptionsUseSessionOnly(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	service := subject.NewService(store)
	if _, err := service.Save(t.Context(), "", subject.Input{Name: "主体 B", TikTokAppID: "123", ClientKey: "private-publisher-key", ClientSecret: "private-publisher-secret"}); err != nil {
		t.Fatal(err)
	}
	rbac := newRBACService(t, store)
	user, err := rbac.CreateUser(t.Context(), adminrbac.UserCreateInput{Username: "subject-reader", Password: "fixture-reader-password", Nickname: "普通管理员", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	login, err := rbac.Login(t.Context(), user.Username, "fixture-reader-password")
	if err != nil {
		t.Fatal(err)
	}
	h := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), rbac, operationshttp.Config{})
	for _, path := range []string{"/admin/v1/subjects/options", "/admin/v1/applications/options"} {
		response := request(t, h, "GET", path, "", login.Token, nil)
		if response.Code != 200 {
			t.Fatalf("安全选项不应依赖配置读取权限：%d", response.Code)
		}
		var rows []map[string]json.RawMessage
		decodeData(t, response, &rows)
		for _, row := range rows {
			for key := range row {
				if strings.Contains(strings.ToLower(key), "secret") || key == "clientKey" || key == "ownerId" {
					t.Fatal("选项暴露发布凭据字段")
				}
			}
		}
	}
	if response := request(t, h, "GET", "/admin/v1/subjects", "", login.Token, nil); response.Code != 403 {
		t.Fatal("无权限账号仍可读取主体配置")
	}
	if response := request(t, h, "GET", "/admin/v1/subjects/options", "", "", nil); response.Code != 401 {
		t.Fatal("匿名账号可读取主体目录")
	}
}
