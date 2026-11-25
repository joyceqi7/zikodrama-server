package consumer_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/testsupport/memory"
)

func TestOfficialPlayPreservesAuthorizationAndReturnsOfficialIDs(t *testing.T) {
	t.Parallel()
	store := memory.NewStore(monetizationSeed(applicationMonetization(true, 0, 2)))
	service := domain.NewService(store, func() time.Time {
		return time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	})
	ctx := application.WithTimeZone(context.Background(), memory.TestApplicationID, "Asia/Kuala_Lumpur")

	result, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5014")
	if err != nil || result.Status != domain.PlayStatusPlayable || result.Playback == nil || result.Preload != nil {
		t.Fatalf("official play mismatch: result=%+v err=%v", result, err)
	}
	if result.Playback.Channel != "byteplus" || result.Playback.AlbumID != "album-1" ||
		result.Playback.EpisodeID != "platform-episode-1" || result.Playback.VID != "vid-1" {
		t.Fatalf("official player identifiers mismatch: %+v", result.Playback)
	}

	locked, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5015")
	if err != nil || locked.Status != domain.PlayStatusAccessRequired || locked.Playback != nil || locked.Preload == nil {
		t.Fatalf("locked episode bypassed access policy: result=%+v err=%v", locked, err)
	}
	if locked.Preload.Channel != "byteplus" || locked.Preload.AlbumID != "album-1" ||
		locked.Preload.EpisodeID != "platform-episode-2" || locked.Preload.VID != "vid-2" {
		t.Fatalf("locked preload media mismatch: %+v", locked.Preload)
	}
}

func TestOfficialPlayRejectsIncompletePlatformMapping(t *testing.T) {
	t.Parallel()
	seed := monetizationSeed(applicationMonetization(true, 0, 2))
	seed.Dramas[0].PlatformAlbumID = ""
	store := memory.NewStore(seed)
	service := domain.NewService(store, time.Now)
	ctx := application.WithTimeZone(context.Background(), memory.TestApplicationID, "Asia/Kuala_Lumpur")

	result, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5014")
	if err != nil || result.Status != domain.PlayStatusUnavailable || result.Playback != nil || result.Preload != nil {
		t.Fatalf("incomplete mapping must remain unavailable: result=%+v err=%v", result, err)
	}
}

// TestOfficialPlayOmitsLegacyCredentials 验证播放协议只下发官方标识，不含直链、自研取证或播放会话。
func TestOfficialPlayOmitsLegacyCredentials(t *testing.T) {
	t.Parallel()
	store := memory.NewStore(monetizationSeed(applicationMonetization(true, 0, 2)))
	service := domain.NewService(store, time.Now)
	ctx := application.WithContext(context.Background(), memory.TestApplicationID)
	result, err := service.ResolveOfficialPlay(ctx, "viewer", "5008", "5014")
	if err != nil || result.Playback == nil {
		t.Fatalf("official play unavailable: %+v %v", result, err)
	}
	raw, err := json.Marshal(result.Playback)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"playAuthToken", "play_auth_token", "directUrl", "direct_url", "playbackSessionId", "forensicWatermarkCode", "playbackSessionStartedAt"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("legacy credential field leaked: %s", forbidden)
		}
	}
}
