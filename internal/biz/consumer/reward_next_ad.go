package consumer

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/pkg/requesttime"
	"log/slog"
	"sort"
)

// rewardEpisodeSnapshot 保留领奖校验阶段已经读取的目录与权益，避免结算成功后
// 为连续广告提示再次读取策略、目录和全量权益。
type rewardEpisodeSnapshot struct {
	drama           Drama
	target          Episode
	episodes        []Episode
	ownedEpisodeIDs map[string]bool
}

// playback 使用已经通过领奖校验且已由事务授予权益的目标集构造官方播放结果。
// 这里不再次执行权益查询；GrantReward 成功是唯一授权前提。
func (s *rewardEpisodeSnapshot) playback() *PlayResult {
	drama := s.drama.Summary()
	episode := s.target.Summary()
	return &PlayResult{
		Status: PlayStatusPlayable, Drama: &drama, Episode: &episode,
		Playback: &PlaybackIdentity{
			Channel: officialPlaybackChannel, AlbumID: s.drama.PlatformAlbumID,
			EpisodeID: s.target.PlatformEpisodeID, VID: s.target.VID,
			PosterURL: s.target.PosterURL, Title: s.target.Title,
			EpisodeNumber: s.target.Number, DurationSecond: s.target.DurationSeconds,
		},
	}
}

// nextRewardAdFromSnapshot 根据后台连续提示配置和本次事务后的已知权益决定下一目标。
// 目录、策略、既有权益和日限额结果均来自当前领奖链路，不再发起依赖查询。
func nextRewardAdFromSnapshot(
	policy monetization.MonetizationPolicy,
	offer *monetization.AccessOffer,
	grant monetization.RewardGrant,
	dailyLimitReached bool,
	snapshot *rewardEpisodeSnapshot,
) *monetization.NextAdPrompt {
	if snapshot == nil || offer == nil || offer.AutoUnlock == nil || !offer.AutoUnlock.Enabled {
		return nil
	}
	if dailyLimitReached {
		return nil
	}
	owned := make(map[string]bool, len(snapshot.ownedEpisodeIDs)+len(grant.UnlockEpisodeIDs))
	for episodeID := range snapshot.ownedEpisodeIDs {
		owned[episodeID] = true
	}
	for _, episodeID := range grant.UnlockEpisodeIDs {
		owned[episodeID] = true
	}
	unlocked := 0
	var next *Episode
	for i := range snapshot.episodes {
		episode := &snapshot.episodes[i]
		if episode.Number <= policy.FreeEpisodeCount || owned[episode.ID] {
			unlocked++
			continue
		}
		if next == nil {
			next = episode
		}
	}
	if next == nil {
		return nil
	}
	if ready, _ := officialPlaybackReady(snapshot.drama, *next); !ready {
		return nil
	}
	return &monetization.NextAdPrompt{
		DramaID: snapshot.drama.ID, EpisodeID: next.ID, EpisodeNumber: next.Number,
		UnlockedCount: unlocked, TotalCount: len(snapshot.episodes), Offer: *offer,
	}
}

// nextRewardAd 一次读取目录、策略和权益后选出后续目标，不循环执行播放验权或调用媒体平台。
// 调用方只在结算成功后使用；任何依赖失败都停止本轮提示，不影响结算事实。
func (s *Service) nextRewardAd(ctx context.Context, visitorID string, settled monetization.RewardGrant) (*monetization.NextAdPrompt, error) {
	dramaID, episodeID := settled.DramaID, settled.EpisodeID
	policy, err := s.ResolveMonetizationPolicy(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "read next ad policy failed", "error", err)
		return nil, err
	}
	if !policy.Enabled() {
		return nil, nil
	}
	var offer *monetization.AccessOffer
	for _, candidate := range policy.RulePolicies() {
		// 重试只恢复原领取规则；配置已切换时停止提示，不借用另一规则的额度。
		if candidate.ProgressKey() != settled.ProgressKey {
			continue
		}
		if candidate.SelectedKind() != monetization.AccessOfferRewardedEpisode {
			continue
		}
		next, err := s.RewardOffer(ctx, visitorID, candidate, candidate.Rule.Code, nil)
		if err != nil {
			slog.ErrorContext(ctx, "read next ad offer failed", "error", err)
			return nil, err
		}
		if next.AutoUnlock == nil || !next.AutoUnlock.Enabled {
			continue
		}
		if candidate.EpisodeReward.DailyRewardLimit > 0 {
			period := monetization.RewardPeriodFor(requesttime.At(ctx, s.now), application.LocationFromContext(ctx))
			count, err := s.store.CountRewardGrants(ctx, visitorID, monetization.RewardTypeEpisodeUnlock, period.StartsAt, candidate.ProgressKey())
			if err != nil {
				slog.ErrorContext(ctx, "read next ad daily count failed", "error", err)
				return nil, err
			}
			if count >= candidate.EpisodeReward.DailyRewardLimit {
				continue
			}
		}
		offer = next
		break
	}
	if offer == nil {
		return nil, nil
	}
	catalog, err := s.store.HasCatalogEntitlement(ctx, visitorID, requesttime.At(ctx, s.now))
	if err != nil {
		slog.ErrorContext(ctx, "read next ad catalog access failed", "error", err)
		return nil, err
	}
	if catalog {
		return nil, nil
	}
	drama, _, err := s.findEpisode(ctx, dramaID, episodeID)
	if err != nil {
		slog.ErrorContext(ctx, "read next ad directory failed", "error", err)
		return nil, err
	}
	grants, err := s.store.ListEntitlements(ctx, visitorID, requesttime.At(ctx, s.now))
	if err != nil {
		slog.ErrorContext(ctx, "read next ad ownership failed", "error", err)
		return nil, err
	}
	owned := map[string]bool{}
	for _, grant := range grants {
		if grant.DramaID == dramaID {
			owned[grant.EpisodeID] = true
		}
	}
	episodes := append([]Episode(nil), drama.Episodes...)
	sort.SliceStable(episodes, func(i, j int) bool { return episodes[i].Number < episodes[j].Number })
	unlocked := 0
	var next *Episode
	for i := range episodes {
		episode := &episodes[i]
		if episode.Number <= policy.FreeEpisodeCount || owned[episode.ID] {
			unlocked++
			continue
		}
		if next == nil {
			next = episode
		}
	}
	if next == nil {
		return nil, nil
	}
	if ready, _ := officialPlaybackReady(*drama, *next); !ready {
		return nil, nil
	}
	return &monetization.NextAdPrompt{DramaID: dramaID, EpisodeID: next.ID, EpisodeNumber: next.Number, UnlockedCount: unlocked, TotalCount: len(episodes), Offer: *offer}, nil
}
