package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"strings"
	"time"
)

// rewardReportBase 将所有分表限制到当前主体的应用；全部应用只在主体内汇总，缺少主体时不返回数据。
func (s *Store) rewardReportBase(ctx context.Context, from, to time.Time) *gorm.DB {
	parts := []string{}
	args := []any{}
	app := application.IDFromContext(ctx)
	for shard := uint16(0); shard < 16; shard++ {
		query := "SELECT application_id,campaign_id,visitor_id,drama_id,episode_id,reward_type,granted_at FROM " + userTable("reward_grants", shard) + " WHERE granted_at >= ? AND granted_at < ? AND application_id IN (SELECT id FROM applications WHERE subject_id=?)"
		args = append(args, from.UTC(), to.UTC(), application.SubjectIDFromContext(ctx))
		if app != "" {
			query += " AND application_id = ?"
			args = append(args, app)
		}
		parts = append(parts, query)
	}
	return s.data.DB().WithContext(ctx).Table("("+strings.Join(parts, " UNION ALL ")+") r", args...)
}

// rewardReportSummaryColumns 将范围去重人数、类型拆分和人均值交由同一数据库快照计算。
const rewardReportSummaryColumns = rewardReportCounts + ",COUNT(*) / NULLIF(COUNT(DISTINCT r.application_id,r.visitor_id),0) AS average_rewards,COALESCE(SUM(r.reward_type='episode_unlock'),0) AS episode_rewards,COALESCE(SUM(r.reward_type='vip_progress'),0) AS vip_rewards"

// rewardReportCounts 按应用及用户成对去重，避免把不同应用的用户误认为同一人。
const rewardReportCounts = "COUNT(*) AS reward_completions, COUNT(DISTINCT r.application_id,r.visitor_id) AS reward_users"

// RewardAnalysis 合并主体内短剧贡献并保留链接的应用归属，使用同一数据库快照。
func (s *Store) RewardAnalysis(ctx context.Context, from, to time.Time) (*operations.RewardAnalysis, error) {
	result := &operations.RewardAnalysis{Dramas: []operations.DramaRewardRank{}, Campaigns: []operations.CampaignRewardContribution{}}
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		snapshot := &Store{data: &Data{db: tx}, ids: s.ids}
		if err := snapshot.rewardReportBase(ctx, from, to).
			Select("r.drama_id, COALESCE(d.title,CONCAT('历史剧 ',r.drama_id)) AS title,"+rewardReportCounts).
			Joins("LEFT JOIN dramas d ON d.id = r.drama_id AND d.subject_id=?", application.SubjectIDFromContext(ctx)).
			Where("r.reward_type = ? AND r.drama_id IS NOT NULL", "episode_unlock").
			Group("r.drama_id,d.title").Order("reward_completions DESC,r.drama_id ASC").Limit(50).Scan(&result.Dramas).Error; err != nil {
			return err
		}
		if err := snapshot.rewardReportBase(ctx, from, to).
			Select("c.id AS campaign_id,c.name,c.code,c.landing_url,c.application_id,COALESCE(a.name,CONCAT('历史小程序 ',c.application_id)) AS application_name,COALESCE(d.title,'未绑定剧') AS drama_title,"+rewardReportCounts+",SUM(r.reward_type='episode_unlock') AS episode_rewards,SUM(r.reward_type='vip_progress') AS vip_rewards").
			Joins("JOIN application_campaigns c ON c.id=r.campaign_id AND c.application_id=r.application_id").
			Joins("LEFT JOIN applications a ON a.id=c.application_id").Joins("LEFT JOIN dramas d ON d.id=c.drama_id AND d.subject_id=?", application.SubjectIDFromContext(ctx)).
			Group("c.id,c.name,c.code,c.landing_url,c.application_id,a.name,d.title").
			Order("reward_completions DESC,c.id ASC").Limit(50).Scan(&result.Campaigns).Error; err != nil {
			return err
		}
		return snapshot.rewardReportBase(ctx, from, to).Select(rewardReportCounts).
			Where("r.reward_type = ? AND r.drama_id IS NULL", "vip_progress").Scan(&result.UnassignedVIP).Error
	})
	return result, err
}

// RewardEpisodes 按目标集序合并跨应用，缺失或已归档避让集序使用 0 提示未知。
func (s *Store) RewardEpisodes(ctx context.Context, id string, from, to time.Time) ([]operations.EpisodeRewardRow, error) {
	result := []operations.EpisodeRewardRow{}
	err := s.rewardReportBase(ctx, from, to).Select("CASE WHEN e.episode_number > 0 THEN e.episode_number ELSE 0 END AS episode_number,"+rewardReportCounts).
		Joins("LEFT JOIN episodes e ON e.id=r.episode_id AND e.drama_id=r.drama_id").
		Where("r.drama_id=? AND r.reward_type=?", id, "episode_unlock").
		Group("CASE WHEN e.episode_number > 0 THEN e.episode_number ELSE 0 END").Order("episode_number ASC").Scan(&result).Error
	return result, err
}

// RewardTrend 使用结算日期归属，范围总人数独立去重而非逐日相加。
func (s *Store) RewardTrend(ctx context.Context, dimension, id string, from, to time.Time) (*operations.RewardTrend, error) {
	result := &operations.RewardTrend{Days: []operations.RewardTrendDay{}}
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		snapshot := &Store{data: &Data{db: tx}, ids: s.ids}
		base := func() *gorm.DB {
			q := snapshot.rewardReportBase(ctx, from, to)
			if dimension == "drama" {
				return q.Where("r.drama_id=? AND r.reward_type=?", id, "episode_unlock")
			}
			// 校验链接与奖励应用一致，防止脏归因跨应用串联。
			return q.Joins("JOIN application_campaigns c ON c.id=r.campaign_id AND c.application_id=r.application_id").Where("c.id=? AND r.reward_type IN ?", id, []string{"episode_unlock", "vip_progress"})
		}
		if err := base().Select("DATE_FORMAT(r.granted_at,'%Y-%m-%d') AS date," + rewardReportCounts).
			Group("DATE_FORMAT(r.granted_at,'%Y-%m-%d')").Order("date ASC").Scan(&result.Days).Error; err != nil {
			return err
		}
		return base().Select(rewardReportCounts).Scan(&result.Total).Error
	})
	return result, err
}
