package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"strings"
	"time"
)

// CampaignRewardRanking 跨全部用户分表聚合已归因奖励；停用链接保留历史贡献。
func (s *Store) CampaignRewardRanking(ctx context.Context, from, to time.Time) ([]operations.CampaignRewardRank, error) {
	app := application.IDFromContext(ctx)
	parts := make([]string, 0, 16)
	args := make([]any, 0, 49)
	for shard := uint16(0); shard < 16; shard++ {
		parts = append(parts, "SELECT campaign_id, visitor_id FROM "+userTable("reward_grants", shard)+" WHERE application_id = ? AND granted_at >= ? AND granted_at < ? AND campaign_id IS NOT NULL")
		args = append(args, app, from.UTC(), to.UTC())
	}
	// 用户 ID 全局唯一；合并后去重，避免同一人多次领奖增加领取人数。
	result := []operations.CampaignRewardRank{}
	err := s.data.DB().WithContext(ctx).
		Table("("+strings.Join(parts, " UNION ALL ")+") r", args...).
		Select("c.id AS campaign_id, c.name, c.code, c.landing_url, COUNT(*) AS reward_completions, COUNT(DISTINCT r.visitor_id) AS reward_users").
		Joins("JOIN application_campaigns c ON c.id = r.campaign_id AND c.application_id = ?", app).
		Group("c.id, c.name, c.code, c.landing_url").
		Order("reward_completions DESC, c.id ASC").Limit(50).Scan(&result).Error
	return result, err
}
