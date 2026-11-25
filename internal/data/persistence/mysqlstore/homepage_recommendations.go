package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/plugin/dbresolver"
)

// homepageRecommendationModel 保存每主体的轮播和热播推荐配置，Redis 中不保存权威副本。
type homepageRecommendationModel struct {
	// SubjectID 将栏目配置固定到运营主体，两渠道仍使用各自的表。
	SubjectID string `gorm:"column:subject_id;type:bigint unsigned"`
	// ID 是数据库生成的内部主键。
	ID uint64 `gorm:"primaryKey"`
	// Section 在主体内唯一区分轮播 carousel 和人工热播 hot。
	Section string
	// Revision 用于整体替换的乐观并发校验。
	Revision int64
	// ConfigJSON 保留有序字符串 ID、标题、显隐和条目创建时间。
	ConfigJSON string `gorm:"column:config_json"`
	// CreatedAt 是首次入库时间，不代替推荐条目的历史时间。
	CreatedAt time.Time
	// UpdatedAt 是最后一次提交时间。
	UpdatedAt time.Time
}

// TableName 返回轮播和热播的权威配置表名。
func (homepageRecommendationModel) TableName() string { return "homepage_recommendations" }

var _ homepage.Repository = (*Store)(nil)

// GetCarousel 从 MySQL 主节点读取当前主体轮播；未配置返回版本零和空数组。
func (s *Store) GetCarousel(ctx context.Context) (homepage.Carousel, error) {
	return s.homepageConfiguration(ctx, "carousel")
}

// GetHot 从 MySQL 主节点读取当前主体人工热播；未配置返回版本零和空数组。
func (s *Store) GetHot(ctx context.Context) (homepage.Carousel, error) {
	return s.homepageConfiguration(ctx, "hot")
}

func (s *Store) homepageConfiguration(ctx context.Context, section string) (homepage.Carousel, error) {
	result := homepage.Carousel{DramaIDs: []string{}}
	var row homepageRecommendationModel
	err := subjectContentQuery(s.data.DB().WithContext(ctx)).Table(recommendationTable(ctx)).Clauses(dbresolver.Write).Where("section = ?", section).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal([]byte(row.ConfigJSON), &result); err != nil {
		return homepage.Carousel{}, err
	}
	result.Revision = row.Revision
	return result, nil
}

// ReplaceCarousel 在事务中整体替换轮播；旧修订返回 ErrConflict，不覆盖并发修改。
func (s *Store) ReplaceCarousel(ctx context.Context, input homepage.Carousel) (homepage.Carousel, error) {
	return s.replaceHomepageConfiguration(ctx, "carousel", input)
}

// ReplaceHot 在事务中整体替换热播并递增版本；清空保留版本，不影响轮播。
func (s *Store) ReplaceHot(ctx context.Context, input homepage.Carousel) (homepage.Carousel, error) {
	return s.replaceHomepageConfiguration(ctx, "hot", input)
}

func (s *Store) replaceHomepageConfiguration(ctx context.Context, section string, input homepage.Carousel) (homepage.Carousel, error) {
	if application.SubjectIDFromContext(ctx) == "" {
		return homepage.Carousel{}, consumer.ErrNotFound
	}
	next := homepage.Carousel{Revision: input.Revision + 1, DramaIDs: input.DramaIDs, Items: input.Items}
	if next.DramaIDs == nil {
		next.DramaIDs = []string{}
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return homepage.Carousel{}, err
	}
	err = s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 与两个渠道的下线保持相同锁序：先按主键锁住所选剧目，再锁推荐栏目。
		var dramas []dramaModel
		ids := recommendationDramaIDs(input)
		if len(ids) > 0 {
			if err := catalogQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", ids).Order("id").Find(&dramas).Error; err != nil {
				return err
			}
		}
		row := homepageRecommendationModel{SubjectID: application.SubjectIDFromContext(ctx), Section: section, ConfigJSON: `{"revision":0,"dramaIds":[]}`}
		// 唯一栏目插入取得写锁；首次并发保存也只能有一个版本零请求成功。
		if err := tx.Table(recommendationTable(ctx)).Clauses(clause.OnConflict{DoUpdates: clause.Assignments(map[string]any{"section": gorm.Expr("section")})}).Create(&row).Error; err != nil {
			return err
		}
		var old homepageRecommendationModel
		if err := subjectContentQuery(tx).Table(recommendationTable(ctx)).Clauses(clause.Locking{Strength: "UPDATE"}).Where("section = ?", section).Take(&old).Error; err != nil {
			return err
		}
		if old.Revision != input.Revision {
			return homepage.ErrConflict
		}
		var previous homepage.Carousel
		if err := json.Unmarshal([]byte(old.ConfigJSON), &previous); err != nil {
			return err
		}
		if err := checkNewRecommendationDramas(tx, recommendationTable(ctx), previous, input, dramas); err != nil {
			return err
		}
		return tx.Model(&old).Table(recommendationTable(ctx)).Updates(map[string]any{"revision": next.Revision, "config_json": string(raw), "updated_at": time.Now().UTC()}).Error
	})
	if err != nil {
		return homepage.Carousel{}, err
	}
	return next, nil
}

func recommendationTable(ctx context.Context) string {
	if application.IsDirect(ctx) {
		return "direct_homepage_recommendations"
	}
	return "homepage_recommendations"
}
