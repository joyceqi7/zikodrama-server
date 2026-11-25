package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"drama-server/internal/pkg/identifier"
	"gorm.io/gorm"
	"strings"
)

// DramaReport 聚合当前主体应用的奖励分片，分页包含主体内零解锁剧；应用筛选不扩大内容边界。
func (s *Store) DramaReport(ctx context.Context, input operations.DramaReportQuery) (*operations.DramaReportPage, error) {
	result := &operations.DramaReportPage{Items: []operations.DramaReportRow{}}
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		snapshot := &Store{data: &Data{db: tx}, ids: s.ids}
		shows, err := snapshot.reportAdShows(ctx, input.From, input.To)
		if err != nil {
			return err
		}
		result.AdShows = shows
		base := func() *gorm.DB {
			q := tx.Table("dramas d").Where("d.subject_id=?", application.SubjectIDFromContext(ctx))
			if input.Query != "" {
				if identifier.ValidDatabaseID(input.Query) {
					q = q.Where("d.id = ?", input.Query)
				} else {
					q = q.Where("LOCATE(?,LOWER(d.title)) > 0", strings.ToLower(input.Query))
				}
			}
			return q
		}
		if err := base().Count(&result.Total).Error; err != nil {
			return err
		}
		// 汇总复用剧目筛选条件，但不分页；同一用户跨剧奖励仅计一个参与用户。
		matched := base().Select("d.id")
		if err := snapshot.rewardReportBase(ctx, input.From, input.To).
			Where("r.reward_type = ? AND r.drama_id IN (?)", "episode_unlock", matched).
			Select(rewardReportSummaryColumns).Scan(&result.Summary).Error; err != nil {
			return err
		}
		rewards := snapshot.rewardReportBase(ctx, input.From, input.To).
			Select("r.drama_id,COUNT(*) AS unlock_count,COUNT(DISTINCT r.application_id,r.visitor_id) AS unlock_users").
			Where("r.reward_type = ? AND r.drama_id IS NOT NULL", "episode_unlock").Group("r.drama_id")
		order := "COALESCE(r.unlock_count,0)"
		if input.Sort == "unlockUsers" {
			order = "COALESCE(r.unlock_users,0)"
		}
		if input.Sort == "averageUnlocks" {
			order = "r.unlock_count / NULLIF(r.unlock_users,0)"
		}
		// 未采集的播放指标全为空，始终按剧 ID 稳定排序，不假装有播放排名。
		if input.Sort == "playUsers" || input.Sort == "firstEpisodeCompletionRate" {
			order = "NULL"
		}
		direction := "DESC"
		if input.Direction == "asc" {
			direction = "ASC"
		}
		if input.Sort == "averageUnlocks" {
			order = "(r.unlock_users IS NULL OR r.unlock_users=0) ASC," + order
		}
		q := base().Joins("LEFT JOIN (?) r ON r.drama_id=d.id", rewards).
			Select("d.id AS drama_id,d.title,COALESCE(r.unlock_count,0) AS unlock_count,COALESCE(r.unlock_users,0) AS unlock_users,r.unlock_count / NULLIF(r.unlock_users,0) AS average_unlocks")
		if order != "NULL" {
			q = q.Order(order + " " + direction)
		}
		return q.Order("d.id ASC").Offset((input.Page - 1) * input.PageSize).Limit(input.PageSize).Scan(&result.Items).Error
	})
	return result, err
}
