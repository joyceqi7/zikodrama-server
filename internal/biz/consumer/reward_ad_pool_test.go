package consumer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/testsupport/memory"
)

func rewardPoolFixture(t *testing.T) (context.Context, *domain.Service, *memory.Store, monetization.MonetizationPolicy) {
	t.Helper()
	doc, _ := monetization.StrategyFromConfig(applicationMonetization(true, 0, 3))
	doc.Rules = append(doc.Rules, monetization.UnlockRule{Code: "entry_ad", Type: "entry_rewarded_ad", Enabled: true, Config: json.RawMessage(`{"requiredAdCount":10}`)})
	units := application.AdUnits{RewardedVideo: []string{"9007199254740993", "ad-b", "ad-c"}, Interstitial: []string{"interstitial"}}
	binding := monetization.StrategyBinding{ApplicationID: memory.TestApplicationID}.WithApplicationAdUnits(doc, units)
	config := monetization.ConfigFromStrategy(monetization.UnlockStrategy{ID: "pool-strategy", Revision: 1, Document: doc}, binding)
	seed := monetizationSeed(config)
	seed.Applications[0].AdUnits = units
	store := memory.NewStore(seed)
	service := domain.NewService(store, func() time.Time { return time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC) })
	return application.WithTimeZone(context.Background(), memory.TestApplicationID, "UTC"), service, store, config.Policy()
}

// TestRewardPoolKeepsOldMiniClaimsAcrossAllocationChanges 验证三类旧合同可领取仍有效的缓存广告，且幂等不受选位改变影响。
func TestRewardPoolKeepsOldMiniClaimsAcrossAllocationChanges(t *testing.T) {
	for _, kind := range []string{monetization.AccessOfferRewardedEpisode, monetization.AccessOfferRewardedVIP, monetization.AccessOfferRewardedEntry} {
		t.Run(kind, func(t *testing.T) {
			ctx, service, store, policy := rewardPoolFixture(t)
			first, err := service.RewardOffer(ctx, "3", policy, kind, nil)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(first)
			if err != nil {
				t.Fatal(err)
			}
			var contract map[string]any
			if err := json.Unmarshal(raw, &contract); err != nil {
				t.Fatal(err)
			}
			if contract["adUnitId"] != "9007199254740993" || contract["adUnitIds"] != nil {
				t.Fatalf("旧 Mini 必须继续收到单个字符串广告位: %s", raw)
			}
			// 从三个候选调整到两个，UID 3 从首项变为第二项，但首项仍可用于缓存 Offer 领奖。
			config, err := store.GetApplicationMonetization(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for code, binding := range config.Bindings {
				binding.AdUnitIDs = []string{"9007199254740993", "ad-b"}
				config.Bindings[code] = binding
			}
			if err := store.SaveApplicationMonetization(ctx, config); err != nil {
				t.Fatal(err)
			}
			current := config.Policy()
			second, err := service.RewardOffer(ctx, "3", current, kind, nil)
			if err != nil || second.AdUnitID != "ad-b" {
				t.Fatalf("候选数量改变后没有按 UID 重新取模: %+v %v", second, err)
			}
			// 模拟旧 Mini 提交配置变更前缓存的单值 Offer。
			input := monetization.ClaimRewardInput{ClaimID: "old-mini-claim", OfferID: first.ID, CompletedAdUnitID: first.AdUnitID, IsEnded: true}
			if kind == monetization.AccessOfferRewardedEpisode {
				input.DramaID, input.EpisodeID = "5008", "5015"
			}
			if kind == monetization.AccessOfferRewardedEntry {
				input.DramaID = "5008"
			}
			claim, err := service.ClaimReward(ctx, "3", input)
			if err != nil || claim.Status != monetization.ClaimStatusGranted {
				t.Fatalf("旧合同领奖失败: %+v %v", claim, err)
			}
			repeat, err := service.ClaimReward(ctx, "3", input)
			if err != nil || repeat.Status != monetization.ClaimStatusAlreadyGranted {
				t.Fatalf("领奖重试未保持幂等: %+v %v", repeat, err)
			}
			grant, found, err := store.FindRewardGrant(ctx, "3", input.ClaimID)
			if err != nil || !found || grant.AdUnitID != first.AdUnitID {
				t.Fatalf("流水必须保留实际完播广告位: %+v %v", grant, err)
			}
			third, err := service.RewardOffer(ctx, "3", current, kind, nil)
			if err != nil || third.AdUnitID != "ad-b" {
				t.Fatalf("同一 UID 的分配不应随领奖或重试改变: %+v %v", third, err)
			}
			if policy.Bindings[policy.Rules[0].Code].AdUnitID != first.AdUnitID {
				t.Fatal("下发不得污染策略快照")
			}
		})
	}
}

// TestRewardPoolRejectsWrongTypeUnknownAndRemovedUnits 验证当前规则候选集合约束。
func TestRewardPoolRejectsWrongTypeUnknownAndRemovedUnits(t *testing.T) {
	ctx, service, store, policy := rewardPoolFixture(t)
	first, err := service.RewardOffer(ctx, "3", policy, monetization.AccessOfferRewardedEntry, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"interstitial", "other-app-ad", "missing-ad"} {
		_, err := service.ClaimReward(ctx, "3", monetization.ClaimRewardInput{ClaimID: "rejected-" + code, OfferID: first.ID, CompletedAdUnitID: code, DramaID: "5008", IsEnded: true})
		if !errors.Is(err, monetization.ErrInvalidRewardClaim) {
			t.Fatalf("非法候选编码必须拒绝: %s %v", code, err)
		}
	}
	config, err := store.GetApplicationMonetization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for code, binding := range config.Bindings {
		binding.AdUnitIDs, binding.AdUnitID = []string{"ad-b"}, "ad-b"
		config.Bindings[code] = binding
	}
	if err := store.SaveApplicationMonetization(ctx, config); err != nil {
		t.Fatal(err)
	}
	_, err = service.ClaimReward(ctx, "3", monetization.ClaimRewardInput{ClaimID: "removed", OfferID: first.ID, CompletedAdUnitID: first.AdUnitID, DramaID: "5008", IsEnded: true})
	if !errors.Is(err, monetization.ErrInvalidRewardClaim) {
		t.Fatalf("规则移除广告位后不应新增奖励: %v", err)
	}
}

// TestRewardPoolNextOfferUsesUIDAndKeepsCompletedUnit 验证下一入口按 UID 分配，不改写旧缓存广告的实际完播流水。
func TestRewardPoolNextOfferUsesUIDAndKeepsCompletedUnit(t *testing.T) {
	ctx, service, store, input := consistencyFixture(t, 1, 0)
	config, err := store.GetApplicationMonetization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	binding := config.Bindings["episode"]
	binding.AdUnitIDs = []string{"episode-ad", "vip-ad"}
	config.Bindings["episode"] = binding
	if err := store.Store.(*memory.Store).SaveApplicationMonetization(ctx, config); err != nil {
		t.Fatal(err)
	}
	first, err := service.ClaimReward(ctx, "3", input)
	if err != nil || first.NextAd == nil || first.NextAd.Offer.AdUnitID != "vip-ad" {
		t.Fatalf("结算下发的下一入口必须按 UID 分配: %+v %v", first, err)
	}
	grant, found, err := store.FindRewardGrant(ctx, "3", input.ClaimID)
	if err != nil || !found || grant.AdUnitID != "episode-ad" {
		t.Fatalf("下一入口选位不能改变实际完播事实: %+v %v", grant, err)
	}
	// 重试恢复路径也必须重新按当前 UID 分配下一入口。
	repeated, err := service.ClaimReward(ctx, "3", input)
	if err != nil || repeated.NextAd == nil || repeated.NextAd.Offer.AdUnitID != "vip-ad" {
		t.Fatalf("重试恢复的下一入口分配不一致: %+v %v", repeated, err)
	}
	next := first.NextAd
	second, err := service.ClaimReward(ctx, "3", monetization.ClaimRewardInput{ClaimID: "next-pool-claim", OfferID: next.Offer.ID, CompletedAdUnitID: next.Offer.AdUnitID, DramaID: next.DramaID, EpisodeID: next.EpisodeID, IsEnded: true})
	if err != nil || second.NextAd == nil || second.NextAd.Offer.AdUnitID != "vip-ad" {
		t.Fatalf("连续广告应稳定选位且可领奖: %+v %v", second, err)
	}
}

// TestRewardPoolNextOfferFailureKeepsCommittedReward 验证下一入口选位失败只停止提示，不撤销已经提交的奖励。
func TestRewardPoolNextOfferFailureKeepsCommittedReward(t *testing.T) {
	ctx, service, store, input := consistencyFixture(t, 1, 0)
	config, err := store.GetApplicationMonetization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	binding := config.Bindings["episode"]
	binding.AdUnitIDs = []string{"episode-ad", "vip-ad"}
	config.Bindings["episode"] = binding
	if err := store.Store.(*memory.Store).SaveApplicationMonetization(ctx, config); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	result, err := service.ClaimReward(ctx, "invalid-uid", input)
	if err != nil || !result.Unlocked || result.NextAd != nil || result.Playback == nil {
		t.Fatalf("后续选位失败不得破坏本次结算: %+v %v", result, err)
	}
	grant, found, err := store.FindRewardGrant(ctx, "invalid-uid", input.ClaimID)
	if err != nil || !found || grant.AdUnitID != input.CompletedAdUnitID {
		t.Fatalf("已经提交的完播事实必须保留: %+v %v", grant, err)
	}
	if !strings.Contains(logs.String(), "select next reward ad unit by uid failed") || !strings.Contains(logs.String(), "requires a decimal visitor ID") || strings.Contains(logs.String(), "invalid-uid") {
		t.Fatalf("后续入口失败缺少操作及实际错误或泄露 UID: %s", logs.String())
	}
}

// TestRewardPoolAcrossInstances 验证 UID 分配跨实例及重启保持一致，奖励仍使用共享仓储与幂等事实。
func TestRewardPoolAcrossInstances(t *testing.T) {
	ctx, firstService, store, policy := rewardPoolFixture(t)
	secondService := domain.NewService(store, func() time.Time { return time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC) })
	offer, err := firstService.RewardOffer(ctx, "3", policy, monetization.AccessOfferRewardedEntry, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if next, err := secondService.RewardOffer(ctx, "3", policy, monetization.AccessOfferRewardedEntry, nil); err != nil || next.AdUnitID != offer.AdUnitID {
			t.Fatalf("跨实例选位不一致: %+v %v", next, err)
		}
	}
	input := monetization.ClaimRewardInput{ClaimID: "cross-instance", OfferID: offer.ID, CompletedAdUnitID: offer.AdUnitID, DramaID: "5008", IsEnded: true}
	claim, err := secondService.ClaimReward(ctx, "3", input)
	if err != nil || claim.Status != monetization.ClaimStatusGranted {
		t.Fatalf("另一实例必须能领取原入口: %+v %v", claim, err)
	}
	// 服务重启后无须恢复任何选位状态，也不能重复累计或丢失奖励。
	restarted := domain.NewService(store, nil)
	if next, err := restarted.RewardOffer(ctx, "3", policy, monetization.AccessOfferRewardedEntry, nil); err != nil || next.AdUnitID != offer.AdUnitID {
		t.Fatalf("重启后的选位不一致: %+v %v", next, err)
	}
	repeat, err := restarted.ClaimReward(ctx, "3", input)
	if err != nil || repeat.Status != monetization.ClaimStatusAlreadyGranted {
		t.Fatalf("重启不得影响幂等事实: %+v %v", repeat, err)
	}
}

// TestRewardPoolUsesSameUIDAcrossRules 验证不同规则均使用相同的完整 UID 取模，不受请求顺序影响。
func TestRewardPoolUsesSameUIDAcrossRules(t *testing.T) {
	ctx, service, _, policy := rewardPoolFixture(t)
	for _, test := range []struct{ uid, want string }{
		{"3", "9007199254740993"}, {"4", "ad-b"}, {"5", "ad-c"},
		{"9007199254740993", "9007199254740993"}, {"4", "ad-b"},
	} {
		for _, kind := range []string{monetization.AccessOfferRewardedEpisode, monetization.AccessOfferRewardedVIP, monetization.AccessOfferRewardedEntry} {
			offer, err := service.RewardOffer(ctx, test.uid, policy, kind, nil)
			if err != nil || offer.AdUnitID != test.want {
				t.Fatalf("规则未按完整 UID 稳定分配: %s %+v %v", kind, offer, err)
			}
		}
	}
}

type rewardPoolFailureStore struct{ domain.Store }

// HasRewardedAdUnit 模拟资源查询失败，不访问真实平台或数据库。
func (s rewardPoolFailureStore) HasRewardedAdUnit(context.Context, string) (bool, error) {
	return false, errors.New("reward ad lookup unavailable")
}

// TestRewardPoolDependencyFailureLogsDetails 验证依赖失败诊断和成功路径的日志级别。
func TestRewardPoolDependencyFailureLogsDetails(t *testing.T) {
	ctx, _, store, policy := rewardPoolFixture(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	service := domain.NewService(rewardPoolFailureStore{store}, nil)
	if offer, err := service.RewardOffer(ctx, "3", policy, monetization.AccessOfferRewardedEntry, nil); err == nil || offer != nil {
		t.Fatal("广告位校验失败不能下发入口")
	}
	if !strings.Contains(logs.String(), "check entry rewarded ad unit failed") || !strings.Contains(logs.String(), "reward ad lookup unavailable") || strings.Contains(logs.String(), "9007199254740993") {
		t.Fatalf("失败日志应包含操作和详情且不输出业务对象: %s", logs.String())
	}
	logs.Reset()
	service = domain.NewService(store, nil)
	if offer, err := service.RewardOffer(ctx, "invalid-uid", policy, monetization.AccessOfferRewardedEntry, nil); err == nil || offer != nil {
		t.Fatal("异常 UID 不应下发多候选入口")
	}
	if !strings.Contains(logs.String(), "select reward ad unit by uid failed") || !strings.Contains(logs.String(), "requires a decimal visitor ID") || strings.Contains(logs.String(), "invalid-uid") {
		t.Fatalf("选位失败必须记录操作与实际错误，不记录 UID: %s", logs.String())
	}
	logs.Reset()
	if _, err := service.RewardOffer(ctx, "3", policy, monetization.AccessOfferRewardedEntry, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Fatal("成功下发不得误报故障")
	}
}
