package biz

import (
	"context"
	"errors"
	"log/slog"

	domainapplication "drama-server/internal/biz/application"
	"drama-server/internal/biz/visitor"
)

var errInvalidApplicationResolver = errors.New("invalid application resolver")

// IdentityProviderFactory 根据当前数据库记录创建应用范围内的 TikTok 身份提供器。
// Factory 可以返回 nil，表示该应用没有配置 TikTok 登录能力。
type IdentityProviderFactory func(domainapplication.Application) visitor.IdentityProvider

// ApplicationResolver 从共享仓储实时解析应用，并在请求范围内创建身份提供器。
// 本类型不保存应用列表、状态或 Provider 缓存，因此所有实例观察同一数据库版本。
type ApplicationResolver struct {
	repository domainapplication.Repository
	factory    IdentityProviderFactory
	// directGate 在组合根校验直连播放依赖；所有公开与认证入口共用。
	directGate func(context.Context) error
}

// NewApplicationResolver 创建多实例安全的应用解析器。
func NewApplicationResolver(
	repository domainapplication.Repository,
	factory IdentityProviderFactory,
) (*ApplicationResolver, error) {
	if repository == nil || factory == nil {
		return nil, errInvalidApplicationResolver
	}
	return &ApplicationResolver{repository: repository, factory: factory}, nil
}

// ConfigureDirectPlaybackGate 在服务启动时固定直连依赖门禁，禁止请求参数切换通道。
func (resolver *ApplicationResolver) ConfigureDirectPlaybackGate(gate func(context.Context) error) {
	resolver.directGate = gate
}

// Resolve 按官方 AppID 读取应用，每次检查当前启用状态；不以内部 ID 或业务编码回退。
func (resolver *ApplicationResolver) Resolve(ctx context.Context, tiktokAppID string) (visitor.Application, visitor.IdentityProvider, error) {
	if resolver == nil || resolver.repository == nil || resolver.factory == nil {
		slog.WarnContext(ctx, "resolve rejected", "error", errInvalidApplicationResolver)
		return visitor.Application{}, nil, errInvalidApplicationResolver
	}
	app, err := resolver.repository.FindApplicationByTikTokAppID(ctx, tiktokAppID)
	if err != nil {
		if errors.Is(err, domainapplication.ErrUnavailable) {
			slog.WarnContext(ctx, "find application by tiktok app id failed", "error", err)
			return visitor.Application{}, nil, err
		}
		slog.ErrorContext(ctx, "find application by tiktok app id failed", "error", err)
		return visitor.Application{}, nil, visitor.DependencyError(visitor.ErrServiceUnavailable, err)
	}
	app = app.Normalized()
	if app.TikTokAppID != tiktokAppID {
		slog.WarnContext(ctx, "resolve application identity mismatch", "error", visitor.ErrApplicationForbidden)
		return visitor.Application{}, nil, visitor.ErrApplicationForbidden
	}
	if app.TikTokAppID == "" || app.Status != domainapplication.StatusActive {
		slog.WarnContext(ctx, "resolve rejected", "error", domainapplication.ErrUnavailable)
		return visitor.Application{}, nil, domainapplication.ErrUnavailable
	}
	// 通道配置损坏或直连依赖不可用时在发号、目录与奖励前拒绝，官方应用保持原解析路径。
	if app.PlaybackProvider != "tiktok_minis" && app.PlaybackProvider != "byteplus_web" {
		slog.WarnContext(ctx, "resolve application playback provider rejected", "error", domainapplication.ErrUnavailable)
		return visitor.Application{}, nil, domainapplication.ErrUnavailable
	}
	if app.PlaybackProvider == "byteplus_web" && resolver.directGate != nil {
		if err := resolver.directGate(ctx); err != nil {
			slog.ErrorContext(ctx, "resolve direct application dependency failed", "application_id", app.ID, "error", err)
			return visitor.Application{}, nil, visitor.DependencyError(visitor.ErrServiceUnavailable, err)
		}
	}
	provider := resolver.factory(app)
	app.ClientSecret = ""
	interstitial := ""
	if units := app.AdUnits.Normalized().Interstitial; len(units) > 0 && app.MonetizationMode != domainapplication.MonetizationModeIAP {
		interstitial = units[0]
	}
	return visitor.Application{
		MiniReview: app.MiniReview, MonetizationMode: app.MonetizationMode,
		SubjectID:            app.SubjectID,
		InterstitialAdUnitID: interstitial,
		ID:                   app.ID, TikTokAppID: app.TikTokAppID,
		IdentityHashNamespace: app.IdentityHashNamespace,
		Name:                  app.Name, LogoURL: app.LogoURL,
		ComplianceBaseURL: app.ComplianceBaseURL,
		TimeZone:          app.TimeZone,
		PlaybackProvider:  app.PlaybackProvider,
	}, provider, nil
}
