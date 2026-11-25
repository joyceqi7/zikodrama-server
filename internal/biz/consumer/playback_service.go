package consumer

import (
	"context"
	"drama-server/internal/pkg/requesttime"
	"errors"
	"log/slog"
	"strings"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/pkg/requestmeta"
)

const officialPlaybackChannel = "byteplus"

// ResolveOfficialPlay 按固定顺序完成 TikTok 官方 Player 的播放授权：
//  1. 校验应用、短剧和剧集标识，不信任客户端提交的平台媒体标识；
//  2. 校验短剧已发布、媒资已就绪且官方 Album/Episode/VID 齐备；
//  3. 根据当前请求的消费策略快照检查免费、逐集与 VIP 权益；
//  4. 权益不足且有解锁入口时返回 Offer，并按策略授权首集开头；有完整权益时返回正片描述符。
//
// 观看进度、收藏和历史只由客户端保存；服务端不建立播放会话或自研取证账本。
// CampaignID 必须来自 VisitorToken 写入的可信 Context，不能信任客户端提交的
// campaign、albumId、episodeId 或 vid。
func (s *Service) ResolveOfficialPlay(
	ctx context.Context,
	visitorID, dramaID, episodeID string,
) (*PlayResult, error) {
	ctx = requesttime.Ensure(ctx, s.now)
	visitorID = strings.TrimSpace(visitorID)
	dramaID = strings.TrimSpace(dramaID)
	episodeID = strings.TrimSpace(episodeID)
	applicationID := application.IDFromContext(ctx)
	logFields := append(requestmeta.LogFields(ctx),
		"flow", "playback_resolve",
		"application_id", applicationID,
		"visitor_id", visitorID,
		"drama_id", dramaID,
		"episode_id", episodeID,
	)
	slog.DebugContext(ctx, "playback resolve started", append(logFields, "stage", "validate_input")...)

	if applicationID == "" || visitorID == "" || dramaID == "" || episodeID == "" {
		slog.WarnContext(ctx, "playback resolve rejected", append(logFields,
			"stage", "validate_input", "outcome", "invalid_context")...)
		slog.WarnContext(ctx, "resolve official play rejected", "error", ErrPlaybackCredential)
		return nil, ErrPlaybackCredential
	}

	drama, episode, err := s.findEpisode(ctx, dramaID, episodeID)
	if err != nil {
		slog.ErrorContext(ctx, "find episode failed", "error", err)
		return nil, err
	}
	result, err := s.resolveLoadedPlayback(ctx, visitorID, drama, episode, nil, monetization.VIPProgress{})
	if err != nil {
		slog.ErrorContext(ctx, "resolve selected episode failed", "error", err)
	}
	return result, err
}

func (s *Service) resolveLoadedPlayback(ctx context.Context, visitorID string, drama *Drama, episode *Episode, policy *monetization.MonetizationPolicy, vipState monetization.VIPProgress) (*PlayResult, error) {
	logFields := append(requestmeta.LogFields(ctx), "flow", "playback_resolve", "application_id", application.IDFromContext(ctx),
		"visitor_id", visitorID, "drama_id", drama.ID, "episode_id", episode.ID)
	summary := drama.Summary()
	episodeSummary := episode.Summary()
	if ready, reason := officialPlaybackReady(*drama, *episode); !ready {
		slog.InfoContext(ctx, "playback resolve unavailable", append(logFields,
			"stage", "validate_platform_media", "outcome", "unavailable", "reason", reason)...)
		return &PlayResult{
			Status: PlayStatusUnavailable, Drama: &summary, Episode: &episodeSummary,
		}, nil
	}

	if policy == nil {
		current, err := s.ResolveMonetizationPolicy(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "resolve playback monetization policy failed", "error", err)
			return nil, err
		}
		policy = &current
		vipState, err = s.playbackVIPProgress(ctx, visitorID, current)
		if err != nil {
			slog.ErrorContext(ctx, "resolve playback vip progress failed", "error", err)
			return nil, err
		}
	}
	playable, err := s.episodeAccessForPolicy(ctx, visitorID, *drama, *episode, *policy, vipState)
	if err != nil {
		slog.ErrorContext(ctx, "episode access failed", "error", err)
		return nil, err
	}
	if !playable {
		result1, resultErr := s.accessRequiredResult(ctx, visitorID, logFields, summary, episodeSummary, vipState, *policy)
		if resultErr != nil {
			slog.ErrorContext(ctx, "access required result failed", "error", resultErr)
			return nil, resultErr
		}
		if result1.Status == PlayStatusAccessRequired {
			if !application.IsDirect(ctx) {
				result1.Preload = officialPlaybackIdentity(*drama, *episode)
			}
			if episode.Number == 1 && policy.EntryAdDelaySeconds > 0 && supportsEntryPreview(ctx) {
				preview, previewErr := s.entryPlaybackPreview(ctx, *drama, *episode, policy.EntryAdDelaySeconds, result1.Preload)
				if previewErr != nil {
					slog.ErrorContext(ctx, "resolve locked first episode preview failed", "error", previewErr)
					return nil, previewErr
				}
				result1.EntryPreview = preview
			}
		}
		return result1, nil
	}

	identity, err := s.playbackIdentity(ctx, *drama, *episode)
	if err != nil {
		slog.ErrorContext(ctx, "resolve playback identity failed", "error", err)
		return nil, err
	}
	slog.InfoContext(ctx, "playback resolve succeeded", append(logFields,
		"stage", "complete",
		"outcome", "playable",
		"platform_album_id", drama.PlatformAlbumID,
		"platform_episode_id", episode.PlatformEpisodeID,
		"vid", episode.VID,
	)...)
	return &PlayResult{
		Status:      PlayStatusPlayable,
		Drama:       &summary,
		Episode:     &episodeSummary,
		Playback:    identity,
		VIPProgress: &vipState,
	}, nil
}

// officialPlaybackIdentity 统一生成已就绪官方媒资描述符；调用方决定其是预热还是播放许可。
func officialPlaybackIdentity(drama Drama, episode Episode) *PlaybackIdentity {
	return &PlaybackIdentity{
		Channel: officialPlaybackChannel, AlbumID: drama.PlatformAlbumID,
		EpisodeID: episode.PlatformEpisodeID, VID: episode.VID,
		PosterURL: episode.PosterURL, Title: episode.Title,
		EpisodeNumber: episode.Number, DurationSecond: episode.DurationSeconds,
	}
}

// ResolveOfficialPlays 按请求顺序批量解析同一短剧的相邻集，供客户端一次准备连续播放窗口。
// 每一项仍独立执行完整权益判断，批量接口不会把列表展示状态当作播放授权。
func (s *Service) ResolveOfficialPlays(
	ctx context.Context,
	visitorID, dramaID string,
	episodeIDs []string,
) ([]*PlayResult, error) {
	ctx = requesttime.Ensure(ctx, s.now)
	results := make([]*PlayResult, 0, len(episodeIDs))
	if len(episodeIDs) == 0 {
		return results, nil
	}
	visitorID, dramaID = strings.TrimSpace(visitorID), strings.TrimSpace(dramaID)
	if application.IDFromContext(ctx) == "" || visitorID == "" || dramaID == "" {
		slog.WarnContext(ctx, "resolve adjacent playback rejected", "error", ErrPlaybackCredential)
		return nil, ErrPlaybackCredential
	}
	// 同一批剧集共用当前权威线上快照，仍逐集核对媒资、权益及播放凭据。
	drama, err := s.findPlaybackDrama(ctx, dramaID)
	if err != nil {
		slog.ErrorContext(ctx, "read adjacent playback drama failed", "error", err)
		return nil, err
	}
	for _, episodeID := range episodeIDs {
		episodeID = strings.TrimSpace(episodeID)
		if episodeID == "" {
			slog.WarnContext(ctx, "resolve adjacent episode rejected", "error", ErrPlaybackCredential)
			return nil, ErrPlaybackCredential
		}
		episode := playbackEpisode(drama, episodeID)
		if episode == nil {
			slog.WarnContext(ctx, "find adjacent episode rejected", "error", ErrNotFound)
			return nil, ErrNotFound
		}
		result, err := s.resolveLoadedPlayback(ctx, visitorID, drama, episode, nil, monetization.VIPProgress{})
		if err != nil {
			slog.ErrorContext(ctx, "resolve adjacent episode playback failed", "error", err, "episode_id", episodeID)
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

// accessRequiredResult 构造权益不足时的稳定业务响应，不自行操作播放器或记录观看历史。
func (s *Service) accessRequiredResult(
	ctx context.Context,
	visitorID string,
	logFields []any,
	drama DramaSummary,
	episode EpisodeSummary,
	vipState monetization.VIPProgress,
	policy monetization.MonetizationPolicy,
) (*PlayResult, error) {
	if policy.Rules != nil {
		return s.ruleAccessRequiredResult(ctx, visitorID, drama, episode, vipState, policy)
	}
	if !policy.Enabled() || policy.OfferKind() == "" {
		slog.InfoContext(ctx, "playback resolve unavailable", append(logFields,
			"stage", "resolve_policy", "outcome", "unavailable", "reason", "no_unlock_offer")...)
		return &PlayResult{
			Status: PlayStatusUnavailable, Drama: &drama, Episode: &episode, VIPProgress: &vipState,
		}, nil
	}
	offer, err := s.RewardOffer(ctx, visitorID, policy, policy.OfferKind(), nil)
	if err != nil {
		slog.ErrorContext(ctx, "reward offer failed", "error", err)
		return nil, err
	}
	var vipOffer *monetization.AccessOffer
	if policy.VIP.Enabled && !vipState.Active {
		vipOffer, err = s.RewardOffer(ctx, visitorID, policy, policy.VIPOfferKind(false), &vipState)
		if err != nil {
			slog.ErrorContext(ctx, "reward offer failed", "error", err)
			return nil, err
		}
	}
	slog.InfoContext(ctx, "playback resolve requires access", append(logFields,
		"stage", "check_entitlement", "outcome", "access_required", "offer_kind", offer.Kind)...)
	return &PlayResult{
		Status: PlayStatusAccessRequired, Drama: &drama, Episode: &episode, Offer: offer,
		VIPOffer: vipOffer, VIPProgress: &vipState,
	}, nil
}

// findEpisode 只从当前应用可见的已发布目录中解析内部剧集标识。
func (s *Service) findEpisode(ctx context.Context, dramaID, episodeID string) (*Drama, *Episode, error) {
	drama, err := s.findPlaybackDrama(ctx, dramaID)
	if err != nil {
		slog.ErrorContext(ctx, "read episode playback drama failed", "error", err)
		return nil, nil, err
	}
	if episodeID = strings.TrimSpace(episodeID); episodeID != "" {
		if episode := playbackEpisode(drama, episodeID); episode != nil {
			return drama, episode, nil
		}
	}
	slog.WarnContext(ctx, "find episode rejected", "error", ErrNotFound)
	return nil, nil, ErrNotFound
}

func (s *Service) findPlaybackDrama(ctx context.Context, dramaID string) (*Drama, error) {
	drama, err := s.store.FindDrama(ctx, strings.TrimSpace(dramaID))
	if err != nil {
		level := slog.LevelError
		if errors.Is(err, ErrNotFound) {
			level = slog.LevelWarn
		}
		slog.Log(ctx, level, "find drama failed", "error", err)
		return nil, err
	}
	published, ok := drama.PublishedForApplication(application.IDFromContext(ctx))
	if !ok {
		slog.WarnContext(ctx, "find episode rejected", "error", ErrNotFound)
		return nil, ErrNotFound
	}
	localized := published.ConsumerView()
	return &localized, nil
}

func playbackEpisode(drama *Drama, episodeID string) *Episode {
	if episodeID == "" && len(drama.Episodes) > 0 {
		return &drama.Episodes[0]
	}
	for index := range drama.Episodes {
		if drama.Episodes[index].ID == episodeID {
			return &drama.Episodes[index]
		}
	}
	return nil
}

// consumerVisible 保证编辑中和已下线短剧不会进入消费者目录。
func consumerVisible(drama Drama) bool {
	return drama.PublishStatus == "published"
}

// officialPlaybackReady 检查官方 Player 的全部本地前置条件，并返回可用于日志
// 和运营门禁的稳定原因码。
func officialPlaybackReady(drama Drama, episode Episode) (bool, string) {
	if drama.PlaybackProvider == "byteplus_web" {
		if drama.PublishStatus != "published" {
			return false, "drama_not_published"
		}
		if episode.MediaStatus != "ready" || episode.VID == "" || episode.MediaSpaceName == "" {
			return false, "media_not_ready"
		}
		return true, "ready"
	}
	switch {
	case drama.PublishStatus != "published":
		return false, "drama_not_published"
	case episode.MediaStatus != "ready":
		return false, "media_not_ready"
	case strings.TrimSpace(drama.PlatformAlbumID) == "":
		return false, "platform_album_missing"
	case strings.TrimSpace(episode.PlatformEpisodeID) == "":
		return false, "platform_episode_missing"
	case strings.TrimSpace(episode.VID) == "":
		return false, "vid_missing"
	default:
		return true, "ready"
	}
}

// ruleAccessRequiredResult 按配置顺序组合已接入获取方式，播放层不判断付款或广告业务细节。
func (s *Service) ruleAccessRequiredResult(ctx context.Context, visitorID string, drama DramaSummary, episode EpisodeSummary, vipState monetization.VIPProgress, policy monetization.MonetizationPolicy) (*PlayResult, error) {
	result := &PlayResult{Status: PlayStatusUnavailable, Drama: &drama, Episode: &episode, VIPProgress: &vipState}
	if !policy.Enabled() {
		return result, nil
	}
	for _, candidate := range policy.RulePolicies() {
		// 入剧广告只由小程序起播门槛使用，不是锁定剧集的解锁入口。
		if candidate.SelectedKind() == monetization.AccessOfferRewardedEntry {
			continue
		}
		if candidate.SelectedKind() == monetization.AccessOfferRewardedEpisode && candidate.EpisodeReward.DailyRewardLimit > 0 {
			period := monetization.RewardPeriodFor(requesttime.At(ctx, s.now), application.LocationFromContext(ctx))
			count, err := s.store.CountRewardGrants(ctx, visitorID, monetization.RewardTypeEpisodeUnlock, period.StartsAt, candidate.ProgressKey())
			if err != nil {
				slog.ErrorContext(ctx, "read episode offer daily limit failed", "error", err)
				return nil, rewardDependencyError(err)
			}
			if count >= candidate.EpisodeReward.DailyRewardLimit {
				continue
			}
		}
		if candidate.VIP.Enabled && vipState.Active {
			continue
		}
		state := vipState
		if candidate.VIP.Enabled {
			period := monetization.RewardPeriodFor(requesttime.At(ctx, s.now), application.LocationFromContext(ctx))
			var err error
			state, err = s.store.FindVIPProgress(ctx, visitorID, candidate, period, requesttime.At(ctx, s.now))
			if err != nil {
				slog.ErrorContext(ctx, "read rule offer progress failed", "error", err)
				return nil, err
			}
		}
		offer, err := s.RewardOffer(ctx, visitorID, candidate, candidate.Rule.Code, &state)
		if err != nil {
			slog.ErrorContext(ctx, "resolve rule access offer failed", "error", err)
			return nil, err
		}
		result.Offers = append(result.Offers, *offer)
		if result.Offer == nil {
			result.Offer = offer
		}
		if offer.Kind == monetization.AccessOfferRewardedVIP && result.VIPOffer == nil {
			result.VIPOffer = offer
		}
	}
	if len(result.Offers) > 0 {
		result.Status = PlayStatusAccessRequired
	}
	return result, nil
}
