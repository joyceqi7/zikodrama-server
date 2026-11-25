package mysqlstore

import (
	"context"
	"errors"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// refreshDashboard 保留内部刷新入口；领取和发布使用短事务，计算期间不持有状态行锁。
func (s *Store) refreshDashboard(ctx context.Context, now time.Time) error {
	return s.refreshDashboardSnapshot(ctx, now)
}

// readDashboardSnapshots 只扫描日期范围所涉及且已存在的月表，历史数据不依赖明细表。
func (s *Store) readDashboardSnapshots(ctx context.Context, from, to time.Time) ([]dashboardSnapshotModel, error) {
	tables, err := s.metricTables(ctx, true)
	if err != nil {
		return nil, err
	}
	result := []dashboardSnapshotModel{}
	for _, table := range tables {
		month, _ := time.Parse("200601", table[len("dashboard_daily_"):])
		if !month.Before(to) || !month.AddDate(0, 1, 0).After(from) {
			continue
		}
		var rows []dashboardSnapshotModel
		if err := s.data.DB().WithContext(ctx).Table(table).Where("application_id = ? AND date >= ? AND date < ?", application.IDFromContext(ctx), from.Format(time.DateOnly), to.Format(time.DateOnly)).Order("date").Find(&rows).Error; err != nil {
			return nil, err
		}
		result = append(result, rows...)
	}
	return result, nil
}

// DashboardDays 优先读取已发布快照，过期结果由 Worker 更新；只有首次无快照才尝试同步初始化。
func (s *Store) DashboardDays(ctx context.Context, from, to time.Time) ([]operations.DashboardDay, error) {
	state, _, err := s.readDashboardState(ctx, application.IDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	if state.RefreshedAt == nil {
		if err := s.refreshDashboard(ctx, time.Now().UTC()); err != nil {
			// 首次汇总尚未生成时不能用空数组伪造零统计；沿用可重试的服务不可用合同。
			if errors.Is(err, errDashboardRefreshBusy) {
				return nil, domain.ErrServiceUnavailable
			}
			return nil, err
		}
	}
	rows, err := s.readDashboardSnapshots(ctx, from, to)
	if err != nil {
		return nil, err
	}
	result := make([]operations.DashboardDay, 0, len(rows))
	for _, row := range rows {
		result = append(result, operations.DashboardDay(row.DashboardCounters))
	}
	return result, nil
}

// MaintainMetrics 刷新应用月汇总、预建明日日表，再删除超过 90 个 UTC 自然日的明细和去重键。
// 七天诊断独立清理；应用汇总失败中止九十天计数清理，保留月汇总及首次采集时间，不操作奖励与权益账本。
func (s *Store) MaintainMetrics(ctx context.Context, now time.Time) error {
	maintenanceStarted := time.Now()
	// 按小批删除短期诊断，不依赖九十天计数分表清理。
	for {
		result := s.data.DB().WithContext(ctx).Where("received_at < ?", now.UTC().Add(-7*24*time.Hour)).Order("received_at").Limit(5000).Delete(&metricPostbackDetailModel{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected < 5000 {
			break
		}
	}

	// 广告阶段与日计数采用同一九十日窗口，按索引小批清理。
	for {
		result := s.data.DB().WithContext(ctx).Where("occurred_at < ?", metricCutoff(now)).Order("occurred_at").Limit(5000).Delete(&metricAdEventModel{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected < 5000 {
			break
		}
	}
	var apps []string
	if err := s.data.DB().WithContext(ctx).Table("applications").Pluck("id", &apps).Error; err != nil {
		return err
	}
	allSummariesReady := true
	for _, app := range apps {
		// 多应用依次刷新时推进本轮逻辑时间，避免后面的应用用几分钟前的开始时间调度。
		if err := s.refreshDashboard(application.WithContext(ctx, app), now.Add(time.Since(maintenanceStarted))); err != nil {
			if errors.Is(err, errDashboardRefreshBusy) {
				// 其他实例仍在刷新时继续处理其他应用，但不能据此清理它需要的旧事实。
				allSummariesReady = false
				continue
			}
			return err
		}
	}
	for _, day := range []time.Time{now, now.AddDate(0, 0, 1)} {
		if err := s.ensureMetricTable(ctx, metricDayTable(day), "metric_events_template"); err != nil {
			return err
		}
	}
	if !allSummariesReady {
		return nil
	}
	tables, err := s.metricTables(ctx, false)
	if err != nil {
		return err
	}
	cutoff := metricCutoff(now)
	for _, entry := range []struct{ table, column string }{{"metric_active_users_daily", "activity_date"}, {"metric_summary_days", "date"}} {
		for {
			deleted := s.data.DB().WithContext(ctx).Table(entry.table).Where(entry.column+" < ?", cutoff.Format(time.DateOnly)).Order(entry.column).Limit(5000).Delete(&struct{}{})
			if deleted.Error != nil {
				return deleted.Error
			}
			if deleted.RowsAffected < 5000 {
				break
			}
		}
	}
	for _, table := range tables {
		day, _ := time.Parse("20060102", table[len("metric_events_"):])
		if day.Before(cutoff) {
			if err := s.data.DB().WithContext(ctx).Migrator().DropTable(table); err != nil {
				return err
			}
		}
	}
	// 独立索引按有序小批删除，避免一个清理事务锁住整段写入。
	for {
		var keys []metricEventKey
		db := s.data.DB().WithContext(ctx)
		if err := db.Where("event_date < ?", cutoff.Format(time.DateOnly)).Order("event_date").Limit(5000).Find(&keys).Error; err != nil {
			return err
		}
		if len(keys) == 0 {
			break
		}
		ids := make([][]any, 0, len(keys))
		for _, key := range keys {
			ids = append(ids, []any{key.ApplicationID, key.UserID, key.EventID, key.Kind})
		}
		if err := db.Where("(application_id,user_id,event_id,kind) IN ?", ids).Delete(&metricEventKey{}).Error; err != nil {
			return err
		}
	}
	return nil
}
