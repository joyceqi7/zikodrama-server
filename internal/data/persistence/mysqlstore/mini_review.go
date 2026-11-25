package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"sort"
	"time"
)

// applicationReviewVersionModel 按应用和包版本保存一条独立审核开关。
type applicationReviewVersionModel struct {
	// ID 是审核版本记录的数据库自增主键。
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	// ApplicationID 绑定可信应用，不能跨应用复用版本设置。
	ApplicationID string `gorm:"column:application_id;type:bigint unsigned"`
	// Version 精确匹配随包上报的三段版本号。
	Version string `gorm:"column:version"`
	// Enabled 只改变当前版本的审核策略。
	Enabled bool `gorm:"column:enabled"`
	// CreatedAt 保留版本首次登记时间。
	CreatedAt time.Time `gorm:"column:created_at"`
	// UpdatedAt 记录最近一次开关修改时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 将版本记录固定到审核版本表。
func (applicationReviewVersionModel) TableName() string { return "application_review_versions" }

// readMiniReviews 在同一事务快照中批量组合版本和清单；写事务在应用行锁之后读取最新值。
func readMiniReviews(tx *gorm.DB, apps []application.Application, locked bool) (map[string]application.MiniReview, error) {
	result := make(map[string]application.MiniReview, len(apps))
	if len(apps) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(apps))
	scopes := map[string]application.Application{}
	for _, app := range apps {
		ids = append(ids, app.ID)
		scopes[reviewScopeKey(app)] = app
		result[app.ID] = application.MiniReview{Versions: []application.MiniReviewVersion{}, DramaIDs: []string{}}
	}
	keys := make([]string, 0, len(scopes))
	for key := range scopes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([][]any, 0, len(keys))
	for _, key := range keys {
		app := scopes[key]
		provider := app.Normalized().PlaybackProvider
		if locked {
			if err := ensureReviewContentList(tx, app.SubjectID, provider); err != nil {
				return nil, err
			}
		}
		pairs = append(pairs, []any{app.SubjectID, provider})
	}
	query := tx.Session(&gorm.Session{NewDB: true})
	if locked {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var lists []reviewContentListModel
	if err := query.Where("(subject_id,playback_provider) IN ?", pairs).Find(&lists).Error; err != nil {
		return nil, err
	}
	byScope := map[string]application.MiniReview{}
	for _, row := range lists {
		review := application.MiniReview{Revision: row.Revision}
		if err := json.Unmarshal(row.DramaIDsJSON, &review.DramaIDs); err != nil {
			return nil, err
		}
		byScope[row.SubjectID+":"+row.PlaybackProvider] = review
	}
	for _, app := range apps {
		review := result[app.ID]
		shared := byScope[reviewScopeKey(app)]
		review.Revision = shared.Revision
		if shared.DramaIDs != nil {
			review.DramaIDs = append([]string{}, shared.DramaIDs...)
		}
		result[app.ID] = review
	}
	query = tx.Session(&gorm.Session{NewDB: true})
	if locked {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var versions []applicationReviewVersionModel
	if err := query.Where("application_id IN ?", ids).Order("id ASC").Find(&versions).Error; err != nil {
		return nil, err
	}
	for _, row := range versions {
		review := result[row.ApplicationID]
		review.Versions = append(review.Versions, application.MiniReviewVersion{Version: row.Version, Enabled: row.Enabled})
		if len(review.Versions) == 1 {
			review.Version, review.Enabled = row.Version, row.Enabled
		}
		result[row.ApplicationID] = review
	}
	return result, nil
}

// readMiniReview 供已锁定应用的写事务读取该应用版本和当前主体渠道共享清单。
func readMiniReview(tx *gorm.DB, id string) (application.MiniReview, error) {
	app, err := reviewScopeApplication(tx, id)
	if err != nil {
		return application.MiniReview{}, err
	}
	items, err := readMiniReviews(tx, []application.Application{app}, true)
	return items[id], err
}

// persistMiniReview 在同一应用事务内保存共享修订，版本修改保留未删除记录的主键与创建时间。
func persistMiniReview(tx *gorm.DB, id string, review application.MiniReview, writeVersions bool) error {
	tx = tx.Session(&gorm.Session{NewDB: true})
	ids := review.DramaIDs
	if ids == nil {
		ids = []string{}
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	app, err := reviewScopeApplication(tx, id)
	if err != nil {
		return err
	}
	list := reviewContentListModel{SubjectID: app.SubjectID, PlaybackProvider: app.Normalized().PlaybackProvider, DramaIDsJSON: raw, Revision: review.Revision, CreatedAt: now, UpdatedAt: now}
	columns := []string{"revision", "updated_at"}
	if !writeVersions {
		columns = append(columns, "drama_ids_json")
	}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "subject_id"}, {Name: "playback_provider"}}, DoUpdates: clause.AssignmentColumns(columns)}).Create(&list).Error; err != nil {
		return err
	}
	if !writeVersions {
		return nil
	}
	versions := review.ConfiguredVersions()
	var stored []applicationReviewVersionModel
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("application_id=?", id).Find(&stored).Error; err != nil {
		return err
	}
	previous := make(map[string]applicationReviewVersionModel, len(stored))
	for _, row := range stored {
		previous[row.Version] = row
	}
	names := make([]string, 0, len(versions))
	for _, row := range versions {
		names = append(names, row.Version)
	}
	remove := tx.Where("application_id=?", id)
	if len(names) > 0 {
		remove = remove.Where("version NOT IN ?", names)
	}
	if err := remove.Delete(&applicationReviewVersionModel{}).Error; err != nil {
		return err
	}
	for _, version := range versions {
		if row, ok := previous[version.Version]; ok {
			// 未改动的版本不更新记录时间；关闭只改开关，保留原主键和登记时间。
			if row.Enabled != version.Enabled {
				if err := tx.Model(&applicationReviewVersionModel{}).Where("id=?", row.ID).Updates(map[string]any{"enabled": version.Enabled, "updated_at": now}).Error; err != nil {
					return err
				}
			}
			continue
		}
		row := applicationReviewVersionModel{ApplicationID: id, Version: version.Version, Enabled: version.Enabled, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

// SaveMiniReviewSettings 在应用行锁内更新独立版本表，共享清单的并发修改会触发修订冲突。
func (s *Store) SaveMiniReviewSettings(ctx context.Context, id string, input application.MiniReview) (application.MiniReview, error) {
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var app applicationModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "monetization_mode", "ad_units_json").Where("id=?", id).Take(&app).Error; err != nil {
			return err
		}
		previous, err := readMiniReview(tx, id)
		if err != nil {
			return err
		}
		if previous.Revision != input.Revision {
			return operations.ErrConflict
		}
		input = previous.WithSettings(input)
		if input.AnyEnabled() && len(input.DramaIDs) == 0 {
			return operations.ErrInvalidInput
		}
		// 与策略保存共用应用行锁，开启前读取当前策略，避免并发移除逐集规则后仍启用审核。
		if input.AnyEnabled() && app.MonetizationMode == application.MonetizationModeIAA {
			var units application.AdUnits
			if err := json.Unmarshal(app.AdUnitsJSON, &units); err != nil {
				return err
			}
			if len(units.RewardedVideo) == 0 {
				return operations.ErrInvalidInput
			}
			var binding strategyBindingModel
			if err := tx.Where("id=?", id).Take(&binding).Error; err != nil {
				return err
			}
			if binding.StrategyID == nil {
				return operations.ErrInvalidInput
			}
			var model unlockStrategyModel
			if err := tx.First(&model, "id=?", *binding.StrategyID).Error; err != nil {
				return err
			}
			strategy, err := model.domain()
			if err != nil {
				return err
			}
			if !monetization.HasEnabledEpisodeRule(strategy.Document) {
				return operations.ErrInvalidInput
			}
		}
		input.Revision++
		return persistMiniReview(tx, id, input, true)
	})
	return input, err
}

// SaveMiniReview 只保存共享清单表，不改写审核版本及应用资料。
func (s *Store) SaveMiniReview(ctx context.Context, id string, input application.MiniReview) (application.MiniReview, error) {
	app, err := s.FindApplication(ctx, id)
	if err != nil {
		return input, err
	}
	saved, err := s.SaveReviewWhitelist(application.ContextForApplication(ctx, app), operations.ReviewWhitelist{Revision: input.Revision, DramaIDs: input.DramaIDs})
	app.MiniReview.Revision = saved.Revision
	app.MiniReview.DramaIDs = saved.DramaIDs
	return app.MiniReview, err
}

// validateMiniReviewStrategy 在应用事务锁内阻止审核期间移除全部逐集解锁入口。
func validateMiniReviewStrategy(review application.MiniReview, mode application.MonetizationMode, doc monetization.StrategyDocument) error {
	if review.AnyEnabled() && mode == application.MonetizationModeIAA && !monetization.HasEnabledEpisodeRule(doc) {
		return operations.ErrInvalidInput
	}
	return nil
}
