package mysqlstore

import (
	"context"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/plugin/dbresolver"
)

// dashboardRefreshResult 只在内存中承接一致性读取，发布时统一提交，不暴露半成品。
type dashboardRefreshResult struct {
	months map[string][]dashboardSnapshotModel
	days   []metricSummaryDayModel
	first  *time.Time
}

func (s *Store) readDashboardState(ctx context.Context, app string) (metricSummaryState, bool, error) {
	var state metricSummaryState
	result := s.data.DB().WithContext(ctx).Clauses(dbresolver.Write).Where("application_id = ?", app).Limit(1).Find(&state)
	return state, result.RowsAffected > 0, result.Error
}

// refreshDashboardSnapshot 领取、计算和发布分离，扫描原始事件期间没有状态行写锁。
func (s *Store) refreshDashboardSnapshot(ctx context.Context, now time.Time) error {
	started := time.Now()
	app := application.IDFromContext(ctx)
	state, exists, err := s.readDashboardState(ctx, app)
	if err != nil {
		return err
	}
	if state.RefreshedAt != nil && now.Sub(*state.RefreshedAt) < time.Minute {
		return nil
	}
	lease, err := s.acquireDashboardRefresh(ctx, app, now, exists)
	if err != nil {
		return err
	}
	defer lease.release(ctx)
	boundedCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	jobCtx, stopHeartbeat := lease.keepAlive(boundedCtx)
	defer stopHeartbeat()
	from := metricCutoff(now)
	to := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, 1)
	for at := from; at.Before(to); at = time.Date(at.Year(), at.Month()+1, 1, 0, 0, 0, 0, time.UTC) {
		if err := s.ensureMetricTable(jobCtx, metricMonthTable(at), "dashboard_daily_template"); err != nil {
			return err
		}
	}
	tables, err := s.metricTables(jobCtx, false)
	if err != nil {
		return err
	}
	if err := s.prepareMetricActiveUsers(jobCtx, app, tables, from, to, lease.state.RefreshedAt == nil); err != nil {
		return err
	}
	result, err := s.calculateDashboard(jobCtx, lease.state, tables, now, from, to)
	if err != nil {
		if cause := context.Cause(jobCtx); cause != nil {
			return cause
		}
		return err
	}
	if err := jobCtx.Err(); err != nil {
		return context.Cause(jobCtx)
	}
	// 提交前停止续期，防止提交清空持有者后，迟到心跳把成功发布误判为租约丢失。
	stopHeartbeat()
	// 调度使用完成时间；统计窗口仍保存启动基准，跨 UTC 日完成不能冒充下一日已计算。
	completed := now.Add(time.Since(started))
	publishCtx, stopPublish := context.WithTimeout(jobCtx, 5*time.Second)
	defer stopPublish()
	return s.publishDashboard(publishCtx, lease, result, now, completed)
}

// calculateDashboard 从独立统计池的同一只读快照读取账本、行为及日活。
func (s *Store) calculateDashboard(ctx context.Context, state metricSummaryState, tables []string, now, from, to time.Time) (dashboardRefreshResult, error) {
	result := dashboardRefreshResult{months: map[string][]dashboardSnapshotModel{}}
	readData := s.metricReadData
	if readData == nil {
		readData = s.data
	}
	reader := &Store{data: &Data{db: readData.DB().Clauses(dbresolver.Write)}}
	err := reader.readSnapshot(ctx, func(tx *gorm.DB) error {
		snapshotStore := &Store{data: &Data{db: tx}, ids: s.ids}
		previous := state.SnapshotAt
		if previous == nil {
			previous = state.RefreshedAt
		}
		if state.RefreshedAt == nil {
			previous = nil
		}
		previousCutoff := from
		if previous != nil {
			previousCutoff = metricCutoff(*previous)
		}
		refreshFrom := dashboardRefreshFrom(now, previous, from)
		ledger, err := snapshotStore.dashboardLedgerDays(ctx, refreshFrom, to)
		if err != nil {
			return err
		}
		// 跨日仍校正账本和 MAU；行为按日修订、迟到及过期完成选日，仅初始化或显式重建扫描全窗。
		behaviors, mau, first, days, err := dashboardRefreshBehavior(tx, state.ApplicationID, tables, from, to, refreshFrom, previousCutoff, state.RefreshedAt == nil)
		if err != nil {
			return err
		}
		result.first, result.days = first, days
		ledgerByDay := map[string]operations.DashboardDay{}
		for _, row := range ledger {
			ledgerByDay[row.Date] = row
		}
		existing, err := snapshotStore.readDashboardSnapshots(ctx, from, to)
		if err != nil {
			return err
		}
		old := map[string]dashboardSnapshotModel{}
		for _, row := range existing {
			old[row.Date] = row
		}
		for at := from; at.Before(to); at = at.AddDate(0, 0, 1) {
			date := at.Format(time.DateOnly)
			day, changed := behaviors[date]
			if at.Before(refreshFrom) && !changed {
				continue
			}
			if !changed {
				day = operations.DashboardDay(old[date].DashboardCounters)
			}
			totals := ledgerByDay[date]
			if at.Before(refreshFrom) {
				totals = operations.DashboardDay(old[date].DashboardCounters)
			}
			day.Date = date
			day.NewUsers, day.RewardCompletions = totals.NewUsers, totals.RewardCompletions
			day.EpisodeUnlocks, day.VIPActivations = totals.EpisodeUnlocks, totals.VIPActivations
			monthly := old[date].MonthlyActiveUsers
			if !at.Before(refreshFrom) && !at.AddDate(0, 0, -29).Before(from) {
				value := mau[date]
				monthly = &value
			}
			result.months[metricMonthTable(at)] = append(result.months[metricMonthTable(at)], dashboardSnapshotModel{
				ApplicationID: state.ApplicationID, DashboardCounters: DashboardCounters(day), MonthlyActiveUsers: monthly, UpdatedAt: now})
		}
		return nil
	})
	return result, err
}

// publishDashboard 用同一短事务验证发布代际、写快照和确认日修订；旧租约不能覆盖后来者。
func (s *Store) publishDashboard(ctx context.Context, lease *dashboardRefreshLease, result dashboardRefreshResult, snapshotAt, completedAt time.Time) error {
	return s.metricWriteData().DB().WithContext(ctx).Clauses(dbresolver.Write).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{"refreshed_at": completedAt, "snapshot_at": snapshotAt, "refresh_owner": "", "refresh_lease_until": nil}
		if result.first != nil {
			updates["first_received_at"] = gorm.Expr("CASE WHEN first_received_at IS NULL OR first_received_at > ? THEN ? ELSE first_received_at END", *result.first, *result.first)
		}
		claimed := tx.Model(&metricSummaryState{}).
			Where("application_id = ? AND refresh_owner = ? AND refresh_generation = ? AND refresh_lease_until > UTC_TIMESTAMP(6)", lease.state.ApplicationID, lease.owner, lease.state.RefreshGeneration).
			Updates(updates)
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected != 1 {
			return errDashboardRefreshLost
		}
		for table, rows := range result.months {
			if err := tx.Table(table).Clauses(clause.OnConflict{UpdateAll: true}).Create(&rows).Error; err != nil {
				return err
			}
		}
		for _, day := range result.days {
			// 不把本轮快照之后提交的新修订标记为已处理。
			if err := tx.Model(&metricSummaryDayModel{}).Where("application_id = ? AND date = ?", day.ApplicationID, day.Date).
				Updates(map[string]any{"behavior_ready": true, "published_revision": gorm.Expr("GREATEST(published_revision, ?)", day.Revision)}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
