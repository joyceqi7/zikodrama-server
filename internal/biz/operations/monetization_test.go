package operations_test

import (
	"context"
	"errors"
	"testing"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

func TestUpdateApplicationMonetizationCreatesRevision(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{Applications: []application.Application{{
		MonetizationMode: application.MonetizationModeIAA, TikTokAppID: "5004", Name: "Indonesia", Status: application.StatusActive,

		AdUnits: application.AdUnits{RewardedVideo: []string{"episode", "vip"}},
	}}})
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	ctx := application.WithContext(context.Background(), "5004")
	input := operations.ApplicationMonetizationInput{
		FreeEpisodeCount:        3,
		EpisodeUnlockAdUnitCode: "episode",
		VIPStrategy: operations.ApplicationVIPStrategyInput{
			Enabled: true, ForceWatchCount: 2, RequiredAdCount: 3, DelaySeconds: 2, AdUnitCode: "vip",
			Title: "VIP hari ini", Content: "Tonton tiga iklan.", FailureMessage: "Coba lagi.",
		},
	}

	created, err := service.UpdateApplicationMonetization(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.ApplicationID != "5004" ||
		created.FreeEpisodeCount != 3 || created.EpisodeUnlockAdUnitCode != "episode" ||
		created.VIPStrategy.ForceWatchCount != 2 || created.VIPStrategy.RequiredAdCount != 3 || created.VIPStrategy.DelaySeconds != 2 {
		t.Fatalf("unexpected first configuration: %+v", created)
	}

	input.VIPStrategy.RequiredAdCount = 5
	updated, err := service.UpdateApplicationMonetization(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.VIPStrategy.RequiredAdCount != 5 {
		t.Fatalf("configuration update was not saved: %+v", updated)
	}

	loaded, err := service.ApplicationMonetization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.VIPStrategy.RequiredAdCount != 5 {
		t.Fatalf("current application configuration was not loaded: %+v", loaded)
	}
}

func TestApplicationMonetizationCannotCrossApplicationBoundary(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{Applications: []application.Application{
		{MonetizationMode: application.MonetizationModeIAA, TikTokAppID: "5004", Name: "Indonesia", Status: application.StatusActive, AdUnits: application.AdUnits{RewardedVideo: []string{"rewarded"}}},
		{MonetizationMode: application.MonetizationModeIAA, TikTokAppID: "5003", Name: "Brazil", Status: application.StatusActive},
	}})
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	idContext := application.WithContext(context.Background(), "5004")
	_, err := service.UpdateApplicationMonetization(idContext, operations.ApplicationMonetizationInput{
		FreeEpisodeCount: 3, EpisodeUnlockAdUnitCode: "rewarded",
	})
	if err != nil {
		t.Fatal(err)
	}

	brContext := application.WithContext(context.Background(), "5003")
	if _, err := service.ApplicationMonetization(brContext); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("another application's configuration must not be visible: %v", err)
	}
}

func TestApplicationMonetizationRequiresRegisteredRewardedVideoCode(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{Applications: []application.Application{{
		MonetizationMode: application.MonetizationModeIAA, TikTokAppID: "5004", Name: "Indonesia", Status: application.StatusActive,

		AdUnits: application.AdUnits{Interstitial: []string{"interstitial"}},
	}}})
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	ctx := application.WithContext(context.Background(), "5004")

	_, err := service.UpdateApplicationMonetization(ctx, operations.ApplicationMonetizationInput{
		FreeEpisodeCount: 3, EpisodeUnlockAdUnitCode: "interstitial",
	})
	if !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("non-rewarded advertising code must be rejected: %v", err)
	}

	_, err = service.UpdateApplicationMonetization(ctx, operations.ApplicationMonetizationInput{
		FreeEpisodeCount: 3, EpisodeUnlockAdUnitCode: "missing",
	})
	if !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("missing rewarded advertising code must be rejected: %v", err)
	}
}

// TestMonetizationSavePreservesRewardLimits 确认旧客户端未提交阈值时不会意外清除已有奖励规则。
func TestMonetizationSavePreservesRewardLimits(t *testing.T) {
	store := memory.NewStore(memory.Seed{Applications: []application.Application{{
		MonetizationMode: application.MonetizationModeIAA, TikTokAppID: "app", Status: application.StatusActive,
		AdUnits: application.AdUnits{RewardedVideo: []string{"reward"}},
	}}})
	ctx := application.WithContext(context.Background(), "app")
	if err := store.SaveApplicationMonetization(ctx, monetization.ApplicationMonetizationConfig{
		ApplicationID: "app", EpisodeReward: monetization.EpisodeRewardPolicy{UnlockEpisodeCount: 3, DailyRewardLimit: 2},
	}); err != nil {
		t.Fatal(err)
	}
	svc := operations.NewService(memory.OperationsRepositories(store), nil)
	if _, err := svc.UpdateApplicationMonetization(ctx, operations.ApplicationMonetizationInput{FreeEpisodeCount: 1, EpisodeUnlockAdUnitCode: "reward"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetApplicationMonetization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.EpisodeReward.UnlockEpisodeCount != 3 || got.EpisodeReward.DailyRewardLimit != 2 {
		t.Fatalf("保存其它字段意外改变奖励阈值: %+v", got.EpisodeReward)
	}
}
