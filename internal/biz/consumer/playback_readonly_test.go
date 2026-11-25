package consumer_test

import (
	"context"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	consumer "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/testsupport/memory"
)

// playbackReadStore 记录显式领取的权益写入入口，验证播放和进度刷新都不会触发该路径。
type playbackReadStore struct {
	*memory.Store
	reconciles int
}

// ReconcileVIPProgress 仅在用户显式领取时允许发放权益。
func (s *playbackReadStore) ReconcileVIPProgress(ctx context.Context, id string, policy monetization.MonetizationPolicy, period monetization.RewardPeriod, now time.Time) (monetization.VIPProgress, error) {
	s.reconciles++
	return s.Store.ReconcileVIPProgress(ctx, id, policy, period, now)
}

func TestPlaybackReadsEntitlementsAndOnlyManualClaimGrantsVIP(t *testing.T) {
	now := time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)
	config := applicationMonetization(true, 0, 2)
	store := &playbackReadStore{Store: memory.NewStore(monetizationSeed(config))}
	service := consumer.NewService(store, func() time.Time { return now })
	ctx := application.WithTimeZone(context.Background(), memory.TestApplicationID, "Asia/Kuala_Lumpur")
	play, err := service.ResolveOfficialPlay(ctx, "readonly-user", "5008", "5015")
	if err != nil || play.VIPOffer == nil {
		t.Fatalf("missing VIP offer: %v", err)
	}
	_, err = service.ClaimReward(ctx, "readonly-user", monetization.ClaimRewardInput{ClaimID: "readonly-vip-claim", OfferID: play.VIPOffer.ID, CompletedAdUnitID: play.VIPOffer.AdUnitID, IsEnded: true})
	if err != nil {
		t.Fatal(err)
	}
	config.VIP.RequiredAdCount = 1
	if err := store.SaveApplicationMonetization(ctx, config); err != nil {
		t.Fatal(err)
	}
	before := store.reconciles
	play, err = service.ResolveOfficialPlay(ctx, "readonly-user", "5008", "5015")
	if err != nil || play.Status == consumer.PlayStatusPlayable {
		t.Fatalf("playback granted unissued VIP: %v", err)
	}
	if store.reconciles != before {
		t.Fatal("playback entered entitlement write transaction")
	}
	vip, err := service.VIPProgress(ctx, "readonly-user")
	if err != nil || vip.Active || vip.AdCount != 1 || store.reconciles != before {
		t.Fatalf("进度刷新不应代替用户领取: %+v %v", vip, err)
	}
	experience, err := service.ReadAdExperience(ctx, "readonly-user")
	if err != nil || experience.VIPState.Active || experience.VIPState.AdCount != 1 || store.reconciles != before {
		t.Fatalf("前台权益查询不应自动领取: %+v %v", experience, err)
	}
	other, err := service.ReadAdExperience(ctx, "other-user")
	if err != nil || other.VIPState.AdCount != 0 || other.VIPState.Active {
		t.Fatalf("查询串用了其他账号的进度: %+v %v", other, err)
	}
	vip, err = service.ClaimVIP(ctx, "readonly-user")
	if err != nil || !vip.Active || store.reconciles != before+1 {
		t.Fatalf("达标后显式领取失败: %+v %v", vip, err)
	}
	before = store.reconciles
	experience, err = service.ReadAdExperience(ctx, "readonly-user")
	if err != nil || !experience.VIPState.Active || experience.VIPOffer != nil || store.reconciles != before {
		t.Fatalf("权益查询未返回已领取状态: %+v %v", experience, err)
	}
	play, err = service.ResolveOfficialPlay(ctx, "readonly-user", "5008", "5015")
	if err != nil || play.Status != consumer.PlayStatusPlayable || store.reconciles != before {
		t.Fatalf("issued VIP not used by read-only playback: %v", err)
	}
}
