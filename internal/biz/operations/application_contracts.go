package operations

import (
	"context"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/campaign"
	"drama-server/internal/biz/monetization"
)

// ApplicationInput 是创建或整体更新 TikTok Minis 应用的运营命令。
// 创建时要求 SubjectID、TikTokAppID、Name、ClientKey、ClientSecret 和 MonetizationMode；TikTok 和 URL 标识都按字符串处理。
type ApplicationInput struct {
	// MiniReview 省略时保留当前审核配置；提供时按修订比较后整体保存。
	MiniReview *application.MiniReview `json:"miniReview,omitempty"`
	// SubjectID 是应用固定所属的发布主体，普通编辑不能迁移已有应用。
	SubjectID string `json:"subjectId"`
	// PlaybackProvider 创建省略时为官方；更新可切换通道，省略时保留原通道。
	PlaybackProvider string `json:"playbackProvider,omitempty"`
	// PlaybackRevision 是编辑时读到的播放修订；变更通道必须提供，历史无绑定应用使用 0，过期值拒绝整个更新。
	PlaybackRevision *int64 `json:"playbackRevision,omitempty"`
	// Name 是运营后台展示的应用名称。
	Name string `json:"name"`
	// LogoURL 是消费者端公开 Logo 的 HTTPS URL，可留空。
	LogoURL string `json:"logoUrl"`
	// TikTokAppID 是 TikTok Minis 平台分配的应用标识，必须按字符串处理。
	TikTokAppID string `json:"tiktokAppId"`
	// ClientKey 是 H5 初始化 TikTok SDK 所需的公开 Client Key；新建必填；历史停用应用可首次补齐，保存后不可更换。
	ClientKey string `json:"clientKey"`
	// ClientSecret 是当前小程序的密钥；创建必填，更新省略或空字符串保留已有值。
	ClientSecret string `json:"clientSecret"`
	// SecretRef 是部署环境中的 TikTok Client Secret 引用名，不保存明文密钥。
	SecretRef string `json:"secretRef"`
	// RegionCode 是主要投放市场代码；GLOBAL 表示不限定单一国家。
	RegionCode string `json:"regionCode"`
	// TimeZone 是应用配置的 IANA 时区，供权益自然日计算；报表是否采用该时区由统计查询决定。
	TimeZone string `json:"timeZone"`

	// ComplianceBaseURL 是兼容已有应用的合规页面 HTTPS 基础地址，新建时可省略。
	ComplianceBaseURL string `json:"complianceBaseUrl"`
	// MonetizationMode 必填 IAA、IAP、IAA+IAP，由 HTTP 入口规范化并校验。
	MonetizationMode application.MonetizationMode `json:"monetizationMode"`
	// Status 是 active 或 disabled；创建默认 disabled，停用应用不能被新的消费者请求解析。
	Status string `json:"status"`
	// AdUnits 是应用在 TikTok Developer Portal 中登记的广告位编码集合。
	AdUnits application.AdUnits `json:"adUnits"`
}

// CampaignInput 定义某个应用内的 TikTok 投放落地规则。
// TikTok Campaign/AdGroup/Ad/Creative ID 只用于对账和归因，必须按字符串保存。
type CampaignInput struct {
	// Code 是写入启动参数 campaignCode 的稳定编码；留空时由服务端生成。
	Code string `json:"code"`
	// Name 是运营后台用于区分投放素材或批次的名称。
	Name string `json:"name"`
	// ExternalCampaignID 是 TikTok Ads Campaign ID，仅用于对账和归因。
	ExternalCampaignID string `json:"externalCampaignId"`
	// ExternalAdGroupID 是 TikTok Ads Ad Group ID，仅用于对账和归因。
	ExternalAdGroupID string `json:"externalAdGroupId"`
	// ExternalAdID 是 TikTok Ads Ad ID，仅用于对账和归因。
	ExternalAdID string `json:"externalAdId"`
	// ExternalCreativeID 是 TikTok Ads Creative ID，仅用于对账和归因。
	ExternalCreativeID string `json:"externalCreativeId"`
	// DramaID 是落地后优先打开的全局短剧标识。
	DramaID string `json:"dramaId"`
	// EpisodeID 是落地后优先打开的剧集标识，必须属于 DramaID。
	EpisodeID string `json:"episodeId"`
	// ExperimentKey 是后续归因或 A/B 分组使用的稳定键，不参与权益判定。
	ExperimentKey string `json:"experimentKey"`
	// Status 是 active 或 disabled；禁用后 Bootstrap 忽略该投放编码。
	Status string `json:"status"`
}

// ApplicationRepository 定义应用管理用例需要的持久化端口。
type ApplicationRepository interface {
	// GetReviewWhitelist 读取当前发布主体和渠道共享的审核列表。
	GetReviewWhitelist(context.Context) (ReviewWhitelist, error)
	// SaveReviewWhitelist 按共享修订保存当前主体渠道的审核列表。
	SaveReviewWhitelist(context.Context, ReviewWhitelist) (ReviewWhitelist, error)
	// SaveMiniReviewSettings 仅按审核修订更新版本和开关，保留最新内容清单及其他应用资料。
	SaveMiniReviewSettings(context.Context, string, application.MiniReview) (application.MiniReview, error)
	// SaveMiniReview 仅按共享修订更新目标应用审核清单，不回写版本、应用资料或密钥。
	SaveMiniReview(context.Context, string, application.MiniReview) (application.MiniReview, error)
	// ListApplications 列出全部应用及其广告位配置；这是全局管理目录，不按当前应用上下文过滤。
	ListApplications(context.Context) ([]application.Application, error)
	// CreateApplication 新增应用并回填数据库分配的字符串主键；官方 AppID 已存在时返回 ErrConflict，不能覆盖已有应用。
	CreateApplication(context.Context, *application.Application) error
	// FindApplication 按内部应用 ID 精确查询配置及当前播放通道，不存在时返回内容域 ErrNotFound。
	FindApplication(context.Context, string) (application.Application, error)
	// SaveApplication 原子保存应用与播放模式，PlaybackRevision 必须等于当前修订；通道变化递增播放修订，全局直连配置无效时拒绝整个更新。ClientSecret 为空时保留现值；模式或广告位变化递增消费修订，身份不可变由 Service 检查。
	SaveApplication(context.Context, application.Application) error
}

// MonetizationRepository 定义应用商业化配置用例需要的持久化端口。
type MonetizationRepository interface {
	// GetApplicationStrategy 在一致快照中读取当前应用的策略及修订；未配置返回空策略，应用不存在返回 ErrNotFound。
	GetApplicationStrategy(context.Context) (monetization.ApplicationStrategy, error)
	// SaveApplicationStrategy 在同一事务读取最新应用配置、校验模式允许的获取方式并保存规则及商品映射；修订号由服务端递增，策略只能属于当前应用。
	SaveApplicationStrategy(context.Context, monetization.ApplicationStrategyInput) (monetization.ApplicationStrategy, error)
	// GetApplicationMonetization 读取上下文应用的当前规则、主文案和自动解析的广告位；尚未配置时返回内容域 ErrNotFound。
	GetApplicationMonetization(context.Context) (monetization.ApplicationMonetizationConfig, error)
	// SaveApplicationMonetization 在同一事务保存当前修订号配置；Revision 为 1 时仅允许首次创建，其余必须比数据库当前值大 1，否则返回 ErrConflict。
	SaveApplicationMonetization(context.Context, monetization.ApplicationMonetizationConfig) error
}

// CampaignRepository 定义投放活动用例需要的持久化端口。
type CampaignRepository interface {
	// ListCampaigns 列出上下文应用的投放活动；返回活动自身的官方落地链接及目标内容标识。
	ListCampaigns(context.Context) ([]campaign.Campaign, error)
	// FindCampaign 按内部活动 ID 查询上下文应用的活动；不存在或属于其他应用时返回内容域 ErrNotFound。
	FindCampaign(context.Context, string) (campaign.Campaign, error)
	// SaveCampaign 在 ID 为空时创建并回填数据库主键，否则更新已有活动，不允许迁移到其他应用；应用归属不符返回内容域 ErrNotFound，唯一键冲突返回 ErrConflict。
	SaveCampaign(context.Context, *campaign.Campaign) error
}
