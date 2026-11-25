package mysqlstore

import (
	"encoding/json"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
)

// unlockStrategyModel 保存单个应用的规则 JSON 和修订信息。
type unlockStrategyModel struct {
	// ApplicationID 标识策略唯一所属的应用。
	ApplicationID string `gorm:"column:application_id;type:bigint unsigned;uniqueIndex"`
	// ID 是策略快照的内部标识。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// Revision 防止并发编辑覆盖已经保存的规则。
	Revision int `gorm:"column:revision"`
	// Document 保存按类型解释的规则与权益模板。
	Document []byte `gorm:"column:rules_json"`
	// CreatedAt 是策略首次建立的时间。
	CreatedAt time.Time `gorm:"column:created_at"`
	// UpdatedAt 是最近规则变更的时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (unlockStrategyModel) TableName() string { return "unlock_strategies" }

// strategyBindingModel 是应用绑定的窄投影，更新应用资料不会覆盖绑定。
type strategyBindingModel struct {
	// ApplicationID 是绑定和平台资源所属的小程序。
	ApplicationID string `gorm:"column:id;primaryKey"`
	// IdentityHashNamespace 仅用于识别迁移前生成的专属策略，不作为应用查询条件。
	IdentityHashNamespace string `gorm:"column:identity_hash_namespace"`
	// StrategyID 引用当前应用的策略快照，空值表示尚未配置。
	StrategyID *string `gorm:"column:unlock_strategy_id;type:bigint unsigned"`
	// Rules 按规则编码保存本小程序的商品映射；旧广告映射在读取时忽略。
	Rules []byte `gorm:"column:unlock_bindings_json"`
	// MonetizationMode 与应用行一起加锁，防止模式变更与策略保存交错。
	MonetizationMode application.MonetizationMode `gorm:"column:monetization_mode"`
	// AdUnits 提供与绑定修订处于同一事务快照的小程序广告位。
	AdUnits []byte `gorm:"column:ad_units_json"`
}

func (strategyBindingModel) TableName() string { return "applications" }

func (m unlockStrategyModel) domain() (monetization.UnlockStrategy, error) {
	var doc monetization.StrategyDocument
	if err := json.Unmarshal(m.Document, &doc); err != nil {
		return monetization.UnlockStrategy{}, err
	}
	if err := monetization.DefaultStrategies().Validate(doc); err != nil {
		return monetization.UnlockStrategy{}, err
	}
	return monetization.UnlockStrategy{ID: m.ID, Revision: m.Revision, Document: doc, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}, nil
}
func (m strategyBindingModel) domain() (monetization.StrategyBinding, error) {
	b := monetization.StrategyBinding{ApplicationID: m.ApplicationID, Rules: map[string]monetization.RuleBinding{}}
	if m.StrategyID != nil {
		b.StrategyID = *m.StrategyID
	}
	if len(m.Rules) > 0 {
		if err := json.Unmarshal(m.Rules, &b.Rules); err != nil {
			return b, err
		}
	}
	b.Rules = monetization.ProductBindings(b.Rules)
	return b, nil
}

// validateBindingAdUnits 使用已加锁应用行中的模式与广告位校验完整性，避免读取旧事务快照。
func validateBindingAdUnits(doc monetization.StrategyDocument, raw []byte, mode application.MonetizationMode) error {
	if err := monetization.ValidateApplicationMode(mode, doc); err != nil {
		return err
	}
	var units application.AdUnits
	if err := json.Unmarshal(raw, &units); err != nil {
		return err
	}
	if monetization.RequiresAdUnits(doc) && !units.Complete() {
		return operations.ErrApplicationAdUnitsIncomplete
	}
	return nil
}
