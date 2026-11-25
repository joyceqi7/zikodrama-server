package mysqlstore

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"

	"drama-server/internal/biz/application"
	consumer "drama-server/internal/biz/consumer"
	domain "drama-server/internal/biz/monetization"
)

// FindVIPProgress 返回应用时区当前奖励周期内的 VIP 进度。
// Revision 只写入响应快照；同一规则按应用、用户和自然日累计，调整阈值不会清零。
func (s *Store) FindVIPProgress(
	ctx context.Context,
	visitorID string,
	policy domain.MonetizationPolicy,
	period domain.RewardPeriod,
	now time.Time,
) (domain.VIPProgress, error) {
	bucketID, bucketErr := requestBucket(ctx)
	if bucketErr != nil {
		return domain.VIPProgress{}, bucketErr
	}
	state := domain.VIPProgress{
		RequiredCount: policy.VIP.RequiredAdCount,
		ProgressDate:  period.Key,
		ApplicationID: policy.ApplicationID,
	}
	var progress vipProgressModel
	err := s.data.DB().WithContext(ctx).Clauses(dbresolver.Write).Table(userTable("application_vip_progress", bucketID)).First(&progress,
		"bucket_id = ? AND application_id = ? AND visitor_id = ? AND period_key = ? AND progress_key = ?",
		bucketID, application.IDFromContext(ctx), visitorID, period.Key, policy.ProgressKey()).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.VIPProgress{}, err
	}
	if err == nil {
		state.AdCount = progress.Completed
	}
	validUntil, err := s.catalogEntitlementValidUntil(ctx, bucketID, visitorID, now)
	if err != nil {
		return domain.VIPProgress{}, err
	}
	if validUntil != nil {
		state.Active = true
		state.ValidUntil = validUntil.UTC()
	}
	return state, nil
}

// ReconcileVIPProgress 仅供显式领取调用，在同一事务中核对进度并写入目录权益。
// now 是请求接收时刻；同日同规则的并发或乱序领取恢复原授予，不改写时间、绕过撤销或重复发放。
func (s *Store) ReconcileVIPProgress(
	ctx context.Context,
	visitorID string,
	policy domain.MonetizationPolicy,
	period domain.RewardPeriod,
	now time.Time,
) (domain.VIPProgress, error) {
	now = now.UTC()
	bucketID, bucketErr := requestBucket(ctx)
	if bucketErr != nil {
		return domain.VIPProgress{}, bucketErr
	}
	// 无需补发时只读返回；需要补发时在用户行事务锁内重新核对。
	state, err := s.FindVIPProgress(ctx, visitorID, policy, period, now)
	if err != nil {
		return domain.VIPProgress{}, err
	}
	if !policy.Enabled() || !policy.VIP.Enabled || state.Active || state.RequiredCount <= 0 || state.AdCount < state.RequiredCount {
		return state, nil
	}
	applicationID := application.IDFromContext(ctx)
	sourceID := fmt.Sprintf("configuration:%x", sha256.Sum256([]byte(period.Key+":"+policy.ProgressKey())))
	err = s.userTransaction(ctx, applicationID, visitorID, func(tx *gorm.DB) error {
		if err := checkVisitor(tx, bucketID, applicationID, visitorID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return consumer.ErrNotFound
			}
			return err
		}
		state = domain.VIPProgress{
			RequiredCount: policy.VIP.RequiredAdCount,
			ProgressDate:  period.Key,
			ApplicationID: policy.ApplicationID,
		}
		var progress vipProgressModel
		err := tx.Table(userTable("application_vip_progress", bucketID)).First(&progress,
			"bucket_id = ? AND application_id = ? AND visitor_id = ? AND period_key = ? AND progress_key = ?",
			bucketID, applicationID, visitorID, period.Key, policy.ProgressKey()).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			state.AdCount = progress.Completed
		}
		// 请求按接收时间确定自然日，但用户锁可能先提交较晚收到的领取。
		// 同一日同一规则已有领取时，以该原始授予时刻恢复幂等；不把稍早请求重建成第二份权益。
		inspectionTime := now
		var original entitlementGrantModel
		originalErr := tx.Table(userTable("entitlement_grants", bucketID)).
			Where("bucket_id = ? AND application_id = ? AND visitor_id = ? AND source_type = ? AND source_id = ?", bucketID, applicationID, visitorID, "vip_manual_claim", sourceID).
			Order("valid_from DESC").Take(&original).Error
		if originalErr != nil && !errors.Is(originalErr, gorm.ErrRecordNotFound) {
			return originalErr
		}
		if originalErr == nil && original.ValidFrom.After(inspectionTime) && original.ValidFrom.Before(period.ExpiresAt) {
			inspectionTime = original.ValidFrom
		}
		// 仍通过当前权益投影和策略校验，撤销后的旧流水不会恢复访问资格。
		validUntil, err := catalogEntitlementValidUntilDB(tx, bucketID, applicationID, visitorID, inspectionTime)
		if err != nil {
			return err
		}
		if validUntil != nil {
			state.Active = true
			state.ValidUntil = validUntil.UTC()
		}
		if !policy.Enabled() || !policy.VIP.Enabled || state.Active ||
			state.RequiredCount <= 0 || state.AdCount < state.RequiredCount {
			return nil
		}

		templates := []domain.GrantTemplate{{Type: "vip", Config: json.RawMessage(`{"scope":"application","adFree":true,"validity":"day"}`)}}
		if selected, ok := policy.ForOffer(domain.AccessOfferRewardedVIP); ok && selected.Rule != nil {
			templates = selected.Rule.Grants
		}
		items, err := domain.DefaultStrategies().BuildEntitlements(templates, domain.GrantContext{ApplicationID: applicationID, UserID: visitorID, SourceType: "vip_manual_claim", SourceID: sourceID, Now: now, PeriodEnd: period.ExpiresAt})
		if err != nil {
			return err
		}
		if err := s.writeEntitlementsTx(ctx, tx, bucketID, applicationID, visitorID, items); err != nil {
			return err
		}

		state.Active = true
		state.ValidUntil = period.ExpiresAt.UTC()
		return nil
	})
	return state, err
}

// HasCatalogEntitlement 判断访客是否持有仍然有效的全目录访问权益。
func (s *Store) HasCatalogEntitlement(ctx context.Context, visitorID string, now time.Time) (bool, error) {
	bucketID, bucketErr := requestBucket(ctx)
	if bucketErr != nil {
		return false, bucketErr
	}
	validUntil, err := s.catalogEntitlementValidUntil(ctx, bucketID, visitorID, now)
	return validUntil != nil, err
}

func (s *Store) catalogEntitlementValidUntil(ctx context.Context, bucketID uint16, visitorID string, now time.Time) (*time.Time, error) {
	return catalogEntitlementValidUntilDB(
		s.data.DB().WithContext(ctx).Clauses(dbresolver.Write), bucketID, application.IDFromContext(ctx), visitorID, now.UTC(),
	)
}

// catalogEntitlementValidUntilDB 通过权益策略判定目录访问，并返回最晚有效期限。
// nil 代表没有目录权益，指向零时间的指针代表无限期，其他值为真实截止时间。
func catalogEntitlementValidUntilDB(
	db *gorm.DB,
	bucketID uint16,
	applicationID, visitorID string,
	now time.Time,
) (*time.Time, error) {
	items, err := activeEntitlementStatesDB(db, bucketID, applicationID, visitorID, []string{"application"}, now)
	if err != nil {
		return nil, err
	}
	registry := domain.DefaultStrategies()
	var latest *time.Time
	for _, item := range items {
		entitlement := domain.Entitlement{ApplicationID: item.ApplicationID, UserID: item.VisitorID, Type: item.Type, Data: item.Data, ValidFrom: item.ValidFrom, ValidUntil: item.ValidUntil, CreatedAt: item.CreatedAt}
		if !registry.Allows(entitlement, applicationID, visitorID, domain.AccessTarget{}, now) {
			continue
		}
		if item.ValidUntil == nil {
			unlimited := time.Time{}
			return &unlimited, nil
		}
		if latest == nil || item.ValidUntil.After(*latest) {
			latest = item.ValidUntil
		}
	}
	return latest, nil
}
