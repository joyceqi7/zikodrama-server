package consumer

import (
	"context"
	"drama-server/internal/biz/monetization"
	"log/slog"
	"time"
)

// vipRewardStrategy 将当前广告规则的领域约束封装为可替换策略。
type vipRewardStrategy struct{}

// Prepare 按当前规则校验自然日累计进度；VIP 不读取或返回剧集目录。
func (vipRewardStrategy) Prepare(ctx context.Context, s *Service, visitorID string, input monetization.ClaimRewardInput, policy monetization.MonetizationPolicy, period monetization.RewardPeriod, now time.Time, grant *monetization.RewardGrant) (*rewardEpisodeSnapshot, error) {

	vipPolicy := policy.VIP
	if !vipPolicy.Enabled || vipPolicy.RequiredAdCount < 1 || !period.ExpiresAt.After(now) {
		slog.WarnContext(ctx, "claim reward rejected", "error", monetization.ErrInvalidRewardClaim)
		return nil, monetization.ErrInvalidRewardClaim
	}
	state, stateErr := s.store.FindVIPProgress(ctx, visitorID, policy, period, now)
	if stateErr != nil {
		slog.ErrorContext(ctx, "find vip progress failed", "error", stateErr)
		return nil, rewardDependencyError(stateErr)
	}
	if state.Active || state.AdCount >= vipPolicy.RequiredAdCount {
		slog.WarnContext(ctx, "claim reward rejected", "error", monetization.ErrInvalidRewardClaim)
		return nil, monetization.ErrInvalidRewardClaim
	}
	grant.Scene = monetization.AdSceneVIPReward
	grant.RewardType = monetization.RewardTypeVIPProgress
	grant.EntitlementScope = monetization.EntitlementScopeCatalog
	grant.RequiredCount = vipPolicy.RequiredAdCount
	grant.DailyRewardLimit = vipPolicy.DailyRewardLimit
	return nil, nil
}
