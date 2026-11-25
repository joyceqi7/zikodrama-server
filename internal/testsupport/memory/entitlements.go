package memory

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"time"
)

// FindOwnedEpisodeIDs 按当前剧和用户批量检查真实测试权益，包含撤销及有效期判定。
func (s *Store) FindOwnedEpisodeIDs(ctx context.Context, userID, dramaID string, episodeIDs []string, now time.Time) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	owned := map[string]bool{}
	for _, id := range episodeIDs {
		if s.hasBenefitLocked(application.IDFromContext(ctx), userID, monetization.AccessTarget{DramaID: dramaID, EpisodeID: id}, now) {
			owned[id] = true
		}
	}
	return owned, nil
}

func (s *Store) hasBenefitLocked(appID, userID string, target monetization.AccessTarget, now time.Time) bool {
	registry := monetization.DefaultStrategies()
	for _, e := range s.benefitGrants {
		if registry.Allows(e, appID, userID, target, now) {
			return true
		}
	}
	return false
}
func (s *Store) writeBenefitsLocked(items []monetization.Entitlement) {
	for index, e := range items {
		existingID := ""
		for _, old := range s.benefitGrants {
			if old.ApplicationID == e.ApplicationID && old.UserID == e.UserID && old.SourceType == e.SourceType && old.SourceID == e.SourceID && old.SourceItem == e.SourceItem {
				existingID = old.ID
				break
			}
		}
		if existingID != "" {
			items[index].ID = existingID
			continue
		}
		e.ID = s.allocateIDLocked()
		items[index].ID = e.ID
		copy := cloneStrategy(e)
		copy.ApplicationID, copy.UserID = e.ApplicationID, e.UserID
		s.benefitGrants[e.ID] = copy
	}
}

// GrantEntitlements 模拟可信事件的原子批量履约。
func (s *Store) GrantEntitlements(ctx context.Context, userID string, items []monetization.Entitlement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range items {
		if e.ApplicationID != application.IDFromContext(ctx) || e.UserID != userID || monetization.DefaultStrategies().ValidateEntitlement(e) != nil {
			return operations.ErrInvalidInput
		}
	}
	s.writeBenefitsLocked(items)
	return nil
}

// RevokeEntitlementSource 只撤销当前应用、当前用户的单个来源。
func (s *Store) RevokeEntitlementSource(ctx context.Context, userID, sourceType, sourceID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.benefitGrants {
		if e.ApplicationID == application.IDFromContext(ctx) && e.UserID == userID && e.SourceType == sourceType && e.SourceID == sourceID && e.RevokedAt == nil {
			e.RevokedAt = &now
			s.benefitGrants[id] = e
		}
	}
	return nil
}
