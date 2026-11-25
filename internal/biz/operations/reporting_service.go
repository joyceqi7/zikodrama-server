package operations

// ReportingService 管理统计和审计，不访问媒资或变现配置。
type ReportingService struct {
	metrics         MetricsRepository
	audits          AuditRepository
	rewards         RewardAnalysisRepository
	dramaReports    DramaReportRepository
	campaignReports CampaignReportRepository
}

// NewReportingService 只装配统计读取和审计记录端口。
func NewReportingService(metrics MetricsRepository, audits AuditRepository) *ReportingService {
	rewards, _ := metrics.(RewardAnalysisRepository)
	dramaReports, _ := metrics.(DramaReportRepository)
	campaignReports, _ := metrics.(CampaignReportRepository)
	return &ReportingService{metrics: metrics, audits: audits, rewards: rewards, dramaReports: dramaReports, campaignReports: campaignReports}
}
