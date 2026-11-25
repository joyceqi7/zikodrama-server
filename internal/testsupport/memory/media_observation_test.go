package memory

import (
	"context"
	"testing"

	domain "drama-server/internal/biz/consumer"
)

// TestPublishedPosterHydration 验证分集表中的官方海报会在读取时按同一 VID 合并到线上内容。
func TestPublishedPosterHydration(t *testing.T) {
	online := domain.Drama{
		ID: "101", PublishStatus: "published",
		Episodes: []domain.Episode{{ID: "201", DramaID: "101", VID: "vid-1", MediaStatus: "ready"}},
	}
	drama := online
	drama.Episodes = []domain.Episode{{ID: "201", DramaID: "101", VID: "vid-1", PosterURL: "https://cover.example.com/episode.jpg", MediaStatus: "ready"}}
	drama.Published = &online
	store := NewStore(Seed{Dramas: []domain.Drama{drama}, PublishedOwnerID: TestApplicationID})
	if store.dramas["101"].Published == nil || store.dramas["101"].Published.Episodes[0].PosterURL != "" {
		t.Fatal("分集海报不应改写发布快照")
	}
	stored, err := store.FindDrama(context.Background(), "101")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Episodes[0].PosterURL != "https://cover.example.com/episode.jpg" || stored.Published == nil ||
		stored.Published.Episodes[0].PosterURL != "https://cover.example.com/episode.jpg" {
		t.Fatalf("官方海报未合并到线上读取结果：%+v", stored)
	}
}

// TestPublishedPosterHydrationRequiresMatchingVID 验证同一分集替换视频后不会把新视频海报串到仍在线的旧版本。
func TestPublishedPosterHydrationRequiresMatchingVID(t *testing.T) {
	online := domain.Drama{
		ID: "101", PublishStatus: "published",
		Episodes: []domain.Episode{{ID: "201", DramaID: "101", VID: "old-vid", PosterURL: "https://cover.example.com/old.jpg"}},
	}
	draft := online
	draft.Episodes = []domain.Episode{{ID: "201", DramaID: "101", VID: "new-vid", PosterURL: "https://cover.example.com/new.jpg"}}
	draft.Published = &online
	store := NewStore(Seed{Dramas: []domain.Drama{draft}})
	stored, err := store.FindDrama(context.Background(), "101")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Published == nil || stored.Published.Episodes[0].PosterURL != "https://cover.example.com/old.jpg" {
		t.Fatalf("新视频海报串入旧发布版本：%+v", stored.Published)
	}
}
