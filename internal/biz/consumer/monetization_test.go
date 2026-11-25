package consumer_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/campaign"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/testsupport/memory"
)

func monetizationDramas() []domain.Drama {
	return []domain.Drama{{
		ID: "5008", Title: "Night Shift", PublishStatus: "published", PlatformAlbumID: "album-1",
		Episodes: []domain.Episode{
			{ID: "5014", DramaID: "5008", Number: 1, PlatformEpisodeID: "platform-episode-1", VID: "vid-1", MediaStatus: "ready"},
			{ID: "5015", DramaID: "5008", Number: 2, PlatformEpisodeID: "platform-episode-2", VID: "vid-2", MediaStatus: "ready"},
		},
	}}
}

func applicationMonetization(vipEnabled bool, forceWatchCount, requiredAdCount int) monetization.ApplicationMonetizationConfig {
	return monetization.ApplicationMonetizationConfig{
		ApplicationID:           memory.TestApplicationID,
		Status:                  monetization.MonetizationEnabled,
		Strategy:                monetization.MonetizationStrategyEpisodeUnlock,
		EpisodeRewardAdUnitCode: "episode-ad",
		VIPAdUnitCode:           "vip-ad",
		FreeEpisodeCount:        1,
		EpisodeReward:           monetization.EpisodeRewardPolicy{UnlockEpisodeCount: 1},
		VIP: monetization.ApplicationVIPPolicy{
			Enabled: vipEnabled, StrongWatchAdCount: forceWatchCount, RequiredAdCount: requiredAdCount,
			Title: "Watch for today", Content: "Complete ads for today's VIP.",
		},
	}
}

func monetizationSeed(config monetization.ApplicationMonetizationConfig) memory.Seed {
	return memory.Seed{
		PublishedOwnerID:        memory.TestApplicationID,
		Dramas:                  monetizationDramas(),
		ApplicationMonetization: []monetization.ApplicationMonetizationConfig{config},
		Applications: []application.Application{{
			TikTokAppID: memory.TestApplicationID, Name: "Drama Minis", MonetizationMode: application.MonetizationModeIAA,
			Status:  application.StatusActive,
			AdUnits: application.AdUnits{RewardedVideo: []string{"episode-ad", "vip-ad"}},
		}},
	}
}

func TestApplicationEpisodeUnlockAndVIPUseIndependentOffers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	store := memory.NewStore(monetizationSeed(applicationMonetization(true, 0, 2)))
	service := domain.NewService(store, func() time.Time { return now })
	ctx := application.WithTimeZone(context.Background(), memory.TestApplicationID, "Asia/Kuala_Lumpur")

	free, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5014")
	if err != nil || free.Status != domain.PlayStatusPlayable {
		t.Fatalf("免费集必须直接播放: result=%+v err=%v", free, err)
	}
	if free.Playback.Channel != "byteplus" || free.Playback.AlbumID != "album-1" ||
		free.Playback.EpisodeID != "platform-episode-1" || free.Playback.VID != "vid-1" {
		t.Fatalf("可播放响应缺少官方 Player 标识：%+v", free.Playback)
	}
	locked, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5015")
	if err != nil || locked.Offer == nil || locked.Offer.Kind != monetization.AccessOfferRewardedEpisode ||
		locked.VIPOffer == nil || locked.VIPOffer.Kind != monetization.AccessOfferRewardedVIP {
		t.Fatalf("锁定集必须同时提供逐集与 VIP Offer: result=%+v err=%v", locked, err)
	}

	if locked.Offer.AdUnitID != "episode-ad" || locked.VIPOffer.AdUnitID != "vip-ad" {
		t.Fatalf("Offer 没有下发对应广告位：episode=%+v vip=%+v", locked.Offer, locked.VIPOffer)
	}
	if _, err := service.ClaimReward(ctx, "visitor-1", monetization.ClaimRewardInput{
		ClaimID: "claim_episode_wrong_001", OfferID: locked.Offer.ID,
		DramaID: "5008", EpisodeID: "5015", CompletedAdUnitID: "vip-ad", IsEnded: true,
	}); !errors.Is(err, monetization.ErrInvalidRewardClaim) {
		t.Fatalf("不能用其他广告位领取逐集权益: %v", err)
	}
	// 普通剧集广告只发剧集权益，同一访客的 VIP 累计必须保持为零。
	episodeClaim, err := service.ClaimReward(ctx, "visitor-1", monetization.ClaimRewardInput{
		ClaimID: "claim_episode_complete_001", OfferID: locked.Offer.ID,
		DramaID: "5008", EpisodeID: "5015", CompletedAdUnitID: locked.Offer.AdUnitID, IsEnded: true,
	})
	if err != nil || episodeClaim == nil || episodeClaim.RewardType != monetization.RewardTypeEpisodeUnlock || !episodeClaim.Unlocked || episodeClaim.VIPProgress != nil {
		t.Fatalf("普通解锁只能发剧集权益: claim=%+v err=%v", episodeClaim, err)
	}
	vipProgress, err := service.VIPProgress(ctx, "visitor-1")
	if err != nil || vipProgress.AdCount != 0 || vipProgress.Active {
		t.Fatalf("普通解锁广告不能计入 VIP 次数: progress=%+v err=%v", vipProgress, err)
	}

	vipClaim := monetization.ClaimRewardInput{
		ClaimID: "claim_vip_complete_0001", OfferID: locked.VIPOffer.ID,
		CompletedAdUnitID: locked.VIPOffer.AdUnitID, IsEnded: true,
	}
	claim, err := service.ClaimReward(ctx, "visitor-1", vipClaim)
	if err != nil || claim.VIPProgress == nil || claim.VIPProgress.AdCount != 1 || claim.VIPProgress.Active {
		t.Fatalf("第一次完整广告只能推进 VIP 进度: claim=%+v err=%v", claim, err)
	}
	if _, err := service.ClaimVIP(ctx, "visitor-1"); !errors.Is(err, monetization.ErrInvalidRewardClaim) {
		t.Fatalf("未达标不能提前领取 VIP: %v", err)
	}
	repeated, err := service.ClaimReward(ctx, "visitor-1", vipClaim)
	if err != nil || repeated.Status != monetization.ClaimStatusAlreadyGranted || repeated.VIPProgress == nil || repeated.VIPProgress.AdCount != 1 {
		t.Fatalf("重复 ClaimID 没有幂等恢复原结算：claim=%+v err=%v", repeated, err)
	}
	vipClaim.ClaimID = "claim_vip_complete_0002"
	claim, err = service.ClaimReward(ctx, "visitor-1", vipClaim)
	if err != nil || claim.VIPProgress == nil || claim.VIPProgress.AdCount != 2 || claim.VIPProgress.Active {
		t.Fatalf("达标广告只累计次数，不自动激活: claim=%+v err=%v", claim, err)
	}
	locked, err = service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5015")
	if err != nil || locked.VIPProgress == nil || locked.VIPProgress.Active {
		t.Fatalf("领取前播放不能借用未激活进度: result=%+v err=%v", locked, err)
	}
	claimed, err := service.ClaimVIP(ctx, "visitor-1")
	if err != nil || !claimed.Active {
		t.Fatalf("达标后手动领取失败: state=%+v err=%v", claimed, err)
	}
	claimedAgain, err := service.ClaimVIP(ctx, "visitor-1")
	if err != nil || !claimedAgain.Active || claimedAgain.AdCount != 2 {
		t.Fatalf("重复领取必须幂等: state=%+v err=%v", claimedAgain, err)
	}
}

func TestCampaignForcedPopupDoesNotBecomePlaybackEntitlementGate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	store := memory.NewStore(monetizationSeed(applicationMonetization(true, 2, 5)))
	service := domain.NewService(store, func() time.Time { return now })
	organic := application.WithTimeZone(context.Background(), memory.TestApplicationID, "Asia/Kuala_Lumpur")
	campaignContext := campaign.WithAttribution(organic, "5006")

	organicResult, err := service.ResolveOfficialPlay(organic, "organic", "5008", "5014")
	if err != nil || organicResult.Status != domain.PlayStatusPlayable {
		t.Fatalf("自然访问免费集必须可播放: result=%+v err=%v", organicResult, err)
	}
	campaignFree, err := service.ResolveOfficialPlay(campaignContext, "campaign", "5008", "5014")
	if err != nil || campaignFree.Status != domain.PlayStatusPlayable {
		t.Fatalf("投放不能阻断免费集播放: result=%+v err=%v", campaignFree, err)
	}
	campaignLocked, err := service.ResolveOfficialPlay(campaignContext, "campaign", "5008", "5015")
	if err != nil || campaignLocked.Offer == nil ||
		campaignLocked.Offer.Kind != monetization.AccessOfferRewardedEpisode {
		t.Fatalf("投放不能替代逐集权益判断: result=%+v err=%v", campaignLocked, err)
	}
}

func TestVIPRewardUsesLatestPolicy(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 10, 15, 59, 0, 0, time.UTC)
	config := applicationMonetization(true, 0, 1)
	store := memory.NewStore(monetizationSeed(config))
	service := domain.NewService(store, func() time.Time { return now })
	ctx := application.WithTimeZone(context.Background(), memory.TestApplicationID, "Asia/Kuala_Lumpur")

	locked, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5015")
	if err != nil || locked.VIPOffer == nil {
		t.Fatalf("缺少 VIP Offer: result=%+v err=%v", locked, err)
	}
	config.VIP.RequiredAdCount = 5
	if err := store.SaveApplicationMonetization(ctx, config); err != nil {
		t.Fatal(err)
	}
	result, err := service.ClaimReward(ctx, "visitor-1", monetization.ClaimRewardInput{
		ClaimID: "claim_stale_offer_0001", OfferID: locked.VIPOffer.ID,
		CompletedAdUnitID: locked.VIPOffer.AdUnitID, IsEnded: true,
	})
	if err != nil || result == nil || result.VIPProgress == nil || result.VIPProgress.RequiredCount != 5 || result.VIPProgress.Active {
		t.Fatalf("配置更新后未按最新策略发放：result=%+v err=%v", result, err)
	}
}

func TestVIPRewardExpiresAtApplicationMidnight(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 10, 15, 59, 0, 0, time.UTC)
	store := memory.NewStore(monetizationSeed(applicationMonetization(true, 0, 1)))
	service := domain.NewService(store, func() time.Time { return now })
	ctx := application.WithTimeZone(context.Background(), memory.TestApplicationID, "Asia/Kuala_Lumpur")
	locked, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5015")
	if err != nil || locked.VIPOffer == nil {
		t.Fatalf("缺少 VIP Offer: result=%+v err=%v", locked, err)
	}
	claim, err := service.ClaimReward(ctx, "visitor-1", monetization.ClaimRewardInput{
		ClaimID: "claim_midnight_vip_001", OfferID: locked.VIPOffer.ID,
		CompletedAdUnitID: locked.VIPOffer.AdUnitID, IsEnded: true,
	})
	wantExpiry := time.Date(2026, 7, 10, 16, 0, 0, 0, time.UTC)
	if err != nil || claim.VIPProgress == nil || claim.VIPProgress.Active {
		t.Fatalf("达标广告不能自动激活 VIP: claim=%+v err=%v", claim, err)
	}
	claimed, err := service.ClaimVIP(ctx, "visitor-1")
	if err != nil || !claimed.Active || !claimed.ValidUntil.Equal(wantExpiry) {
		t.Fatalf("手动领取的 VIP 必须在应用午夜失效: state=%+v err=%v", claimed, err)
	}
}
