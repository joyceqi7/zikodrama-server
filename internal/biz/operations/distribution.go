package operations

import (
	"context"
	"drama-server/internal/biz/application"
	"log/slog"
	"strings"
	"time"
)

const distributionReconcileInterval = 10 * time.Minute

// DistributionSource 是可分发剧目的平台归属，不加载剧集内容。
type DistributionSource struct {
	// SubjectID 限定同主体应用可以授权此 Album。
	SubjectID string
	// DramaID 标识共享短剧。
	DramaID string
	// OwnerID 保留创建 Album 时的实际拥有方。
	OwnerID string
	// AlbumID 是官方剧目标识。
	AlbumID string
	// Online 表示已有可播放的线上版本。
	Online bool
}

// DramaDistribution 保存一部剧在一个应用的独立授权状态。
type DramaDistribution struct {
	// DramaID 标识共享短剧。
	DramaID string `json:"dramaId"`
	// ApplicationID 标识目标小程序。
	ApplicationID string `json:"applicationId"`
	// OwnerApplicationID 标识负责官方授权调用的拥有方。
	OwnerApplicationID string `json:"ownerApplicationId"`
	// AlbumID 标识官方剧目。
	AlbumID string `json:"albumId"`
	// Enabled 表示运营是否允许该剧分发到此应用。
	Enabled bool `json:"enabled"`
	// Desired 表示结合应用与上线状态后的授权目标。
	Desired bool `json:"desiredAuthorized"`
	// Status 表示 pending、authorized、revoked、failed 或 attention_required。
	Status string `json:"status"`
	// Attempts 是当前目标下连续失败次数。
	Attempts int `json:"attempts"`
	// ErrorCode 是不含平台敏感响应的错误分类。
	ErrorCode string `json:"errorCode"`
	// NextAttemptAt 保留旧响应字段；同步授权不自动重试，确认后置为零值。
	NextAttemptAt time.Time `json:"nextAttemptAt"`
	// Revision 记录授权事实修订，阻止历史调用覆盖新结果。
	Revision int64 `json:"-"`
	// LeaseOwner 保留历史存储兼容；新的同步授权不领取任务。
	LeaseOwner string `json:"-"`
	// LeaseExpiresAt 限定远端调用占用时间。
	LeaseExpiresAt time.Time `json:"-"`
}

// ApplicationDramaAuthorizationQuery 是按应用查看线上短剧授权状态的分页条件。
type ApplicationDramaAuthorizationQuery struct {
	// Page 从 1 开始。
	Page int
	// PageSize 是单页数量，HTTP 入口限制为最多 100。
	PageSize int
	// Query 按短剧标题搜索。
	Query string
	// View 区分未授权的 available 与已关联授权流程的 tracked。
	View string
}

// ApplicationDramaAuthorizationItem 是一部线上短剧对目标应用的有效授权状态。
type ApplicationDramaAuthorizationItem struct {
	// ID 是短剧内部标识，始终按字符串传输。
	ID string `json:"id"`
	// Title 是当前短剧标题。
	Title string `json:"title"`
	// Status 区分未授权、发布方、已授权、处理中及失败状态。
	Status string `json:"status"`
}

// ApplicationDramaAuthorizationPage 是指定应用的线上短剧授权分页。
type ApplicationDramaAuthorizationPage struct {
	// Items 是当前页短剧及状态。
	Items []ApplicationDramaAuthorizationItem `json:"items"`
	// Total 是同一视图和搜索条件下的短剧总数。
	Total int64 `json:"total"`
}

// DistributionCall 在请求内执行一次官方授权或撤权，不查询媒体或审核流程。
type DistributionCall func(context.Context, DramaDistribution) error

// SynchronousDistributionRepository 保存请求内调用的确认结果，复用原关系表。
type SynchronousDistributionRepository interface {
	// ApplyDistribution 核对当前应用与线上内容，调用平台并保存成功或失败事实。
	ApplyDistribution(context.Context, string, string, bool, bool, DistributionCall) error
}

// DistributionRepository 保存授权策略和平台确认结果，不承担授权任务队列。
type DistributionRepository interface {
	// DistributionSources 返回所有具有官方归属的短剧摘要。
	DistributionSources(context.Context) ([]DistributionSource, error)
	// FindDistributionSource 返回指定短剧的官方归属；尚未同步到平台时返回空结果。
	FindDistributionSource(context.Context, string) (*DistributionSource, error)
	// EnsureDistribution 为应用和剧目补齐关系，并按上线及应用状态更新目标；保留人工撤权。
	EnsureDistribution(context.Context, DistributionSource, application.Application) (bool, error)
	// ListDistributions 返回某部剧的分发进度。
	ListDistributions(context.Context, string) ([]DramaDistribution, error)
	// ListApplicationDramaAuthorizations 只读分页查询指定应用的线上短剧及有效授权状态，不逐剧调用平台。
	ListApplicationDramaAuthorizations(context.Context, string, ApplicationDramaAuthorizationQuery) (ApplicationDramaAuthorizationPage, error)
}

// ApplicationDramaAuthorizations 返回指定应用的线上短剧授权视图；应用不存在时沿用注册表的未找到错误。
func (s *DramaService) ApplicationDramaAuthorizations(ctx context.Context, appID string, query ApplicationDramaAuthorizationQuery) (ApplicationDramaAuthorizationPage, error) {
	if s.repositories.Content.Distributions == nil {
		slog.ErrorContext(ctx, "list application drama authorizations unavailable", "error", ErrPublicationUnavailable)
		return ApplicationDramaAuthorizationPage{}, ErrPublicationUnavailable
	}
	_, err := s.repositories.Application.Registry.FindApplication(ctx, appID)
	if err != nil {
		slog.ErrorContext(ctx, "find application for drama authorizations failed", "error", err)
		return ApplicationDramaAuthorizationPage{}, err
	}

	page, err := s.repositories.Content.Distributions.ListApplicationDramaAuthorizations(ctx, appID, query)
	if err != nil {
		slog.ErrorContext(ctx, "list application drama authorizations failed", "error", err)
	}
	return page, err
}

// DistributionRevoker 是官方移除跨应用授权的能力边界。
type DistributionRevoker interface {
	// RevokeAlbum 移除给定目标应用的剧目权限，并检查逐项结果。
	RevokeAlbum(context.Context, string, []string) error
}

// DramaDistributions 只读返回授权关系，不触发授权或对账。
func (s *DramaService) DramaDistributions(ctx context.Context, id string) ([]DramaDistribution, error) {
	if s.repositories.Content.Distributions == nil {
		return nil, ErrPublicationUnavailable
	}
	rows, err := s.repositories.Content.Distributions.ListDistributions(ctx, id)
	if err != nil {
		slog.ErrorContext(ctx, "list drama distributions failed", "error", err)
	}
	return rows, err
}

// SetDramaDistribution 发起独立授权或撤权，不重新上传、转码或送审。
func (s *DramaService) SetDramaDistribution(ctx context.Context, id, appID string, enabled bool) error {
	if s.repositories.Content.Distributions == nil {
		slog.WarnContext(ctx, "official authorization repository unavailable", "error", ErrPublicationUnavailable)
		return ErrPublicationUnavailable
	}
	app, err := s.repositories.Application.Registry.FindApplication(ctx, appID)
	if err != nil {
		slog.ErrorContext(ctx, "find official distribution application failed", "error", err)
		return err
	}

	if selected := application.SubjectIDFromContext(ctx); selected != "" && selected != app.SubjectID {
		slog.WarnContext(ctx, "cross subject distribution rejected", "error", ErrConflict)
		return ErrConflict
	}
	ctx = application.WithSubject(ctx, app.SubjectID)

	repo, ok := s.repositories.Content.Distributions.(SynchronousDistributionRepository)
	if !ok {
		slog.WarnContext(ctx, "synchronous authorization repository unavailable", "error", ErrPublicationUnavailable)
		return ErrPublicationUnavailable
	}
	err = repo.ApplyDistribution(ctx, id, appID, enabled, false, s.applyOfficialDistribution)
	if err != nil {
		slog.ErrorContext(ctx, "apply official distribution in request failed", "drama_id", id, "application_id", appID, "error", err)
	}
	return err

}

// AuthorizeApplicationDramas 在请求内逐项授权选中的线上短剧，返回已确认数量；失败不会回滚平台已确认的授权。
func (s *DramaService) AuthorizeApplicationDramas(ctx context.Context, appID string, dramaIDs []string) (int, error) {
	if s.repositories.Content.Distributions == nil {
		slog.WarnContext(ctx, "official authorization repository unavailable", "error", ErrPublicationUnavailable)
		return 0, ErrPublicationUnavailable
	}
	app, err := s.repositories.Application.Registry.FindApplication(ctx, appID)
	if err != nil {
		slog.ErrorContext(ctx, "find distribution application failed", "error", err)
		return 0, err
	}
	if app.Status != application.StatusActive || strings.TrimSpace(app.ClientKey) == "" {
		slog.WarnContext(ctx, "authorize application dramas rejected", "error", ErrConflict)
		return 0, ErrConflict
	}
	if selected := application.SubjectIDFromContext(ctx); selected != "" && selected != app.SubjectID {
		slog.WarnContext(ctx, "cross subject batch authorization rejected", "error", ErrConflict)
		return 0, ErrConflict
	}
	ctx = application.WithSubject(ctx, app.SubjectID)
	sources, err := s.repositories.Content.Distributions.DistributionSources(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "validate batch authorization sources failed", "error", err)
		return 0, err
	}
	available := map[string]bool{}
	for _, source := range sources {
		available[source.DramaID] = source.Online
	}
	seen := map[string]bool{}
	for _, id := range dramaIDs {
		if seen[id] || !available[id] {
			slog.WarnContext(ctx, "official authorization source rejected", "error", ErrConflict)
			return 0, ErrConflict
		}
		seen[id] = true
	}
	count := 0
	for _, id := range dramaIDs {
		if err := s.SetDramaDistribution(ctx, id, appID, true); err != nil {
			slog.ErrorContext(ctx, "authorize selected drama failed", "error", err)
			return count, err
		}
		count++
	}
	return count, nil

}

// ReconcileDistributions 补齐新增应用与新上线剧的双向分发，重启后可再次执行。
func (s *DramaService) ReconcileDistributions(ctx context.Context) error {
	repo := s.repositories.Content.Distributions
	if repo == nil {
		return nil
	}
	sources, err := repo.DistributionSources(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "read distribution sources failed", "error", err)
		return err
	}
	apps, err := s.repositories.Application.Registry.ListApplications(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "read distribution applications failed", "error", err)
		return err
	}
	var firstErr error
	for _, source := range sources {
		if err := s.reconcileDistributionSource(ctx, source, apps); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// reconcileDistributionSource 只补齐一部短剧需要新增或变更的应用关系。
func (s *DramaService) reconcileDistributionSource(ctx context.Context, source DistributionSource, apps []application.Application) error {
	ctx = application.WithSubject(ctx, source.SubjectID)
	repo := s.repositories.Content.Distributions
	rows, err := repo.ListDistributions(ctx, source.DramaID)
	if err != nil {
		slog.ErrorContext(ctx, "read existing distributions failed", "error", err)
		return err
	}
	existing := make(map[string]DramaDistribution, len(rows))
	for _, row := range rows {
		existing[row.ApplicationID] = row
	}
	for _, app := range apps {
		if app.SubjectID != source.SubjectID {
			continue
		}
		current, found := existing[app.ID]
		available := source.Online && app.Status == application.StatusActive && app.ClientKey != ""
		// 周期对账只写新关系和已变化的意图，避免无效锁定内容行。
		if !found && !available {
			continue
		}
		if found && current.Desired == (available && current.Enabled) {
			continue
		}
		if _, err := repo.EnsureDistribution(ctx, source, app); err != nil {
			slog.ErrorContext(ctx, "reconcile drama distribution failed", "drama_id", source.DramaID, "application_id", app.TikTokAppID, "error", err)
			return err
		}
	}
	rows, err = repo.ListDistributions(ctx, source.DramaID)
	if err != nil {
		slog.ErrorContext(ctx, "read reconciled authorization facts failed", "error", err)
		return err
	}
	var firstErr error
	direct, ok := repo.(SynchronousDistributionRepository)
	if ok {
		for _, row := range rows {
			if row.Status == "pending" {
				if err := direct.ApplyDistribution(ctx, source.DramaID, row.ApplicationID, row.Enabled, true, s.applyOfficialDistribution); err != nil {
					slog.ErrorContext(ctx, "apply changed authorization policy failed", "error", err)
					if firstErr == nil {
						firstErr = err
					}
				}
			}
		}
	}
	return firstErr
}

func (s *DramaService) runDistributions(ctx context.Context) {
	ticker := time.NewTicker(distributionReconcileInterval)
	defer ticker.Stop()
	for {
		if err := s.ReconcileDistributions(ctx); err != nil {
			slog.ErrorContext(ctx, "reconcile official authorization policy failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// applyOfficialDistribution 调用同步授权接口，失败直接交给请求或策略对账边界。
func (s *DramaService) applyOfficialDistribution(ctx context.Context, job DramaDistribution) error {
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	owner, err := s.loadPublicationOwner(callCtx, job.OwnerApplicationID)
	if err == nil {
		var target application.Application
		target, err = s.repositories.Application.Registry.FindApplication(callCtx, job.ApplicationID)
		if err == nil {
			var provider TikTokMediaProvider
			if owner.TikTokAppID != target.TikTokAppID && s.tiktokProviderFactory != nil {
				provider = s.tiktokProviderFactory(owner)
			}
			// 官方授权绑定应用身份，当前播放通道可切换，不能因此阻止预授权或撤权。
			if owner.TikTokAppID == target.TikTokAppID {
				// 拥有方无需向自己授权；只同步本站数字应用 ID 的可见性。
				if job.Desired && target.Status != application.StatusActive {
					err = ErrConflict
				}
			} else if provider == nil {
				err = ErrPublicationUnavailable
			} else if job.Desired && target.Status != application.StatusActive {
				err = ErrConflict
			} else if job.Desired {
				err = provider.AuthorizeAlbum(callCtx, job.AlbumID, []string{target.ClientKey})
			} else if revoke, ok := provider.(DistributionRevoker); ok {
				err = revoke.RevokeAlbum(callCtx, job.AlbumID, []string{target.ClientKey})
			} else {
				err = ErrPublicationUnavailable
			}
		}
	}
	if err != nil {
		slog.ErrorContext(ctx, "call official authorization failed", "drama_id", job.DramaID, "application_id", job.ApplicationID, "error", err)
	}
	return err
}
