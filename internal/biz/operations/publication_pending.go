package operations

import (
	"context"
	"log/slog"
	"slices"

	domain "drama-server/internal/biz/consumer"
)

// CanPrepareNextBatch 仅允许连载剧在送审至上线期间准备下一批；旧任务无快照时保持冻结。
func CanPrepareNextBatch(drama domain.Drama, publication TikTokPublication) bool {
	if drama.PublishStatus != "publishing" || drama.Status != domain.DramaStatusUpdating || publication.ContentSnapshot == nil {
		return false
	}
	switch publication.Stage {
	case PublicationStageSubmitReview, PublicationStagePollReview, PublicationStagePollAppeal, PublicationStageAwaitPublish, PublicationStageSetOnlineVersion, PublicationStageListAlbum:
		return publication.Status != PublicationStatusRejected && publication.Status != PublicationStatusOffline
	default:
		return false
	}
}

// NextBatchEpisodeMutable 检查分集不属于本次冻结快照，且位于冻结集序之后。
func NextBatchEpisodeMutable(drama domain.Drama, publication TikTokPublication, episode domain.Episode) bool {
	if !CanPrepareNextBatch(drama, publication) || episode.DramaID != drama.ID {
		return false
	}
	for _, frozen := range publication.ContentSnapshot.Episodes {
		if frozen.ID == episode.ID || episode.Number <= frozen.Number {
			return false
		}
	}
	return true
}

// ValidNextBatchEdit 校验完整替换请求保留剧资料和冻结分集，只允许调整下一批分集。
// 仓储必须在锁定短剧、当前任务和分集后再次调用，不能依赖请求前的状态检查。
func ValidNextBatchEdit(previous, next domain.Drama, publication TikTokPublication) bool {
	if !CanPrepareNextBatch(previous, publication) || previous.Title != next.Title || previous.Description != next.Description ||
		previous.CoverURL != next.CoverURL || previous.Status != next.Status || previous.Year != next.Year ||
		previous.Language != next.Language || previous.DramaType != next.DramaType || !slices.Equal(previous.TagIDs, next.TagIDs) {
		return false
	}
	byID := make(map[string]domain.Episode, len(next.Episodes))
	for _, episode := range next.Episodes {
		byID[episode.ID] = episode
	}
	for _, frozen := range publication.ContentSnapshot.Episodes {
		ep, exists := byID[frozen.ID]
		if !exists || ep.Number != frozen.Number || ep.Title != frozen.Title || ep.Description != frozen.Description {
			return false
		}
	}
	for _, episode := range next.Episodes {
		if NextBatchEpisodeMutable(previous, publication, episode) {
			continue
		}
		if !slices.ContainsFunc(publication.ContentSnapshot.Episodes, func(frozen domain.Episode) bool { return frozen.ID == episode.ID && frozen.Number == episode.Number }) {
			return false
		}
	}
	return true
}

// HasPendingEpisodes 判断当前编辑区是否含本次上线快照之外的分集，避免上线完成吞掉下一批待办。
func HasPendingEpisodes(current, online domain.Drama) bool {
	ids := make(map[string]bool, len(online.Episodes))
	for _, episode := range online.Episodes {
		ids[episode.ID] = true
	}
	return slices.ContainsFunc(current.Episodes, func(episode domain.Episode) bool { return !ids[episode.ID] })
}

func (s *DramaService) episodeContentMutable(ctx context.Context, drama domain.Drama, episode domain.Episode) (bool, error) {
	if dramaContentMutable(drama) {
		return true, nil
	}
	if drama.PublishStatus != "publishing" || drama.Status != domain.DramaStatusUpdating || s.repositories.Content.Publications == nil {
		return false, nil
	}
	publication, err := s.repositories.Content.Publications.FindTikTokPublicationByDramaID(ctx, drama.ID)
	if err != nil {
		slog.ErrorContext(ctx, "check next batch media editability failed", "error", err)
		return false, err
	}
	return NextBatchEpisodeMutable(drama, publication, episode), nil
}
