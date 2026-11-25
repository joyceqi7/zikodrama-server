package operations

import (
	"context"
	"log/slog"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
)

// RefreshTikTokReview 为自动查询失败后的人工兜底，按内部短剧标识查询一次当前冻结版本，不上线。
// 审核中、待上线及审核查询失败可更新状态；送审结果未知时只记录官方观察，不清除写入检查点或重发请求。
func (s *DramaService) RefreshTikTokReview(ctx context.Context, dramaID string) (*TikTokPublication, error) {
	if s == nil || s.tiktokProviderFactory == nil {
		slog.WarnContext(ctx, "refresh TikTok review unavailable", "error", ErrPublicationUnavailable)
		return nil, ErrPublicationUnavailable
	}
	p, err := s.FindTikTokPublication(ctx, dramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find manual review publication failed", "error", err)
		return nil, err
	}
	queryStage := p.Stage == PublicationStagePollReview || p.Stage == PublicationStagePollAppeal || p.Stage == PublicationStageAwaitPublish
	queryStatus := p.Status == PublicationStatusUnderReview || p.Status == PublicationStatusApproved || p.Status == PublicationStatusFailed
	uncertainSubmit := p.Status == PublicationStatusFailed && p.Stage == PublicationStageSubmitReview &&
		p.ErrorCode == "external_write_uncertain" && p.ExternalWriteStarted && p.ReviewID == ""
	if ((!queryStage || !queryStatus || p.ExternalWriteStarted || p.ReviewID == "") && !uncertainSubmit) ||
		p.PlatformAlbumID == "" || p.PlatformVersion < 1 || publicationLeaseActive(*p) {
		slog.WarnContext(ctx, "refresh TikTok review rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	drama, err := s.repositories.Content.Catalog.FindDrama(ctx, dramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find manual review drama failed", "error", err)
		return nil, err
	}
	owner, err := s.loadPublicationOwner(ctx, p.OwnerApplicationID)
	if err != nil {
		slog.ErrorContext(ctx, "load manual review owner failed", "error", err)
		return nil, err
	}
	if !manualPublicationOwnerMatches(*p, owner) {
		slog.WarnContext(ctx, "manual review owner changed", "error", ErrPublicationNotReady)
		return nil, ErrPublicationNotReady
	}
	provider := s.tiktokProviderFactory(owner)
	if provider == nil {
		slog.WarnContext(ctx, "manual review provider unavailable", "error", ErrPublicationUnavailable)
		return nil, ErrPublicationUnavailable
	}
	observation, err := provider.QueryAlbum(ctx, p.PlatformAlbumID, p.PlatformVersion)
	if err != nil {
		slog.ErrorContext(ctx, "query TikTok review manually failed", "error", err)
		return nil, err
	}
	now := time.Now().UTC()
	if uncertainSubmit {
		// QueryAlbum 只返回此 Album/version 的审核状态，不返回 review_id；旧审核状态也可能仍可见。
		// 因此只保存诊断事实，不能据此解除非幂等写标记或自动重发送审。
		if observation.ReviewStatus < 0 || observation.ReviewStatus > 7 {
			err := &PlatformCallError{Code: "unknown_review_status"}
			slog.ErrorContext(ctx, "inspect uncertain TikTok review failed", "error", err)
			return nil, err
		}
		p.ReviewStatus = observation.ReviewStatus
		p.ReviewItems = observation.ReviewItems
		p.ReviewFailReasons = append([]string(nil), observation.ReviewFailReasons...)
		p.PlatformPublishStatus = observation.PublishStatus
		p.UpdatedAt = now
		if err := s.repositories.Content.Publications.UpdateTikTokPublication(ctx, *p, nil); err != nil {
			slog.ErrorContext(ctx, "save uncertain TikTok review observation failed", "error", err)
			return nil, err
		}
		publicationLog(ctx, slog.LevelInfo, *p, "review_inspected_without_replay", p.ErrorCode)
		return p, nil
	}
	if err := applyTikTokReviewObservation(p, drama, observation, now); err != nil {
		slog.ErrorContext(ctx, "apply manual review observation failed", "error", err)
		return nil, err
	}
	if p.Status == PublicationStatusUnderReview && s.publicationJobs != nil {
		p.NextAttemptAt = now.Add(s.publicationJobs.reviewPollDelay(*p))
	}
	// 仓储比较修订号与租约，查询期间发生的撤回、上线或其他更新不能被旧结果覆盖。
	if err := s.repositories.Content.Publications.UpdateTikTokPublication(ctx, *p, drama); err != nil {
		slog.ErrorContext(ctx, "save manual review observation failed", "error", err)
		return nil, err
	}
	publicationLog(ctx, slog.LevelInfo, *p, "review_refreshed_manually", p.ErrorCode)
	return p, nil
}

// applyTikTokReviewObservation 把一次官方查询投影为稳定业务状态，供自动轮询与人工兜底复用。
func applyTikTokReviewObservation(p *TikTokPublication, drama *domain.Drama, observation AlbumObservation, now time.Time) error {
	if observation.ReviewStatus < 0 || observation.ReviewStatus > 7 {
		return &PlatformCallError{Code: "unknown_review_status"}
	}
	p.ReviewStatus, p.ReviewItems = observation.ReviewStatus, observation.ReviewItems
	p.ReviewFailReasons = append([]string(nil), observation.ReviewFailReasons...)
	p.PlatformPublishStatus = observation.PublishStatus
	p.ErrorCode, p.ErrorMessage, p.Attempts = "", "", 0
	p.PublishRequested = false
	p.Status = PublicationStatusUnderReview
	p.NextAttemptAt = time.Time{}
	if p.Stage == PublicationStageAwaitPublish {
		p.Stage = PublicationStagePollReview
	}
	appealPending := p.Stage == PublicationStagePollAppeal && p.Appeal != nil && p.ReviewStatus == 3 &&
		p.ReviewItems[p.Appeal.ItemID] != 6 && p.ReviewItems[p.Appeal.ItemID] != 7
	switch {
	case appealPending:
		// 单项申诉受理后可能仍返回旧驳回结果，只更新观察事实，不重复提交申诉。
		if p.SubmittedAt != nil && now.Sub(*p.SubmittedAt) > 7*24*time.Hour {
			p.Status, p.ErrorCode = PublicationStatusFailed, "appeal_result_pending"
			p.ErrorMessage = publicationFriendlyMessage(p.ErrorCode, false)
		}
	case p.ReviewStatus == 2 || p.ReviewStatus == 6:
		p.Status, p.Stage = PublicationStatusApproved, PublicationStageAwaitPublish
	case p.ReviewStatus == 3 || p.ReviewStatus == 7:
		p.Status, p.ErrorCode = PublicationStatusRejected, "review_rejected"
		p.ErrorMessage = "TikTok 审核未通过，请根据平台审核意见修改后重新提交。"
		// 审核驳回先停在待处理状态，只有运营保存修改后才重新成为可同步草稿。
		drama.PublishStatus = "rejected"
	case p.ReviewStatus == 4:
		p.Status, p.Stage, p.ErrorCode = PublicationStatusRejected, PublicationStageDone, "review_withdrawn"
		p.ErrorMessage = "本次 TikTok 审核已撤回，可保留现有内容并选择审核优先级重新提交。"
		drama.PublishStatus = "draft"
	}
	p.UpdatedAt = now
	return nil
}

// PublishTikTokPublication 在当前请求内选择已审核版本并上架，冻结版本不受新草稿影响。
func (s *DramaService) PublishTikTokPublication(ctx context.Context, dramaID, publicationID string) (*TikTokPublication, error) {
	return s.executeTikTokRelease(ctx, dramaID, TikTokReleaseInput{Action: "publish", SelectedPublicationID: publicationID})
}

// publicationLeaseActive 允许已过期的旧租约通过修订号竞争恢复，活动 Worker 仍独占写入。
func publicationLeaseActive(p TikTokPublication) bool {
	return p.LeaseOwner != "" && p.LeaseExpiresAt.After(time.Now().UTC())
}

// manualPublicationOwnerMatches 禁止在手动查询时把冻结的官方资源带到另一账号或空间。
func manualPublicationOwnerMatches(p TikTokPublication, owner application.Application) bool {
	return p.OwnerScope == (PublicationOwnerScope{Region: p.OwnerScope.Region, ClientKey: owner.ClientKey, AccountID: owner.BytePlusAccountID, SpaceName: owner.BytePlusSpaceName})
}
