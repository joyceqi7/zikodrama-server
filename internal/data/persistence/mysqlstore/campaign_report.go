package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"strings"
)

// CampaignReport 跨分片聚合逐集和 VIP 奖励，按链接及应用共同关联以排除脏归因。
func (s *Store) CampaignReport(ctx context.Context, input operations.CampaignReportQuery) (*operations.CampaignReportPage, error) {
	result := &operations.CampaignReportPage{Items: []operations.CampaignReportRow{}}
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		snapshot := &Store{data: &Data{db: tx}, ids: s.ids}
		shows, err := snapshot.reportAdShows(ctx, input.From, input.To)
		if err != nil {
			return err
		}
		result.AdShows = shows
		base := func() *gorm.DB {
			q := tx.Table("application_campaigns c").Where("c.application_id IN (?)", tx.Table("applications").Select("id").Where("subject_id=?", application.SubjectIDFromContext(ctx)))
			// 空应用范围保留本主体全部链接；单应用同时约束配置与奖励账本。
			if app := application.IDFromContext(ctx); app != "" {
				q = q.Where("c.application_id = ?", app)
			}
			if input.Query != "" {
				search := strings.ToLower(input.Query)
				q = q.Where("LOCATE(?,LOWER(c.name)) > 0 OR LOCATE(?,LOWER(c.code)) > 0", search, search)
			}
			return q
		}
		if err := base().Count(&result.Total).Error; err != nil {
			return err
		}
		matched := base().Select("c.id")
		if err := snapshot.rewardReportBase(ctx, input.From, input.To).
			Joins("JOIN application_campaigns c ON c.id=r.campaign_id AND c.application_id=r.application_id").
			Where("c.id IN (?) AND r.reward_type IN ?", matched, []string{"episode_unlock", "vip_progress"}).
			Select(rewardReportSummaryColumns).Scan(&result.Summary).Error; err != nil {
			return err
		}
		// 无链接、已删除链接或不匹配应用的归因均单独汇总，不混入有效链接排行。
		if err := snapshot.rewardReportBase(ctx, input.From, input.To).
			Joins("LEFT JOIN application_campaigns c ON c.id=r.campaign_id AND c.application_id=r.application_id").
			Where("c.id IS NULL AND r.reward_type IN ?", []string{"episode_unlock", "vip_progress"}).
			Select(rewardReportSummaryColumns).Scan(&result.Unattributed).Error; err != nil {
			return err
		}
		rewards := snapshot.rewardReportBase(ctx, input.From, input.To).
			Select("r.campaign_id,r.application_id,"+rewardReportCounts+",SUM(r.reward_type='episode_unlock') AS episode_rewards,SUM(r.reward_type='vip_progress') AS vip_rewards").
			Where("r.campaign_id IS NOT NULL AND r.reward_type IN ?", []string{"episode_unlock", "vip_progress"}).Group("r.campaign_id,r.application_id")
		order := "COALESCE(r.reward_completions,0)"
		if input.Sort == "rewardUsers" {
			order = "COALESCE(r.reward_users,0)"
		}
		// 零领取人数没有有效人均值，升降序均放在有效记录之后。
		if input.Sort == "averageRewards" {
			order = "(r.reward_users IS NULL OR r.reward_users=0) ASC,r.reward_completions / NULLIF(r.reward_users,0)"
		}
		direction := "DESC"
		if input.Direction == "asc" {
			direction = "ASC"
		}
		return base().Joins("LEFT JOIN (?) r ON r.campaign_id=c.id AND r.application_id=c.application_id", rewards).
			Joins("LEFT JOIN applications a ON a.id=c.application_id").Joins("LEFT JOIN dramas d ON d.id=c.drama_id AND d.subject_id=?", application.SubjectIDFromContext(ctx)).
			Select("c.id AS campaign_id,c.application_id,c.name,c.code,c.status,c.landing_url,COALESCE(a.name,CONCAT('历史小程序 ',c.application_id)) AS application_name,COALESCE(d.title,CONCAT('历史剧 ',c.drama_id),'未绑定剧') AS drama_title,COALESCE(r.reward_completions,0) AS reward_completions,COALESCE(r.reward_users,0) AS reward_users,COALESCE(r.episode_rewards,0) AS episode_rewards,COALESCE(r.vip_rewards,0) AS vip_rewards,r.reward_completions / NULLIF(r.reward_users,0) AS average_rewards").
			Order(order + " " + direction).Order("c.id ASC").Offset((input.Page - 1) * input.PageSize).Limit(input.PageSize).Scan(&result.Items).Error
	})
	return result, err
}
