package operations

import (
	"context"
	domain "drama-server/internal/biz/consumer"
	"log/slog"
	"time"
)

// TikTokReleaseState 是已选官方版本的确认事实，与新内容的同步和审核记录独立。
type TikTokReleaseState struct {
	// SelectedPublicationID 指向此版本原有的冻结内容记录，不产生上下架任务。
	SelectedPublicationID string `json:"selectedPublicationId,omitempty"`
	// Version 是明确选定的官方版本，下架后仍保留。
	Version int `json:"version"`
	// Status 是平台确认的 online/offline 状态。
	Status string `json:"status"`
}

// TikTokReleaseInput 绑定页面确认的版本，防止迟到请求操作另一版本。
type TikTokReleaseInput struct {
	// Action 是 list/delist；内部 publish 用于明确选择已审核的新版本。
	Action string `json:"action"`
	// SelectedPublicationID 是原有冻结版本记录标识。
	SelectedPublicationID string `json:"selectedPublicationId"`
}

// TikTokReleaseCall 在剧目串行门禁内调用平台，返回已确认的版本和消费者快照。
// 平台结果无法确认时保留最后确认事实，不把失败包装为排队成功。
type TikTokReleaseCall func(context.Context, TikTokPublication, domain.Drama) (TikTokPublication, *domain.Drama, error)

// TikTokReleaseRepository 复用现有上架记录保存已选版本，不创建任务或新字段。
type TikTokReleaseRepository interface {
	// FindTikTokReleaseState 只读最后确认的已选版本。
	FindTikTokReleaseState(context.Context, string) (*TikTokReleaseState, error)
	// ExecuteTikTokRelease 串行核对版本和推荐引用，在请求内调用平台并原子保存确认结果。
	ExecuteTikTokRelease(context.Context, string, TikTokReleaseInput, TikTokReleaseCall) (*TikTokPublication, error)
}

// FindTikTokReleaseState 返回本地已确认版本，不调用平台或写入数据。
func (s *DramaService) FindTikTokReleaseState(ctx context.Context, dramaID string) (*TikTokReleaseState, error) {
	repo, ok := s.repositories.Content.Publications.(TikTokReleaseRepository)
	if !ok {
		return nil, ErrPublicationUnavailable
	}
	state, err := repo.FindTikTokReleaseState(ctx, dramaID)
	if err != nil {
		slog.ErrorContext(ctx, "read official release state failed", "drama_id", dramaID, "error", err)
	}
	return state, err
}

// ChangeTikTokRelease 在当前请求内独立上下架已选版本，不重新上传、同步或送审。
func (s *DramaService) ChangeTikTokRelease(ctx context.Context, dramaID string, input TikTokReleaseInput) (*TikTokPublication, error) {
	if input.Action != "list" && input.Action != "delist" {
		return nil, ErrInvalidInput
	}
	return s.executeTikTokRelease(ctx, dramaID, input)
}

func (s *DramaService) executeTikTokRelease(ctx context.Context, dramaID string, input TikTokReleaseInput) (*TikTokPublication, error) {
	repo, ok := s.repositories.Content.Publications.(TikTokReleaseRepository)
	if !ok || s.tiktokProviderFactory == nil {
		return nil, ErrPublicationUnavailable
	}
	result, err := repo.ExecuteTikTokRelease(ctx, dramaID, input, func(callCtx context.Context, p TikTokPublication, drama domain.Drama) (TikTokPublication, *domain.Drama, error) {
		owner, e := s.loadPublicationOwner(callCtx, p.OwnerApplicationID)
		if e != nil {
			return p, nil, e
		}
		if !manualPublicationOwnerMatches(p, owner) {
			return p, nil, ErrPublicationNotReady
		}
		provider := s.tiktokProviderFactory(owner)
		if provider == nil {
			return p, nil, ErrPublicationUnavailable
		}
		if input.Action == "publish" {
			if e := provider.SetOnlineVersion(callCtx, p.PlatformAlbumID, p.PlatformVersion); e != nil {
				confirmed, qerr := s.publicationJobs.confirmOfficialOnlineState(callCtx, provider, p, false)
				if qerr != nil {
					slog.ErrorContext(callCtx, "confirm official selected version failed", "error", qerr)
				}
				if !confirmed {
					return p, nil, e
				}
				slog.WarnContext(callCtx, "set online version returned error but official state is confirmed", "error", e)
			}
		}
		now := time.Now().UTC()
		p.UpdatedAt = now
		p.Stage = PublicationStageDone
		p.ErrorCode, p.ErrorMessage = "", ""
		p.PublishRequested = true
		p.LeaseOwner = ""
		p.LeaseExpiresAt = time.Time{}
		online := domain.FreezeContent(*p.ContentSnapshot)
		online.PublishStatus = "published"
		online.PlatformAlbumID = p.PlatformAlbumID
		online.PlatformVersion = p.PlatformVersion
		online.OwnerApplicationID = p.OwnerApplicationID
		for i := range online.Episodes {
			online.Episodes[i].PlatformEpisodeID = p.EpisodeIDsBySequence[online.Episodes[i].Number]
		}
		if drama.Published != nil {
			online.AuthorizedApplicationIDs = append([]string(nil), drama.Published.AuthorizedApplicationIDs...)
		}
		listed := input.Action != "delist"
		if e := provider.SetAlbumListed(callCtx, p.PlatformAlbumID, listed); e != nil {
			observation, qerr := provider.QueryAlbum(callCtx, p.PlatformAlbumID, p.PlatformVersion)
			if qerr != nil {
				slog.ErrorContext(callCtx, "confirm official listing failed", "error", qerr)
			}
			expected := 2
			if listed {
				expected = 1
			}
			if qerr == nil && observation.PublishStatus == expected && observation.OnlineVersion == p.PlatformVersion {
				slog.WarnContext(callCtx, "set album listed returned error but official state is confirmed", "error", e)
			} else {
				// 版本选择已成功时必须保存这个事实，即使接下来的上架响应无法确认。
				// 已在线 Album 的版本立即切换；原来下线的 Album 保留新选择但仍按最后确认的下线状态展示。
				if input.Action == "publish" {
					p.Status, p.PlatformPublishStatus = PublicationStatusOffline, 2
					if drama.Published != nil {
						p.Status, p.PlatformPublishStatus = PublicationStatusOnline, 1
						p.OnlineAt = &now
						return p, &online, e
					}
					return p, nil, e
				}
				p.Stage = PublicationStageListAlbum
				return p, nil, e
			}
		}
		p.Status, p.PlatformPublishStatus = PublicationStatusOffline, 2
		if !listed {
			return p, nil, nil
		}
		p.Status, p.PlatformPublishStatus = PublicationStatusOnline, 1
		p.OnlineAt = &now
		return p, &online, nil
	})
	if err != nil {
		slog.Log(ctx, publicationErrorLevel(err), "change official release failed", "drama_id", dramaID, "action", input.Action, "error", err)
		return nil, err
	}
	publicationLog(ctx, slog.LevelInfo, *result, "release_confirmed", "")
	return result, nil
}

// ReleaseStateFromTasks 兼容尚未生成独立上架记录的历史冻结版本，仅认已完成的上下架事实。
func ReleaseStateFromTasks(tasks []TikTokPublication) *TikTokReleaseState {
	var selected *TikTokPublication
	for i := range tasks {
		p := &tasks[i]
		if (p.OnlineAt != nil || p.PublishRequested && p.Stage == PublicationStageDone) && p.ContentSnapshot != nil && (p.Status == PublicationStatusOnline || p.Status == PublicationStatusOffline) && (selected == nil || newerReleaseFact(*p, *selected)) {
			selected = p
		}
	}
	if selected == nil {
		return nil
	}
	return &TikTokReleaseState{SelectedPublicationID: selected.ID, Version: selected.PlatformVersion, Status: selected.Status}
}
func newerReleaseFact(a, b TikTokPublication) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return newerPublication(a, b)
}
func newerPublication(a, b TikTokPublication) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	return len(a.ID) > len(b.ID) || len(a.ID) == len(b.ID) && a.ID > b.ID
}

// ValidateSelectedRelease 检查已审核快照与原版本集号映射，不读取当前草稿。
func ValidateSelectedRelease(selected TikTokPublication, drama domain.Drama, action string) error {
	if action == "publish" && (selected.Status != PublicationStatusApproved || selected.Stage != PublicationStageAwaitPublish || publicationLeaseActive(selected) || selected.ExternalWriteStarted) {
		return ErrConflict
	}
	if selected.ContentSnapshot == nil || selected.PlatformAlbumID == "" || selected.PlatformVersion < 1 || (selected.ReviewStatus != 2 && selected.ReviewStatus != 6) || !completeEpisodeMapping(selected.ContentSnapshot.Episodes, selected.EpisodeIDsBySequence) {
		return ErrPublicationNotReady
	}

	if action != "publish" && drama.Published != nil && drama.Published.PlatformVersion != selected.PlatformVersion {
		return ErrConflict
	}
	return nil
}
