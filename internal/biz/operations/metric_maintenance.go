package operations

import (
	"context"
	"log/slog"
	"time"
)

// MetricMaintenanceRepository 维护日期明细、月度汇总和90日明细生命周期；不删除权威业务账本。
type MetricMaintenanceRepository interface {
	// MaintainMetrics 按 now 的 UTC 日期刷新统计、准备分表并清理过期明细；失败时返回具体错误。
	MaintainMetrics(context.Context, time.Time) error
}

// MaintainMetrics 调用可选的持久化维护能力；测试内存仓储没有维护任务。
func (s *ReportingService) MaintainMetrics(ctx context.Context, now time.Time) error {
	repository, ok := s.metrics.(MetricMaintenanceRepository)
	if !ok {
		return nil
	}
	if err := repository.MaintainMetrics(ctx, now); err != nil {
		slog.ErrorContext(ctx, "maintain dashboard partitions failed", "error", err)
		return err
	}
	return nil
}
