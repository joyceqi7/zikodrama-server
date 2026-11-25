package biz

import (
	"context"
	"log/slog"
	"strings"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/campaign"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/sharding"
	"drama-server/internal/biz/visitor"
)

// ConsumerBootstrap 编排消费者启动链路。
// HTTP 层先规范化并校验请求参数；请求语言已由 Styx 写入 context。应用解析、可信投放归因、变现策略、VIP
// 状态和落地目标都在此处完成。这样其他传输协议复用启动链路时不会复制业务
// 判断，也不会允许客户端自行伪造 CampaignID。
type ConsumerBootstrap struct {
	// visitors 管理 TikTok 静默登录、稳定用户识别和无状态访问凭据。
	visitors *visitor.Service
	// consumer 提供目录、变现和权益用例。
	consumer *domain.Service
}

// NewConsumerBootstrap 创建消费者启动用例。
func NewConsumerBootstrap(
	visitors *visitor.Service,
	consumer *domain.Service,
) *ConsumerBootstrap {
	return &ConsumerBootstrap{visitors: visitors, consumer: consumer}
}

// BootstrapResult 是消费者启动接口的业务结果。
// 内部应用配置修订、完整广告位清单和持久化键不会通过该结构暴露。
type BootstrapResult struct {
	// MiniReview 是当前应用与包版本解析后的有效审核状态。
	MiniReview application.MiniReviewState `json:"miniReview"`
	// InterstitialAdUnitID 是公开插屏位，不含奖励凭据。
	InterstitialAdUnitID string `json:"interstitialAdUnitId,omitempty"`
	// TikTokOpenID 仅供当前登录用户向 TikTok 回传标准事件，不包含昵称、头像或用户令牌。
	TikTokOpenID string `json:"tiktokOpenId,omitempty"`
	// VisitorID 是广告奖励和权益共同引用的内部主体标识，不是 TikTok OpenID。
	VisitorID string `json:"visitorId"`
	// ApplicationID 是本次启动解析得到的官方 TikTok AppID。
	ApplicationID string `json:"applicationId"`
	// TikTokAppID 是构建包校验启动归属使用的官方 AppID。
	TikTokAppID string `json:"tiktokAppId"`
	// VisitorToken 是服务端签发的应用范围访问凭据，身份状态由 IdentityState 表示。
	VisitorToken string `json:"visitorToken"`
	// IdentityState 声明本次主体是否已绑定 TikTok 身份。
	IdentityState visitor.IdentityState `json:"identityState"`
	// AppName 是权威应用配置中的展示名称。
	AppName string `json:"appName"`
	// LogoURL 是应用图标的公开展示地址。
	LogoURL string `json:"logoUrl"`
	// ComplianceBaseURL 是当前应用合规站页面的 HTTPS 基础地址。
	ComplianceBaseURL string `json:"complianceBaseUrl"`

	// Monetization 是当前配置的公开规则，不包含内部修订或完整广告位配置。
	Monetization monetization.ConsumerMonetization `json:"monetization"`
	// VIPState 是服务端只读得到的当前自然日 VIP 进度与已领取权益。
	VIPState monetization.VIPProgress `json:"vipState"`
	// VIPOffer 是当前可领取的 VIP 广告入口；关闭 VIP 或权益已生效时为空。
	VIPOffer *monetization.AccessOffer `json:"vipOffer"`
	// LaunchTarget 是验证为当前应用可见的落地剧集，无有效目标时为空并进入首页。
	LaunchTarget *domain.LaunchTarget `json:"launchTarget"`
	// Campaign 是本次启动经服务端解析的投放归因，自然启动时为空。
	Campaign *campaign.Attribution `json:"campaign"`
	// PlaybackSDK 由固定应用通道声明官方合同或直连公开配置，不包含长期凭据。
	PlaybackSDK domain.PlaybackSDKConfig `json:"playbackSdk"`
}

// Execute 编排公开启动、静默登录身份、稳定用户识别和首页业务上下文。
func (uc *ConsumerBootstrap) Execute(ctx context.Context, input visitor.BootstrapInput) (BootstrapResult, error) {
	created, err := uc.visitors.Bootstrap(ctx, input)
	if err != nil {
		slog.ErrorContext(ctx, "bootstrap failed", "error", err)
		return BootstrapResult{}, err
	}
	ctx = application.WithTimeZone(ctx, created.ApplicationID, created.TimeZone)
	ctx = application.WithPlayback(ctx, created.PlaybackProvider)
	ctx = application.WithSubject(ctx, created.SubjectID)
	ctx = application.WithMiniReview(ctx, created.MiniReview, created.MonetizationMode)
	ctx = sharding.WithBucketID(ctx, created.BucketID)
	if created.Campaign != nil {
		ctx = campaign.WithAttribution(ctx, created.Campaign.ID)
	}
	experience, err := uc.consumer.ReadAdExperience(ctx, created.VisitorID)
	if err != nil {
		slog.ErrorContext(ctx, "read bootstrap ad experience failed", "error", err)
		return BootstrapResult{}, err
	}

	launchDramaID := firstLaunchValue(input.LaunchQuery, "dramaId", "drama_id", "albumId", "album_id")
	launchEpisodeID := firstLaunchValue(input.LaunchQuery, "episodeId", "episode_id")
	// 有效投放的落地目标以服务端记录为准，不能被同一链接附带的任意目标参数覆盖。
	if created.Campaign != nil && created.Campaign.DramaID != "" {
		launchDramaID, launchEpisodeID = created.Campaign.DramaID, created.Campaign.EpisodeID
	}
	launchTarget, err := uc.consumer.ResolveLaunchTarget(ctx, launchDramaID, launchEpisodeID)
	if err != nil {
		slog.ErrorContext(ctx, "resolve launch target failed", "error", err)
		return BootstrapResult{}, err
	}
	// 官方来源保持原固定协议；直连配置必须有效才能完成启动。
	playbackSDK, err := uc.consumer.PlaybackConfiguration(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "resolve bootstrap playback configuration failed", "error", err)
		return BootstrapResult{}, err
	}

	// 兼容现有 Mini 的空广告位门禁，关闭插屏不删除应用原有广告位配置。
	if !experience.InterstitialEnabled {
		created.InterstitialAdUnitID = ""
	}
	return BootstrapResult{
		MiniReview:           application.MiniReviewFromContext(ctx),
		InterstitialAdUnitID: created.InterstitialAdUnitID,
		TikTokAppID:          created.TikTokAppID,
		TikTokOpenID:         created.EventOpenID,
		VisitorID:            created.VisitorID, ApplicationID: created.ApplicationID,
		VisitorToken: created.VisitorToken, IdentityState: created.IdentityState,
		AppName: created.ApplicationName, LogoURL: created.ApplicationLogoURL,
		ComplianceBaseURL: created.ApplicationComplianceBaseURL,
		Monetization:      experience.Monetization, VIPState: experience.VIPState, VIPOffer: experience.VIPOffer,
		LaunchTarget: launchTarget, Campaign: created.Campaign, PlaybackSDK: playbackSDK,
	}, nil
}

// firstLaunchValue 按兼容参数名优先级取首个非空启动值，目标仍须经过可见目录校验。
func firstLaunchValue(query map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(query[key]); value != "" {
			return value
		}
	}
	return ""
}
