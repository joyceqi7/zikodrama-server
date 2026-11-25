package operations

import (
	"context"
	"drama-server/internal/biz/application"
	content "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"errors"
	"log/slog"
)

// MiniReviewCatalog 仅提供审核清单校验所需的当前渠道剧目读取。
type MiniReviewCatalog interface {
	// FindDrama 返回当前主体渠道内容，具体应用可见性仍由已发布快照校验。
	FindDrama(context.Context, string) (*content.Drama, error)
	// FindVisibleSummaries 在已鉴权主体渠道管理上下文返回实际已发布内容。
	FindVisibleSummaries(context.Context, []string) ([]content.DramaSummary, error)
}

// ReviewWhitelist 是独立菜单维护的应用首页白名单，修订与应用审核开关共用。
type ReviewWhitelist struct {
	// Revision 防止旧清单覆盖最新配置。
	Revision int64 `json:"revision"`
	// DramaIDs 保留失效引用供运营移除。
	DramaIDs []string `json:"dramaIds"`
	// Dramas 是仍可见的线上摘要，缺失项由管理端标记不可用。
	Dramas []content.DramaSummary `json:"dramas"`
}

// ReviewSettings 返回独立菜单所需的版本、开关及修订，不泄露清单或应用密钥。
func (s *ApplicationService) ReviewSettings(ctx context.Context) (application.MiniReview, error) {
	app, err := s.FindApplication(ctx, application.IDFromContext(ctx))
	if err != nil {
		slog.ErrorContext(ctx, "read review settings application failed", "error", err)
		return application.MiniReview{}, err
	}
	result := app.MiniReview
	result.Versions = result.ConfiguredVersions()
	result.DramaIDs = nil
	return result, nil
}

// SaveReviewSettings 只保存审核版本与开关，沿用清单和 IAA 策略门禁，不整体回写应用资料。
func (s *ApplicationService) SaveReviewSettings(ctx context.Context, input application.MiniReview) (application.MiniReview, error) {
	app, err := s.FindApplication(ctx, application.IDFromContext(ctx))
	if err != nil {
		slog.ErrorContext(ctx, "read application before review settings save failed", "error", err)
		return application.MiniReview{}, err
	}
	if app.MiniReview.Revision != input.Revision {
		slog.WarnContext(ctx, "review settings revision rejected", "error", ErrConflict)
		return application.MiniReview{}, ErrConflict
	}
	app.MiniReview = app.MiniReview.WithSettings(input)
	app.MiniReviewWrite = true
	if len(app.MiniReview.ConfiguredVersions()) > 100 {
		slog.WarnContext(ctx, "review version limit rejected", "error", ErrInvalidInput)
		return application.MiniReview{}, ErrInvalidInput
	}
	if err := s.validateMiniReview(ctx, *app); err != nil {
		slog.Log(ctx, publicationErrorLevel(err), "review settings prerequisites rejected", "error", err)
		return application.MiniReview{}, err
	}
	saved, err := s.registry.SaveMiniReviewSettings(ctx, app.ID, app.MiniReview)
	if err != nil {
		slog.ErrorContext(ctx, "save review settings failed", "error", err)
		return application.MiniReview{}, err
	}
	saved.DramaIDs = nil
	return saved, nil
}

// ReviewWhitelist 返回当前主体和发布渠道的共享审核列表。
func (s *ApplicationService) ReviewWhitelist(ctx context.Context) (ReviewWhitelist, error) {
	result, err := s.registry.GetReviewWhitelist(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "read channel review list failed", "error", err)
		return result, err
	}
	result.Dramas = []content.DramaSummary{}
	if s.catalog == nil {
		return result, ErrInvalidInput
	}
	result.Dramas, err = s.catalog.FindVisibleSummaries(ctx, result.DramaIDs)
	if err != nil {
		slog.ErrorContext(ctx, "read channel review list summaries failed", "error", err)
	}
	return result, err
}

// SaveReviewWhitelist 新增项必须属于当前主体渠道的线上内容，已有失效引用可以保留或移除。
func (s *ApplicationService) SaveReviewWhitelist(ctx context.Context, input ReviewWhitelist) (ReviewWhitelist, error) {
	previous, err := s.registry.GetReviewWhitelist(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "read channel review list before save failed", "error", err)
		return input, err
	}
	if previous.Revision != input.Revision {
		slog.WarnContext(ctx, "channel review list revision rejected", "error", ErrConflict)
		return input, ErrConflict
	}
	if s.catalog == nil {
		return input, ErrInvalidInput
	}
	input.Dramas, err = s.catalog.FindVisibleSummaries(ctx, input.DramaIDs)
	if err != nil {
		slog.ErrorContext(ctx, "validate channel review list summaries failed", "error", err)
		return input, err
	}
	visible := map[string]bool{}
	for _, drama := range input.Dramas {
		visible[drama.ID] = true
	}
	old := map[string]bool{}
	for _, id := range previous.DramaIDs {
		old[id] = true
	}
	for _, id := range input.DramaIDs {
		if !visible[id] && !old[id] {
			slog.WarnContext(ctx, "channel review list target rejected", "error", ErrInvalidInput)
			return input, ErrInvalidInput
		}
	}
	saved, err := s.registry.SaveReviewWhitelist(ctx, input)
	if err != nil {
		slog.ErrorContext(ctx, "save channel review list failed", "error", err)
		return input, err
	}
	saved.Dramas = input.Dramas
	return saved, nil
}

// validateMiniReview 保证开启审核时仍有实际可见的首页内容和可执行的逐集策略。
func (s *ApplicationService) validateMiniReview(ctx context.Context, app application.Application) error {
	if !app.MiniReview.AnyEnabled() {
		return nil
	}
	ctx = application.ContextForApplication(ctx, app)
	if app.MonetizationMode == application.MonetizationModeIAA {
		if s.settings == nil {
			return ErrInvalidInput
		}
		config, err := s.settings.GetApplicationMonetization(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "read mini review unlock strategy failed", "error", err)
			if errors.Is(err, content.ErrNotFound) {
				return ErrInvalidInput
			}
			return err
		}
		doc, _ := monetization.StrategyFromConfig(config)
		if !monetization.HasEnabledEpisodeRule(doc) || len(app.AdUnits.RewardedVideo) == 0 {
			return ErrInvalidInput
		}
	}
	if !app.MiniReviewWrite {
		return nil
	}
	if s.catalog == nil {
		return ErrInvalidInput
	}
	visible := false
	for _, id := range app.MiniReview.DramaIDs {
		drama, err := s.catalog.FindDrama(ctx, id)
		if errors.Is(err, content.ErrNotFound) {
			continue
		}
		if err != nil {
			slog.ErrorContext(ctx, "read mini review homepage drama failed", "drama_id", id, "error", err)
			return err
		}
		if _, ok := drama.PublishedForApplication(app.ID); ok {
			visible = true
		}
	}
	if !visible {
		return ErrInvalidInput
	}
	return nil
}
