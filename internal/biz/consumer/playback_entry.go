package consumer

import (
	"context"
	"errors"
	"log/slog"

	"drama-server/internal/biz/monetization"
	"drama-server/internal/pkg/requesttime"
)

// CampaignPlaybackPreparation 保存投放首屏可公开展示的目标信息，不包含媒体标识或权益。
type CampaignPlaybackPreparation struct {
	// Target 是当前应用线上版本中已确认的业务剧集标识。
	Target LaunchTarget `json:"target"`
	// CoverURL 是已发布的目标集海报，缺少分集海报时使用剧封面。
	CoverURL string `json:"coverUrl"`
	// Title 是当前发布版本的剧名。
	Title string `json:"title"`
	// EpisodeNumber 是目标集序号。
	EpisodeNumber int `json:"episodeNumber"`
	// EpisodeCount 是当前发布版本包含的集数。
	EpisodeCount int `json:"episodeCount"`
}

// PrepareCampaignPlayback 提前准备公开展示并让详情读取复用目录缓存；正式起播仍读取最新发布快照和权益。
func (s *Service) PrepareCampaignPlayback(ctx context.Context, dramaID, episodeID string) (*CampaignPlaybackPreparation, error) {
	drama, err := s.FindDrama(ctx, dramaID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "read campaign playback preparation failed", "error", err)
		return nil, err
	}
	episode := playbackEpisode(drama, episodeID)
	if episode == nil {
		return nil, nil
	}
	cover := episode.PosterURL
	if cover == "" {
		cover = drama.CoverURL
	}
	return &CampaignPlaybackPreparation{
		Target:   LaunchTarget{DramaID: drama.ID, EpisodeID: episode.ID},
		CoverURL: cover, Title: drama.Title,
		EpisodeNumber: episode.Number, EpisodeCount: len(drama.Episodes),
	}, nil
}

// PlaybackEntry 是当前观众对目标剧集的一次起播决策。
type PlaybackEntry struct {
	// Target 是已确认可见的内部剧集定位。
	Target LaunchTarget `json:"target"`
	// Drama 是首屏需要的剧目摘要。
	Drama DramaSummary `json:"drama"`
	// Episode 是首屏需要的目标集摘要。
	Episode EpisodeSummary `json:"episode"`
	// EntryAdStatus 是当前规则和应用自然日下的广告进度。
	EntryAdStatus EntryAdStatus `json:"entryAdStatus"`
	// VIPState 是当前观众的权威 VIP 进度和有效期。
	VIPState monetization.VIPProgress `json:"vipState"`
	// Play 是当前可交付的正片或试看决策；需先完成入剧广告或 VIP 时可以为空。
	Play *PlayResult `json:"play,omitempty"`
}

// ResolvePlaybackEntry 使用同一份权威线上目录和策略处理自然点选与投放落地。
// 调用方先校验入口及目标来源；剧目不可见或目标集失效时返回空结果。
func (s *Service) ResolvePlaybackEntry(ctx context.Context, visitorID, dramaID, episodeID string) (*PlaybackEntry, error) {
	if dramaID == "" {
		return nil, nil
	}
	ctx = WithEntryPreview(requesttime.Ensure(ctx, s.now))
	drama, err := s.findPlaybackDrama(ctx, dramaID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "read playback entry drama failed", "error", err)
		return nil, err
	}
	episode := playbackEpisode(drama, episodeID)
	if episode == nil {
		return nil, nil
	}
	policy, err := s.ResolveMonetizationPolicy(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "read playback entry policy failed", "error", err)
		return nil, err
	}
	// 广告进度与 VIP 都只读，同一请求快照下并行查询；两项均成功才可执行后续门槛和签名。
	entryRead := make(chan struct{})
	var entryStatus EntryAdStatus
	var entryErr error
	go func() {
		defer close(entryRead)
		entryStatus, entryErr = s.entryAdStatusForPolicy(ctx, visitorID, drama.ID, policy, true)
	}()
	vip, err := s.playbackVIPProgress(ctx, visitorID, policy)
	<-entryRead
	if entryErr != nil {
		slog.ErrorContext(ctx, "read playback entry ad status failed", "error", entryErr)
		return nil, entryErr
	}
	if err != nil {
		slog.ErrorContext(ctx, "read playback entry vip failed", "error", err)
		return nil, err
	}
	entry := &PlaybackEntry{
		Target: LaunchTarget{DramaID: drama.ID, EpisodeID: episode.ID},
		Drama:  drama.Summary(), Episode: episode.Summary(), EntryAdStatus: entryStatus, VIPState: vip,
	}
	count := entryStatus.CompletedCount
	if entryStatus.CountBy == "shown" {
		count = entryStatus.ShownCount
	}
	needsAd := entryStatus.Enabled && count < entryStatus.RequiredCount
	needsVIP := policy.Enabled() && policy.VIP.Enabled && policy.VIP.RequireBeforePlayback && !vip.Active
	// 先完成广告的访问不提前签发正片凭据；配置延后时只给首集现有权益或受限试看。
	if (needsAd || needsVIP) && (episode.Number != 1 || policy.EntryAdDelaySeconds == 0) {
		return entry, nil
	}
	entry.Play, err = s.resolveLoadedPlayback(ctx, visitorID, drama, episode, &policy, vip)
	if err != nil {
		slog.ErrorContext(ctx, "resolve playback entry source failed", "error", err)
		return nil, err
	}
	return entry, nil
}
