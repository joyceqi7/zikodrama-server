package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ensureDramaNotRecommended 在剧目锁之后读取当前渠道的权威推荐；隐藏和历史纯 ID 配置也须先移除。
func ensureDramaNotRecommended(tx *gorm.DB, dramaID string, direct bool) error {
	table := "homepage_recommendations"
	if direct {
		table = "direct_homepage_recommendations"
	}
	var rows []homepageRecommendationModel
	if err := subjectContentQuery(tx).Table(table).Clauses(clause.Locking{Strength: "UPDATE"}).Where("section IN ?", []string{"carousel", "hot"}).Order("section").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var config homepage.Carousel
		if err := json.Unmarshal([]byte(row.ConfigJSON), &config); err != nil {
			return err
		}
		if config.ReferencesDrama(dramaID) {
			return fmt.Errorf("%w: section=%s", homepage.ErrDramaRecommended, row.Section)
		}
	}
	return nil
}

// CheckTikTokUnpublish 在官方请求发出前复核推荐配置，阻止历史已排队任务绕过新下线门禁。
func (s *Store) CheckTikTokUnpublish(ctx context.Context, dramaID string) error {
	return s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var drama dramaModel
		if err := catalogQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", dramaID).Take(&drama).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return consumer.ErrNotFound
			}
			return err
		}
		return ensureDramaNotRecommended(tx, dramaID, false)
	})
}

// recommendationDramaIDs 合并两种配置的引用，避免隐藏条目或历史字段逃过并发保护。
func recommendationDramaIDs(config homepage.Carousel) []string {
	ids := append([]string{}, config.DramaIDs...)
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	for _, item := range config.Items {
		if !seen[item.DramaID] {
			ids = append(ids, item.DramaID)
			seen[item.DramaID] = true
		}
	}
	return ids
}

// checkNewRecommendationDramas 在共享剧目锁内重新校验新增引用，防止读取候选后与下线并发交错。
func checkNewRecommendationDramas(tx *gorm.DB, table string, previous, next homepage.Carousel, dramas []dramaModel) error {
	if err := attachSubjectStates(tx, dramas); err != nil {
		return err
	}
	byID := make(map[string]dramaModel, len(dramas))
	for _, drama := range dramas {
		byID[drama.ID] = drama
	}
	for _, id := range recommendationDramaIDs(next) {
		// 既有失效引用仍允许保留或逐项移除；此次新加的引用必须仍在线且未排队下架。
		if previous.ReferencesDrama(id) {
			continue
		}
		row, ok := byID[id]
		if !ok {
			return consumer.ErrNotFound
		}
		if table == "direct_homepage_recommendations" {
			var publication directPublicationModel
			err := subjectContentQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("drama_id = ?", id).Take(&publication).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return consumer.ErrNotFound
			}
			if err != nil {
				return err
			}
			if publication.Status != "published" || publication.CurrentVersionID == nil {
				return consumer.ErrNotFound
			}
		} else {
			drama, err := row.domain()
			if err != nil {
				return err
			}
			if drama.Published == nil || drama.Published.PublishStatus != "published" {
				return consumer.ErrNotFound
			}
			var latest tiktokPublicationModel
			err = subjectPublicationQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("drama_id = ?", id).Order("created_at DESC, id DESC").Take(&latest).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil && latest.Stage == operations.PublicationStageDelistAlbum && latest.Status != operations.PublicationStatusOffline {
				return homepage.ErrConflict
			}
		}
	}
	return nil
}
