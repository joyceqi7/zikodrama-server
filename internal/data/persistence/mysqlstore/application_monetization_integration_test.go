package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	consumer "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"errors"
	"testing"
	"time"
)

// assertMergedApplicationMonetization 验证合表后资料与变现写入互不覆盖，并保持初次配置和并发修订语义。
func assertMergedApplicationMonetization(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	app := application.Application{SubjectID: "1", TikTokAppID: "merged-config-app", Name: "Before", MonetizationMode: application.MonetizationModeIAA, AdUnits: application.AdUnits{RewardedVideo: []string{"reward"}, Interstitial: []string{"interstitial"}}, Status: application.StatusActive, TimeZone: "UTC", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateApplication(ctx, &app); err != nil {
		t.Fatal(err)
	}
	ctx = application.WithContext(ctx, app.ID)
	if _, err := store.GetApplicationMonetization(ctx); !errors.Is(err, consumer.ErrNotFound) {
		t.Fatal("unconfigured application should retain not-found semantics")
	}
	config := monetization.ApplicationMonetizationConfig{ApplicationID: app.ID, Status: "disabled", Strategy: "episode_unlock", FreeEpisodeCount: 2, VIP: monetization.ApplicationVIPPolicy{Title: "VIP fixture", RequiredAdCount: 3}, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveApplicationMonetization(ctx, config); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveApplicationMonetization(ctx, config); err != nil {
		t.Fatal("相同配置重复保存应按最新状态生效", err)
	}
	loadedApp, err := store.FindApplication(ctx, app.ID)
	if err != nil || loadedApp.Status != application.StatusActive {
		t.Fatal("monetization status overwrote app status")
	}
	app.Name = "After"
	if err = store.SaveApplication(ctx, app); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetApplicationMonetization(ctx)
	if err != nil || loaded.Document == nil || loaded.Document.FreeEpisodeCount != 2 {
		t.Fatal("application save overwrote monetization")
	}
	config.Status = "enabled"
	config.VIP.Title = ""
	config.FreeEpisodeCount = 0
	config.UpdatedAt = now.Add(time.Second)
	if err = store.SaveApplicationMonetization(ctx, config); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.GetApplicationMonetization(ctx)
	if err != nil || loaded.VIP.Title != "" || loaded.FreeEpisodeCount != 0 || !loaded.CreatedAt.Equal(now) {
		t.Fatal("zero-value update or creation timestamp was lost")
	}
	loadedApp, err = store.FindApplication(ctx, app.ID)
	if err != nil || loadedApp.Name != "After" {
		t.Fatal("monetization update overwrote application")
	}
}
