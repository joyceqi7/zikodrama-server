package mysqlstore

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
)

// ListEntitlements 返回用户当前仍有效的逐集权益。
// 目录级 VIP 权益由 vip_progress.go 维护，避免两类权益混用。
func (s *Store) ListEntitlements(ctx context.Context, visitorID string, now time.Time) ([]monetization.EntitlementGrant, error) {
	bucketID, err := requestBucket(ctx)
	if err != nil {
		return nil, err
	}
	return listEntitlementsDB(s.data.DB().WithContext(ctx).Clauses(dbresolver.Write), bucketID, application.IDFromContext(ctx), visitorID, now)
}

// listEntitlementsDB 可复用领取事务连接，保证权益读取发生在 Redis 资源锁内。
func listEntitlementsDB(db *gorm.DB, bucketID uint16, applicationID, visitorID string, now time.Time) ([]monetization.EntitlementGrant, error) {
	models, err := activeEntitlementStatesDB(db, bucketID, applicationID, visitorID, nil, now)
	if err != nil {
		return nil, err
	}
	items := []monetization.EntitlementGrant{}
	seen := map[string]bool{}
	for _, model := range models {
		var data monetization.AccessData
		if err := json.Unmarshal(model.Data, &data); err != nil {
			return nil, err
		}
		if model.Type != "episode_access" && model.Type != "drama_access" {
			continue
		}
		for _, episodeID := range data.EpisodeIDs {
			key := data.DramaID + "\x00" + episodeID
			if seen[key] {
				continue
			}
			seen[key] = true
			item := monetization.EntitlementGrant{ApplicationID: model.ApplicationID, VisitorID: model.VisitorID, DramaID: data.DramaID, EpisodeID: episodeID, GrantedAt: model.CreatedAt}
			if model.ValidUntil != nil {
				item.ExpiresAt = *model.ValidUntil
			}
			items = append(items, item)
		}
	}
	return items, nil
}
