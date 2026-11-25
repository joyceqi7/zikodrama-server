package operations

import (
	"context"
	"log/slog"
	"time"

	domain "drama-server/internal/biz/consumer"
)

// reconcileWithdrawReview 将撤回前后查到的终态同步到任务；已结束的审核不再停留在撤回队列。
// 返回 true 表示已处理终态，审核仍进行或状态尚未可见时由调用方继续处理。
func (runner *publicationJobRunner) reconcileWithdrawReview(ctx context.Context, publication *TikTokPublication, drama *domain.Drama, observation AlbumObservation) bool {
	// 整版被拒可能是申诉期间保留的旧结果，单项申诉尚无终态时仍允许撤回。
	if observation.ReviewStatus == 3 && publication.Appeal != nil {
		state := observation.ReviewItems[publication.Appeal.ItemID]
		if state != 6 && state != 7 {
			return false
		}
	}
	switch observation.ReviewStatus {
	case 2, 3, 4, 6, 7:
		// 使用统一审核投影：只有状态 4 恢复为已撤回草稿，通过仍等待人工上线，申诉被拒仍保留审核意见。
		publication.Stage = PublicationStagePollReview
		if err := applyTikTokReviewObservation(publication, drama, observation, time.Now().UTC()); err != nil {
			slog.ErrorContext(ctx, "apply withdraw review observation failed", "error", err)
			runner.fail(ctx, *publication, err)
			return true
		}
		outcome := "withdraw_skipped_review_completed"
		if observation.ReviewStatus == 4 {
			outcome = "review_withdrawn"
		}
		runner.persist(ctx, *publication, drama, outcome)
		return true
	default:
		return false
	}
}
