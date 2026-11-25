package consumer

import "testing"

// TestSummaryFirstEpisodePoster 验证首集首帧图与首集标识始终来自同一剧集。
func TestSummaryFirstEpisodePoster(t *testing.T) {
	d := Drama{Episodes: []Episode{{ID: "11", PosterURL: "/first.jpg"}, {ID: "12", PosterURL: "/second.jpg"}}}
	if got := d.Summary(); got.FirstEpisodeID != "11" || got.FirstEpisodePosterURL != "/first.jpg" {
		t.Fatalf("unexpected summary: %+v", got)
	}
	d.Episodes = nil
	if got := d.Summary(); got.FirstEpisodeID != "" || got.FirstEpisodePosterURL != "" {
		t.Fatal("empty drama has first episode")
	}
}
