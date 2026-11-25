package biz

import (
	"context"
	"log/slog"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/campaign"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/visitor"
)

// ConsumerSessionResult 只返回登录身份和可信归因，不编排广告、权益、播放器或落地响应。
type ConsumerSessionResult struct {
	// ApplicationID 是内部应用主键，所有个人数据按此应用隔离。
	ApplicationID string `json:"applicationId"`
	// TikTokAppID 供构建包验证应用归属。
	TikTokAppID string `json:"tiktokAppId"`
	// VisitorID 是当前应用的持久用户主体。
	VisitorID string `json:"visitorId"`
	// VisitorToken 是访问个人接口的凭据，不记录到日志。
	VisitorToken string `json:"visitorToken"`
	// IdentityState 区分正式 TikTok 身份和仅开发使用的匿名身份。
	IdentityState visitor.IdentityState `json:"identityState"`
	// TikTokOpenID 只供当前用户直接向官方 SDK 回传事件。
	TikTokOpenID string `json:"tiktokOpenId,omitempty"`
	// Campaign 是服务端解析且已绑定到凭据的归因，不能由落地接口覆盖。
	Campaign *campaign.Attribution `json:"campaign"`
}

// CreateSession 复用既有认证规则，登录成功不依赖任何个人权益或播放器初始化。
func (uc *ConsumerBootstrap) CreateSession(ctx context.Context, input visitor.BootstrapInput) (ConsumerSessionResult, error) {
	created, err := uc.visitors.Bootstrap(ctx, input)
	if err != nil {
		slog.ErrorContext(ctx, "create consumer session failed", "error", err)
		return ConsumerSessionResult{}, err
	}
	slog.InfoContext(ctx, "consumer session created", "application_id", created.ApplicationID, "visitor_id", created.VisitorID)
	return ConsumerSessionResult{ApplicationID: created.ApplicationID, TikTokAppID: created.TikTokAppID,
		VisitorID: created.VisitorID, VisitorToken: created.VisitorToken, IdentityState: created.IdentityState,
		TikTokOpenID: created.EventOpenID, Campaign: created.Campaign}, nil
}

// PrepareCampaignPlayback 在身份安装前准备同一应用的目标集，后续起播仍按已认证观众的权限决定。
func (uc *ConsumerBootstrap) PrepareCampaignPlayback(ctx context.Context, tiktokAppID, code string) (*domain.CampaignPlaybackPreparation, error) {
	app, dramaID, episodeID, err := uc.visitors.PublicCampaignTarget(ctx, tiktokAppID, code)
	if err != nil {
		slog.ErrorContext(ctx, "resolve public campaign target failed", "error", err)
		return nil, err
	}
	if dramaID == "" {
		return nil, nil
	}
	ctx = application.WithSubject(application.WithPlayback(application.WithTimeZone(ctx, app.ID, app.TimeZone), app.PlaybackProvider), app.SubjectID)
	result, err := uc.consumer.PrepareCampaignPlayback(ctx, dramaID, episodeID)
	if err != nil {
		slog.ErrorContext(ctx, "read prepared campaign playback failed", "error", err)
	}
	return result, err
}

// ConsumerAppConfig 是应用级公开配置，不携带用户、归因、VIP 进度或广告领取入口。
type ConsumerAppConfig struct {
	// MiniReview 是当前应用与包版本解析后的有效审核状态。
	MiniReview application.MiniReviewState `json:"miniReview"`
	// ApplicationID 用于与已认证会话核对应用归属。
	ApplicationID string `json:"applicationId"`
	// TikTokAppID 用于与构建包核对应用归属。
	TikTokAppID string `json:"tiktokAppId"`
	// AppName 是应用展示名称。
	AppName string `json:"appName"`
	// LogoURL 是公开品牌图标。
	LogoURL string `json:"logoUrl"`
	// ComplianceBaseURL 是合规页面地址。
	ComplianceBaseURL string `json:"complianceBaseUrl"`
	// InterstitialAdUnitID 是公开插屏广告位。
	InterstitialAdUnitID string `json:"interstitialAdUnitId,omitempty"`
	// Monetization 只包含客户端公开规则，不包含私人进度或完整广告位候选集合。
	Monetization monetization.ConsumerMonetization `json:"monetization"`
	// PlaybackSDK 决定官方或独立播放器，不包含长期凭据。
	PlaybackSDK domain.PlaybackSDKConfig `json:"playbackSdk"`
}

// PublicAppConfig 可与登录并行读取，失败不会撤销已建立的会话。
func (uc *ConsumerBootstrap) PublicAppConfig(ctx context.Context, tiktokAppID string) (ConsumerAppConfig, error) {
	app, err := uc.visitors.PublicApplication(ctx, tiktokAppID)
	if err != nil {
		slog.ErrorContext(ctx, "resolve public app configuration failed", "error", err)
		return ConsumerAppConfig{}, err
	}
	ctx = application.WithPlayback(application.WithTimeZone(ctx, app.ID, app.TimeZone), app.PlaybackProvider)
	ctx = application.WithSubject(ctx, app.SubjectID)
	ctx = application.WithMiniReview(ctx, app.MiniReview, app.MonetizationMode)
	policy, err := uc.consumer.ResolveMonetizationPolicy(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "read public app policy failed", "error", err)
		return ConsumerAppConfig{}, err
	}
	playback, err := uc.consumer.PlaybackConfiguration(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "read public app playback configuration failed", "error", err)
		return ConsumerAppConfig{}, err
	}
	// 新版 Mini 从公开配置读取插屏位；关闭后省略编码，首页和暂停入口共同停止展示。
	if !policy.AllowsInterstitial() {
		app.InterstitialAdUnitID = ""
	}
	return ConsumerAppConfig{MiniReview: application.MiniReviewFromContext(ctx), ApplicationID: app.ID, TikTokAppID: app.TikTokAppID, AppName: app.Name,
		LogoURL: app.LogoURL, ComplianceBaseURL: app.ComplianceBaseURL, InterstitialAdUnitID: app.InterstitialAdUnitID,
		Monetization: monetization.ConsumerMonetizationFromPolicy(policy), PlaybackSDK: playback}, nil
}
