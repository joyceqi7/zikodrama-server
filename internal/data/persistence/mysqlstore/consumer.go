package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/plugin/dbresolver"

	"drama-server/internal/biz/application"
	consumer "drama-server/internal/biz/consumer"
	domain "drama-server/internal/biz/monetization"
)

// HasEntitlement 判断访客是否持有仍然有效的指定分集权益。
func (s *Store) HasEntitlement(ctx context.Context, visitorID, dramaID, episodeID string, now time.Time) (bool, error) {
	bucketID, err := requestBucket(ctx)
	if err != nil {
		return false, err
	}
	return hasEntitlementDB(s.data.DB().WithContext(ctx).Clauses(dbresolver.Write), bucketID, application.IDFromContext(ctx), visitorID, domain.AccessTarget{DramaID: dramaID, EpisodeID: episodeID}, now)
}

// CountRewardGrants 统计指定时间之后已经写入结算流水的某类广告奖励。
func (s *Store) CountRewardGrants(ctx context.Context, visitorID, rewardType string, since time.Time, progressKey ...string) (int, error) {
	bucketID, err := requestBucket(ctx)
	if err != nil {
		return 0, err
	}
	var count int64
	query := s.data.DB().WithContext(ctx).Clauses(dbresolver.Write).Table(userTable("reward_grants", bucketID)).
		Where("bucket_id = ? AND application_id = ? AND visitor_id = ? AND reward_type = ? AND granted_at >= ?",
			bucketID, application.IDFromContext(ctx), visitorID, rewardType, since.UTC())
	if len(progressKey) > 0 {
		query = query.Where("progress_key = ?", progressKey[0])
	}
	err = query.Count(&count).Error
	return int(count), err
}

// FindRewardGrant 恢复同一 Visitor 已经成功落账的 ClaimID。若 ClaimID 属于其他
// Visitor，则明确拒绝，不能把全局幂等键碰撞伪装成“尚未结算”。
func (s *Store) FindRewardGrant(ctx context.Context, visitorID, claimID string) (domain.RewardGrant, bool, error) {
	bucketID, bucketErr := requestBucket(ctx)
	if bucketErr != nil {
		return domain.RewardGrant{}, false, bucketErr
	}
	var model rewardGrantModel
	err := s.data.DB().WithContext(ctx).Clauses(dbresolver.Write).Table(userTable("reward_grants", bucketID)).Where("bucket_id = ? AND application_id = ? AND claim_id = ?", bucketID, application.IDFromContext(ctx), claimID).Take(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.RewardGrant{}, false, nil
	}
	if err != nil {
		return domain.RewardGrant{}, false, err
	}
	if model.VisitorID != visitorID {
		return domain.RewardGrant{}, false, domain.ErrInvalidRewardClaim
	}
	grant, err := model.domain()
	return grant, err == nil, err
}

// GrantReward 在 MySQL 用户行锁下完成幂等流水写入、日限额校验和权益发放。
// RewardGrant 是本事务唯一的成功凭证；没有流水时绝不提交权益或 VIP 进度。
// 已认证用户的桶内记录缺失返回可重试故障，保留原领取请求和记录缺失原因。
func (s *Store) GrantReward(ctx context.Context, grant domain.RewardGrant, now time.Time) (domain.ClaimResult, error) {
	now = now.UTC()
	applicationID := application.IDFromContext(ctx)
	bucketID, err := requestBucket(ctx)
	if err != nil {
		return domain.ClaimResult{}, err
	}
	var result domain.ClaimResult
	var dailyLimitReached bool
	err = s.userTransaction(ctx, applicationID, grant.VisitorID, func(tx *gorm.DB) error {
		// userTransaction 的同一条锁定查询已确认用户存在及归属，不重复读取用户行。
		var existing rewardGrantModel
		existingErr := tx.Table(userTable("reward_grants", bucketID)).Where("bucket_id = ? AND application_id = ? AND claim_id = ?", bucketID, applicationID, grant.ClaimID).Take(&existing).Error
		if existingErr == nil {
			if existing.VisitorID != grant.VisitorID {
				return fmt.Errorf("reward claim belongs to another user: %w", domain.ErrInvalidRewardClaim)
			}
			existingGrant, err := existing.domain()
			if err != nil {
				return err
			}
			result = claimResultFromGrantModel(existingGrant, domain.ClaimStatusAlreadyGranted)
			return nil
		}
		if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
			return existingErr
		}
		grant.ApplicationID = applicationID

		if grant.DailyRewardLimit > 0 {
			var granted int64
			if err := tx.Table(userTable("reward_grants", bucketID)).
				Where("application_id = ? AND visitor_id = ? AND reward_type = ? AND granted_at >= ? AND progress_key = ?",
					applicationID, grant.VisitorID, grant.RewardType, grant.PeriodStartsAt.UTC(), grant.ProgressKey).
				Count(&granted).Error; err != nil {
				return err
			}
			if granted >= int64(grant.DailyRewardLimit) {
				return consumer.ErrRateLimited
			}
			dailyLimitReached = granted+1 >= int64(grant.DailyRewardLimit)
		}

		if grant.RewardType == domain.RewardTypeEpisodeUnlock {
			owned, err := hasEntitlementDB(tx, bucketID, applicationID, grant.VisitorID, domain.AccessTarget{DramaID: grant.DramaID, EpisodeID: grant.EpisodeID}, now)
			if err != nil {
				return err
			}
			if owned {
				return fmt.Errorf("target episode is already unlocked: %w", domain.ErrInvalidRewardClaim)
			}

		} else if grant.RewardType == domain.RewardTypeVIPProgress {
			state, err := findVIPProgressTx(tx, bucketID, applicationID, grant.VisitorID, grant, now)
			if err != nil {
				return err
			}
			if state.Active || state.AdCount >= state.RequiredCount || !grant.PeriodExpiresAt.After(now) {
				return fmt.Errorf("VIP reward is already complete or its period has expired: %w", domain.ErrInvalidRewardClaim)
			}
		} else if grant.RewardType == domain.RewardTypeEntryProgress {
			if !grant.PeriodExpiresAt.After(now) || grant.RequiredCount < 1 ||
				grant.DailyRewardLimit != 0 {
				return fmt.Errorf("entry reward period or threshold is invalid: %w", domain.ErrInvalidRewardClaim)
			}
			// 入剧达标不丢弃有效完播：记录原键及接收日，使响应丢失后的跨日重试仍能恢复原事实。
		} else {
			return fmt.Errorf("unsupported reward type: %w", domain.ErrInvalidRewardClaim)
		}

		ownedIDs := map[string]bool{}
		if grant.RewardType == domain.RewardTypeEpisodeUnlock && grant.BuildContext != nil {
			owned, err := rewardOwnedEpisodeIDsDB(tx, bucketID, applicationID, grant.VisitorID, grant.DramaID, grant.OwnershipEpisodeIDs, now)
			if err != nil {
				return err
			}
			ownedIDs = owned
			if err := domain.RebuildRewardEntitlements(&grant, ownedIDs); err != nil {
				return err
			}
		}
		model, err := newRewardGrantModel(grant)
		if err != nil {
			return err
		}
		model.BucketID = bucketID
		created := tx.Table(userTable("reward_grants", bucketID)).Clauses(clause.OnConflict{DoNothing: true}).Create(&model)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			if err := tx.Table(userTable("reward_grants", bucketID)).Where("bucket_id = ? AND application_id = ? AND claim_id = ?", bucketID, applicationID, grant.ClaimID).Take(&existing).Error; err != nil {
				return err
			}
			if existing.VisitorID != grant.VisitorID {
				return fmt.Errorf("concurrent reward claim belongs to another user: %w", domain.ErrInvalidRewardClaim)
			}
			existingGrant, err := existing.domain()
			if err != nil {
				return err
			}
			result = claimResultFromGrantModel(existingGrant, domain.ClaimStatusAlreadyGranted)
			return nil
		}

		result = claimResultFromGrantModel(grant, domain.ClaimStatusGranted)
		result.DailyLimitReached = dailyLimitReached
		result.OwnedEpisodeIDs = ownedIDs
		if grant.RewardType == domain.RewardTypeVIPProgress {
			state, err := s.grantVIPProgress(ctx, tx, bucketID, applicationID, grant.VisitorID, grant, now)
			if err != nil {
				return err
			}
			result.VIPProgress = &state
			return nil
		}
		if grant.RewardType == domain.RewardTypeEntryProgress {
			return nil
		}
		items, err := domain.RewardEntitlements(grant)
		if err != nil {
			return err
		}
		if err := s.writeEntitlementsTx(ctx, tx, bucketID, applicationID, grant.VisitorID, items); err != nil {
			return err
		}
		return saveRewardEntitlementSnapshot(tx, bucketID, grant, items)
	})
	return result, err
}

// findVIPProgressTx 读取当前周期进度和有效目录权益。调用方已持有用户行事务锁。
func findVIPProgressTx(
	tx *gorm.DB,
	bucketID uint16,
	applicationID, visitorID string,
	grant domain.RewardGrant,
	now time.Time,
) (domain.VIPProgress, error) {
	requiredCount := grant.RequiredCount
	if requiredCount < 1 {
		requiredCount = 1
	}
	state := domain.VIPProgress{
		RequiredCount: requiredCount, ProgressDate: grant.PeriodKey,
		ApplicationID: grant.ApplicationID,
	}
	var progress vipProgressModel
	err := tx.Table(userTable("application_vip_progress", bucketID)).First(&progress, "bucket_id = ? AND application_id = ? AND visitor_id = ? AND period_key = ? AND progress_key = ?",
		bucketID, applicationID, visitorID, grant.PeriodKey, grant.ProgressKey).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.VIPProgress{}, err
	}
	if err == nil {
		state.AdCount = progress.Completed
	}
	validUntil, err := catalogEntitlementValidUntilDB(tx, bucketID, applicationID, visitorID, now)
	if err != nil {
		return domain.VIPProgress{}, err
	}
	if validUntil != nil {
		state.Active = true
		state.ValidUntil = validUntil.UTC()
	}
	return state, nil
}

// grantVIPProgress 在 MySQL 用户行锁保护的奖励事务中增加当前周期进度。
// 达到 RequiredCount 后仍只记录进度，目录权益必须由用户显式领取。
func (s *Store) grantVIPProgress(
	ctx context.Context,
	tx *gorm.DB,
	bucketID uint16,
	applicationID, visitorID string,
	grant domain.RewardGrant,
	now time.Time,
) (domain.VIPProgress, error) {
	requiredCount := grant.RequiredCount
	if requiredCount < 1 {
		requiredCount = 1
	}
	var progress vipProgressModel
	err := tx.Table(userTable("application_vip_progress", bucketID)).
		First(&progress, "bucket_id = ? AND application_id = ? AND visitor_id = ? AND period_key = ? AND progress_key = ?",
			bucketID, applicationID, visitorID, grant.PeriodKey, grant.ProgressKey).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		progress = vipProgressModel{
			BucketID:      bucketID,
			ApplicationID: applicationID, VisitorID: visitorID, PeriodKey: grant.PeriodKey,
			Completed: 1, Required: requiredCount, UpdatedAt: now, ProgressKey: grant.ProgressKey,
		}
		if err := tx.Table(userTable("application_vip_progress", bucketID)).Create(&progress).Error; err != nil {
			return domain.VIPProgress{}, err
		}
	case err != nil:
		return domain.VIPProgress{}, err
	default:
		progress.Completed++
		if progress.Completed > requiredCount {
			progress.Completed = requiredCount
		}
		progress.Required = requiredCount
		progress.UpdatedAt = now
		if err := tx.Table(userTable("application_vip_progress", bucketID)).Where("id = ?", progress.ID).Select("completed_count", "required_count", "updated_at").Updates(&progress).Error; err != nil {
			return domain.VIPProgress{}, err
		}
	}
	return findVIPProgressTx(tx, bucketID, applicationID, visitorID, grant, now)
}

// checkVisitor 在已持有用户行事务锁的写入路径内再次校验用户归属。
func checkVisitor(tx *gorm.DB, bucketID uint16, applicationID, visitorID string) error {
	var visitor consumerUserModel
	return tx.Table(userTable("users", bucketID)).Select("id").
		First(&visitor, "bucket_id = ? AND application_id = ? AND id = ?", bucketID, applicationID, visitorID).Error
}

func newRewardGrantModel(grant domain.RewardGrant) (rewardGrantModel, error) {
	unlockEpisodeIDs, err := json.Marshal(nonNilStrings(grant.UnlockEpisodeIDs))
	if err != nil {
		return rewardGrantModel{}, err
	}
	entitlements, err := json.Marshal(grant.Entitlements)
	if err != nil {
		return rewardGrantModel{}, err
	}
	return rewardGrantModel{
		// 兼容既有非空 JSON 列，不再持久化可被误用于后续领取的策略条款。
		EntitlementsJSON: entitlements, RuleSnapshotJSON: []byte("null"), ProgressKey: grant.ProgressKey,
		ClaimID: grant.ClaimID, ApplicationID: grant.ApplicationID, CampaignID: grant.CampaignID,
		VisitorID: grant.VisitorID, DramaID: stringPointer(grant.DramaID), EpisodeID: stringPointer(grant.EpisodeID),
		UnlockEpisodeIDsJSON: unlockEpisodeIDs, AdUnitID: grant.AdUnitID,
		Scene: grant.Scene, RewardType: grant.RewardType,
		PeriodKey: grant.PeriodKey, PeriodStartsAt: timePointer(grant.PeriodStartsAt),
		PeriodExpiresAt: timePointer(grant.PeriodExpiresAt), RewardRequiredCount: grant.RequiredCount,
		DailyRewardLimit: grant.DailyRewardLimit, EntitlementScope: grant.EntitlementScope,
		GrantedAt: grant.GrantedAt.UTC(),
	}, nil
}

func (model rewardGrantModel) domain() (domain.RewardGrant, error) {
	var unlockEpisodeIDs []string
	if len(model.UnlockEpisodeIDsJSON) > 0 {
		if err := json.Unmarshal(model.UnlockEpisodeIDsJSON, &unlockEpisodeIDs); err != nil {
			return domain.RewardGrant{}, err
		}
	}
	grant := domain.RewardGrant{
		ClaimID: model.ClaimID, ApplicationID: model.ApplicationID, CampaignID: model.CampaignID,
		VisitorID: model.VisitorID, UnlockEpisodeIDs: nonNilStrings(unlockEpisodeIDs),
		AdUnitID: model.AdUnitID, Scene: model.Scene,
		RewardType:    model.RewardType,
		PeriodKey:     model.PeriodKey,
		RequiredCount: model.RewardRequiredCount, DailyRewardLimit: model.DailyRewardLimit,
		EntitlementScope: model.EntitlementScope, GrantedAt: model.GrantedAt.UTC(),
	}
	if len(model.EntitlementsJSON) > 0 {
		if err := json.Unmarshal(model.EntitlementsJSON, &grant.Entitlements); err != nil {
			return domain.RewardGrant{}, err
		}
	}
	for i := range grant.Entitlements {
		grant.Entitlements[i].ApplicationID = grant.ApplicationID
		grant.Entitlements[i].UserID = grant.VisitorID
	}
	// 历史规则 JSON 不参与恢复；实际授予内容和结算事实已独立持久化。
	grant.ProgressKey = model.ProgressKey
	if model.DramaID != nil {
		grant.DramaID = *model.DramaID
	}
	if model.EpisodeID != nil {
		grant.EpisodeID = *model.EpisodeID
	}
	if model.PeriodStartsAt != nil {
		grant.PeriodStartsAt = model.PeriodStartsAt.UTC()
	}
	if model.PeriodExpiresAt != nil {
		grant.PeriodExpiresAt = model.PeriodExpiresAt.UTC()
	}
	return grant, nil
}

func claimResultFromGrantModel(grant domain.RewardGrant, status string) domain.ClaimResult {
	return domain.ClaimResult{
		SettledGrant: &grant,
		ClaimID:      grant.ClaimID, Status: status, Unlocked: grant.RewardType == domain.RewardTypeEpisodeUnlock,
		DramaID: grant.DramaID, EpisodeID: grant.EpisodeID, RewardType: grant.RewardType,
	}
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

// saveRewardEntitlementSnapshot 在奖励事务内保存已落账的数字权益主键，重放时读回同一快照。
func saveRewardEntitlementSnapshot(tx *gorm.DB, bucketID uint16, grant domain.RewardGrant, items []domain.Entitlement) error {
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return tx.Table(userTable("reward_grants", bucketID)).Where("bucket_id = ? AND application_id = ? AND claim_id = ?", bucketID, grant.ApplicationID, grant.ClaimID).UpdateColumn("entitlements_json", raw).Error
}
