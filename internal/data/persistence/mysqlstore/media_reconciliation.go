package mysqlstore

import (
	"context"
	"errors"
	"time"

	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/plugin/dbresolver"
)

const mediaReconciliationLease = 5 * time.Minute

// mediaReconciliationCheckpointModel 以单行检查点保证多实例不重复领取同一轮剧目。
type mediaReconciliationCheckpointModel struct {
	ID              uint8      `gorm:"column:id;primaryKey"`
	CursorID        string     `gorm:"column:cursor_id;type:bigint unsigned"`
	PendingCursorID string     `gorm:"column:pending_cursor_id;type:bigint unsigned"`
	LeaseOwner      string     `gorm:"column:lease_owner"`
	LeaseExpiresAt  *time.Time `gorm:"column:lease_expires_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
}

// TableName 固定媒资巡检的 MySQL 检查点表。
func (mediaReconciliationCheckpointModel) TableName() string {
	return "media_reconciliation_checkpoint"
}

// ClaimMediaReconciliation 在单行事务锁下领取下一部剧；过期租约重做未提交的剧。
func (s *Store) ClaimMediaReconciliation(ctx context.Context, owner string) (string, bool, error) {
	if owner == "" {
		return "", false, operations.ErrInvalidInput
	}
	var dramaID string
	claimed := false
	err := s.data.DB().WithContext(ctx).Clauses(dbresolver.Write).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		seed := mediaReconciliationCheckpointModel{ID: 1, CursorID: "0", PendingCursorID: "0", UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}
		var checkpoint mediaReconciliationCheckpointModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&checkpoint, "id = ?", 1).Error; err != nil {
			return err
		}
		if checkpoint.LeaseOwner != "" && checkpoint.LeaseExpiresAt != nil && checkpoint.LeaseExpiresAt.After(now) {
			return nil
		}
		var drama dramaModel
		err := tx.Model(&dramaModel{}).Select("id").Where("id > ?", checkpoint.CursorID).Order("id ASC").Take(&drama).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			claimed = true
			return tx.Model(&checkpoint).Updates(map[string]any{
				"cursor_id": "0", "pending_cursor_id": "0", "lease_owner": "", "lease_expires_at": nil, "updated_at": now,
			}).Error
		}
		if err != nil {
			return err
		}
		expires := now.Add(mediaReconciliationLease)
		if err := tx.Model(&checkpoint).Updates(map[string]any{
			"pending_cursor_id": drama.ID, "lease_owner": owner, "lease_expires_at": expires, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		dramaID, claimed = drama.ID, true
		return nil
	})
	return dramaID, claimed, err
}

// RenewMediaReconciliation 仅当前持有者可延长租约，避免慢速远端观察时被其他实例重领。
func (s *Store) RenewMediaReconciliation(ctx context.Context, owner, dramaID string) error {
	now := time.Now().UTC()
	result := s.data.DB().WithContext(ctx).Clauses(dbresolver.Write).Model(&mediaReconciliationCheckpointModel{}).
		Where("id = ? AND lease_owner = ? AND pending_cursor_id = ? AND lease_expires_at > ?", 1, owner, dramaID, now).
		Updates(map[string]any{"lease_expires_at": now.Add(mediaReconciliationLease), "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return operations.ErrConflict
	}
	return nil
}

// FinishMediaReconciliation 成功时才推进游标；取消或失败只释放租约供下一轮重做。
func (s *Store) FinishMediaReconciliation(ctx context.Context, owner, dramaID string, completed bool) error {
	updates := map[string]any{"lease_owner": "", "lease_expires_at": nil, "pending_cursor_id": "0", "updated_at": time.Now().UTC()}
	if completed {
		updates["cursor_id"] = dramaID
	}
	result := s.data.DB().WithContext(ctx).Clauses(dbresolver.Write).Model(&mediaReconciliationCheckpointModel{}).
		Where("id = ? AND lease_owner = ? AND pending_cursor_id = ?", 1, owner, dramaID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return operations.ErrConflict
	}
	return nil
}
