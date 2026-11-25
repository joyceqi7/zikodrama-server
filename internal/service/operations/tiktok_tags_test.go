package operations_test

import (
	"context"
	"drama-server/internal/biz/adminrbac"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
	"net/http"
	"strings"
	"testing"
)

// TestTikTokTagEnglishTranslationHTTP 检查保存读回、输入边界、鉴权及空值回退。
func TestTikTokTagEnglishTranslationHTTP(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	rbac := newRBACService(t, store)
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), rbac, operationshttp.Config{})
	login, err := rbac.Login(context.Background(), "admin", "local-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		id, body string
		status   int
	}{
		{"21", `{"englishName":"  Mystery & Suspense  "}`, 200},
		{"21", `{}`, 400}, {"21", `{"englishName":null}`, 400}, {"21", `{"englishName":123}`, 400},
		{"21", `{"englishName":"` + strings.Repeat("a", 101) + `"}`, 400},
		{"21", `{"englishName":"line\nbreak"}`, 400}, {"32", `{"englishName":"Missing"}`, 404},
		{"invalid", `{"englishName":"Missing"}`, 400},
	} {
		res := request(t, handler, http.MethodPut, "/admin/v1/tiktok-tags/"+sample.id, "application/json", login.Token, strings.NewReader(sample.body))
		if res.Code != sample.status {
			t.Fatalf("%s 应返回 %d，实际 %d", sample.id, sample.status, res.Code)
		}
	}
	res := request(t, handler, http.MethodGet, "/admin/v1/tiktok-tags", "", login.Token, nil)
	var tags []domain.TikTokTag
	decodeData(t, res, &tags)
	if len(tags) != 45 || tags[20].ID != "21" || tags[20].OfficialName != "悬疑推理" || tags[20].EnglishName != "Mystery & Suspense" {
		t.Fatal("保存读回或非法请求保护失败")
	}
	user, err := rbac.CreateUser(context.Background(), adminrbac.UserCreateInput{Username: "tag-reader", Password: "reader-test-password", Status: adminrbac.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := rbac.Login(context.Background(), user.Username, "reader-test-password")
	if err != nil {
		t.Fatal(err)
	}
	denied := request(t, handler, http.MethodPut, "/admin/v1/tiktok-tags/21", "application/json", viewer.Token, strings.NewReader(`{"englishName":"No access"}`))
	if denied.Code != 403 {
		t.Fatal("未授权写入未拒绝")
	}
	cleared := request(t, handler, http.MethodPut, "/admin/v1/tiktok-tags/21", "application/json", login.Token, strings.NewReader(`{"englishName":" "}`))
	var tag domain.TikTokTag
	decodeData(t, cleared, &tag)
	if tag.DisplayName() != "悬疑推理" {
		t.Fatal("清空未回退")
	}
}
