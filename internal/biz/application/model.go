package application

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	// StatusActive 表示应用允许被消费者实时解析及用于有效投放。
	StatusActive = "active"
	// StatusDisabled 表示应用停用，后续请求不能继续通过应用解析。
	StatusDisabled = "disabled"
)

// MonetizationMode 描述应用配置的收入方式，不替代解锁策略和商品权益规则。
type MonetizationMode string

const (
	// MonetizationModeIAA 表示广告变现。
	MonetizationModeIAA MonetizationMode = "IAA"
	// MonetizationModeIAP 表示内购变现。
	MonetizationModeIAP MonetizationMode = "IAP"
	// MonetizationModeHybrid 表示广告与内购混合变现。
	MonetizationModeHybrid MonetizationMode = "IAA+IAP"
)

// ErrUnavailable 表示应用不存在、已停用或缺少请求所需的公开配置。
var ErrUnavailable = errors.New("application unavailable")

// Repository 是应用聚合的读取端口；实现必须以共享持久化数据为权威。
type Repository interface {
	// FindApplicationByTikTokAppID 按官方 AppID 读取权威应用记录，找不到时返回 ErrUnavailable，重复配置返回错误；不回退到业务编码。
	FindApplicationByTikTokAppID(context.Context, string) (Application, error)
}

// Application 表示一个独立注册的 TikTok Minis 应用。
// 平台标识符即使只包含数字，也始终保持字符串类型。
type Application struct {
	// MiniReview 保存当前应用手填版本的审核配置。
	MiniReview MiniReview `json:"miniReview"`
	// MiniReviewWrite 标记本次命令显式更新审核配置；旧客户端省略时保留数据库最新值。
	MiniReviewWrite bool `json:"-"`
	// SubjectID 是 TikTok 发布归属；直连播放不以主体发布配置作为门禁。
	SubjectID string `json:"subjectId"`
	// PlaybackProvider 是当前应用播放链路，运营可按修订切换，历史应用缺省为官方。
	PlaybackProvider string `json:"playbackProvider"`
	// PlaybackRevision 记录配置修订。
	PlaybackRevision int64 `json:"playbackRevision"`
	// ID 是数据库自增主键的十进制字符串，用于内部关联和管理接口。
	ID string `json:"id"`
	// Name 是该 Minis 应用面向用户的展示名称。
	Name string `json:"name"`
	// LogoURL 是应用图标的公开 HTTPS 地址。
	LogoURL string `json:"logoUrl"`
	// TikTokAppID 是 TikTok 开发者平台应用标识，按字符串保留。
	TikTokAppID string `json:"tiktokAppId"`
	// IdentityHashNamespace 仅保留迁移前 OpenID 摘要的命名空间；新应用留空，不对外暴露。
	IdentityHashNamespace string `json:"-"`
	// ClientKey 是调用 TikTok 官方接口的应用客户端标识，不是 ClientSecret。
	ClientKey string `json:"clientKey"`
	// ClientSecret 是仅供服务端身份提供器使用的客户端密钥，不参与 JSON 序列化或日志。
	ClientSecret string `json:"-"`
	// ClientSecretConfigured 仅表示是否已持久化应用密钥，不返回密钥内容。
	ClientSecretConfigured bool `json:"clientSecretConfigured"`
	// SecretRef 是部署侧解析平台密钥使用的引用，不是密钥明文。
	SecretRef string `json:"secretRef"`
	// BytePlusAccountID 仅承载全局分发配置解析出的媒资拥有方账号，不保存到应用表。
	// 平台即使返回纯数字也必须按字符串保存，避免精度损失。
	BytePlusAccountID string `json:"-"`
	// BytePlusSpaceName 仅承载全局媒资拥有方的 VOD Space，不保存到应用表。
	BytePlusSpaceName string `json:"-"`
	// RegionCode 是应用运营地域，GLOBAL 表示未限定单一国家地区。
	RegionCode string `json:"regionCode"`
	// TimeZone 是奖励进度与 VIP 截止时间划分自然日的 IANA 时区。
	TimeZone string `json:"timeZone"`

	// ComplianceBaseURL 是应用合规页面使用的 HTTPS 基础地址。
	ComplianceBaseURL string `json:"complianceBaseUrl"`
	// AdUnits 是应用在 TikTok Developer Portal 中登记的广告位编码。
	AdUnits AdUnits `json:"adUnits"`
	// MonetizationMode 是应用的变现方式，由运营配置。
	MonetizationMode MonetizationMode `json:"monetizationMode"`
	// Status 控制应用是否启用，消费者每次解析均检查该状态。
	Status string `json:"status"`
	// CreatedAt 记录应用首次创建时间。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 记录应用配置最近修改时间。
	UpdatedAt time.Time `json:"updatedAt"`
}

// Normalized 返回清理应用资料空白与大小写后的应用值副本。
func (a Application) Normalized() Application {
	if a.PlaybackProvider == "" {
		a.PlaybackProvider = "tiktok_minis"
	}
	a.Name = strings.TrimSpace(a.Name)
	a.LogoURL = strings.TrimSpace(a.LogoURL)
	a.TikTokAppID = strings.TrimSpace(a.TikTokAppID)
	a.ClientKey = strings.TrimSpace(a.ClientKey)
	a.SecretRef = strings.TrimSpace(a.SecretRef)
	a.BytePlusAccountID = strings.TrimSpace(a.BytePlusAccountID)
	a.BytePlusSpaceName = strings.TrimSpace(a.BytePlusSpaceName)
	a.RegionCode = strings.ToUpper(strings.TrimSpace(a.RegionCode))
	a.TimeZone = strings.TrimSpace(a.TimeZone)
	a.ComplianceBaseURL = strings.TrimSpace(a.ComplianceBaseURL)
	a.AdUnits = a.AdUnits.Normalized()
	a.Status = strings.ToLower(strings.TrimSpace(a.Status))
	return a
}
