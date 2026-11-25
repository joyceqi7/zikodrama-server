package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"drama-server/internal/biz/application"
	content "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetApplicationStrategy 在同一事务快照中读取应用修订、策略和商品映射。
func (s *Store) GetApplicationStrategy(ctx context.Context) (monetization.ApplicationStrategy, error) {
	var result monetization.ApplicationStrategy
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model strategyBindingModel
		if err := tx.First(&model, "id = ?", application.IDFromContext(ctx)).Error; err != nil {
			return err
		}
		binding, err := model.domain()
		if err != nil {
			return err
		}
		result = monetization.ApplicationStrategy{ApplicationID: binding.ApplicationID, MonetizationMode: model.MonetizationMode, Rules: binding.Rules}
		if binding.StrategyID == "" {
			return nil
		}
		var stored unlockStrategyModel
		if err := tx.First(&stored, "id = ?", binding.StrategyID).Error; err != nil {
			// 历史数据可能仍引用已清理的策略；按未配置返回，让运营可以直接新建。
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		strategy, err := stored.domain()
		if err != nil {
			return err
		}
		result.Strategy = &strategy
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = content.ErrNotFound
	}
	return result, err
}

// SaveApplicationStrategy 原子保存当前应用独立策略；以事务内最新配置为准，修订号仅由服务端递增。
func (s *Store) SaveApplicationStrategy(ctx context.Context, input monetization.ApplicationStrategyInput) (monetization.ApplicationStrategy, error) {
	var result monetization.ApplicationStrategy
	id := application.IDFromContext(ctx)
	if id == "" {
		return result, operations.ErrInvalidInput
	}
	raw, err := json.Marshal(input.Document)
	if err != nil {
		return result, err
	}
	rules := monetization.ProductBindings(input.Rules)
	mapping, err := json.Marshal(rules)
	if err != nil {
		return result, err
	}
	err = s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var app strategyBindingModel
		// 先锁定应用，广告位、变现模式和策略保存都按同一顺序串行化。
		// 保存使用锁内最新修订，避免应用配置并发变化造成规则与模式不一致。
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&app, "id = ?", id).Error; err != nil {
			return err
		}
		previousID := ""
		var strategy unlockStrategyModel
		if app.StrategyID != nil {
			previousID = *app.StrategyID
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&strategy, "id = ?", previousID).Error; err != nil {
				// 引用失效不代表应用所属策略已删除，下面还需按归属恢复绑定。
				if errors.Is(err, gorm.ErrRecordNotFound) {
					previousID = ""
				} else {
					return err
				}
			}
			if previousID != "" && strategy.ApplicationID != id {
				return operations.ErrConflict
			}
		}
		if previousID == "" {
			// 每个应用只能拥有一份策略；空引用或失效引用须优先复用所属记录，避免唯一索引冲突。
			strategy = unlockStrategyModel{}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("application_id = ?", id).First(&strategy).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			} else {
				previousID = strategy.ID
			}
		}
		review, err := readMiniReview(tx, id)
		if err != nil {
			return err
		}
		if err := validateMiniReviewStrategy(review, app.MonetizationMode, input.Document); err != nil {
			return err
		}
		if err := validateBindingAdUnits(input.Document, app.AdUnits, app.MonetizationMode); err != nil {
			return err
		}
		now := time.Now().UTC()
		created := previousID == ""
		if created {
			strategy = unlockStrategyModel{ApplicationID: id, Revision: 1, Document: raw, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&strategy).Error; err != nil {
				return err
			}
		}
		if !created {
			strategy.Document, strategy.UpdatedAt = raw, now
			strategy.Revision++
			if err := tx.Model(&unlockStrategyModel{}).Where("id = ?", strategy.ID).Updates(map[string]any{"rules_json": raw, "revision": strategy.Revision, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&strategyBindingModel{}).Where("id = ?", id).Updates(map[string]any{"unlock_strategy_id": strategy.ID, "unlock_bindings_json": mapping}).Error; err != nil {
			return err
		}
		saved, err := strategy.domain()
		if err != nil {
			return err
		}
		result = monetization.ApplicationStrategy{ApplicationID: id, MonetizationMode: app.MonetizationMode, Strategy: &saved, Rules: rules}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = content.ErrNotFound
	}
	return result, err
}
