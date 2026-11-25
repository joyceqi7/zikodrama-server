package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// categoryRankingModel 保存当前主体官方标签的有序成员及运营配置，不复制标签译名或剧目内容。
type categoryRankingModel struct {
	// SubjectID 使成员、排序、隐藏和修订仅影响当前运营主体。
	SubjectID string `gorm:"column:subject_id;type:bigint unsigned"`
	// ID 是数据库生成的分类主键。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// TagID 是官方 TikTok 字符串标签标识。
	TagID string `gorm:"column:tag_id"`
	// Revision 在成员或运营配置变化时递增，防止覆盖并发上线和排序操作。
	Revision int64 `gorm:"column:revision"`
	// ItemsJSON 保存自动关联成员及其顺序和展示状态。
	ItemsJSON []byte `gorm:"column:items_json"`
	// CreatedAt 是分类首次关联时间，使用 UTC。
	CreatedAt time.Time `gorm:"column:created_at"`
	// UpdatedAt 是成员或运营配置最近变更时间，使用 UTC。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 指定官方分类表，不使用数据库外键。
func (categoryRankingModel) TableName() string { return "category_rankings" }

func (m categoryRankingModel) ranking() (homepage.Ranking, error) {
	result := homepage.Ranking{Revision: m.Revision, Items: []homepage.RankingItem{}}
	if err := json.Unmarshal(m.ItemsJSON, &result.Items); err != nil {
		return homepage.Ranking{}, err
	}
	return result, nil
}

// GetRankings 读取当前主体与渠道的分类表；线上成员由发布事务维护，读取不写库。
func (s *Store) GetRankings(ctx context.Context) (map[string]homepage.Ranking, error) {
	var rows []categoryRankingModel
	if err := subjectContentQuery(s.data.DB().WithContext(ctx)).Table(rankingTable(ctx)).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]homepage.Ranking, len(rows))
	for _, row := range rows {
		ranking, err := row.ranking()
		if err != nil {
			return nil, err
		}
		result[row.TagID] = ranking
	}
	return result, nil
}

// ReplaceRanking 锁定当前主体的单个分类并按修订更新运营配置；不能手动关联新剧，省略的成员保留为隐藏，只有线上标签变更或下架才删除关联。
func (s *Store) ReplaceRanking(ctx context.Context, tagID string, input homepage.Ranking) (homepage.Ranking, error) {
	var saved homepage.Ranking
	if application.SubjectIDFromContext(ctx) == "" {
		return saved, consumer.ErrNotFound
	}
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row categoryRankingModel
		err := subjectContentQuery(tx).Table(rankingTable(ctx)).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tag_id = ?", tagID).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return consumer.ErrNotFound
		}
		if err != nil {
			return err
		}
		if input.Revision != row.Revision {
			return homepage.ErrConflict
		}
		previous, err := row.ranking()
		if err != nil {
			return err
		}
		allowed := make(map[string]bool, len(previous.Items))
		for _, item := range previous.Items {
			allowed[item.DramaID] = true
		}
		saved = homepage.Ranking{Revision: row.Revision + 1, Items: append([]homepage.RankingItem{}, input.Items...)}
		seen := make(map[string]bool, len(input.Items))
		for _, item := range input.Items {
			if !allowed[item.DramaID] || seen[item.DramaID] {
				return consumer.ErrNotFound
			}
			seen[item.DramaID] = true
		}
		for _, item := range previous.Items {
			if !seen[item.DramaID] {
				item.Visible = false
				saved.Items = append(saved.Items, item)
			}
		}
		raw, err := json.Marshal(saved.Items)
		if err != nil {
			return err
		}
		return tx.Model(&row).Table(rankingTable(ctx)).Updates(map[string]any{"revision": saved.Revision, "items_json": raw, "updated_at": time.Now().UTC()}).Error
	})
	return saved, err
}

type categoryRankingKey struct{ tagID string }

// categoryMembership 只从线上快照产生关联，草稿改标签不改变已上线版本的分类。
func categoryMembership(raw []byte) (map[categoryRankingKey]bool, error) {
	result := map[categoryRankingKey]bool{}
	var published *consumer.Drama
	if len(raw) == 0 {
		return result, nil
	}
	if err := json.Unmarshal(raw, &published); err != nil {
		return nil, err
	}
	if published == nil || published.PublishStatus != "published" {
		return result, nil
	}
	for _, tagID := range published.TagIDs {
		if _, ok := consumer.FindTikTokTag(tagID); ok {
			result[categoryRankingKey{tagID}] = true
		}
	}

	return result, nil
}

// syncCategoryRankings 与线上快照共用事务；按固定顺序锁分类，保留仍有效成员的运营配置，新上线剧自动追加。
func syncCategoryRankings(tx *gorm.DB, dramaID string, before, after []byte) error {
	// 每个主体只同步自己的线上快照，不合并其他主体的分类成员。
	return syncCategoryRankingsTable(tx, "category_rankings", dramaID, before, after)
}
func syncDirectCategoryRankings(tx *gorm.DB, dramaID string, before, after []byte) error {
	// 直连版本快照处于 ready；渠道上架事务赋予 published 投影，不修改版本本体。
	normalized := make([][]byte, 2)
	for i, raw := range [][]byte{before, after} {
		if len(raw) == 0 || string(raw) == "null" {
			normalized[i] = raw
			continue
		}
		var snapshot consumer.Drama
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			return err
		}
		snapshot.PublishStatus = "published"
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		normalized[i] = encoded
	}
	return syncCategoryRankingsTable(tx, "direct_category_rankings", dramaID, normalized[0], normalized[1])
}
func syncCategoryRankingsTable(tx *gorm.DB, table, dramaID string, before, after []byte) error {
	oldKeys, err := categoryMembership(before)
	if err != nil {
		return err
	}
	nextKeys, err := categoryMembership(after)
	if err != nil {
		return err
	}
	all := map[categoryRankingKey]bool{}
	for key := range oldKeys {
		all[key] = true
	}
	for key := range nextKeys {
		all[key] = true
	}
	keys := make([]categoryRankingKey, 0, len(all))
	for key := range all {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].tagID < keys[j].tagID
	})
	for _, key := range keys {
		row := categoryRankingModel{SubjectID: application.SubjectIDFromContext(tx.Statement.Context), TagID: key.tagID, ItemsJSON: []byte("[]")}
		if err := tx.Table(table).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		// 冲突插入可能只返回旧主键；重新读取锁定行才能保留其他剧目刚提交的排序与成员。
		row = categoryRankingModel{}
		if err := subjectContentQuery(tx).Table(table).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tag_id = ?", key.tagID).Take(&row).Error; err != nil {
			return err
		}
		ranking, err := row.ranking()
		if err != nil {
			return err
		}
		found, changed := false, false
		items := make([]homepage.RankingItem, 0, len(ranking.Items)+1)
		for _, item := range ranking.Items {
			if item.DramaID == dramaID {
				found = true
				if !nextKeys[key] {
					changed = true
					continue
				}
			}
			items = append(items, item)
		}
		if nextKeys[key] && !found {
			items = append(items, homepage.RankingItem{DramaID: dramaID, Visible: true})
			changed = true
		}
		if !changed {
			continue
		}
		raw, err := json.Marshal(items)
		if err != nil {
			return err
		}
		if err := tx.Model(&row).Table(table).Updates(map[string]any{"revision": row.Revision + 1, "items_json": raw, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
	}
	return nil
}

func rankingTable(ctx context.Context) string {
	if application.IsDirect(ctx) {
		return "direct_category_rankings"
	}
	return "category_rankings"
}
