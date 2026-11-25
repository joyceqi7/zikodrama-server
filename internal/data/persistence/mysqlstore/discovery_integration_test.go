package mysqlstore

import (
	"context"
	"encoding/json"
	"testing"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// assertPaginatedDramaQueries 验证真实数据库分页使用线上摘要，详情仍返回完整分集。
func assertPaginatedDramaQueries(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	for _, id := range []string{"2021", "2022", "2023"} {
		published := domain.Drama{ID: id, Title: "pagination fixture " + id, Description: "search description", Status: "updating", PublishStatus: "published", OwnerApplicationID: "1024", TagIDs: []string{"21"}, PlatformVersion: 3, Episodes: []domain.Episode{{ID: id + "1", Number: 1, Title: "private episode", VID: "private-vid"}}}
		if id == "2023" {
			published.OwnerApplicationID = "9999"
		}
		draft := published
		draft.Title = "edited " + id
		draft.Published = &published
		draft.Episodes = nil
		if err := store.SaveDrama(ctx, draft); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(published)
		if err := seedOfficialProjection(store.data.DB().WithContext(ctx), draft.ID, raw); err != nil {
			t.Fatal(err)
		}
	}
	appCtx := application.WithContext(ctx, "1024")
	first, err := store.DiscoverDramas(appCtx, domain.DiscoveryQuery{Query: "pagination fixture", Limit: 1})
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	next, err := store.DiscoverDramas(appCtx, domain.DiscoveryQuery{Query: "pagination fixture", Limit: 1, Offset: 1, Tag: "悬疑推理", Status: "ongoing"})
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == first.Items[0].ID || next.NextCursor != "" {
		t.Fatalf("next page: %+v %v", next, err)
	}
	if next.Items[0].EpisodeCount != 1 || next.Items[0].Status != "ongoing" {
		t.Fatal("summary does not describe published version")
	}
	other, err := store.DiscoverDramas(application.WithContext(ctx, "1016"), domain.DiscoveryQuery{Limit: 20})
	if err != nil || len(other.Items) != 0 || len(other.Tags) != 0 {
		t.Fatal("application isolation violated")
	}
	admin, err := store.ListDramaPage(ctx, operations.DramaListQuery{Page: 1, PageSize: 1, Query: "edited 202", Publication: "published"})
	if err != nil || admin.Total != 3 || len(admin.Items) != 1 || admin.Items[0].EpisodeCount != 0 {
		t.Fatalf("admin summary: %+v %v", admin, err)
	}
	detail, err := domain.NewService(store, nil).FindDrama(appCtx, first.Items[0].ID)
	if err != nil || len(detail.Episodes) != 1 || detail.Episodes[0].VID != "private-vid" {
		t.Fatalf("detail unavailable: %v", err)
	}
}
