package consumer

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"drama-server/internal/biz/monetization"
)

// entryRewardStrategy 结算当天入剧广告次数，不发放内容权益。
type entryRewardStrategy struct{}

// Prepare 校验入剧来源和接收日；N 仅作为达标门槛，不能阻止记录已经完成的广告。
func (entryRewardStrategy) Prepare(ctx context.Context, s *Service, _ string, input monetization.ClaimRewardInput, policy monetization.MonetizationPolicy, period monetization.RewardPeriod, now time.Time, grant *monetization.RewardGrant) (*rewardEpisodeSnapshot, error) {
	if policy.Rule == nil || policy.Rule.Type != "entry_rewarded_ad" || input.EpisodeID != "" || !period.ExpiresAt.After(now) {
		slog.WarnContext(ctx, "entry ad claim rejected", "error", monetization.ErrInvalidRewardClaim)
		return nil, monetization.ErrInvalidRewardClaim
	}
	var config monetization.EntryRewardedAdConfig
	if err := json.Unmarshal(policy.Rule.Config, &config); err != nil {
		slog.ErrorContext(ctx, "decode entry ad claim rule failed", "error", err)
		return nil, err
	}
	grant.Scene = monetization.AdSceneEntryReward
	if config.EffectiveScope() == "user_drama" && input.DramaID == "" {
		slog.WarnContext(ctx, "entry reward target required by rule", "error", monetization.ErrInvalidRewardClaim)
		return nil, monetization.ErrInvalidRewardClaim
	}
	grant.RewardType = monetization.RewardTypeEntryProgress
	grant.RequiredCount = config.RequiredAdCount
	if input.DramaID != "" {
		drama, err := s.FindDrama(ctx, input.DramaID)
		if err != nil {
			slog.ErrorContext(ctx, "check entry reward drama failed", "error", err)
			return nil, err
		}
		grant.DramaID = drama.ID
	}
	return nil, nil
}
