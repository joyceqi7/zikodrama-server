package mysqlstore

import (
	"encoding/json"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// tiktokDramaStateModel 是按主体保存的官方线上投影，不用于直连版本或共享草稿。
type tiktokDramaStateModel struct {
	// SubjectID 是发布主体主键。
	SubjectID string `gorm:"primaryKey;type:bigint unsigned"`
	// DramaID 是共享短剧主键。
	DramaID string `gorm:"primaryKey;type:bigint unsigned"`
	// PlatformAlbumID 保存此主体已创建的 Album。
	PlatformAlbumID string
	// PublishedSnapshot 是此主体已确认上线内容，下架时清空。
	PublishedSnapshot []byte
	// UpdatedAt 是投影最近更新时间。
	UpdatedAt time.Time
}

// TableName 返回主体官方状态表。
func (tiktokDramaStateModel) TableName() string { return "tiktok_drama_states" }

// attachSubjectStates 批量合并当前主体的官方投影；缺失时清空，绝不读取其他主体结果。
func attachSubjectStates(tx *gorm.DB, rows []dramaModel) error {
	if application.IsDirect(tx.Statement.Context) {
		return nil
	}
	ids := make([]string, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
		rows[i].PublishedSnapshot = nil
		rows[i].PlatformAlbumID = ""
	}
	id := application.SubjectIDFromContext(tx.Statement.Context)
	if id == "" || len(ids) == 0 {
		return nil
	}
	var states []tiktokDramaStateModel
	if err := tx.Where("subject_id=? AND drama_id IN ?", id, ids).Find(&states).Error; err != nil {
		return err
	}
	byID := map[string]tiktokDramaStateModel{}
	for _, state := range states {
		byID[state.DramaID] = state
	}
	for i := range rows {
		state := byID[rows[i].ID]
		rows[i].PublishedSnapshot = state.PublishedSnapshot
		rows[i].PlatformAlbumID = state.PlatformAlbumID
	}
	return nil
}

func attachSubjectState(tx *gorm.DB, row *dramaModel) error {
	rows := []dramaModel{*row}
	if err := attachSubjectStates(tx, rows); err != nil {
		return err
	}
	*row = rows[0]
	return nil
}

// saveSubjectState 只更新本主体线上快照；授权和上线的写入在调用方事务中提交。
func saveSubjectState(tx *gorm.DB, dramaID, albumID string, published *domain.Drama) error {
	id := application.SubjectIDFromContext(tx.Statement.Context)
	if id == "" {
		return operations.ErrInvalidInput
	}
	if err := requireOwnedDrama(tx, dramaID); err != nil {
		return err
	}
	var raw []byte
	var err error
	if published != nil {
		raw, err = json.Marshal(published)
		if err != nil {
			return err
		}
	}
	row := tiktokDramaStateModel{SubjectID: id, DramaID: dramaID, PlatformAlbumID: albumID, PublishedSnapshot: raw, UpdatedAt: time.Now().UTC()}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "subject_id"}, {Name: "drama_id"}}, DoUpdates: clause.AssignmentColumns([]string{"platform_album_id", "published_snapshot", "updated_at"})}).Create(&row).Error
}

// subjectPublicationQuery 强制按主体限定版本；缺少上下文也不读取其他主体。
func subjectPublicationQuery(tx *gorm.DB) *gorm.DB {
	if id := application.SubjectIDFromContext(tx.Statement.Context); id != "" {
		return tx.Where("subject_id=?", id)
	}
	return tx.Where("1=0")
}
