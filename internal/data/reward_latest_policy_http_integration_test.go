package data

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/data/persistence/mysqlstore"
)

// assertLatestRewardPolicyHTTP 验证真实 HTTP 与持久化链路按当前规则发奖，重试只恢复已发事实。
func assertLatestRewardPolicyHTTP(t *testing.T, store *mysqlstore.Store, base, token, appID string) {
	t.Helper()
	ctx := application.WithContext(context.Background(), appID)
	saveCount := func(count int) {
		t.Helper()
		config := monetization.ApplicationMonetizationConfig{
			ApplicationID: appID, Status: monetization.MonetizationEnabled,
			Strategy: monetization.MonetizationStrategyEpisodeUnlock, FreeEpisodeCount: 1,
			EpisodeRewardAdUnitCode: "fixture-reward",
			EpisodeReward:           monetization.EpisodeRewardPolicy{UnlockEpisodeCount: count},
			CreatedAt:               time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		if err := store.SaveApplicationMonetization(ctx, config); err != nil {
			t.Fatalf("更新测试广告策略失败: %v", err)
		}
	}
	claim := func(input monetization.ClaimRewardInput, status int) domain.RewardClaimResult {
		t.Helper()
		raw := iaaRequest(t, base, "POST", "/api/v1/reward-grants", token, "", input, status)
		var result domain.RewardClaimResult
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("解析领奖合同失败: %v", err)
		}
		if result.Playback == nil || result.Playback.Status != domain.PlayStatusPlayable || result.Playback.Episode.ID != input.EpisodeID {
			t.Fatal("内容领奖未直接返回目标集的播放授权")
		}
		return result
	}
	assertPlayable := func(episode int, want bool) {
		t.Helper()
		result := iaaPlay(t, base, token, episode)
		if (result.Status == domain.PlayStatusPlayable) != want {
			t.Fatalf("第 %d 集可播放状态应为 %t，实际状态 %s", episode, want, result.Status)
		}
	}

	// 广告开始时是一次两集，完播前改为一集；沿用原 Offer 必须按最新规则成功结算。
	beforeChange := iaaPlay(t, base, token, 4)
	if beforeChange.Offer == nil {
		t.Fatal("配置变更前没有领取入口")
	}
	firstInput := iaaClaim(beforeChange, "latest-policy-first")
	saveCount(1)
	if result := claim(firstInput, 201); result.Status != monetization.ClaimStatusGranted {
		t.Fatal("旧 Offer 的首次领取未按最新策略结算")
	}
	assertPlayable(4, true)
	assertPlayable(5, false)

	// 提升解锁数不能让已成功的 ClaimID 再发奖；新的 ClaimID 才按新的数量结算。
	pendingPlay := iaaPlay(t, base, token, 5)
	if pendingPlay.Offer == nil {
		t.Fatal("下一目标没有领取入口")
	}
	saveCount(3)
	if result := claim(firstInput, 200); result.Status != monetization.ClaimStatusAlreadyGranted {
		t.Fatal("重试没有恢复原有领取状态")
	}
	assertPlayable(5, false)
	claim(iaaClaim(pendingPlay, "latest-policy-second"), 201)
	for _, episode := range []int{5, 6, 7} {
		assertPlayable(episode, true)
	}
	assertPlayable(8, false)
}
