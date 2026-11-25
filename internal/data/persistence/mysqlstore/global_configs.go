package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"drama-server/internal/biz/globalconfig"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/plugin/dbresolver"
)

// globalConfigModel 将完整配置与操作记录持久化。
type globalConfigModel struct {
	// ID 是配置记录的内部主键。
	ID uint64 `gorm:"primaryKey"`
	// Key 是稳定的业务配置键。
	Key string `gorm:"column:config_key"`
	// Value 是仅供服务端使用的原始 JSON，不写入诊断日志。
	Value string `gorm:"column:config_value"`
	// Remark 保存配置用途说明。
	Remark string
	// CreatedAt 保存配置首次创建时间。
	CreatedAt *time.Time `gorm:"autoCreateTime:false"`
	// UpdatedAt 保存最后一次业务修改时间。
	UpdatedAt *time.Time `gorm:"autoUpdateTime:false"`
	// UpdatedBy 保存已认证的操作人。
	UpdatedBy string
}

// TableName 返回全局配置的权威表。
func (globalConfigModel) TableName() string { return "global_configs" }

func (m globalConfigModel) entry() globalconfig.Entry {
	return globalconfig.Entry{Key: m.Key, Value: json.RawMessage(m.Value), Remark: m.Remark, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, UpdatedBy: m.UpdatedBy}
}

// GlobalConfigRepository 只从 MySQL 主节点读取配置，防止读副本延迟恢复旧凭据。
type GlobalConfigRepository struct{ db *gorm.DB }

// NewGlobalConfigRepository 复用既有连接；不建表、不写默认数据。
// 配置包含密钥，此仓储关闭 SQL 参数日志；业务层仍记录具体数据库错误。
func NewGlobalConfigRepository(data *Data) *GlobalConfigRepository {
	return &GlobalConfigRepository{db: data.DB().Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Clauses(dbresolver.Write)}
}

// List 按键返回数据库中的全部原始配置，仅限服务端内部使用。
func (r *GlobalConfigRepository) List(ctx context.Context) ([]globalconfig.Entry, error) {
	var models []globalConfigModel
	if err := r.db.WithContext(ctx).Order("config_key").Find(&models).Error; err != nil {
		return nil, err
	}
	rows := make([]globalconfig.Entry, 0, len(models))
	for _, m := range models {
		rows = append(rows, m.entry())
	}
	return rows, nil
}

// Get 返回数据库中的完整配置；不存在时返回 ErrNotFound。
func (r *GlobalConfigRepository) Get(ctx context.Context, key string) (globalconfig.Entry, error) {
	var m globalConfigModel
	err := r.db.WithContext(ctx).Where("config_key = ?", key).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return globalconfig.Entry{}, globalconfig.ErrNotFound
	}
	return m.entry(), err
}

// Put 事务内整体替换配置并保留创建时间；内部拥有方标识一经设置不可改变。
func (r *GlobalConfigRepository) Put(ctx context.Context, row globalconfig.Entry) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		m := globalConfigModel{Key: row.Key, Value: string(row.Value), Remark: row.Remark, CreatedAt: &now, UpdatedAt: &now, UpdatedBy: row.UpdatedBy}
		// 唯一键插入或无操作更新取得排他锁，串行化同一配置的首次创建与后续修改。
		if err := tx.Clauses(clause.OnConflict{DoUpdates: clause.Assignments(map[string]any{"config_key": gorm.Expr("config_key")})}).Create(&m).Error; err != nil {
			return err
		}
		var old globalConfigModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("config_key = ?", row.Key).Take(&old).Error; err != nil {
			return err
		}

		return tx.Model(&globalConfigModel{}).Where("config_key = ?", row.Key).Updates(map[string]any{"config_value": m.Value, "remark": m.Remark, "updated_at": now, "updated_by": row.UpdatedBy}).Error
	})
}

// Delete 删除配置及其元数据，不存在时返回领域错误。
func (r *GlobalConfigRepository) Delete(ctx context.Context, key string) error {
	result := r.db.WithContext(ctx).Where("config_key = ?", key).Delete(&globalConfigModel{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return globalconfig.ErrNotFound
	}
	return nil
}

// GlobalConfigs 返回共享当前数据库连接的全局配置仓储，不创建独立连接池。
func (s *Store) GlobalConfigs() *GlobalConfigRepository { return NewGlobalConfigRepository(s.data) }
