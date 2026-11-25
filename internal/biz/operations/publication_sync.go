package operations

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// TikTokExecutionRepository 用现有修订与租约保护请求内的官方写入，不把请求交给 Worker。
type TikTokExecutionRepository interface {
	// BeginTikTokPublicationCall 核对当前版本及修订，保存请求的阶段并取得短期独占权。
	BeginTikTokPublicationCall(context.Context, *TikTokPublication) error
}
type publicationCallKey struct{}

// albumUpdateRetryKey 仅在本次人工命令中允许受版本保护的恢复，不持久化或传递给 Worker。
type albumUpdateRetryKey struct{}

// albumUpdateErrorLevel 将尚未确认的查询结果视为待办；实际平台和存储故障仍记录错误。
func albumUpdateErrorLevel(err error) slog.Level {
	var failure *PlatformCallError
	if errors.As(err, &failure) && failure.Code == "album_update_unconfirmed" {
		return slog.LevelWarn
	}
	return slog.LevelError
}

type publicationCallState struct {
	result *TikTokPublication
	err    error
}

func publicationCall(ctx context.Context) *publicationCallState {
	value, _ := ctx.Value(publicationCallKey{}).(*publicationCallState)
	return value
}

// PublicationReadState 只投影已停止调用的人工待办，不改写历史记录或自动重发平台请求。
// 升级前排队的同步步骤和失效请求租约都允许页面显示恢复入口；未知写入仍须先核对。
func PublicationReadState(p TikTokPublication, now time.Time) TikTokPublication {
	if p.LeaseOwner != "" && p.LeaseExpiresAt.After(now) {
		return p
	}
	legacyQueued := p.Status == PublicationStatusQueued && p.Stage != PublicationStageRegisterVideos && p.Stage != PublicationStageTranscodeVideos && p.Stage != PublicationStagePollReview && p.Stage != PublicationStagePollAppeal
	interrupted := p.Status == PublicationStatusProcessing && (p.LeaseOwner != "" || !p.UpdatedAt.After(now.Add(-defaultPublicationLease)))
	if !legacyQueued && !interrupted {
		return p
	}
	p.Status = PublicationStatusFailed
	if p.ExternalWriteStarted || p.ErrorCode == "external_write_uncertain" {
		p.ErrorCode = "external_write_uncertain"
		p.ErrorMessage = publicationFriendlyMessage(p.ErrorCode, false)
	} else if p.ErrorCode == "" {
		p.ErrorCode = "dependency_error"
		p.ErrorMessage = "上次操作尚未完成，请核对后继续。系统不会自动重发平台请求。"
	}
	return p
}

// executePublicationCall 同步推进所有立即可完成步骤，仅在平台媒体或审核结果尚未完成时退出。
func (s *DramaService) executePublicationCall(ctx context.Context, p *TikTokPublication) (*TikTokPublication, error) {
	repo, ok := s.repositories.Content.Publications.(TikTokExecutionRepository)
	if !ok || s.publicationJobs == nil {
		slog.WarnContext(ctx, "synchronous official operation unavailable", "error", ErrPublicationUnavailable)
		return nil, ErrPublicationUnavailable
	}
	requestID, err := secureID("publication-call")
	if err != nil {
		slog.ErrorContext(ctx, "create official request lease failed", "error", err)
		return nil, err
	}
	p.LeaseOwner = requestID
	p.LeaseExpiresAt = time.Now().UTC().Add(s.publicationJobs.leaseDuration)
	p.Status = PublicationStatusProcessing
	p.UpdatedAt = time.Now().UTC()
	if err = repo.BeginTikTokPublicationCall(ctx, p); err != nil {
		slog.Log(ctx, publicationErrorLevel(err), "begin synchronous official operation failed", "error", err)
		return nil, err
	}
	return s.publicationJobs.executeClaimed(ctx, *p)
}

func (runner *publicationJobRunner) executeClaimed(ctx context.Context, p TikTokPublication) (*TikTokPublication, error) {
	state := &publicationCallState{}
	callCtx := context.WithValue(ctx, publicationCallKey{}, state)
	for {
		state.result = nil
		runner.processClaimed(callCtx, p)
		if state.err != nil {
			return state.result, state.err
		}
		if state.result == nil {
			slog.ErrorContext(ctx, "official operation did not save a result", "publication_id", p.ID, "error", ErrConflict)
			return nil, ErrConflict
		}
		p = *state.result
		if p.Status != PublicationStatusProcessing {
			return &p, nil
		}
		if err := ctx.Err(); err != nil {
			runner.fail(callCtx, p, err)
			return state.result, err
		}
	}
}
