package operations

import (
	"net/url"
	"testing"

	"drama-server/internal/biz/campaign"
	content "drama-server/internal/biz/consumer"
)

func TestCampaignMinisPathUsesOfficialIDsAsDisplayHints(t *testing.T) {
	t.Parallel()
	c := campaign.Campaign{Code: "test-campaign", DramaID: "663", EpisodeID: "18426"}
	drama := &content.Drama{
		Title: "Published drama", CoverURL: "https://img.tkdrama.site/covers/drama.jpg", PlatformAlbumID: "album-1",
		Episodes: []content.Episode{
			{ID: "18425", Number: 1, PlatformEpisodeID: "episode-1"},
			{ID: "18426", Number: 2, PlatformEpisodeID: "episode-2"},
		},
	}
	for _, official := range []bool{true, false} {
		path, ok := campaignMinisPath(c, drama, official)
		parsed, err := url.Parse(path)
		if !ok || err != nil {
			t.Fatalf("invalid minis path %q: %v", path, err)
		}
		query := parsed.Query()
		if query.Get("coverEpisodeNumber") != "2" || query.Get("coverEpisodeCount") != "2" ||
			query.Has("dramaId") || query.Has("episodeId") || query.Has("vid") {
			t.Fatalf("display hints changed the trusted launch fields: %v", query)
		}
		if official {
			if query.Get("platformDramaId") != "album-1" || query.Get("platformEpisodeId") != "episode-2" {
				t.Fatalf("official display IDs are missing: %v", query)
			}
		} else if query.Has("platformDramaId") || query.Has("platformEpisodeId") {
			t.Fatalf("direct player link contains official IDs: %v", query)
		}
	}
	drama.CoverURL += "?token=temporary"
	if path, ok := campaignMinisPath(c, drama, true); ok || path != "/?campaignCode=test-campaign" {
		t.Fatalf("signed cover must not be added to campaign path: %q", path)
	}
}
