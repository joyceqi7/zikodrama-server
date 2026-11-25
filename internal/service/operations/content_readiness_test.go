package operations_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
)

// TestLegacyDraftCatalogHTTP 验证编辑列表的草稿筛选包含历史渠道状态，原始状态和线上内容不会因查询改写。
func TestLegacyDraftCatalogHTTP(t *testing.T) {
	dramas := []consumer.Drama{}
	for i, status := range []string{"draft", "published", "publishing", "unpublishing", "rejected", "ready"} {
		dramas = append(dramas, consumer.Drama{ID: fmt.Sprint(201 + i), Title: "历史内容", PublishStatus: status})
	}
	store := memory.NewStore(memory.Seed{Dramas: dramas})
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), newRBACService(t, store), operationshttp.Config{})
	var login struct {
		Token string `json:"token"`
	}
	decodeData(t, request(t, handler, "POST", "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`)), &login)
	for _, filter := range []string{"draft", "ready"} {
		var page operations.DramaListPage
		decodeData(t, request(t, handler, "GET", "/admin/v1/dramas?contentStatus="+filter, "", login.Token, nil), &page)
		want := int64(5)
		if filter == "ready" {
			want = 1
		}
		if page.Total != want {
			t.Fatalf("历史状态未按编辑状态筛选: filter=%s total=%d", filter, page.Total)
		}
	}
	for _, original := range dramas {
		current, err := store.FindDrama(context.Background(), original.ID)
		if err != nil || current.PublishStatus != original.PublishStatus {
			t.Fatal("列表改写历史状态", err)
		}
	}
}

// TestBatchReadyHTTPContract 验证入口规范化、主键与数量校验、事务拒绝、内容筛选以及无版本合同。
func TestBatchReadyHTTPContract(t *testing.T) {
	draft := consumer.Drama{ID: "201", Title: "内容", Description: "简介", CoverURL: "https://image.example/cover", PublishStatus: "draft", Episodes: []consumer.Episode{{ID: "301", DramaID: "201", Number: 1, VID: "source", MediaStatus: "ready"}}}
	store := memory.NewStore(memory.Seed{Dramas: []consumer.Drama{draft}})
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), newRBACService(t, store), operationshttp.Config{})
	var login struct {
		Token string `json:"token"`
	}
	decodeData(t, request(t, handler, "POST", "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`)), &login)
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf(`"%d"`, i+1)
	}
	for _, body := range []string{`{}`, `{"dramaIds":[]}`, `{"dramaIds":["201"," 201 "]}`, `{"dramaIds":["0"]}`, `{"dramaIds":["201x"]}`, `{"dramaIds":["18446744073709551616"]}`, `{"dramaIds":[201]}`, `{"dramaIds":["201"],"versionId":"1"}`, `{"dramaIds":[` + strings.Join(tooMany, ",") + `]}`} {
		result := request(t, handler, "POST", "/admin/v1/dramas/ready", "application/json", login.Token, strings.NewReader(body))
		if result.Code != http.StatusBadRequest {
			t.Fatalf("非法批量输入未拒绝：%d", result.Code)
		}
		d, _ := store.FindDrama(context.Background(), "201")
		if d.PublishStatus != "draft" {
			t.Fatal("校验失败改变内容")
		}
	}
	missing := request(t, handler, "POST", "/admin/v1/dramas/ready", "application/json", login.Token, strings.NewReader(`{"dramaIds":["201","999"]}`))
	if missing.Code != http.StatusNotFound {
		t.Fatal("未找到内容错误未映射", missing.Code)
	}
	d, _ := store.FindDrama(context.Background(), "201")
	if d.PublishStatus != "draft" {
		t.Fatal("整批未回滚")
	}
	denied := request(t, handler, "POST", "/admin/v1/dramas/ready", "application/json", "", strings.NewReader(`{"dramaIds":["201"]}`))
	if denied.Code != http.StatusUnauthorized {
		t.Fatal("批量就绪缺少鉴权", denied.Code)
	}
	draft.Description = ""
	if err := store.SaveDrama(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	incomplete := request(t, handler, "POST", "/admin/v1/dramas/ready", "application/json", login.Token, strings.NewReader(`{"dramaIds":["201"]}`))
	var rejection responseEnvelope
	if err := json.Unmarshal(incomplete.Body.Bytes(), &rejection); err != nil {
		t.Fatal(err)
	}
	if incomplete.Code != http.StatusConflict || rejection.Reason != "content_not_ready" {
		t.Fatal("就绪拒绝混入渠道配置", incomplete.Code, rejection.Reason)
	}
	draft.Description = "简介"
	if err := store.SaveDrama(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		result := request(t, handler, "POST", "/admin/v1/dramas/ready", "application/json", login.Token, strings.NewReader(`{"dramaIds":[" 201 "]}`))
		if result.Code != http.StatusOK {
			t.Fatal("规范化后就绪失败", result.Code)
		}
		var ready struct {
			ReadyCount int `json:"readyCount"`
		}
		decodeData(t, result, &ready)
		if ready.ReadyCount != 1 {
			t.Fatal("批量响应错误")
		}
	}
	for _, status := range []string{"draft", "ready"} {
		result := request(t, handler, "GET", "/admin/v1/dramas?page=1&pageSize=20&contentStatus="+status, "", login.Token, nil)
		var page operations.DramaListPage
		decodeData(t, result, &page)
		want := int64(0)
		if status == "ready" {
			want = 1
		}
		if page.Total != want {
			t.Fatal("内容筛选混入发布状态", status, page.Total)
		}
	}
	for _, filter := range []string{"contentStatus=published", "publication=direct_published"} {
		result := request(t, handler, "GET", "/admin/v1/dramas?"+filter, "", login.Token, nil)
		if result.Code != http.StatusBadRequest {
			t.Fatal("内容入口仍接受非法状态", filter, result.Code)
		}
	}
}
