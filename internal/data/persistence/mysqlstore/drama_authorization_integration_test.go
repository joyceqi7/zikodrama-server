package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"encoding/json"
	"testing"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// TestDramaAuthorizationSummaryIntegration 验证分页摘要只按线上快照计数，草稿修改与旧任务不覆盖授权结果。
func TestDramaAuthorizationSummaryIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithSubject(context.Background(), "1")
	published := domain.Drama{ID: "9007199254740993", Title: "授权统计联调", Status: "updating", PublishStatus: "published", PlatformVersion: 3, OwnerApplicationID: "1024", AuthorizedApplicationIDs: []string{"1024", "1016", "1016", "", "9007199254740994"}, TagIDs: []string{}}
	draft := published
	draft.PublishStatus = "draft"
	draft.OwnerApplicationID = "9999"
	draft.AuthorizedApplicationIDs = []string{"4", "5", "6", "7"}
	draft.Published = &published
	assert := func(want int, owner string) {
		t.Helper()
		if err := f.store.SaveDrama(ctx, draft); err != nil {
			t.Fatal(err)
		}
		// 普通编辑不会覆盖线上版本；测试显式模拟发布或撤权后的快照投影。
		snapshot, err := json.Marshal(draft.Published)
		if err != nil {
			t.Fatal(err)
		}
		if err := seedOfficialProjection(f.store.data.DB().WithContext(ctx), draft.ID, snapshot); err != nil {
			t.Fatal(err)
		}
		page, err := f.store.ListDramaPage(ctx, operations.DramaListQuery{Page: 1, PageSize: 20, Query: "授权统计联调"})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("读取列表失败: %+v %v", page, err)
		}
		item := page.Items[0]
		if item.AuthorizedApplicationCount != want || item.OwnerApplicationID != owner {
			t.Fatalf("授权摘要不符: %+v", item)
		}
	}
	assert(2, "1024")
	published.AuthorizedApplicationIDs = nil
	assert(0, "1024")
	published.PublishStatus = "offline"
	published.AuthorizedApplicationIDs = []string{"1016"}
	assert(0, "1024")
	draft.Published = nil
	assert(0, "")
}
