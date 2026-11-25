package tiktokmedia

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"drama-server/internal/biz/operations"
)

// RetryAlbumUpdate 仅供运营人工恢复：先核实已写入的新版本；确认官方仍停在原版本时，
// 携带该版本号恢复冻结内容。官方版本冲突保护阻止延迟的原请求与本次请求创建重复版本。
// 首次同步、查询缺项、版本推进或审核中均不重新写入，不能被 Worker 自动调用。
func (c *Client) RetryAlbumUpdate(ctx context.Context, draft operations.AlbumDraft) (operations.UpdateAlbumResult, error) {
	versions, diagnostic, err := c.queryAlbumUpdate(ctx, draft)
	if err != nil {
		return operations.UpdateAlbumResult{}, err
	}
	if !canRetryAlbumFromBase(draft, versions) {
		return resolveAlbumUpdate(ctx, draft, versions, diagnostic)
	}
	slog.InfoContext(ctx, "retry TikTok album update from confirmed base", "album_id", draft.AlbumID, "base_version", draft.Version)
	updated, updateErr := c.UpdateAlbum(ctx, draft)
	var failure *operations.PlatformCallError
	if errors.As(updateErr, &failure) && failure.Code == "album_version_conflict" {
		// 查询后原请求或其他操作者可能已创建版本。冲突只再次核对，绝不改用新版本号重放。
		confirmed, confirmErr := c.ResolveAlbumUpdate(ctx, draft)
		if confirmErr != nil {
			return operations.UpdateAlbumResult{}, fmt.Errorf("retry TikTok album update failed (%v): %w", updateErr, confirmErr)
		}
		return confirmed, nil
	}
	return updated, updateErr
}

// canRetryAlbumFromBase 要求原版本及其分集身份完整可见；并发保护不能以缺失或推测的版本号替代。
func canRetryAlbumFromBase(draft operations.AlbumDraft, versions []albumRecoveryVersion) bool {
	if draft.Version < 1 {
		return false
	}
	var base *albumRecoveryVersion
	for index := range versions {
		version := &versions[index]
		if version.AlbumID.String() != draft.AlbumID || version.Version > draft.Version || version.CurrentVersion != draft.Version {
			return false
		}
		if version.Version == draft.Version {
			if base != nil {
				return false
			}
			base = version
		}
	}
	if base == nil || base.ReviewStatus == nil ||
		!allowedAlbumRetryReviewStatus(*base.ReviewStatus) || base.AlbumInfo.SequenceCount < 1 ||
		len(base.Episodes) != base.AlbumInfo.SequenceCount {
		return false
	}
	ids := make(map[string]bool, len(base.Episodes))
	sequences := make(map[int]bool, len(base.Episodes))
	for _, episode := range base.Episodes {
		id := episode.EpisodeID.String()
		if _, err := numericID(id); err != nil || ids[id] || episode.Sequence < 1 || episode.Sequence > len(base.Episodes) || sequences[episode.Sequence] || strings.TrimSpace(episode.VID) == "" {
			return false
		}
		ids[id], sequences[episode.Sequence] = true, true
	}
	used := make(map[string]bool, len(draft.Episodes))
	for _, episode := range draft.Episodes {
		id := strings.TrimSpace(episode.PlatformEpisodeID)
		if id == "" {
			continue
		}
		if !ids[id] || used[id] {
			return false
		}
		used[id] = true
	}
	return true
}

func allowedAlbumRetryReviewStatus(status int) bool {
	switch status {
	case 0, 2, 3, 4, 6, 7:
		return true
	default:
		return false
	}
}
