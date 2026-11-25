package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// publicationRecordModel 映射独立记录；ID与任务相同，不依赖数据库外键。
type publicationRecordModel struct {
	// SubjectID 固定记录所属主体，审核与上线分别筛选。
	SubjectID string `gorm:"column:subject_id;type:bigint unsigned"`
	// ID 是发布任务及冻结审核版本的关联ID。
	ID string `gorm:"column:id;primaryKey;type:bigint unsigned"`
	// DramaID 是共享短剧ID。
	DramaID string `gorm:"column:drama_id"`
	// Title 保存提交时名称。
	Title string `gorm:"column:title"`
	// Status 是独立业务状态。
	Status string `gorm:"column:status"`
	// ActorID 是可信管理员ID，Worker更新时不覆盖。
	ActorID string `gorm:"column:actor_id"`
	// ActorName 是关联管理员得到的可读名称，只读。
	ActorName string `gorm:"column:actor_name;->"`
	// RecordJSON 仅保存既有管理API允许公开的字段，不存内部账号与媒体快照。
	RecordJSON []byte `gorm:"column:record_json"`
	// CreatedAt 是首次进入此业务流程的时间。
	CreatedAt time.Time `gorm:"column:created_at"`
	// TaskID 是关联查询得到的最新任务，只读不写入记录表。
	TaskID string `gorm:"column:task_id;->"`
	// TaskStatus 是最新任务的状态，只读。
	TaskStatus string `gorm:"column:task_status;->"`
	// TaskStage 是最新任务的阶段，只读。
	TaskStage string `gorm:"column:task_stage;->"`
	// UpdatedAt 是最近业务状态更新时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func publicationRecordTable(kind string) (string, error) {
	switch kind {
	case "reviews":
		return "tiktok_review_records", nil
	case "releases":
		return "tiktok_release_records", nil
	}
	return "", operations.ErrInvalidInput
}

// syncPublicationRecords 必须与任务CAS在同一事务执行，记录失败时任务状态同样回滚。
func syncPublicationRecords(tx *gorm.DB, p operations.TikTokPublication) error {
	if p.SubjectID == "" {
		return operations.ErrInvalidInput
	}
	tx = tx.WithContext(application.WithSubject(tx.Statement.Context, p.SubjectID))
	title := ""
	if p.ContentSnapshot != nil {
		title = p.ContentSnapshot.Title
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	row := publicationRecordModel{SubjectID: p.SubjectID, ID: p.ID, DramaID: p.DramaID, Title: title, Status: p.Status, ActorID: operations.PublicationActor(tx.Statement.Context), RecordJSON: raw, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
	release := p.PublishRequested || p.OnlineAt != nil || p.Status == operations.PublicationStatusOnline || p.Status == operations.PublicationStatusOffline || p.Stage == operations.PublicationStageDelistAlbum
	if !release {
		// 同步任务不是送审记录；只在官方受理后留档，避免把同步失败伪装为审核失败。
		if p.SubmittedAt == nil && p.ReviewID == "" {
			return nil
		}
		if p.SubmittedAt != nil {
			row.CreatedAt = *p.SubmittedAt
		}
		if p.Status == operations.PublicationStatusRejected && p.ReviewStatus == 4 {
			row.Status = "withdrawn"
		}
		return tx.Table("tiktok_review_records").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"title", "status", "record_json", "updated_at", "created_at"})}).Create(&row).Error
	}
	// 上架记录只保存确认事实；同步阶段、失败恢复和旧排队记录不能覆盖已选版本。
	if p.Stage != operations.PublicationStageDone || p.Status != operations.PublicationStatusOnline && p.Status != operations.PublicationStatusOffline {
		return nil
	}
	// 下架操作可能复用后续失败的任务，记录仍归属实际在线版本。
	if !p.PublishRequested && p.OnlineAt == nil && (p.Stage == operations.PublicationStageDelistAlbum || p.Status == operations.PublicationStatusOffline) {
		var online publicationRecordModel
		err := subjectPublicationQuery(tx).Table("tiktok_release_records").Where("drama_id = ? AND (status IN ? OR (status = 'failed' AND JSON_UNQUOTE(JSON_EXTRACT(record_json, '$.stage')) = 'delist_album'))", p.DramaID, []string{"online", "unpublishing"}).Order("created_at DESC, id DESC").Take(&online).Error
		if err != nil {
			return err
		}
		status := "unpublishing"
		if p.Status == operations.PublicationStatusOffline {
			status = "offline"
		}
		if p.Status == operations.PublicationStatusFailed {
			status = "failed"
		}
		var result operations.TikTokPublication
		if err := json.Unmarshal(online.RecordJSON, &result); err != nil {
			return err
		}
		result.Status, result.Stage, result.ErrorCode, result.ErrorMessage = p.Status, p.Stage, p.ErrorCode, p.ErrorMessage
		result.UpdatedAt = p.UpdatedAt
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Table("tiktok_release_records").Where("id = ?", online.ID).Updates(map[string]any{"status": status, "record_json": data, "updated_at": p.UpdatedAt}).Error
	}
	// 审核已经结束，其时间、审核意见和通过状态不随上线、下架状态变化。
	row.CreatedAt = p.UpdatedAt
	switch {
	case p.Status == operations.PublicationStatusOnline:
		row.Status = "online"
	case p.Status == operations.PublicationStatusOffline:
		row.Status = "offline"
	case p.Status == operations.PublicationStatusFailed:
		row.Status = "failed"
	case p.Stage == operations.PublicationStageDelistAlbum:
		row.Status = "unpublishing"
	default:
		row.Status = "publishing"
	}
	if row.Status == "online" || row.Status == "offline" {
		if err := subjectPublicationQuery(tx).Table("tiktok_release_records").Where("drama_id = ? AND id <> ? AND status = ?", p.DramaID, p.ID, "online").Updates(map[string]any{"status": "superseded", "updated_at": p.UpdatedAt}).Error; err != nil {
			return err
		}
	}
	return tx.Table("tiktok_release_records").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"status", "record_json", "updated_at"})}).Create(&row).Error
}

func decodePublicationRecord(row publicationRecordModel) (operations.PublicationRecord, error) {
	result := operations.PublicationRecord{ID: row.ID, DramaID: row.DramaID, Title: row.Title, Status: row.Status, ActorID: row.ActorID, ActorName: row.ActorName, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if err := json.Unmarshal(row.RecordJSON, &result.Publication); err != nil {
		return result, err
	}
	result.Publication.SubjectID = row.SubjectID
	result.Current = row.TaskID == row.ID
	if result.Current {
		result.TaskStatus = row.TaskStatus
		result.TaskStage = row.TaskStage
	}
	return result, nil
}

// ListPublicationRecords 按选定主体和已校验筛选分页读取独立表，不按应用隔离。
func (s *Store) ListPublicationRecords(ctx context.Context, q operations.PublicationRecordQuery) (operations.PublicationRecordPage, error) {
	result := operations.PublicationRecordPage{Items: []operations.PublicationRecord{}}
	table, err := publicationRecordTable(q.Kind)
	if err != nil {
		return result, err
	}
	db := s.publicationRecordsQuery(ctx, table)
	if q.DramaID != "" {
		db = db.Where("records.drama_id = ?", q.DramaID)
	}
	if q.Status != "" {
		db = db.Where("records.status = ?", q.Status)
	}
	if err = db.Count(&result.Total).Error; err != nil {
		return result, err
	}
	var rows []publicationRecordModel
	if err = db.Order("records.created_at DESC, records.id DESC").Offset((q.Page - 1) * q.PageSize).Limit(q.PageSize).Find(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		item, err := decodePublicationRecord(row)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// FindPublicationRecord 读取指定记录，未找到返回业务NotFound。
func (s *Store) FindPublicationRecord(ctx context.Context, kind, id string) (operations.PublicationRecord, error) {
	table, err := publicationRecordTable(kind)
	if err != nil {
		return operations.PublicationRecord{}, err
	}
	var row publicationRecordModel
	if err = s.publicationRecordsQuery(ctx, table).Where("records.id = ?", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = domain.ErrNotFound
		}
		return operations.PublicationRecord{}, err
	}
	return decodePublicationRecord(row)
}

// publicationRecordsQuery 一次关联最新任务，避免每行额外查询并统一历史操作门禁。
func (s *Store) publicationRecordsQuery(ctx context.Context, table string) *gorm.DB {
	scope := "tiktok_publications"
	order := "p.created_at DESC,p.id DESC"
	condition := ""
	if table == "tiktok_release_records" {
		scope = "tiktok_release_records"
		order = "p.updated_at DESC,p.id DESC"
		condition = " AND " + confirmedReleasePredicate("p")
	}
	query := s.data.DB().WithContext(ctx).Table(table+" AS records").Where("records.subject_id=?", application.SubjectIDFromContext(ctx)).Select("records.*, COALESCE(NULLIF(actor.nickname,''), actor.username, '') AS actor_name, COALESCE(latest.id,'') AS task_id, COALESCE(latest.status,'') AS task_status, '' AS task_stage").Joins("LEFT JOIN admin_users actor ON actor.id=records.actor_id").Joins("LEFT JOIN " + scope + " latest ON latest.id=(SELECT p.id FROM " + scope + " p WHERE p.drama_id=records.drama_id AND p.subject_id=records.subject_id" + condition + " ORDER BY " + order + " LIMIT 1)")
	if table == "tiktok_review_records" {
		query = query.Select("records.*, COALESCE(NULLIF(actor.nickname,''), actor.username, '') AS actor_name, COALESCE(latest.id,'') AS task_id, COALESCE(latest.status,'') AS task_status, COALESCE(latest.stage,'') AS task_stage")
	}
	if table == "tiktok_review_records" {
		// 兼容旧表中已保存的同步记录，不删除历史数据，但不再作为审核事实展示。
		query = query.Where("(JSON_UNQUOTE(JSON_EXTRACT(records.record_json, '$.reviewId')) <> '' OR JSON_EXTRACT(records.record_json, '$.submittedAt') IS NOT NULL)")
	}
	return query
}
