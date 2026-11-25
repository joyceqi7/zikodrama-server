package consumer_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/testsupport/memory"
)

func TestEntryAdProgressIsDailyAndDoesNotUnlockEpisodes(t *testing.T) {
	now := time.Date(2026, 10, 8, 15, 58, 0, 0, time.UTC)
	config := applicationMonetization(false, 0, 0)
	config.StrategyID = "strategy-1"
	config.Document = &monetization.StrategyDocument{SchemaVersion: 1, Enabled: true, FreeEpisodeCount: 1, Rules: []monetization.UnlockRule{
		{Code: "entry_ad", Type: "entry_rewarded_ad", Enabled: true, Config: json.RawMessage(`{"requiredAdCount":2}`), Grants: []monetization.GrantTemplate{}},
	}}
	config.Bindings = map[string]monetization.RuleBinding{"entry_ad": {AdUnitID: "episode-ad"}}
	store := memory.NewStore(monetizationSeed(config))
	service := domain.NewService(store, func() time.Time { return now })
	ctx := application.WithTimeZone(context.Background(), memory.TestApplicationID, "Asia/Kuala_Lumpur")

	status, err := service.EntryAdStatus(ctx, "visitor-1", "5008")
	if err != nil || !status.Enabled || status.CompletedCount != 0 || status.RequiredCount != 2 || status.Offer == nil || status.Offer.Kind != monetization.AccessOfferRewardedEntry {
		t.Fatalf("入剧广告入口错误: %+v %v", status, err)
	}
	offer := *status.Offer
	// 客户端拦截不改变服务端原有播放解析结果。
	play, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5014")
	if err != nil || play.Status != domain.PlayStatusPlayable {
		t.Fatalf("服务端播放解析不应被入剧规则改写: %+v %v", play, err)
	}
	for index := 1; index <= 2; index++ {
		input := monetization.ClaimRewardInput{ClaimID: "entry-claim-" + string(rune('0'+index)), OfferID: offer.ID, CompletedAdUnitID: offer.AdUnitID, DramaID: "5008", IsEnded: true}
		claim, claimErr := service.ClaimReward(ctx, "visitor-1", input)
		if claimErr != nil || claim.RewardType != monetization.RewardTypeEntryProgress || claim.Unlocked || claim.Playback != nil {
			t.Fatalf("入剧广告不得发放剧集或直接播放: %+v %v", claim, claimErr)
		}
		status, err = service.EntryAdStatus(ctx, "visitor-1", "5008")
		if err != nil || status.CompletedCount != index {
			t.Fatalf("结算后进度错误: %+v %v", status, err)
		}
		if index == 1 {
			repeated, repeatErr := service.ClaimReward(ctx, "visitor-1", input)
			if repeatErr != nil || repeated.Status != monetization.ClaimStatusAlreadyGranted {
				t.Fatalf("同一 claimId 应只结算一次: %+v %v", repeated, repeatErr)
			}
		}
	}
	if status.Offer == nil {
		t.Fatal("事实入口应由 Mini 自行决定是否使用")
	}
	extra, err := service.ClaimReward(ctx, "visitor-1", monetization.ClaimRewardInput{ClaimID: "entry-claim-3", OfferID: offer.ID, CompletedAdUnitID: offer.AdUnitID, DramaID: "5008", IsEnded: true})
	if err != nil || extra.Status != monetization.ClaimStatusGranted {
		t.Fatalf("达标后的有效完播仍须落账: %+v %v", extra, err)
	}
	status, err = service.EntryAdStatus(ctx, "visitor-1", "5008")
	if err != nil || status.CompletedCount != 3 {
		t.Fatalf("达标后应保留完整观看事实: %+v %v", status, err)
	}
	now = now.Add(3 * time.Minute)
	status, err = service.EntryAdStatus(ctx, "visitor-1", "5008")
	if err != nil || status.CompletedCount != 0 || status.Offer == nil {
		t.Fatalf("应用当地次日必须清零: %+v %v", status, err)
	}
}
