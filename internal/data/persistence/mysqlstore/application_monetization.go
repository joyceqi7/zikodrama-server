package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"

	"drama-server/internal/biz/application"
	content "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetApplicationMonetization 通过事务快照读取一致的规则与绑定修订。
func (s *Store) GetApplicationMonetization(ctx context.Context) (monetization.ApplicationMonetizationConfig, error) {
	// 读取绑定与规则在一个只读事务快照中，避免配置变化混合新旧 Offer 条款。
	var result monetization.ApplicationMonetizationConfig
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var bmodel strategyBindingModel
		if err := tx.First(&bmodel, "id = ?", application.IDFromContext(ctx)).Error; err != nil {
			return err
		}
		b, err := bmodel.domain()
		if err != nil {
			return err
		}
		if b.StrategyID == "" {
			return content.ErrNotFound
		}
		var smodel unlockStrategyModel
		if err := tx.First(&smodel, "id = ?", b.StrategyID).Error; err != nil {
			return err
		}
		strategy, err := smodel.domain()
		if err != nil {
			return err
		}
		var units application.AdUnits
		if err := json.Unmarshal(bmodel.AdUnits, &units); err != nil {
			return err
		}
		result = monetization.ConfigFromStrategy(strategy, b.WithApplicationAdUnits(strategy.Document, units))
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = content.ErrNotFound
	}
	return result, err
}

// SaveApplicationMonetization 适配旧广告配置入口；新面板通过应用策略接口整体编辑。
func (s *Store) SaveApplicationMonetization(ctx context.Context, c monetization.ApplicationMonetizationConfig) error {
	if c.ApplicationID == "" || (application.IDFromContext(ctx) != "" && c.ApplicationID != application.IDFromContext(ctx)) {
		return operations.ErrInvalidInput
	}
	doc, bindings := monetization.StrategyFromConfig(c)
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	mapping, err := json.Marshal(monetization.ProductBindings(bindings))
	if err != nil {
		return err
	}
	return s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current strategyBindingModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", c.ApplicationID).Error; err != nil {
			return err
		}
		strategyID := ""
		if current.StrategyID != nil {
			strategyID = *current.StrategyID
		}
		// 应用行已锁定，策略只能归属于当前应用，避免跨应用改写规则。
		var strategy unlockStrategyModel
		if current.StrategyID != nil {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&strategy, "id = ?", strategyID).Error; err != nil {
				return err
			}
		}
		if current.StrategyID != nil && strategy.ApplicationID != c.ApplicationID {
			return operations.ErrConflict
		}
		review, err := readMiniReview(tx, c.ApplicationID)
		if err != nil {
			return err
		}
		if err := validateMiniReviewStrategy(review, current.MonetizationMode, doc); err != nil {
			return err
		}
		if err := validateBindingAdUnits(doc, current.AdUnits, current.MonetizationMode); err != nil {
			return err
		}
		if current.StrategyID == nil {
			model := unlockStrategyModel{ApplicationID: c.ApplicationID, Revision: 1, Document: raw, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
			if err := tx.Create(&model).Error; err != nil {
				return err
			}
			strategyID = model.ID
		} else {
			// 旧配置入口仍按修订号更新当前应用的唯一策略。
			updated := tx.Model(&unlockStrategyModel{}).Where("id = ? AND revision = ?", strategyID, strategy.Revision).Updates(map[string]any{"revision": strategy.Revision + 1, "rules_json": raw, "updated_at": c.UpdatedAt})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return operations.ErrConflict
			}
		}
		return tx.Model(&strategyBindingModel{}).Where("id = ?", c.ApplicationID).Updates(map[string]any{"unlock_strategy_id": strategyID, "unlock_bindings_json": mapping}).Error
	})
}
