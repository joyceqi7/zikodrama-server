// Package visitor 定义消费者主体、TikTok 静默登录身份和访问凭据。
// Visitor 是短剧业务中关联奖励和权益的持久主体。预发与生产必须绑定 TikTok 身份；
// 匿名启动只用于本地开发和自动化测试，不是生产登录失败的降级路径。
package visitor

import (
	"context"
	"drama-server/internal/biz/application"
	"errors"

	"drama-server/internal/biz/campaign"
)

var (
	// ErrUnauthorized 只表示访问凭据或一次性登录码无效、Visitor 已不存在。
	ErrUnauthorized = errors.New("visitor credential is invalid")
	// ErrInvalidRequest 表示启动参数不完整，不应通过重新登录修复。
	ErrInvalidRequest = errors.New("visitor request is invalid")
	// ErrApplicationForbidden 表示有效凭据的应用归属与当前权威应用不一致。
	ErrApplicationForbidden = errors.New("visitor application scope is forbidden")
	// ErrServiceUnavailable 表示身份持久化等服务依赖暂不可用，凭据不因此失效。
	ErrServiceUnavailable = errors.New("visitor service dependency is unavailable")
	// ErrIdentityUnavailable 表示平台身份服务或服务端登录配置不可用。
	ErrIdentityUnavailable = errors.New("tiktok identity provider is unavailable")
)

// IdentityState 描述 Visitor 当前是否已经绑定 TikTok OpenID。
type IdentityState string

const (
	// IdentityStateAnonymous 表示仅建立内部访客主体，未完成 TikTok 身份绑定；只允许开发与测试使用。
	IdentityStateAnonymous IdentityState = "anonymous"
	// IdentityStateTikTokAuthenticated 表示访客已绑定当前应用范围的 TikTok 身份。
	IdentityStateTikTokAuthenticated IdentityState = "tiktok_authenticated"
)

// Identity 是 TikTok 登录码交换得到的服务端身份，禁止写入日志。
// 只有 OpenID 可通过独立启动字段交给当前用户进行官方事件回传，平台 Token 不出服务端。
type Identity struct {
	// OpenID 是当前应用范围内的 TikTok 用户标识。
	OpenID string
}

// IdentityProvider 是应用范围内的 TikTok 登录适配端口。
type IdentityProvider interface {
	// ExchangeCode 用当前应用的一次性登录码交换平台身份；无效码返回 ErrUnauthorized，网络或平台故障保留独立错误。
	ExchangeCode(context.Context, string) (Identity, error)
}

// TikTokIdentityRecord 是可安全持久化的平台身份记录。
// 仅保存应用归属与身份摘要，不持久化 OpenID 原文或平台令牌。
type TikTokIdentityRecord struct {
	// ApplicationID 是身份或凭据归属的应用数据库自增主键，用于隔离不同 Minis 应用。
	ApplicationID string
	// OpenIDHash 是应用标识与 OpenID 的联合摘要，供同应用身份查找和唯一绑定。
	OpenIDHash string
}

// UserRef 是消费者主体标识及其稳定逻辑桶，两个字段必须作为同一身份结果使用。
type UserRef struct {
	// ID 是 号段分配器生成的十进制字符串业务标识。
	ID string
	// BucketID 是用户域 1024 个稳定逻辑桶之一。
	BucketID uint16
	// AuthVersion 是 MySQL 权威账号的凭据版本；旧版本 VisitorToken 须重新静默登录。
	AuthVersion uint64
}

// CredentialVersionStore 读取当前账号的权威凭据版本；生产分片仓储必须实现。
type CredentialVersionStore interface {
	// VisitorCredentialVersionRef 按应用、桶和用户从主库读取版本；主体不存在时 exists=false。
	VisitorCredentialVersionRef(context.Context, string, UserRef) (version uint64, exists bool, err error)
}

// Application 是签发 Visitor 凭据所需的最小应用视图。
type Application struct {
	// MiniReview 与 MonetizationMode 来自实时应用配置，不写入身份凭据。
	MiniReview application.MiniReview
	// MonetizationMode 限定审核时是否仅执行逐集规则。
	MonetizationMode application.MonetizationMode
	// SubjectID 只决定官方发布归属，不参与身份签名或直连门禁。
	SubjectID string `json:"-"`
	// PlaybackProvider 来自权威应用记录，只决定官方或直连模式。
	PlaybackProvider string
	// ID 是权威应用的数据库自增主键，以字符串传递。
	ID string

	// InterstitialAdUnitID 是应用选中的公开插屏广告位。
	InterstitialAdUnitID string
	// TikTokAppID 是构建包使用的官方应用标识，始终按字符串保留。
	TikTokAppID string
	// IdentityHashNamespace 仅用于兼容历史 OpenID 摘要，不参与应用定位或对外响应。
	IdentityHashNamespace string
	// Name 是应用公开展示名称。
	Name string
	// LogoURL 是应用公开展示图标地址。
	LogoURL string
	// ComplianceBaseURL 是应用合规页面的 HTTPS 基础地址。
	ComplianceBaseURL string
	// TimeZone 是应用业务自然日使用的 IANA 时区，供奖励周期计算。
	TimeZone string
}

// ApplicationResolver 每次从权威应用数据解析有效应用和可选 TikTok Provider。
type ApplicationResolver interface {
	// Resolve 按官方 AppID 读取有效应用及身份提供器；应用不可用与依赖故障保留区别。
	Resolve(context.Context, string) (Application, IdentityProvider, error)
}

// CampaignResolver 只解析指定应用内仍可用于启动的投放编码。
type CampaignResolver interface {
	// ResolveCampaign 按应用标识和投放编码查找仍有效的归因；不存在、停用或目标不可用时返回 found=false；查询故障返回错误。
	ResolveCampaign(context.Context, string, string) (campaign.Attribution, bool, error)
}

// Store 在统一用户记录中持久化 Visitor 主体和 TikTok 身份摘要，不保存客户端持有的 VisitorToken。
type Store interface {
	// CreateVisitor 在指定内部应用下创建主体，成功后回填字符串 ID，不保存 Token。
	CreateVisitor(context.Context, string, *string) error
	// VisitorExists 检查内部访客标识是否仍存在且属于给定应用。
	VisitorExists(context.Context, string, string) (bool, error)
	// ResolveTikTokIdentity 按内部应用 ID 与 OpenID 摘要查找或创建用户，并发调用返回同一用户。
	ResolveTikTokIdentity(context.Context, TikTokIdentityRecord) (string, error)
}

// ShardedStore 为支持稳定逻辑桶的持久化实现补充带路由结果的身份方法。
// Service 保留 Store 兼容测试替身，生产 MySQL 必须实现本接口。
type ShardedStore interface {
	// CreateVisitorRef 创建匿名主体并返回其稳定逻辑桶。
	CreateVisitorRef(context.Context, string) (UserRef, error)
	// VisitorExistsRef 在指定逻辑桶验证主体归属。
	VisitorExistsRef(context.Context, string, UserRef) (bool, error)
	// ResolveTikTokIdentityRef 路由并恢复或创建 TikTok 用户。
	ResolveTikTokIdentityRef(context.Context, TikTokIdentityRecord) (UserRef, error)
}

// BootstrapInput 是公开启动接口输入。VisitorToken 可省略，LoginCode 在预发和生产必填；
// 旧凭据失效时仍须使用本次静默登录结果恢复当前 TikTok 账号，不能沿用其他账号权益。
type BootstrapInput struct {
	// TikTokAppID 是构建包绑定的官方 AppID，不接受内部应用标识或业务编码。
	TikTokAppID string `json:"tiktokAppId"`
	// LoginCode 是 TikTok 静默登录返回的一次性授权码。
	LoginCode string `json:"loginCode,omitempty"`
	// VisitorToken 是服务端先前签发的应用范围访问凭据。
	VisitorToken string `json:"visitorToken,omitempty"`
	// LaunchQuery 保存 TikTok 启动入口携带的业务参数。
	LaunchQuery map[string]string `json:"launchQuery"`
}

// Context 是服务端从 VisitorToken 恢复的可信消费者上下文。
type Context struct {
	// MiniReview 是本次实时应用审核配置。
	MiniReview application.MiniReview
	// MonetizationMode 来自实时应用，不保存在 Token 中。
	MonetizationMode application.MonetizationMode
	// SubjectID 来自最新应用，不存入凭据或消费者合同。
	SubjectID string `json:"-"`
	// PlaybackProvider 来自最新应用记录，不保存在用户凭据；直连参数共用全局默认配置。
	PlaybackProvider string
	// InterstitialAdUnitID 仅供当前应用展示普通插屏。
	InterstitialAdUnitID string
	// EventOpenID 只在本次静默登录成功后提供给该用户的官方事件回传，不写入日志。
	EventOpenID string
	// ApplicationID 是身份或凭据归属的应用数据库自增主键，用于隔离不同 Minis 应用。
	ApplicationID string
	// TikTokAppID 是权威应用记录中的官方 AppID，供客户端核对启动归属。
	TikTokAppID string
	// ApplicationName 是本次解析得到的最新应用展示名称。
	ApplicationName string
	// ApplicationLogoURL 是本次解析得到的应用图标地址。
	ApplicationLogoURL string
	// ApplicationComplianceBaseURL 是本次解析得到的应用合规页面基础地址。
	ApplicationComplianceBaseURL string
	// TimeZone 是应用业务自然日使用的 IANA 时区，供奖励周期计算。
	TimeZone string

	// VisitorID 是奖励和权益使用的持久业务主体标识，不是平台 OpenID。
	VisitorID string
	// BucketID 是服务端签名凭据恢复的稳定逻辑桶，不向公开响应暴露。
	BucketID uint16
	// VisitorToken 是应用范围的加密访问凭据，不得写入日志。
	VisitorToken string
	// IdentityState 标记当前可信主体是否已经绑定平台身份。
	IdentityState IdentityState
	// Campaign 保存本次启动解析的可信归因快照；自然启动时为空。
	Campaign *campaign.Attribution
}
