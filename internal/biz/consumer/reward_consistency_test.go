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

type quotaFailureStore struct{ domain.Store }

func (s quotaFailureStore) CountRewardGrants(context.Context, string, string, time.Time, ...string) (int, error) {
	return 0, errors.New("quota storage unavailable")
}

func TestRewardOfferQuotaFailureIsLoggedAndNotOffered(t *testing.T) {
	ctx, _, store, _ := consistencyFixture(t, 1, 1)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	service := domain.NewService(quotaFailureStore{Store: store}, time.Now)
	result, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5014")
	if err == nil || result != nil {
		t.Fatalf("额度未知不能提供广告入口: %+v %v", result, err)
	}
	if !strings.Contains(logs.String(), "read episode offer daily limit failed") || !strings.Contains(logs.String(), "quota storage unavailable") {
		t.Fatalf("缺少操作与错误详情: %s", logs.String())
	}
}

// interleavedRewardStore 在首个请求完成准备后插入另一次结算，稳定复现事务前快照过时。
type interleavedRewardStore struct {
	domain.Store
	before func()
}

func (s *interleavedRewardStore) GrantReward(ctx context.Context, grant monetization.RewardGrant, now time.Time) (monetization.ClaimResult, error) {
	if s.before != nil {
		before := s.before
		s.before = nil
		before()
	}
	return s.Store.GrantReward(ctx, grant, now)
}

func consistencyFixture(t *testing.T, count, limit int, configure ...func(*monetization.ApplicationMonetizationConfig)) (context.Context, *domain.Service, *interleavedRewardStore, monetization.ClaimRewardInput) {
	t.Helper()
	config := applicationMonetization(false, 0, count)
	ruleConfig, _ := json.Marshal(map[string]any{"requiredAdCount": 1, "dailyRewardLimit": limit, "autoUnlock": map[string]any{"enabled": true, "countdownSeconds": 5}})
	grantConfig, _ := json.Marshal(map[string]any{"selection": "next_locked_episodes", "count": count, "validity": "unlimited"})
	config.Document = &monetization.StrategyDocument{SchemaVersion: 1, Enabled: true, Rules: []monetization.UnlockRule{{Code: "episode", Type: "rewarded_ad", Enabled: true, Config: ruleConfig, Grants: []monetization.GrantTemplate{{Type: "episode_access", Config: grantConfig}}}}}
	config.Bindings = map[string]monetization.RuleBinding{"episode": {AdUnitID: "episode-ad"}}
	for _, change := range configure {
		change(&config)
	}
	seed := monetizationSeed(config)
	seed.Dramas[0].Episodes = append(seed.Dramas[0].Episodes,
		domain.Episode{ID: "5016", DramaID: "5008", Number: 3, PlatformEpisodeID: "p3", VID: "v3", MediaStatus: "ready"},
		domain.Episode{ID: "5017", DramaID: "5008", Number: 4, PlatformEpisodeID: "p4", VID: "v4", MediaStatus: "ready"})
	store := &interleavedRewardStore{Store: memory.NewStore(seed)}
	service := domain.NewService(store, func() time.Time { return time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC) })
	ctx := application.WithTimeZone(context.Background(), memory.TestApplicationID, "UTC")
	play, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5014")
	if err != nil || play.Offer == nil {
		t.Fatalf("取得入口失败: %v", err)
	}
	return ctx, service, store, monetization.ClaimRewardInput{ClaimID: "primary", OfferID: play.Offer.ID, DramaID: "5008", EpisodeID: "5014", CompletedAdUnitID: play.Offer.AdUnitID, IsEnded: true}
}

// TestRewardConcurrentDuplicateRejectsChangedTarget 验证并发同键不能替换已落库目标，原授予保持不变。
func TestRewardConcurrentDuplicateRejectsChangedTarget(t *testing.T) {
	ctx, service, store, input := consistencyFixture(t, 1, 0)
	store.before = func() {
		other := input
		other.EpisodeID = "5015"
		if _, err := service.ClaimReward(ctx, "visitor-1", other); err != nil {
			t.Fatal(err)
		}
	}
	result, err := service.ClaimReward(ctx, "visitor-1", input)
	if !errors.Is(err, monetization.ErrInvalidRewardClaim) || result != nil {
		t.Fatalf("同键替换目标必须拒绝: %+v %v", result, err)
	}
	stored, found, err := store.FindRewardGrant(ctx, "visitor-1", input.ClaimID)
	if err != nil || !found || stored.EpisodeID != "5015" {
		t.Fatalf("同键冲突不能改写已落库目标: %+v %v", stored, err)
	}
	play, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5014")
	if err != nil || play.Status == domain.PlayStatusPlayable {
		t.Fatalf("未授予目标不应可播: %+v %v", play, err)
	}
}

func TestRewardRebuildsBatchAfterOtherClaim(t *testing.T) {
	ctx, service, store, input := consistencyFixture(t, 2, 0)
	store.before = func() {
		other := input
		other.ClaimID = "other"
		other.EpisodeID = "5015"
		if _, err := service.ClaimReward(ctx, "visitor-1", other); err != nil {
			t.Fatal(err)
		}
	}
	result, err := service.ClaimReward(ctx, "visitor-1", input)
	if err != nil {
		t.Fatal(err)
	}
	grant, found, err := store.FindRewardGrant(ctx, "visitor-1", input.ClaimID)
	if err != nil || !found || len(grant.UnlockEpisodeIDs) != 2 || grant.UnlockEpisodeIDs[0] != "5014" || grant.UnlockEpisodeIDs[1] != "5017" {
		t.Fatalf("锁内必须跳过其他请求已解锁的集: %+v %v", grant.UnlockEpisodeIDs, err)
	}
	if result.NextAd != nil {
		t.Fatalf("整剧已解锁应停止提示: %+v", result.NextAd)
	}
}

func TestRewardNextAdIncludesEarlierLockedEpisode(t *testing.T) {
	ctx, service, _, input := consistencyFixture(t, 1, 0)
	input.EpisodeID = "5017"
	for i := 0; i < 2; i++ {
		result, err := service.ClaimReward(ctx, "visitor-1", input)
		if err != nil || result.NextAd == nil || result.NextAd.EpisodeID != "5014" {
			t.Fatalf("首次和重试都应回到前面的锁定集: %+v %v", result, err)
		}
	}
}

func TestRewardDailyLimitHidesOfferBeforeAd(t *testing.T) {
	ctx, service, _, input := consistencyFixture(t, 1, 1)
	if _, err := service.ClaimReward(ctx, "visitor-1", input); err != nil {
		t.Fatal(err)
	}
	play, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5015")
	if err != nil || play.Offer != nil || len(play.Offers) != 0 {
		t.Fatalf("限额耗尽不能继续提供广告入口: %+v %v", play, err)
	}
}

func TestRewardRetryKeepsRuleAndRuleScopedQuota(t *testing.T) {
	ctx, service, _, input := consistencyFixture(t, 1, 1, func(config *monetization.ApplicationMonetizationConfig) {
		other := config.Document.Rules[0]
		other.Code = "episode_b"
		other.Config = json.RawMessage(`{"requiredAdCount":1,"dailyRewardLimit":2,"autoUnlock":{"enabled":true,"countdownSeconds":7}}`)
		config.Document.Rules = append(config.Document.Rules, other)
		config.Bindings[other.Code] = monetization.RuleBinding{AdUnitID: "episode-ad"}
	})
	if _, err := service.ClaimReward(ctx, "visitor-1", input); err != nil {
		t.Fatal(err)
	}
	play, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5015")
	if err != nil || len(play.Offers) != 1 || play.Offer.RuleCode != "episode_b" {
		t.Fatalf("A 规则额度不应影响 B: %+v %v", play, err)
	}
	input.ClaimID, input.EpisodeID, input.OfferID = "rule-b", "5015", play.Offer.ID
	for i := 0; i < 2; i++ {
		result, err := service.ClaimReward(ctx, "visitor-1", input)
		if err != nil || result.NextAd == nil || result.NextAd.EpisodeID != "5016" || result.NextAd.Offer.RuleCode != "episode_b" {
			t.Fatalf("首次和重试必须保留 B 规则及其剩余额度: %+v %v", result, err)
		}
	}
}
