package mysqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m entitlementGrantModel) domain() monetization.Entitlement {
	return monetization.Entitlement{ID: m.ID, ApplicationID: m.ApplicationID, UserID: m.VisitorID, Type: m.Type, Data: m.Data, SourceType: m.SourceType, SourceID: m.SourceID, SourceItem: m.SourceItem, ValidFrom: m.ValidFrom, ValidUntil: m.ValidUntil, RevokedAt: m.RevokedAt, CreatedAt: m.GrantedAt}
}

func entitlementGrantModelFromDomain(e monetization.Entitlement, bucketID uint16) entitlementGrantModel {
	return entitlementGrantModel{ID: e.ID, BucketID: bucketID, ApplicationID: e.ApplicationID, VisitorID: e.UserID, Type: e.Type, Data: e.Data, SourceType: e.SourceType, SourceID: e.SourceID, SourceItem: e.SourceItem, ValidFrom: e.ValidFrom, ValidUntil: e.ValidUntil, RevokedAt: e.RevokedAt, GrantedAt: e.CreatedAt}
}

// activeEntitlementStatesDB 读取按目标展开且在判定时刻有效的鉴权投影。
func activeEntitlementStatesDB(db *gorm.DB, bucketID uint16, appID, userID string, targetKeys []string, now time.Time) ([]entitlementStateModel, error) {
	items := []entitlementStateModel{}
	query := db.Table(userTable("user_entitlement_state", bucketID)).Where("bucket_id = ? AND application_id = ? AND visitor_id = ? AND valid_from <= ? AND (valid_until IS NULL OR valid_until > ?)", bucketID, appID, userID, now.UTC(), now.UTC())
	if len(targetKeys) > 0 {
		query = query.Where("target_key IN ?", targetKeys)
	}
	err := query.Order("created_at DESC").Find(&items).Error
	return items, err
}

// hasEntitlementDB 只查询指定目标可能命中的投影键，不扫描不可变流水或 JSON 全表。
func hasEntitlementDB(db *gorm.DB, bucketID uint16, appID, userID string, target monetization.AccessTarget, now time.Time) (bool, error) {
	keys := []string{"application"}
	if target.DramaID != "" {
		keys = append(keys, "drama:"+target.DramaID)
	}
	if target.DramaID != "" && target.EpisodeID != "" {
		keys = append(keys, "episode:"+target.DramaID+":"+target.EpisodeID)
	}
	items, err := activeEntitlementStatesDB(db, bucketID, appID, userID, keys, now)
	if err != nil {
		return false, err
	}
	registry := monetization.DefaultStrategies()
	for _, item := range items {
		entitlement := monetization.Entitlement{ApplicationID: item.ApplicationID, UserID: item.VisitorID, Type: item.Type, Data: item.Data, ValidFrom: item.ValidFrom, ValidUntil: item.ValidUntil, CreatedAt: item.CreatedAt}
		if registry.Allows(entitlement, appID, userID, target, now) {
			return true, nil
		}
	}
	return false, nil
}

// entitlementTargetKeys 把通用权益快照展开为播放鉴权可以精确索引的目标键。
func entitlementTargetKeys(e monetization.Entitlement) ([]string, error) {
	var data monetization.AccessData
	if err := json.Unmarshal(e.Data, &data); err != nil {
		return nil, err
	}
	switch e.Type {
	case "vip":
		return []string{"application"}, nil
	case "drama_access":
		return []string{"drama:" + data.DramaID}, nil
	case "episode_access":
		keys := make([]string, 0, len(data.EpisodeIDs))
		for _, episodeID := range data.EpisodeIDs {
			keys = append(keys, "episode:"+data.DramaID+":"+episodeID)
		}
		return keys, nil
	default:
		return nil, fmt.Errorf("unsupported entitlement type %q", e.Type)
	}
}

// writeEntitlementsTx 先追加不可变授予流水，再在同一事务写入当前鉴权投影。
func (s *Store) writeEntitlementsTx(ctx context.Context, tx *gorm.DB, bucketID uint16, appID, userID string, items []monetization.Entitlement) error {
	for index, e := range items {
		if e.ApplicationID != appID || e.UserID != userID || e.RevokedAt != nil || monetization.DefaultStrategies().ValidateEntitlement(e) != nil {
			return operations.ErrInvalidInput
		}
		model := entitlementGrantModelFromDomain(e, bucketID)
		// 主键由当前物理分表分配；输入快照可能携带另一条记录的 ID，不能参与插入。
		model.ID = ""
		grantTable := userTable("entitlement_grants", bucketID)
		created := tx.Table(grantTable).Clauses(clause.OnConflict{DoNothing: true}).Create(&model)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			var existing entitlementGrantModel
			if err := tx.Table(grantTable).First(&existing, "bucket_id = ? AND application_id = ? AND visitor_id = ? AND source_type = ? AND source_id = ? AND source_item = ?", bucketID, appID, userID, e.SourceType, e.SourceID, e.SourceItem).Error; err != nil {
				return err
			}
			model.ID = existing.ID
			if !sameEntitlementSnapshot(existing, model) {
				return operations.ErrConflict
			}
		}
		items[index].ID = model.ID
		var revoked int64
		if err := tx.Table(userTable("entitlement_revocations", bucketID)).Where("bucket_id = ? AND application_id = ? AND visitor_id = ? AND source_type = ? AND source_id = ?", bucketID, appID, userID, e.SourceType, e.SourceID).Count(&revoked).Error; err != nil {
			return err
		}
		if revoked > 0 {
			continue
		}
		keys, err := entitlementTargetKeys(e)
		if err != nil {
			return err
		}
		for _, key := range keys {
			state := entitlementStateModel{BucketID: bucketID, ApplicationID: appID, VisitorID: userID, GrantID: model.ID, Type: e.Type, TargetKey: key, Data: e.Data, ValidFrom: e.ValidFrom, ValidUntil: e.ValidUntil, CreatedAt: e.CreatedAt}
			if err := tx.Table(userTable("user_entitlement_state", bucketID)).Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// GrantEntitlements 为指定用户原子追加可信事件的授予流水和权益投影。
// 输入 ID 不参与写入；同一来源重试复用已有记录，不同条款返回冲突，提交成功后才回填分表内自增 ID。
func (s *Store) GrantEntitlements(ctx context.Context, userID string, items []monetization.Entitlement) error {
	if len(items) == 0 {
		return nil
	}
	bucketID, err := requestBucket(ctx)
	if err != nil {
		return err
	}
	appID := application.IDFromContext(ctx)
	allocated := append([]monetization.Entitlement(nil), items...)
	err = s.userTransaction(ctx, appID, userID, func(tx *gorm.DB) error {
		if err := checkVisitor(tx, bucketID, appID, userID); err != nil {
			return err
		}
		return s.writeEntitlementsTx(ctx, tx, bucketID, appID, userID, allocated)
	})
	if err == nil {
		copy(items, allocated)
	}
	return err
}

// RevokeEntitlementSource 追加不可变撤销事实并删除对应当前投影，重复事件保持幂等。
func (s *Store) RevokeEntitlementSource(ctx context.Context, userID, sourceType, sourceID string, now time.Time) error {
	if userID == "" || sourceType == "" || sourceID == "" || now.IsZero() {
		return operations.ErrInvalidInput
	}
	bucketID, err := requestBucket(ctx)
	if err != nil {
		return err
	}
	appID := application.IDFromContext(ctx)
	// 用户行事务锁使撤销与同账号授予串行；不存在的来源也记录撤销事实以阻止迟到重放。
	return s.userTransaction(ctx, appID, userID, func(tx *gorm.DB) error {
		if err := checkVisitor(tx, bucketID, appID, userID); err != nil {
			return err
		}
		revocation := entitlementRevocationModel{BucketID: bucketID, ApplicationID: appID, VisitorID: userID, SourceType: sourceType, SourceID: sourceID, RevokedAt: now.UTC()}
		if err := tx.Table(userTable("entitlement_revocations", bucketID)).Clauses(clause.OnConflict{DoNothing: true}).Create(&revocation).Error; err != nil {
			return err
		}
		grantIDs := tx.Table(userTable("entitlement_grants", bucketID)).Select("id").Where("bucket_id = ? AND application_id = ? AND visitor_id = ? AND source_type = ? AND source_id = ?", bucketID, appID, userID, sourceType, sourceID)
		// 自增主键只在物理表内唯一，授予流水与待删除投影必须使用同一分片。
		return tx.Table(userTable("user_entitlement_state", bucketID)).Where("grant_id IN (?)", grantIDs).Delete(&entitlementStateModel{}).Error
	})
}

// sameEntitlementSnapshot 拒绝同一来源项被不同条款重放。
func sameEntitlementSnapshot(a, b entitlementGrantModel) bool {
	var left, right any
	if json.Unmarshal(a.Data, &left) != nil || json.Unmarshal(b.Data, &right) != nil {
		return false
	}
	sameEnd := (a.ValidUntil == nil && b.ValidUntil == nil) || (a.ValidUntil != nil && b.ValidUntil != nil && a.ValidUntil.Truncate(time.Microsecond).Equal(b.ValidUntil.Truncate(time.Microsecond)))
	return a.Type == b.Type && a.ValidFrom.Truncate(time.Microsecond).Equal(b.ValidFrom.Truncate(time.Microsecond)) && sameEnd && reflect.DeepEqual(left, right)
}
