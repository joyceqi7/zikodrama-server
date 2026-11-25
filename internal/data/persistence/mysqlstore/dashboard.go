package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"time"
)

// dashboardLedgerDays 按日期扫描账本，仅用于生成可重复刷新的月表快照。
func (s *Store) dashboardLedgerDays(ctx context.Context, from, to time.Time) ([]operations.DashboardDay, error) {
	days := map[string]*operations.DashboardDay{}
	db := s.data.DB().WithContext(ctx)
	app := application.IDFromContext(ctx)
	for shard := uint16(0); shard < 16; shard++ {
		for _, query := range []struct{ table, timestamp, metric, expression, kind string }{
			{"users", "created_at", "users", "COUNT(*)", ""},
			{"reward_grants", "granted_at", "rewards", "COUNT(*)", ""},
			{"entitlement_grants", "granted_at", "episodes", "COALESCE(SUM(JSON_LENGTH(data, '$.episodeIds')),0)", "episode_access"},
			{"entitlement_grants", "granted_at", "vip", "COUNT(*)", "vip"},
		} {
			var rows []struct {
				Date  string
				Value int64
			}
			statement := db.Table(userTable(query.table, shard)).Select("DATE_FORMAT("+query.timestamp+", '%Y-%m-%d') AS date, "+query.expression+" AS value").Where("application_id = ? AND "+query.timestamp+" >= ? AND "+query.timestamp+" < ?", app, from.UTC(), to.UTC())
			if query.kind != "" {
				statement = statement.Where("type = ?", query.kind)
			}
			if err := statement.Group("date").Scan(&rows).Error; err != nil {
				return nil, err
			}
			for _, row := range rows {
				if days[row.Date] == nil {
					days[row.Date] = &operations.DashboardDay{Date: row.Date}
				}
				day := days[row.Date]
				switch query.metric {
				case "users":
					day.NewUsers += row.Value
				case "rewards":
					day.RewardCompletions += row.Value
				case "episodes":
					day.EpisodeUnlocks += row.Value
				case "vip":
					day.VIPActivations += row.Value
				}
			}
		}
	}
	result := make([]operations.DashboardDay, 0, len(days))
	for _, day := range days {
		result = append(result, *day)
	}
	return result, nil
}
