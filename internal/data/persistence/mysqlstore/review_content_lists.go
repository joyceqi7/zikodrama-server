package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// reviewContentListModel 保存同主体同渠道共享的审核内容和并发修订。
type reviewContentListModel struct {
	// ID 是审核清单的数据库自增主键。
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	// SubjectID 隔离不同发布主体的内容。
	SubjectID string `gorm:"column:subject_id;type:bigint unsigned"`
	// PlaybackProvider 区分官方发布与直连发布的审核内容。
	PlaybackProvider string `gorm:"column:playback_provider"`
	// DramaIDsJSON 保留清单顺序和待清理的失效内容引用。
	DramaIDsJSON []byte `gorm:"column:drama_ids_json"`
	// Revision 在同一主体渠道内串行保护审核版本与共享清单，并隔离首页缓存。
	Revision int64 `gorm:"column:revision"`
	// CreatedAt 是清单首次建立时间。
	CreatedAt time.Time `gorm:"column:created_at"`
	// UpdatedAt 是审核配置最近变更时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 将渠道审核内容固定保存到独立清单表。
func (reviewContentListModel) TableName() string { return "review_content_lists" }

// reviewScopeKey 将发布主体和可信播放渠道共同作为共享范围。
func reviewScopeKey(app application.Application) string {
	return app.SubjectID + ":" + app.Normalized().PlaybackProvider
}

// reviewScopeApplication 从当前事务读取应用的实际渠道，调用方负责先锁定应用行。
func reviewScopeApplication(tx *gorm.DB, id string) (application.Application, error) {
	var row struct{ SubjectID, Provider string }
	err := tx.Session(&gorm.Session{NewDB: true}).Table("applications AS a").Select("a.subject_id, COALESCE(p.provider,'tiktok_minis') AS provider").Joins("LEFT JOIN application_playback_profiles AS p ON p.application_id=a.id").Where("a.id=?", id).Take(&row).Error
	return application.Application{ID: id, SubjectID: row.SubjectID, PlaybackProvider: row.Provider}, err
}

// ensureReviewContentList 先初始化并锁定渠道行，避免两个应用首次保存时丢失共享修订。
func ensureReviewContentList(tx *gorm.DB, subjectID, provider string) error {
	tx = tx.Session(&gorm.Session{NewDB: true})
	now := time.Now().UTC()
	row := reviewContentListModel{SubjectID: subjectID, PlaybackProvider: provider, DramaIDsJSON: []byte("[]"), CreatedAt: now, UpdatedAt: now}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

// lockReviewScopes 在切换播放渠道前按固定顺序锁住两份清单，避免渠道往返导致旧表单重新生效。
func lockReviewScopes(tx *gorm.DB, apps ...application.Application) error {
	scopes := map[string]application.Application{}
	for _, app := range apps {
		scopes[reviewScopeKey(app)] = app
	}
	keys := make([]string, 0, len(scopes))
	for key := range scopes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		app := scopes[key]
		if err := ensureReviewContentList(tx, app.SubjectID, app.Normalized().PlaybackProvider); err != nil {
			return err
		}
	}
	return nil
}

// GetReviewWhitelist 读取当前主体和渠道的共享审核列表，不依赖外壳应用选择器。
func (s *Store) GetReviewWhitelist(ctx context.Context) (operations.ReviewWhitelist, error) {
	result := operations.ReviewWhitelist{DramaIDs: []string{}}
	var row reviewContentListModel
	err := s.data.DB().WithContext(ctx).Where("subject_id=? AND playback_provider=?", application.SubjectIDFromContext(ctx), application.PlaybackFromContext(ctx).Provider).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Revision = row.Revision
	err = json.Unmarshal(row.DramaIDsJSON, &result.DramaIDs)
	return result, err
}

// SaveReviewWhitelist 保存主体渠道的共享列表；任何应用仍有开启版本时不能清空列表。
func (s *Store) SaveReviewWhitelist(ctx context.Context, input operations.ReviewWhitelist) (operations.ReviewWhitelist, error) {
	subjectID, provider := application.SubjectIDFromContext(ctx), application.PlaybackFromContext(ctx).Provider
	if subjectID == "" {
		return input, operations.ErrInvalidInput
	}
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureReviewContentList(tx, subjectID, provider); err != nil {
			return err
		}
		var row reviewContentListModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("subject_id=? AND playback_provider=?", subjectID, provider).Take(&row).Error; err != nil {
			return err
		}
		if row.Revision != input.Revision {
			return operations.ErrConflict
		}
		if len(input.DramaIDs) == 0 {
			// 所有版本写入先锁定此渠道行，因此读取已提交版本即可安全阻止并发开启。
			var active int64
			if err := tx.Table("application_review_versions AS v").Joins("JOIN applications AS a ON a.id=v.application_id").Joins("LEFT JOIN application_playback_profiles AS p ON p.application_id=a.id").Where("a.subject_id=? AND COALESCE(p.provider,'tiktok_minis')=? AND v.enabled=1", subjectID, provider).Count(&active).Error; err != nil {
				return err
			}
			if active > 0 {
				return operations.ErrInvalidInput
			}
		}
		raw, err := json.Marshal(input.DramaIDs)
		if err != nil {
			return err
		}
		input.Revision++
		return tx.Model(&reviewContentListModel{}).Where("id=?", row.ID).Updates(map[string]any{"drama_ids_json": raw, "revision": input.Revision, "updated_at": time.Now().UTC()}).Error
	})
	return input, err
}
