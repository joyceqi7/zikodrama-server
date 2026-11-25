package operations

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/subject"
)

// ApplicationPorts 聚合 TikTok 应用；广告位编码随应用聚合一起保存。
type ApplicationPorts struct {
	// Subjects 保存发布主体，不承担管理员或直连内容隔离。
	Subjects subject.Repository
	// Registry 保存全局应用配置与广告位，是应用身份和授权范围的查询来源。
	Registry ApplicationRepository
}

// MonetizationPorts 聚合应用消费设置的运营端口。
type MonetizationPorts struct {
	// Settings 按应用读取并按修订号保存广告解锁和 VIP 策略。
	Settings MonetizationRepository
}

// CampaignPorts 聚合 TikTok 获客投放链路的运营端口。
type CampaignPorts struct {
	// Repository 保存应用内投放规则及官方 Minis 落地链接。
	Repository CampaignRepository
}

// ContentPorts 聚合全局内容目录边界。
// 短剧按主体隔离，同主体应用共用内容；TikTok 标签目录及翻译保持全局共享。
type ContentPorts struct {
	// Records 提供按主体保存的审核与上线记录的分页及历史读取。
	Records PublicationRecordRepository
	// Tags 保存所有应用共享的官方标签英文展示名。
	Tags TikTokTagRepository
	// OwnerApplication 从选定主体读取拥有方身份及凭据，不混入分发应用列表。
	OwnerApplication func(context.Context, string) (application.Application, error)
	// OwnerApplicationID 从当前主体读取稳定的官方资产拥有方。
	OwnerApplicationID func(context.Context) (string, error)
	// Distributions 维护独立于内容版本的应用分发关系和重试任务。
	Distributions DistributionRepository
	// Catalog 维护全局短剧编辑聚合、分集和媒资绑定。
	Catalog CatalogRepository

	// Publications 保存 TikTok 官方剧目版本的异步发布编排事实。
	Publications TikTokPublicationRepository
}

// MediaPorts 聚合可被多实例 Worker 竞争处理的媒资任务和远端清理队列。
type MediaPorts struct {
	// Jobs 持久化剧的视频上传、封面临时结果和远端删除任务。
	Jobs MediaJobRepository
}

// ReportingPorts 聚合只读运营统计与管理员写操作审计。
type ReportingPorts struct {
	// Metrics 从奖励与权益事实生成应用统计。
	Metrics MetricsRepository
	// Audits 记录管理员请求的执行结果，供问题追溯。
	Audits AuditRepository
	// Postbacks 分页读取当前应用已脱敏的 TikTok 关键行为回传明细。
	Postbacks TikTokPostbackLogRepository
	// Ads 读取同一指标管道内的广告调用漏斗。
	Ads AdAnalyticsRepository
}

// Repositories 是 operations 各领域用例的端口集合。
// 每个用例只校验实际使用的窄端口，不要求所有领域依赖同时存在。
type Repositories struct {
	// ConsumerUsers 提供按应用聚合的观众只读列表。
	ConsumerUsers ConsumerUserRepository
	// Application 提供全局应用管理所需持久化端口。
	Application ApplicationPorts
	// Monetization 提供应用消费策略与文案版本的持久化端口。
	Monetization MonetizationPorts
	// Campaign 提供应用内投放目标和官方链接的持久化端口。
	Campaign CampaignPorts
	// Content 提供共享短剧目录、标签及官方发布任务端口。
	Content ContentPorts
	// Media 提供剧的视频上传、封面临时结果和删除任务端口。
	Media MediaPorts
	// Reporting 提供只读指标与管理员操作审计端口。
	Reporting ReportingPorts
}
