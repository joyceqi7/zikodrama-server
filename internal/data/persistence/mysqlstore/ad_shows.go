package mysqlstore

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/plugin/dbresolver"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
)

// adShowModel 保存通用展示字段与可选内容来源，不保存场景 JSON 或逐场景扩展字段。
type adShowModel struct {
	// ID 是物理表内独立自增主键，不使用用户 UID 号段。
	ID string `gorm:"column:id;primaryKey;autoIncrement"`
	// BucketID 来自已认证主体的逻辑桶。
	BucketID uint16 `gorm:"column:bucket_id"`
	// ApplicationID 来自已认证应用的内部主键。
	ApplicationID string `gorm:"column:application_id"`
	// VisitorID 来自已认证的持久用户主体。
	VisitorID string `gorm:"column:visitor_id"`
	// EventID 同一展示补报时使用原值，不能重复计数。
	EventID string `gorm:"column:event_id"`
	// Scene 隔离当前广告使用场景。
	Scene string `gorm:"column:scene"`
	// AdUnitID 是实际成功展示的公开广告位。
	AdUnitID string `gorm:"column:ad_unit_id"`
	// DramaID 是可选来源剧目，不限定展示场景。
	DramaID *string `gorm:"column:drama_id"`
	// EpisodeID 是可选来源剧集，由业务校验所属剧目。
	EpisodeID *string `gorm:"column:episode_id"`
	// OccurredAt 保存首次接收展示确认的服务器 UTC 时刻；历史客户端时间记录保持原值。
	OccurredAt time.Time `gorm:"column:occurred_at"`
}

// TableName 是 Schema 对照模型；业务存取按用户逻辑桶路由到对应分表。
func (adShowModel) TableName() string { return "ad_shows_00" }

// FindAdShow 从用户分片主库读取既有事实；重试可跨日恢复，不重新验证当前广告和目录配置。
func (s *Store) FindAdShow(ctx context.Context, visitorID, eventID string) (monetization.AdShow, bool, error) {
	bucket, err := requestBucket(ctx)
	if err != nil {
		return monetization.AdShow{}, false, err
	}
	var model adShowModel
	err = s.data.DB().WithContext(ctx).Clauses(dbresolver.Write).Table(userTable("ad_shows", bucket)).
		Where("bucket_id = ? AND application_id = ? AND visitor_id = ? AND event_id = ?", bucket, application.IDFromContext(ctx), visitorID, eventID).Take(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return monetization.AdShow{}, false, nil
	}
	if err != nil {
		return monetization.AdShow{}, false, err
	}
	show := monetization.AdShow{EventID: model.EventID, Scene: model.Scene, AdUnitID: model.AdUnitID, OccurredAt: model.OccurredAt}
	if model.DramaID != nil {
		show.DramaID = *model.DramaID
	}
	if model.EpisodeID != nil {
		show.EpisodeID = *model.EpisodeID
	}
	return show, true, nil
}

// RecordAdShow 保存每条有效展示并核对同键目标；入剧沿用用户行事务锁，不因达标跳过写入。
func (s *Store) RecordAdShow(ctx context.Context, visitorID string, show monetization.AdShow) error {
	bucket, err := requestBucket(ctx)
	if err != nil {
		return err
	}
	model := adShowModel{BucketID: bucket, ApplicationID: application.IDFromContext(ctx), VisitorID: visitorID,
		EventID: show.EventID, Scene: show.Scene, AdUnitID: show.AdUnitID, DramaID: nullableString(show.DramaID), EpisodeID: nullableString(show.EpisodeID),
		OccurredAt: show.OccurredAt.UTC().Truncate(time.Microsecond)}
	db := s.data.DB().WithContext(ctx).Clauses(dbresolver.Write)
	if show.Scene != monetization.AdSceneEntryReward {
		return saveAdShowDB(db, bucket, model)
	}
	return s.userTransaction(ctx, model.ApplicationID, visitorID, func(tx *gorm.DB) error {
		// 唯一键保留首次接收时间，随后核对不可变目标；当日达标不影响幂等事实保存。
		return saveAdShowDB(tx, bucket, model)
	})
}

// saveAdShowDB 保留通用展示幂等合同，不将规则或门槛写入通用表。
func saveAdShowDB(db *gorm.DB, bucket uint16, model adShowModel) error {
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Table(userTable("ad_shows", bucket)).Create(&model).Error; err != nil {
		return err
	}
	var matching int64
	if err := db.Table(userTable("ad_shows", bucket)).
		Where("bucket_id = ? AND application_id = ? AND visitor_id = ? AND event_id = ?", bucket, model.ApplicationID, model.VisitorID, model.EventID).
		Where("scene = ? AND ad_unit_id = ? AND drama_id <=> ? AND episode_id <=> ?", model.Scene, model.AdUnitID, model.DramaID, model.EpisodeID).Count(&matching).Error; err != nil {
		return err
	}
	if matching != 1 {
		return monetization.ErrInvalidAdShow
	}
	return nil
}

// CountAdShows 只查询展示表；场景、内容来源和时间作为普通索引维度。
func (s *Store) CountAdShows(ctx context.Context, visitorID string, query monetization.AdShowQuery) (int, error) {
	bucket, err := requestBucket(ctx)
	if err != nil {
		return 0, err
	}
	db := s.data.DB().WithContext(ctx).Clauses(dbresolver.Write).Table(userTable("ad_shows", bucket)).
		Where("bucket_id = ? AND application_id = ? AND visitor_id = ? AND scene = ? AND occurred_at >= ? AND occurred_at < ?", bucket, application.IDFromContext(ctx), visitorID, query.Scene, query.From.UTC(), query.Until.UTC())
	if query.DramaID != "" {
		db = db.Where("drama_id = ?", query.DramaID)
	}
	if query.EpisodeID != "" {
		db = db.Where("episode_id = ?", query.EpisodeID)
	}
	var count int64
	if err := db.Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

// CountEntryAdCompletions 独立读取完整观看奖励；展示上报或展示存储不可用不影响观看进度读取。
func (s *Store) CountEntryAdCompletions(ctx context.Context, visitorID, progressKey, dramaID string, from, until time.Time) (int, error) {
	bucket, err := requestBucket(ctx)
	if err != nil {
		return 0, err
	}
	db := s.data.DB().WithContext(ctx).Clauses(dbresolver.Write).Table(userTable("reward_grants", bucket)).
		Where("bucket_id = ? AND application_id = ? AND visitor_id = ? AND progress_key = ? AND reward_type = ? AND granted_at >= ? AND granted_at < ?", bucket, application.IDFromContext(ctx), visitorID, progressKey, monetization.RewardTypeEntryProgress, from.UTC(), until.UTC())
	if dramaID != "" {
		db = db.Where("drama_id = ?", dramaID)
	}
	var count int64
	if err := db.Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

// nullableString 把无内容目标映射为 SQL NULL，保留标识符字符串精度。
func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
