package consumer

import (
	"context"
	"drama-server/internal/biz/monetization"
	"log/slog"
	"time"
)

// episodeRewardStrategy 将当前广告规则的领域约束封装为可替换策略。
type episodeRewardStrategy struct{}

// Prepare 校验目标目录和现有访问权益，并返回本请求唯一的目录读取结果供授予及播放复用。
func (episodeRewardStrategy) Prepare(ctx context.Context, s *Service, visitorID string, input monetization.ClaimRewardInput, policy monetization.MonetizationPolicy, period monetization.RewardPeriod, now time.Time, grant *monetization.RewardGrant) (*rewardEpisodeSnapshot, error) {

	drama, episode, findErr := s.findEpisode(ctx, input.DramaID, input.EpisodeID)
	if findErr != nil {
		slog.ErrorContext(ctx, "find episode failed", "error", findErr)
		return nil, rewardDependencyError(findErr)
	}
	// 目标验权已覆盖 VIP、整剧和逐集权益，不额外重复查询目录级权益。
	alreadyUnlocked, entitlementErr := s.store.HasEntitlement(ctx, visitorID, drama.ID, episode.ID, now)
	if entitlementErr != nil {
		slog.ErrorContext(ctx, "has entitlement failed", "error", entitlementErr)
		return nil, rewardDependencyError(entitlementErr)
	}
	if ready, _ := officialPlaybackReady(*drama, *episode); !ready || episode.Number <= policy.FreeEpisodeCount || alreadyUnlocked {
		slog.WarnContext(ctx, "claim reward rejected", "error", monetization.ErrInvalidRewardClaim)
		return nil, monetization.ErrInvalidRewardClaim
	}
	if policy.Rule == nil && policy.EpisodeReward.DailyRewardLimit > 0 {
		count, countErr := s.store.CountRewardGrants(ctx, visitorID, monetization.RewardTypeEpisodeUnlock, period.StartsAt, policy.ProgressKey())
		if countErr != nil {
			slog.ErrorContext(ctx, "count reward grants failed", "error", countErr)
			return nil, rewardDependencyError(countErr)
		}
		if count >= policy.EpisodeReward.DailyRewardLimit {
			slog.WarnContext(ctx, "claim reward rejected", "error", ErrRateLimited)
			return nil, ErrRateLimited
		}
	}
	grant.Scene = monetization.AdSceneEpisodeUnlock
	grant.RewardType = monetization.RewardTypeEpisodeUnlock
	grant.EntitlementScope = monetization.EntitlementScopeEpisode
	grant.DailyRewardLimit = policy.EpisodeReward.DailyRewardLimit
	grant.DramaID = drama.ID
	grant.EpisodeID = episode.ID
	return &rewardEpisodeSnapshot{drama: *drama, target: *episode}, nil
}
