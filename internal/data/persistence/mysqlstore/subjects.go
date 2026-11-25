package mysqlstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"drama-server/internal/biz/subject"
	mysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// subjectModel 保存主体及官方发布凭据，相关 SQL 禁止输出参数。
type subjectModel struct {
	// ID 是数据库主键。
	ID string `gorm:"primaryKey;autoIncrement;type:bigint unsigned"`
	// Name 是唯一主体名称。
	Name string
	// Remark 是主体说明。
	Remark string
	// OwnerID 是已有官方资产引用的稳定标识。
	OwnerID string `gorm:"column:publisher_owner_id"`
	// TikTokAppID 是发布方官方标识。
	TikTokAppID string `gorm:"column:tiktok_app_id"`
	// ClientKey 是发布方公开标识。
	ClientKey string
	// ClientSecret 是只在服务端读取的发布凭据。
	ClientSecret string
	// CreatedAt 是创建时间。
	CreatedAt time.Time
	// UpdatedAt 是最后保存时间。
	UpdatedAt time.Time
}

// TableName 指向主体权威表，不自动建表。
func (subjectModel) TableName() string { return "subjects" }

func (m subjectModel) domain() subject.Subject {
	return subject.Subject{ID: m.ID, Name: m.Name, Remark: m.Remark, OwnerID: m.OwnerID, TikTokAppID: m.TikTokAppID, ClientKey: m.ClientKey, ClientSecret: m.ClientSecret, ClientSecretConfigured: m.ClientSecret != "", CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
}

func (s *Store) subjectDB(ctx context.Context) *gorm.DB {
	return s.data.DB().WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent), QueryFields: true})
}

// ListSubjects 读取完整主体目录，凭据只能由 Service 使用，HTTP 不直接返回。
func (s *Store) ListSubjects(ctx context.Context) ([]subject.Subject, error) {
	var rows []subjectModel
	if err := s.subjectDB(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]subject.Subject, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.domain())
	}
	return result, nil
}

// FindSubject 按主键读取主体及服务端凭据，找不到返回主体未找到错误。
func (s *Store) FindSubject(ctx context.Context, id string) (subject.Subject, error) {
	var row subjectModel
	err := s.subjectDB(ctx).Where("id=?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return subject.Subject{}, subject.ErrNotFound
	}
	return row.domain(), err
}

// CreateSubject 事务内分配主键和稳定拥有方标识，不复制其他主体的配置。
func (s *Store) CreateSubject(ctx context.Context, row *subject.Subject) error {
	m := subjectModel{Name: row.Name, Remark: row.Remark, TikTokAppID: row.TikTokAppID, ClientKey: row.ClientKey, ClientSecret: row.ClientSecret}
	err := s.subjectDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&m).Error; err != nil {
			return err
		}
		m.OwnerID = "subject-" + m.ID
		return tx.Model(&m).Update("publisher_owner_id", m.OwnerID).Error
	})
	if err != nil {
		return subjectStoreError(err)
	}
	*row = m.domain()
	return nil
}

// UpdateSubject 锁定主体后保存；已有发布记录时禁止替换发布身份，空密钥保留数据库现值。
func (s *Store) UpdateSubject(ctx context.Context, row subject.Subject) error {
	return subjectStoreError(s.subjectDB(ctx).Transaction(func(tx *gorm.DB) error {
		var old subjectModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", row.ID).Take(&old).Error; err != nil {
			return err
		}
		if old.ClientKey != row.ClientKey || old.TikTokAppID != row.TikTokAppID {
			// 只允许尚无官方资产的主体更换发布身份；空密钥不能沿用并发轮换后的其他身份凭据。
			for _, table := range []string{"tiktok_publications", "tiktok_drama_states"} {
				var count int64
				if err := tx.Table(table).Where("subject_id=?", row.ID).Count(&count).Error; err != nil {
					return err
				}
				if count > 0 {
					return subject.ErrConflict
				}
			}
			if row.ClientKey != "" && row.ClientSecret == "" {
				return subject.ErrNotConfigured
			}
		}
		values := map[string]any{"name": row.Name, "remark": row.Remark, "tiktok_app_id": row.TikTokAppID, "client_key": row.ClientKey, "updated_at": time.Now().UTC()}
		if row.ClientSecret != "" || row.ClientKey == "" {
			values["client_secret"] = row.ClientSecret
		}
		return tx.Model(&old).Updates(values).Error
	}))
}

func subjectStoreError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return subject.ErrNotFound
	}
	var sqlErr *mysql.MySQLError
	if errors.As(err, &sqlErr) && sqlErr.Number == 1062 {
		return subject.ErrConflict
	}
	if err != nil {
		return fmt.Errorf("subject persistence failed: %w", err)
	}
	return nil
}
