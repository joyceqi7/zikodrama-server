package operations

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"drama-server/internal/biz/application"
	content "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/subject"
)

// Applications 返回应用注册表中的运营配置；不按当前应用筛选，展示字段由 HTTP 层裁剪。
func (s *ApplicationService) Applications(ctx context.Context) ([]application.Application, error) {
	if s == nil || s.registry == nil {
		slog.WarnContext(ctx, "applications rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}

	result1, resultErr := s.registry.ListApplications(ctx)
	if resultErr != nil {
		slog.ErrorContext(ctx, "list applications failed", "error", resultErr)
	}
	return result1, resultErr
}

// FindApplication 按内部应用 ID 读取配置；不存在时保留仓储错误，由 HTTP 层统一映射。
func (s *ApplicationService) FindApplication(ctx context.Context, id string) (*application.Application, error) {
	if s == nil || s.registry == nil {
		slog.WarnContext(ctx, "find application rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	app, err := s.registry.FindApplication(ctx, strings.TrimSpace(id))
	if err != nil {
		slog.ErrorContext(ctx, "find application failed", "error", err)
		return nil, err
	}
	return &app, nil
}

// CreateApplication 接收入口已校验的配置，缺省沿用官方通道；检查平台身份唯一性并交由仓储保存应用及播放模式。
func (s *ApplicationService) CreateApplication(ctx context.Context, input ApplicationInput) (*application.Application, error) {
	// 新应用尚无可见内容与解锁策略，审核只能在创建后通过已有应用配置开启。
	if input.MiniReview != nil && (input.MiniReview.AnyEnabled() || len(input.MiniReview.DramaIDs) > 0) {
		slog.WarnContext(ctx, "create application mini review rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	if s == nil || s.registry == nil {
		slog.WarnContext(ctx, "create application rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	if s.subjects == nil {
		slog.ErrorContext(ctx, "create application subject repository unavailable", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	if _, err := s.subjects.FindSubject(ctx, input.SubjectID); err != nil {
		if errors.Is(err, subject.ErrNotFound) {
			slog.WarnContext(ctx, "create application subject rejected", "error", err)
			return nil, ErrInvalidInput
		}
		slog.ErrorContext(ctx, "find application subject failed", "error", err)
		return nil, err
	}
	app := applicationFromInput(input)
	if app.PlaybackProvider == "" {
		app.PlaybackProvider = "tiktok_minis"
	}
	if app.PlaybackProvider != "tiktok_minis" && app.PlaybackProvider != "byteplus_web" {
		slog.WarnContext(ctx, "create application playback provider rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	now := time.Now().UTC()
	app.CreatedAt, app.UpdatedAt = now, now
	if err := s.validateApplicationUniqueness(ctx, app, false); err != nil {
		slog.ErrorContext(ctx, "validate application uniqueness failed", "error", err)
		return nil, err
	}
	if err := s.registry.CreateApplication(ctx, &app); err != nil {
		slog.ErrorContext(ctx, "save application failed", "error", err)
		return nil, err
	}
	return &app, nil
}

// validateApplicationAdUnits 校验当前模式与已有规则；IAA/混合应用启用及启用广告规则要求配齐广告位，纯内购不要求。
func (s *ApplicationService) validateApplicationAdUnits(ctx context.Context, app application.Application) error {
	if app.Status == application.StatusActive && app.MonetizationMode != application.MonetizationModeIAP && !app.AdUnits.Complete() {
		return ErrApplicationAdUnitsIncomplete
	}
	if s.settings == nil {
		return nil
	}
	config, err := s.settings.GetApplicationMonetization(application.WithContext(ctx, app.ID))
	if errors.Is(err, content.ErrNotFound) {
		slog.DebugContext(ctx, "application has no unlock strategy", "error", err)
		return nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "get application monetization failed", "error", err)
		return err
	}
	doc, _ := monetization.StrategyFromConfig(config)
	if err := monetization.ValidateApplicationMode(app.MonetizationMode, doc); err != nil {
		return err
	}
	if monetization.RequiresAdUnits(doc) && !app.AdUnits.Complete() {
		return ErrApplicationAdUnitsIncomplete
	}
	return nil
}

// UpdateApplication 保留应用身份、权益归属和创建时间；通道变更要求最新播放修订，省略播放字段的请求保持原通道，变现模式须兼容已有规则。
func (s *ApplicationService) UpdateApplication(ctx context.Context, id string, input ApplicationInput) (*application.Application, error) {
	existing, err := s.FindApplication(ctx, id)
	if err != nil {
		slog.ErrorContext(ctx, "find application failed", "error", err)
		return nil, err
	}
	app := applicationFromInput(input)
	if input.MiniReview == nil {
		app.MiniReview = existing.MiniReview
	} else {
		app.MiniReview = existing.MiniReview.WithSettings(*input.MiniReview)
	}
	if app.PlaybackProvider == "" {
		app.PlaybackProvider = existing.Normalized().PlaybackProvider
	}
	if app.SubjectID != existing.SubjectID {
		slog.WarnContext(ctx, "update application subject rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	if app.PlaybackProvider != "tiktok_minis" && app.PlaybackProvider != "byteplus_web" {
		slog.WarnContext(ctx, "update application playback provider rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	playbackChanged := app.PlaybackProvider != existing.Normalized().PlaybackProvider
	if (playbackChanged && input.PlaybackRevision == nil) || (input.PlaybackRevision != nil && *input.PlaybackRevision != existing.PlaybackRevision) {
		slog.WarnContext(ctx, "update application playback revision rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	app.PlaybackRevision = existing.PlaybackRevision
	// 平台 ID 始终固定，Client Key 仅允许从未配置状态补齐一次，避免已有授权、观众和投放迁移。
	if (existing.ClientKey != "" && app.ClientKey != existing.ClientKey) || app.TikTokAppID != existing.TikTokAppID {
		slog.WarnContext(ctx, "update application rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	// 配置修改不能改变历史用户的身份摘要输入。
	app.ClientSecretConfigured = app.ClientSecret != "" || existing.ClientSecret != ""
	app.ID = existing.ID
	app.IdentityHashNamespace = existing.IdentityHashNamespace
	app.CreatedAt = existing.CreatedAt
	app.UpdatedAt = time.Now().UTC()
	if err := s.validateApplicationUniqueness(ctx, app, true); err != nil {
		slog.ErrorContext(ctx, "validate application uniqueness failed", "error", err)
		return nil, err
	}
	if err := s.validateApplicationAdUnits(ctx, app); err != nil {
		logStrategySaveError(ctx, "validate application ad units failed", err)
		return nil, err
	}
	if err := s.validateMiniReview(ctx, app); err != nil {
		slog.ErrorContext(ctx, "validate mini review failed", "error", err)
		return nil, err
	}
	if err := s.registry.SaveApplication(ctx, app); err != nil {
		if errors.Is(err, directplay.ErrInvalid) {
			slog.WarnContext(ctx, "save application playback profile rejected", "error", err)
		} else {
			logStrategySaveError(ctx, "save application failed", err)
		}
		return nil, err
	}
	if app.MiniReviewWrite {
		app.MiniReview.Revision++
	}
	if playbackChanged {
		app.PlaybackRevision++
	}
	return &app, nil
}

// validateApplicationUniqueness 排除候选自身后检查 ClientKey 和 TikTok AppID；任一被其他应用使用均返回冲突。
func (s *ApplicationService) validateApplicationUniqueness(ctx context.Context, candidate application.Application, updating bool) error {
	items, err := s.registry.ListApplications(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list applications failed", "error", err)
		return err
	}
	for _, item := range items {
		if updating && item.TikTokAppID == candidate.TikTokAppID {
			continue
		}
		if (candidate.ClientKey != "" && item.ClientKey == candidate.ClientKey) ||
			(candidate.TikTokAppID != "" && item.TikTokAppID == candidate.TikTokAppID) {
			slog.WarnContext(ctx, "validate application uniqueness rejected", "error", ErrConflict)
			return ErrConflict
		}
	}
	return nil
}

// applicationFromInput 将入口已校验的应用配置映射为持久化模型。
func applicationFromInput(input ApplicationInput) application.Application {
	app := application.Application{
		SubjectID: input.SubjectID,
		Name:      input.Name, LogoURL: input.LogoURL,
		PlaybackProvider: input.PlaybackProvider,
		TikTokAppID:      input.TikTokAppID, ClientKey: input.ClientKey,
		SecretRef: input.SecretRef, ClientSecret: input.ClientSecret, ClientSecretConfigured: input.ClientSecret != "",
		RegionCode: input.RegionCode, TimeZone: input.TimeZone, ComplianceBaseURL: input.ComplianceBaseURL,
		AdUnits: input.AdUnits, Status: input.Status,
	}
	if input.MiniReview != nil {
		app.MiniReview = *input.MiniReview
		app.MiniReviewWrite = true
	}
	app.MonetizationMode = input.MonetizationMode
	return app
}
