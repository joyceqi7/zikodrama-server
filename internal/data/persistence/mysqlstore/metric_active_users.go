package mysqlstore

import (
	"context"
	"sort"
	"strings"
	"time"

	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/plugin/dbresolver"
)

// metricActiveUserModel 每天每用户只保存一次活跃，不记录剧目或观看历史。
type metricActiveUserModel struct {
	// ApplicationID 隔离认证应用。
	ApplicationID string `gorm:"primaryKey"`
	// ActivityDate 是事实的 UTC 日期。
	ActivityDate string `gorm:"primaryKey"`
	// UserID 是认证业务用户，保持字符串精度。
	UserID string `gorm:"primaryKey"`
}

// TableName 返回精确日活集合，不依赖 Redis 计数或近似去重。
func (metricActiveUserModel) TableName() string { return "metric_active_users_daily" }

// metricSummaryDayModel 记录日活初始化和行为汇总覆盖版本，不把接收时间当作提交水位。
type metricSummaryDayModel struct {
	// ApplicationID 隔离认证应用。
	ApplicationID string `gorm:"primaryKey"`
	// Date 是 UTC 事实日期。
	Date string `gorm:"primaryKey"`
	// Revision 仅在接受新事实的同一事务中递增。
	Revision uint64
	// PublishedRevision 是已提交快照覆盖的修订，并发新增事实仍保持待处理。
	PublishedRevision uint64
	// ActivityReady 表示该日历史日活已经初始化。
	ActivityReady bool
	// BehaviorReady 表示已有完整的行为快照，包括空日期。
	BehaviorReady bool
}

// TableName 返回每日汇总工作状态表。
func (metricSummaryDayModel) TableName() string { return "metric_summary_days" }

// recordMetricActivity 与首次事实落库共用事务；重复上报不增加日活或刷新修订。
func recordMetricActivity(tx *gorm.DB, rows []metricReceiptModel) error {
	if len(rows) == 0 {
		return nil
	}
	days := map[string]bool{}
	actives := []metricActiveUserModel{}
	for _, row := range rows {
		date := row.OccurredAt.UTC().Format(time.DateOnly)
		if days[date] {
			continue
		}
		days[date] = true
		actives = append(actives, metricActiveUserModel{ApplicationID: row.ApplicationID, ActivityDate: date, UserID: row.UserID})
	}
	// 不同用户跨日补报时也按日期取得工作状态行锁，避免相反日期顺序造成死锁。
	sort.Slice(actives, func(i, j int) bool { return actives[i].ActivityDate < actives[j].ActivityDate })
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&actives).Error; err != nil {
		return err
	}
	for _, active := range actives {
		day := metricSummaryDayModel{ApplicationID: active.ApplicationID, Date: active.ActivityDate, Revision: 1}
		if err := tx.Clauses(clause.OnConflict{DoUpdates: clause.Assignments(map[string]any{"revision": gorm.Expr("revision + 1")})}).Create(&day).Error; err != nil {
			return err
		}
	}
	return nil
}

// prepareMetricActiveUsers 只回填未初始化日期；已初始化日期由事实写入事务维护，显式重建时重新回源。
// 回源读取与小批插入分开，不在扫描事件表时锁住刷新状态或日修订行。
func (s *Store) prepareMetricActiveUsers(ctx context.Context, app string, tables []string, from, to time.Time, rebuild bool) error {
	// 每条语句克隆干净会话，读取日状态不能污染后续日活模型的写入字段。
	db := s.metricWriteData().DB().WithContext(ctx).Clauses(dbresolver.Write).Session(&gorm.Session{})
	var states []metricSummaryDayModel
	if err := db.Where("application_id = ? AND date >= ? AND date < ?", app, from.Format(time.DateOnly), to.Format(time.DateOnly)).Find(&states).Error; err != nil {
		return err
	}
	ready := map[string]bool{}
	for _, state := range states {
		ready[state.Date] = state.ActivityReady
	}
	readData := s.metricReadData
	if readData == nil {
		readData = s.data
	}
	for _, table := range metricEventTables(tables, from, to) {
		day, _ := time.Parse("20060102", strings.TrimPrefix(table, "metric_events_"))
		date := day.Format(time.DateOnly)
		if ready[date] && !rebuild {
			continue
		}
		var users []string
		if err := readData.DB().WithContext(ctx).Clauses(dbresolver.Write).Table(table).
			Where("application_id = ?", app).Distinct("user_id").Pluck("user_id", &users).Error; err != nil {
			return err
		}
		for offset := 0; offset < len(users); offset += 500 {
			batch := make([]metricActiveUserModel, 0, min(500, len(users)-offset))
			for _, user := range users[offset:min(offset+500, len(users))] {
				batch = append(batch, metricActiveUserModel{ApplicationID: app, ActivityDate: date, UserID: user})
			}
			if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&batch).Error; err != nil {
				return err
			}
		}
		state := metricSummaryDayModel{ApplicationID: app, Date: date, ActivityReady: true}
		if err := db.Clauses(clause.OnConflict{DoUpdates: clause.Assignments(map[string]any{"activity_ready": true})}).Create(&state).Error; err != nil {
			return err
		}
	}
	return nil
}

// dashboardActiveAudience 在精确日活集合上滑动去重，避免每轮扫描三十张原始事件表和 SQL 窗口排序。
// 返回每日人数及滚动三十日人数；保留窗口不足的 MAU 是否可发布仍由快照发布逻辑判断。
func dashboardActiveAudience(tx *gorm.DB, app string, from, to time.Time) (map[string]int64, map[string]int64, error) {
	cursor, err := tx.Model(&metricActiveUserModel{}).Select("activity_date,user_id").
		Where("application_id = ? AND activity_date >= ? AND activity_date < ?", app, from.Format(time.DateOnly), to.Format(time.DateOnly)).
		Order("activity_date,user_id").Rows()
	if err != nil {
		return nil, nil, err
	}
	defer cursor.Close()
	daily, monthly := map[string]int64{}, map[string]int64{}
	// 每个用户仅保留最近活跃日；从旧日期桶移除后再放入新桶，内存不会随重复活跃天数倍增。
	lastSeen := map[string]string{}
	buckets := map[string]map[string]struct{}{}
	var pendingDate, pendingUser string
	hasRow := cursor.Next()
	if hasRow {
		if err := cursor.Scan(&pendingDate, &pendingUser); err != nil {
			return nil, nil, err
		}
	}
	for day := from; day.Before(to); day = day.AddDate(0, 0, 1) {
		if err := tx.Statement.Context.Err(); err != nil {
			return nil, nil, err
		}
		date := day.Format(time.DateOnly)
		bucket := map[string]struct{}{}
		buckets[date] = bucket
		for hasRow && pendingDate <= date {
			if previous, exists := lastSeen[pendingUser]; exists {
				delete(buckets[previous], pendingUser)
			}
			lastSeen[pendingUser] = date
			bucket[pendingUser] = struct{}{}
			daily[date]++
			hasRow = cursor.Next()
			if hasRow {
				if err := cursor.Scan(&pendingDate, &pendingUser); err != nil {
					return nil, nil, err
				}
			}
		}
		expired := day.AddDate(0, 0, -30).Format(time.DateOnly)
		for user := range buckets[expired] {
			delete(lastSeen, user)
		}
		delete(buckets, expired)
		monthly[date] = int64(len(lastSeen))
	}
	if err := cursor.Err(); err != nil {
		return nil, nil, err
	}
	return daily, monthly, nil
}

// dashboardRefreshBehavior 只重算有新事实、尚未初始化或被迟到及过期完成影响的日期。
// previousCutoff 用于识别上轮快照之后离开保留窗口的完成键，保持完成先到时的窗口统计口径。
func dashboardRefreshBehavior(tx *gorm.DB, app string, tables []string, cutoff, to, refreshFrom, previousCutoff time.Time, full bool) (map[string]operations.DashboardDay, map[string]int64, *time.Time, []metricSummaryDayModel, error) {
	var states []metricSummaryDayModel
	if err := tx.Where("application_id = ? AND date >= ? AND date < ?", app, cutoff.Format(time.DateOnly), to.Format(time.DateOnly)).Find(&states).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	byDate := map[string]metricSummaryDayModel{}
	for _, state := range states {
		byDate[state.Date] = state
	}
	selected := map[string]bool{}
	for _, table := range metricEventTables(tables, cutoff, to) {
		at, _ := time.Parse("20060102", strings.TrimPrefix(table, "metric_events_"))
		date := at.Format(time.DateOnly)
		state := byDate[date]
		if full || !state.BehaviorReady || state.Revision != state.PublishedRevision {
			selected[date] = true
		}
	}
	if !full {
		// 查询完整事实键，保留完成晚于开始很多天以及两个并发事务交错提交的回补能力。
		for _, table := range metricEventTables(tables, cutoff, to) {
			at, _ := time.Parse("20060102", strings.TrimPrefix(table, "metric_events_"))
			if !selected[at.Format(time.DateOnly)] {
				continue
			}
			var dates []string
			if err := tx.Table(table+" AS recent").
				Joins(`JOIN metric_event_keys origin ON origin.application_id=recent.application_id AND origin.user_id=recent.user_id AND origin.event_id=recent.event_id
 AND origin.kind=CASE recent.kind WHEN 'play_complete' THEN 'play_start' WHEN 'rewarded_complete' THEN 'rewarded_show' END`).
				Where("recent.application_id=? AND recent.kind IN ? AND origin.event_date>=? AND origin.event_date<?", app, []string{"play_complete", "rewarded_complete"}, cutoff.Format(time.DateOnly), to.Format(time.DateOnly)).
				Distinct("origin.event_date").Pluck("origin.event_date", &dates).Error; err != nil {
				return nil, nil, nil, nil, err
			}
			for _, date := range dates {
				selected[date] = true
			}
		}
		if previousCutoff.Before(cutoff) {
			// 完成请求可能先于开始请求落库；完成键离开保留窗口时，只回算仍在窗口内的匹配开始日。
			// 读取过期日期索引而非重扫全部事件日表；维护任务在所有应用刷新成功后才清理这些键。
			var dates []string
			if err := tx.Table("metric_event_keys AS expired").
				Joins(`JOIN metric_event_keys origin ON origin.application_id=expired.application_id AND origin.user_id=expired.user_id AND origin.event_id=expired.event_id
 AND origin.kind=CASE expired.kind WHEN 'play_complete' THEN 'play_start' WHEN 'rewarded_complete' THEN 'rewarded_show' END`).
				Where("expired.application_id=? AND expired.kind IN ? AND expired.event_date>=? AND expired.event_date<? AND origin.event_date>=? AND origin.event_date<?",
					app, []string{"play_complete", "rewarded_complete"}, previousCutoff.Format(time.DateOnly), cutoff.Format(time.DateOnly), cutoff.Format(time.DateOnly), to.Format(time.DateOnly)).
				Distinct("origin.event_date").Pluck("origin.event_date", &dates).Error; err != nil {
				return nil, nil, nil, nil, err
			}
			for _, date := range dates {
				selected[date] = true
			}
		}
	}
	behaviorTables := []string{}
	ack := []metricSummaryDayModel{}
	for _, table := range metricEventTables(tables, cutoff, to) {
		at, _ := time.Parse("20060102", strings.TrimPrefix(table, "metric_events_"))
		date := at.Format(time.DateOnly)
		if selected[date] {
			behaviorTables = append(behaviorTables, table)
			state := byDate[date]
			state.ApplicationID, state.Date = app, date
			ack = append(ack, state)
		}
	}
	var rows []struct {
		// DashboardDay 汇总该 UTC 事实日的行为。
		operations.DashboardDay
		// FirstReceivedAt 保留首次事实接收时间。
		FirstReceivedAt *time.Time
	}
	query, args := dashboardBehaviorQuery(behaviorTables, cutoff, to, app, true)
	if query != "" {
		if err := tx.Table("(?) AS behaviors", gorm.Expr(query, args...)).Select("*").Scan(&rows).Error; err != nil {
			return nil, nil, nil, nil, err
		}
	}
	audienceFrom := refreshFrom.AddDate(0, 0, -29)
	if audienceFrom.Before(cutoff) {
		audienceFrom = cutoff
	}
	daily, monthly, err := dashboardActiveAudience(tx, app, audienceFrom, to)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	days := map[string]operations.DashboardDay{}
	var first *time.Time
	for _, row := range rows {
		// 历史迟到日期可能在本轮 MAU 窗口之前，单日去重查询仍只访问小日活集合。
		if _, ok := daily[row.Date]; !ok {
			if err := tx.Model(&metricActiveUserModel{}).Where("application_id=? AND activity_date=?", app, row.Date).Count(&row.ActiveUsers).Error; err != nil {
				return nil, nil, nil, nil, err
			}
		} else {
			row.ActiveUsers = daily[row.Date]
		}
		days[row.Date] = row.DashboardDay
		if row.FirstReceivedAt != nil && (first == nil || row.FirstReceivedAt.Before(*first)) {
			first = row.FirstReceivedAt
		}
	}
	return days, monthly, first, ack, nil
}
