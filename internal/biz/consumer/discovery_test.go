package consumer_test

import (
	"context"
	"drama-server/internal/biz/application"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/testsupport/memory"
)

func TestDiscoveryFiltersPrimaryCatalogAndPaginatesDeterministically(t *testing.T) {
	t.Parallel()
	store := memory.NewStore(memory.Seed{
		PublishedOwnerID: memory.TestApplicationID,
		Dramas: []domain.Drama{
			{ID: "5008", Title: "Hidden Truth", Description: "A buried secret", TagIDs: []string{"21"}, Status: "completed", PublishStatus: "published", Episodes: []domain.Episode{{ID: "5014", Number: 1}}},
			{ID: "5009", Title: "Summer Promise", TagIDs: []string{"23"}, Status: "ongoing", PublishStatus: "published", Episodes: []domain.Episode{{ID: "5015", Number: 1}}},
			{ID: "drama-3", Title: "Night Witness", TagIDs: []string{"21"}, Status: "completed", PublishStatus: "published", Episodes: []domain.Episode{{ID: "episode-3", Number: 1}}},
		},
	})
	service := domain.NewService(store, time.Now)
	ctx := application.WithContext(context.Background(), memory.TestApplicationID)

	matched, err := service.Discover(ctx, domain.DiscoveryQuery{Query: "buried", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(matched.Items) != 1 || matched.Items[0].ID != "5008" || matched.Items[0].Title != "Hidden Truth" {
		t.Fatalf("expected primary text match, got %+v", matched.Items)
	}

	first, err := service.Discover(ctx, domain.DiscoveryQuery{Tag: "悬疑推理", Status: "completed", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].ID != "5008" || first.NextCursor == "" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	second, err := service.Discover(ctx, domain.DiscoveryQuery{Tag: "悬疑推理", Status: "completed", Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != "drama-3" || second.NextCursor != "" {
		t.Fatalf("unexpected second page: %+v", second)
	}
	if len(first.Tags) != 2 || first.Tags[0] != "悬疑推理" || first.Tags[1] != "甜宠" {
		t.Fatalf("expected stable catalog tags, got %+v", first.Tags)
	}
}
