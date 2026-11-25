package operations

import (
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/subject"
)

// Service 只装配运营模块；各模块独立持有业务依赖，HTTP 入口选择对应模块调用。
type Service struct {
	// SubjectService 管理主体资料和 TikTok 发布方。
	SubjectService *subject.Service
	// ConsumerUserService 提供观众列表查询。
	*ConsumerUserService
	// TikTokPostbackLogService 提供关键行为回传日志分页查询。
	*TikTokPostbackLogService
	// AdAnalyticsService 提供独立菜单的广告统计。
	*AdAnalyticsService
	// DramaService 管理短剧聚合，包括分集、媒资、发布、分发授权和相关 Worker。
	*DramaService
	// ApplicationService 管理平台应用身份与配置。
	*ApplicationService
	// MonetizationService 管理解锁策略与应用绑定。
	*MonetizationService
	// CampaignService 管理投放目标与官方落地链接。
	*CampaignService
	// ReportingService 提供消费统计与审计。
	*ReportingService
}

// NewService 在组合根分配端口，不把所有仓储继续传给每个业务模块。
func NewService(repositories Repositories, media MediaProvider) *Service {
	drama := NewDramaService(repositories.Content, repositories.Media, repositories.Application.Registry, media)
	return &Service{
		SubjectService:           subject.NewService(repositories.Application.Subjects),
		AdAnalyticsService:       NewAdAnalyticsService(repositories.Reporting.Ads),
		ConsumerUserService:      NewConsumerUserService(repositories.ConsumerUsers),
		TikTokPostbackLogService: NewTikTokPostbackLogService(repositories.Reporting.Postbacks),
		DramaService:             drama,
		ApplicationService:       NewApplicationService(repositories.Application.Registry, repositories.Monetization.Settings, repositories.Application.Subjects, repositories.Content.Catalog),
		MonetizationService:      NewMonetizationService(repositories.Monetization.Settings, repositories.Application.Registry),
		CampaignService: NewCampaignService(repositories.Campaign.Repository, repositories.Application.Registry, repositories.Content.Catalog, func(app application.Application) TikTokMediaProvider {
			// 官方适配器在启动阶段配置；投放只获取客户端，不持有短剧 Worker 或发布状态。
			if drama.tiktokProviderFactory == nil {
				return nil
			}
			return drama.tiktokProviderFactory(app)
		}),
		ReportingService: NewReportingService(repositories.Reporting.Metrics, repositories.Reporting.Audits),
	}
}
