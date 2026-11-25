package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/pkg/requesttime"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"log/slog"
	"sort"
	"time"
)

// metricReceiptModel 只保留计数去重键和日期，不存储剧目、进度、媒资或广告凭据。
type metricReceiptModel struct {
	// BucketID 是凭据确认的逻辑桶。
	BucketID uint16 `gorm:"column:bucket_id;primaryKey"`
	// ApplicationID 是可信应用内部主键。
	ApplicationID string `gorm:"column:application_id;primaryKey"`
	// UserID 是可信用户主键。
	UserID string `gorm:"column:user_id;primaryKey"`
	// EventID 是客户端固定的一次事实键。
	EventID string `gorm:"column:event_id;primaryKey"`
	// Kind 区分同一过程的开始和结束事实。
	Kind string `gorm:"column:kind;primaryKey"`
	// OccurredAt 是首次确认的服务器 UTC 时间；旧记录继续保留其原始观察时间。
	OccurredAt time.Time `gorm:"column:occurred_at"`
	// ReceivedAt 是服务器首次接收时间，重试不覆盖。
	ReceivedAt time.Time `gorm:"column:received_at"`
}

// TableName 声明日表模板；运行时按 UTC 事实日期选择物理表。
func (metricReceiptModel) TableName() string { return "metric_events_template" }

// RecordMetrics 在同一事务内写入去重键和日期明细；跨日重试不能改写首次事实时间。
func (s *Store) RecordMetrics(ctx context.Context, userID string, events []domain.MetricEvent) error {
	bucket, err := requestBucket(ctx)
	if err != nil {
		return err
	}
	now := requesttime.At(ctx, time.Now)
	cutoff := metricCutoff(now)
	details := map[string]*domain.PostbackDetails{}
	ads := map[string]*domain.AdMetricDetails{}
	rows := make([]metricReceiptModel, 0, len(events))
	tables := map[string]bool{}
	kindSet := map[string]bool{}
	for _, e := range events {
		details[e.ID+":"+e.Kind] = e.Postback
		ads[e.ID+":"+e.Kind] = e.Ad
		if e.OccurredAt.Before(cutoff) || e.OccurredAt.After(now.Add(5*time.Minute)) {
			return fmt.Errorf("metric event is outside retained date window")
		}
		rows = append(rows, metricReceiptModel{BucketID: bucket, ApplicationID: application.IDFromContext(ctx), UserID: userID, EventID: e.ID, Kind: e.Kind, OccurredAt: e.OccurredAt.UTC(), ReceivedAt: now})
		tables[metricDayTable(e.OccurredAt)] = true
		kindSet[e.Kind] = true
	}
	// DDL 在业务事务外完成，避免 MySQL 隐式提交拆散批量原子性。
	for table := range tables {
		if err := s.ensureMetricTable(ctx, table, "metric_events_template"); err != nil {
			return err
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].EventID == rows[j].EventID {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].EventID < rows[j].EventID
	})
	insertedCount := int64(0)
	err = s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		accepted := make([]metricReceiptModel, 0, len(rows))
		for _, row := range rows {
			key := metricEventKey{ApplicationID: row.ApplicationID, UserID: row.UserID, EventID: row.EventID, Kind: row.Kind, EventDate: row.OccurredAt.Format(time.DateOnly)}
			inserted := tx.Clauses(clause.Insert{Modifier: "IGNORE"}).Create(&key)
			if inserted.Error != nil {
				return inserted.Error
			}
			if inserted.RowsAffected == 0 {
				continue
			}
			if err := tx.Table(metricDayTable(row.OccurredAt)).Create(&row).Error; err != nil {
				return err
			}
			if detail := details[row.EventID+":"+row.Kind]; detail != nil {
				payload, err := json.Marshal(detail)
				if err != nil {
					return err
				}
				entry := metricPostbackDetailModel{ApplicationID: row.ApplicationID, UserID: row.UserID, EventID: row.EventID, Kind: row.Kind, Scene: detail.Scene, ClientVersion: detail.ClientVersion, ErrorCode: detail.ErrorCode, Payload: string(payload), ReceivedAt: now}
				if err := tx.Create(&entry).Error; err != nil {
					return err
				}
			}
			if detail := ads[row.EventID+":"+row.Kind]; detail != nil {
				entry := metricAdEventModel{ApplicationID: row.ApplicationID, UserID: row.UserID, EventID: row.EventID, Kind: row.Kind, AdMetricDetails: *detail, OccurredAt: row.OccurredAt, ReceivedAt: now}
				if err := tx.Create(&entry).Error; err != nil {
					return err
				}
			}
			insertedCount++
			accepted = append(accepted, row)
		}
		return recordMetricActivity(tx, accepted)
	})
	if err != nil {
		return err
	}
	kinds := make([]string, 0, len(kindSet))
	for kind := range kindSet {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	slog.InfoContext(ctx, "consumer metric records persisted",
		"application_id", application.IDFromContext(ctx),
		"kinds", kinds,
		"event_count", len(events),
		"inserted_count", insertedCount,
		"duplicate_count", int64(len(events))-insertedCount,
	)
	return nil
}
