package operations_test

import (
	"net/http"
	"strings"
	"testing"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
)

// TestDraftHTTPWithoutEpisodesCanBeSavedAndCompletedLater 验证空草稿的 HTTP 保存、读取及后续补集约束。
func TestDraftHTTPWithoutEpisodesCanBeSavedAndCompletedLater(t *testing.T) {
	t.Parallel()
	store := memory.NewStore(memory.Seed{})
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	handler := operationshttp.NewHandler(service, newRBACService(t, store), operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var session struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &session)
	created := request(t, handler, http.MethodPost, "/admin/v1/dramas", "application/json", session.Token, strings.NewReader(`{"title":"先存草稿","description":"短剧简介","episodes":[]}`))
	if created.Code != http.StatusCreated {
		t.Fatalf("空分集草稿保存失败：%d", created.Code)
	}
	var draft domain.Drama
	decodeData(t, created, &draft)
	read := request(t, handler, http.MethodGet, "/admin/v1/dramas/"+draft.ID, "", session.Token, nil)
	decodeData(t, read, &draft)
	if draft.Title != "先存草稿" || len(draft.Episodes) != 0 || draft.PublishStatus != "draft" {
		t.Fatal("读取结果没有保留空分集草稿")
	}

	updated := request(t, handler, http.MethodPut, "/admin/v1/dramas/"+draft.ID, "application/json", session.Token, strings.NewReader(`{"title":"修改后的草稿","description":"短剧简介","episodes":[]}`))
	if updated.Code != http.StatusOK {
		t.Fatalf("空分集草稿更新失败：%d", updated.Code)
	}
	added := request(t, handler, http.MethodPut, "/admin/v1/dramas/"+draft.ID, "application/json", session.Token, strings.NewReader(`{"title":"修改后的草稿","description":"短剧简介","episodes":[{"number":1,"title":"第一集"}]}`))
	if added.Code != http.StatusOK {
		t.Fatalf("稍后补充分集失败：%d", added.Code)
	}
	read = request(t, handler, http.MethodGet, "/admin/v1/dramas/"+draft.ID, "", session.Token, nil)
	decodeData(t, read, &draft)
	if len(draft.Episodes) != 1 || draft.Episodes[0].ID == "" || draft.Episodes[0].MediaStatus == "ready" {
		t.Fatal("分集应保存为待上传状态")
	}
	invalid := request(t, handler, http.MethodPut, "/admin/v1/dramas/"+draft.ID, "application/json", session.Token, strings.NewReader(`{"title":"非法集序","description":"短剧简介","episodes":[{"number":2,"title":"第二集"}]}`))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("非连续集序应拒绝：%d", invalid.Code)
	}
	omitted := request(t, handler, http.MethodPut, "/admin/v1/dramas/"+draft.ID, "application/json", session.Token, strings.NewReader(`{"title":"遗漏分集的请求","description":"短剧简介"}`))
	if omitted.Code != http.StatusBadRequest {
		t.Fatalf("更新省略分集时应拒绝以免误删：%d", omitted.Code)
	}
}
