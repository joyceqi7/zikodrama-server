package operations

import (
	"context"
	"log/slog"
	"time"
)

// AppealTikTokReview 在当前请求内提交被拒项目的申诉，只有结果查询保留后台处理。
func (s *DramaService) AppealTikTokReview(ctx context.Context, dramaID string, input TikTokReviewAppealInput) (*TikTokPublication, error) {
	return s.executeReviewAction(ctx, dramaID, &input)
}

// WithdrawTikTokReview 在当前请求内撤回审核，保留当前线上内容。
func (s *DramaService) WithdrawTikTokReview(ctx context.Context, dramaID string) (*TikTokPublication, error) {
	return s.executeReviewAction(ctx, dramaID, nil)
}

// executeReviewAction 用已有修订号与租约原子替换待处理阶段，避免和 Worker 同时写平台。
func (s *DramaService) executeReviewAction(ctx context.Context, dramaID string, appeal *TikTokReviewAppealInput) (*TikTokPublication, error) {
	p, err := s.FindTikTokPublication(ctx, dramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find review action publication failed", "error", err)
		return nil, err
	}
	reject := func() (*TikTokPublication, error) {
		slog.WarnContext(ctx, "queue review action rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	if p.ExternalWriteStarted || p.LeaseOwner != "" || p.PlatformAlbumID == "" || p.PlatformVersion < 1 || p.ReviewID == "" {
		return reject()
	}
	if appeal != nil {
		if p.Status != PublicationStatusRejected || (p.ReviewStatus != 3 && p.ReviewStatus != 7) {
			return reject()
		}
		belongs := appeal.ItemType == 1 && appeal.ItemID == p.PlatformAlbumID
		if appeal.ItemType == 2 {
			for _, id := range p.EpisodeIDsBySequence {
				if id == appeal.ItemID {
					belongs = true
				}
			}
		}
		if !belongs {
			return reject()
		}
		// 有单项结果时只受理明确被拒（3）的对象，整版被拒不能代替剧目信息的单项结果。
		// 完全缺少单项结果的历史任务仍在当前请求内查询，不推断未返回的项目状态。
		if state, known := p.ReviewItems[appeal.ItemID]; (known && state != 3) || (!known && len(p.ReviewItems) > 0) {
			return reject()
		}
		p.Appeal, p.Stage = appeal, PublicationStageAppealReview
	} else {
		if p.Status != PublicationStatusUnderReview || (p.Stage != PublicationStagePollReview && p.Stage != PublicationStagePollAppeal) {
			return reject()
		}
		p.Stage = PublicationStageWithdrawReview
	}
	p.ErrorCode, p.ErrorMessage = "", ""
	p.Attempts = 0
	p.NextAttemptAt, p.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	return s.executePublicationCall(ctx, p)
}
