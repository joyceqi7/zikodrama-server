package mysqlstore

import (
	"context"
	"encoding/json"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

// FindOwnedEpisodeIDs 从主库批量读取当前剧的有效投影，用于幂等恢复后返回最新选集状态。
func (s *Store) FindOwnedEpisodeIDs(ctx context.Context, visitorID, dramaID string, episodeIDs []string, now time.Time) (map[string]bool, error) {
	bucket, err := requestBucket(ctx)
	if err != nil {
		return nil, err
	}
	return rewardOwnedEpisodeIDsDB(s.data.DB().WithContext(ctx).Clauses(dbresolver.Write), bucket, application.IDFromContext(ctx), visitorID, dramaID, episodeIDs, now)
}

// rewardOwnedEpisodeIDsDB 只读取本次规则和连续提示需要的当前剧投影，空需求不查询数据库。
// 用户锁由调用方持有；目录及整剧权益一并参与判定，不扫描其他剧或授予历史。
func rewardOwnedEpisodeIDsDB(db *gorm.DB, bucketID uint16, appID, userID, dramaID string, episodeIDs []string, now time.Time) (map[string]bool, error) {
	owned := map[string]bool{}
	if len(episodeIDs) == 0 {
		return owned, nil
	}
	wanted := make(map[string]bool, len(episodeIDs))
	keys := []string{"application", "drama:" + dramaID}
	for _, episodeID := range episodeIDs {
		if !wanted[episodeID] {
			wanted[episodeID] = true
			keys = append(keys, "episode:"+dramaID+":"+episodeID)
		}
	}
	items, err := activeEntitlementStatesDB(db, bucketID, appID, userID, keys, now)
	if err != nil {
		return nil, err
	}
	registry := monetization.DefaultStrategies()
	seen := map[string]bool{}
	for _, item := range items {
		// 同一批授予按目标展开多个投影，但其 JSON 覆盖集合只需解析一次。
		if seen[item.GrantID] {
			continue
		}
		seen[item.GrantID] = true
		var data monetization.AccessData
		if err := json.Unmarshal(item.Data, &data); err != nil {
			return nil, err
		}
		all := item.Type == "vip" || item.Type == "drama_access" && data.IncludeFutureEpisodes
		probe := episodeIDs[0]
		if !all {
			if len(data.EpisodeIDs) == 0 {
				continue
			}
			probe = data.EpisodeIDs[0]
		}
		entitlement := monetization.Entitlement{ApplicationID: item.ApplicationID, UserID: item.VisitorID, Type: item.Type, Data: item.Data, ValidFrom: item.ValidFrom, ValidUntil: item.ValidUntil, CreatedAt: item.CreatedAt}
		// 先经现有策略校验类型、归属及有效期，再展开已验证的覆盖集合，避免逐集重复解析 JSON。
		if !registry.Allows(entitlement, appID, userID, monetization.AccessTarget{DramaID: dramaID, EpisodeID: probe}, now) {
			continue
		}
		if all {
			for episodeID := range wanted {
				owned[episodeID] = true
			}
			continue
		}
		for _, episodeID := range data.EpisodeIDs {
			if wanted[episodeID] {
				owned[episodeID] = true
			}
		}
	}
	return owned, nil
}
