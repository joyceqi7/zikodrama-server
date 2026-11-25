package mysqlstore

import (
	"context"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// BeginTikTokPublicationCall 复用原有修订和租约，原子占用页面确认的版本，不产生队列项。
func (s *Store) BeginTikTokPublicationCall(ctx context.Context, p *operations.TikTokPublication) error {
	return s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var drama dramaModel
		if err := catalogQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", p.DramaID).Take(&drama).Error; err != nil {
			return err
		}
		current, err := lockedLatestPublication(tx, p.DramaID)
		if err != nil {
			return err
		}
		if current.ID != p.ID || current.Revision != p.Revision || current.LeaseOwner != "" && current.LeaseExpiresAt.After(time.Now().UTC()) {
			return operations.ErrConflict
		}
		model, err := newTikTokPublicationModel(*p)
		if err != nil {
			return err
		}
		model.Revision++
		if err := gormAffectedOrNotFound(tx.Model(&tiktokPublicationModel{}).Where("id=? AND revision=?", p.ID, p.Revision).Select("*").Omit("created_at").Updates(&model)); err != nil {
			return err
		}
		p.Revision = model.Revision
		return nil
	})
}
