package memory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
)

// 查询访问者在当前奖励周期内的会员进度。
func (s *Store) FindVIPProgress(ctx context.Context, visitorID string, policy monetization.MonetizationPolicy, period monetization.RewardPeriod, now time.Time) (monetization.VIPProgress, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.vipProgressLocked(recordApplicationID(application.IDFromContext(ctx)), visitorID, policy, period, now), nil
}

// ReconcileVIPProgress 仅在用户显式领取时发放已达标的会员权益。
func (s *Store) ReconcileVIPProgress(ctx context.Context, visitorID string, policy monetization.MonetizationPolicy, period monetization.RewardPeriod, now time.Time) (monetization.VIPProgress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	applicationID := recordApplicationID(application.IDFromContext(ctx))
	state := s.vipProgressLocked(applicationID, visitorID, policy, period, now)
	if !policy.Enabled() || !policy.VIP.Enabled || state.Active ||
		state.RequiredCount < 1 || state.AdCount < state.RequiredCount {
		return state, nil
	}
	sourceID := fmt.Sprintf("configuration:%x", sha256.Sum256([]byte(period.Key+":"+policy.ProgressKey())))
	templates := []monetization.GrantTemplate{{Type: "vip", Config: json.RawMessage(`{"scope":"application","adFree":true,"validity":"day"}`)}}
	if selected, ok := policy.ForOffer(monetization.AccessOfferRewardedVIP); ok && selected.Rule != nil {
		templates = selected.Rule.Grants
	}
	items, err := monetization.DefaultStrategies().BuildEntitlements(templates, monetization.GrantContext{ApplicationID: applicationID, UserID: visitorID, SourceType: "vip_manual_claim", SourceID: sourceID, Now: now, PeriodEnd: period.ExpiresAt})
	if err != nil {
		return state, err
	}
	s.writeBenefitsLocked(items)
	state.Active = true
	state.ValidUntil = period.ExpiresAt
	return state, nil
}

// 判断权益是否满足当前条件。
func (s *Store) HasCatalogEntitlement(ctx context.Context, visitorID string, now time.Time) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.activeCatalogEntitlementLocked(recordApplicationID(application.IDFromContext(ctx)), visitorID, now)
	return exists, nil
}

// 执行会员进度对应的业务处理。
func (s *Store) vipProgressLocked(applicationID, visitorID string, policy monetization.MonetizationPolicy, period monetization.RewardPeriod, now time.Time) monetization.VIPProgress {
	state := monetization.VIPProgress{
		RequiredCount: policy.VIP.RequiredAdCount, ProgressDate: period.Key,
		ApplicationID: policy.ApplicationID,
	}
	state.AdCount = s.vipProgress[scopedVIPProgressKey(applicationID, visitorID, period.Key+policy.ProgressKey())].Completed
	if entitlement, exists := s.activeCatalogEntitlementLocked(applicationID, visitorID, now); exists {
		state.Active = true
		state.ValidUntil = entitlement.ExpiresAt
	}
	return state
}

// activeCatalogEntitlementLocked 从目录权益账本中选出当前有效期最晚的一条记录。
// 调用方必须已经持有 Store 的读锁或写锁。
func (s *Store) activeCatalogEntitlementLocked(applicationID, visitorID string, now time.Time) (entitlement, bool) {
	var selected entitlement
	found := false
	registry := monetization.DefaultStrategies()
	for _, e := range s.benefitGrants {
		if !registry.Allows(e, applicationID, visitorID, monetization.AccessTarget{}, now) {
			continue
		}
		item := entitlement{ApplicationID: e.ApplicationID, VisitorID: e.UserID, SourceID: e.SourceID, GrantedAt: e.CreatedAt}
		if e.ValidUntil != nil {
			item.ExpiresAt = *e.ValidUntil
		}
		if !found || item.ExpiresAt.IsZero() || (!selected.ExpiresAt.IsZero() && item.ExpiresAt.After(selected.ExpiresAt)) {
			selected = item
			found = true
		}
	}
	return selected, found
}

// 生成奖励周期范围内隔离的会员进度键。
func scopedVIPProgressKey(applicationID, visitorID, periodKey string) string {
	return recordApplicationID(applicationID) + "\x00" + visitorID + "\x00" + periodKey
}
