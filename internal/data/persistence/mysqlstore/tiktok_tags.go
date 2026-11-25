package mysqlstore

import (
	"context"
	"time"

	domain "drama-server/internal/biz/consumer"
	"gorm.io/gorm/clause"
)

// ListTikTokTags 合并固定目录和数据库翻译；不允许数据库新增官方身份。
func (s *Store) ListTikTokTags(ctx context.Context) ([]domain.TikTokTag, error) {
	var rows []tikTokTagEnglishNameModel
	if err := s.data.DB().WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	names := make(map[string]string, len(rows))
	for _, row := range rows {
		names[row.TagID] = row.EnglishName
	}
	tags := domain.TikTokTagCatalog()
	for i := range tags {
		tags[i].EnglishName = names[tags[i].ID]
	}
	return tags, nil
}

// SaveTikTokTagEnglishName 原子覆盖一个标签的英文名，重复保存不会创建额外记录。
func (s *Store) SaveTikTokTagEnglishName(ctx context.Context, id, name string) error {
	return s.data.DB().WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tag_id"}}, DoUpdates: clause.AssignmentColumns([]string{"english_name", "updated_at"})}).Create(&tikTokTagEnglishNameModel{TagID: id, EnglishName: name, UpdatedAt: time.Now().UTC()}).Error
}

// tikTokTagEnglishNameModel 仅保存英文翻译，官方原名由代码目录提供。
type tikTokTagEnglishNameModel struct {
	// TagID 是官方字符串标识，不建立数据库外键。
	TagID string `gorm:"column:tag_id;primaryKey"`
	// EnglishName 是可清空的英文展示名。
	EnglishName string `gorm:"column:english_name"`
	// UpdatedAt 保存最近维护时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 对应独立英文翻译表。
func (tikTokTagEnglishNameModel) TableName() string { return "tiktok_tag_english_names" }

// CountTikTokTagDramas 只聚合可信主体的当前编辑标签；重复标签和同主体多应用不重复计数。
func (s *Store) CountTikTokTagDramas(ctx context.Context) (map[string]int64, error) {
	var rows []struct {
		// TagID 是 JSON 中保存的官方字符串标识。
		TagID string
		// DramaCount 是具有该标签的不同短剧数量。
		DramaCount int64
	}
	err := subjectContentQuery(s.data.DB().WithContext(ctx).Table("dramas AS d")).
		Joins("JOIN JSON_TABLE(d.tag_ids_json, '$[*]' COLUMNS(tag_id VARCHAR(64) PATH '$')) AS tags ON TRUE").
		Select("tags.tag_id, COUNT(DISTINCT d.id) AS drama_count").Group("tags.tag_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.TagID] = row.DramaCount
	}
	return counts, nil
}
