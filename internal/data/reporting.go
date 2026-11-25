package data

import (
	"context"
	"time"

	"drama-server/internal/biz/operations"
	"drama-server/internal/data/persistence/mysqlstore"
)

// reportingRepository 将统计读取导向独立池，保留原 Worker 的清理、分表维护写入连接。
// 仪表盘重统计在独立池的只读事务完成，租约和发布另用业务池的短事务。
type reportingRepository struct {
	*mysqlstore.Store
	maintenance operations.MetricMaintenanceRepository
}

// MaintainMetrics 保持后台统计维护使用原业务仓储，避免仅切换查询时改变任务的持久化路径。
func (r *reportingRepository) MaintainMetrics(ctx context.Context, now time.Time) error {
	return r.maintenance.MaintainMetrics(ctx, now)
}

var (
	_ operations.MetricsRepository           = (*reportingRepository)(nil)
	_ operations.RewardAnalysisRepository    = (*reportingRepository)(nil)
	_ operations.DramaReportRepository       = (*reportingRepository)(nil)
	_ operations.CampaignReportRepository    = (*reportingRepository)(nil)
	_ operations.TikTokPostbackLogRepository = (*reportingRepository)(nil)
	_ operations.AdAnalyticsRepository       = (*reportingRepository)(nil)
	_ operations.MetricMaintenanceRepository = (*reportingRepository)(nil)
)
