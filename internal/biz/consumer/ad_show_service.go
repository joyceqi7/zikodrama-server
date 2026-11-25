package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/pkg/requesttime"
)

// logAdShowFailure 区分预期关联拒绝与非预期依赖故障，保留实际错误详情。
func logAdShowFailure(ctx context.Context, operation string, err error) {
	level := slog.LevelError
	if errors.Is(err, monetization.ErrInvalidAdShow) || errors.Is(err, ErrNotFound) {
		level = slog.LevelWarn
	}
	slog.Log(ctx, level, operation, "error", err)
}

// RecordAdShow 校验广告位、内容与入剧范围；有效事实均幂等保存，达标只决定是否还需拉起广告。
func (s *Service) RecordAdShow(ctx context.Context, visitorID string, input monetization.AdShowInput) error {
	ctx = requesttime.Ensure(ctx, s.now)
	// 展示确认按首次收到请求的服务器时间归日，客户端时间仅保持旧请求可解析。
	input.OccurredAt = requesttime.At(ctx, s.now)
	existing, found, err := s.store.FindAdShow(ctx, visitorID, input.EventID)
	if err != nil {
		slog.ErrorContext(ctx, "find original ad show failed", "error", err)
		return err
	}
	if found {
		// 历史记录优先恢复；日期、广告配置和目录后续变化不改写原确认结果。
		if existing.Scene != input.Scene || existing.AdUnitID != input.AdUnitID || existing.DramaID != input.DramaID || existing.EpisodeID != input.EpisodeID {
			slog.WarnContext(ctx, "ad show retry immutable content rejected", "error", monetization.ErrInvalidAdShow)
			return monetization.ErrInvalidAdShow
		}
		return nil
	}
	configured, err := s.store.HasAdUnit(ctx, input.AdUnitID)
	if err != nil {
		slog.ErrorContext(ctx, "check ad show unit failed", "error", err)
		return err
	}
	if !configured {
		slog.WarnContext(ctx, "ad show unit rejected", "error", monetization.ErrInvalidAdShow)
		return monetization.ErrInvalidAdShow
	}
	if input.EpisodeID != "" {
		if _, _, err := s.findEpisode(ctx, input.DramaID, input.EpisodeID); err != nil {
			logAdShowFailure(ctx, "check ad show episode failed", err)
			return err
		}
	} else if input.DramaID != "" {
		if _, err := s.FindDrama(ctx, input.DramaID); err != nil {
			logAdShowFailure(ctx, "check ad show drama failed", err)
			return err
		}
	}
	if input.Scene == monetization.AdSceneEntryReward {
		policy, err := s.ResolveMonetizationPolicy(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "resolve ad show entry policy failed", "error", err)
			return err
		}
		for _, selected := range policy.RulePolicies() {
			if !policy.Enabled() {
				break
			}
			if selected.Rule == nil || selected.Rule.Type != "entry_rewarded_ad" {
				continue
			}
			var config monetization.EntryRewardedAdConfig
			if err := json.Unmarshal(selected.Rule.Config, &config); err != nil {
				slog.ErrorContext(ctx, "decode ad show entry rule failed", "error", err)
				return err
			}
			if config.EffectiveScope() == "user_drama" && input.DramaID == "" {
				slog.WarnContext(ctx, "ad show entry target required by rule", "error", monetization.ErrInvalidAdShow)
				return monetization.ErrInvalidAdShow
			}
			break
		}
	}
	if err := s.store.RecordAdShow(ctx, visitorID, input); err != nil {
		logAdShowFailure(ctx, "save ad show failed", err)
		return err
	}
	return nil
}

// AdShowStatus 是当前应用自然日的展示次数，不含 N、策略或放行结论。
type AdShowStatus struct {
	// ShownCount 是所查询场景与可选内容范围内的原始成功展示次数。
	ShownCount int `json:"shownCount"`
	// ProgressDate 是应用时区的自然日日期。
	ProgressDate string `json:"progressDate"`
	// ValidUntil 是下一应用当地午夜的 UTC 时刻。
	ValidUntil string `json:"validUntil"`
}

// AdShowStatus 查询指定场景的每日展示，允许首页等无内容目标的场景；门槛由 Mini 自行判断。
func (s *Service) AdShowStatus(ctx context.Context, visitorID, scene, dramaID, episodeID string) (AdShowStatus, error) {
	ctx = requesttime.Ensure(ctx, s.now)
	if episodeID != "" {
		if _, _, err := s.findEpisode(ctx, dramaID, episodeID); err != nil {
			logAdShowFailure(ctx, "check ad show count episode failed", err)
			return AdShowStatus{}, err
		}
	} else if dramaID != "" {
		if _, err := s.FindDrama(ctx, dramaID); err != nil {
			logAdShowFailure(ctx, "check ad show count drama failed", err)
			return AdShowStatus{}, err
		}
	}
	period := monetization.RewardPeriodFor(requesttime.At(ctx, s.now), application.LocationFromContext(ctx))
	count, err := s.store.CountAdShows(ctx, visitorID, monetization.AdShowQuery{Scene: scene, DramaID: dramaID, EpisodeID: episodeID, From: period.StartsAt, Until: period.ExpiresAt})
	if err != nil {
		slog.ErrorContext(ctx, "count ad shows failed", "error", err)
		return AdShowStatus{}, err
	}
	return AdShowStatus{ShownCount: count, ProgressDate: period.Key, ValidUntil: period.ExpiresAt.Format(time.RFC3339)}, nil
}
