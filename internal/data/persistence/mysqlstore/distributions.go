package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type distributionModel struct {
	// ID 是数据库生成的分发关系主键。
	ID                           string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	operations.DramaDistribution `gorm:"embedded"`
}

func (distributionModel) TableName() string { return "drama_distributions" }

// DistributionSources 只读取官方归属和上线标记，避免轮询加载视频列表。
func (s *Store) DistributionSources(ctx context.Context) ([]operations.DistributionSource, error) {
	var rows []operations.DistributionSource
	q := s.data.DB().WithContext(ctx).Table("tiktok_drama_states AS state").Select("state.subject_id,state.drama_id,sub.publisher_owner_id AS owner_id,state.platform_album_id AS album_id,COALESCE(JSON_UNQUOTE(JSON_EXTRACT(state.published_snapshot,'$.publishStatus'))='published',false) AS online").Joins("JOIN subjects sub ON sub.id=state.subject_id").Where("state.platform_album_id<>''")
	if id := application.SubjectIDFromContext(ctx); id != "" {
		q = q.Where("state.subject_id=?", id)
	}
	err := q.Order("state.subject_id,state.drama_id").Scan(&rows).Error
	return rows, err
}

// FindDistributionSource 返回选定主体的剧目归属；不允许从其他主体选择最近记录。
func (s *Store) FindDistributionSource(ctx context.Context, id string) (*operations.DistributionSource, error) {
	var row operations.DistributionSource
	result := s.data.DB().WithContext(ctx).Table("tiktok_drama_states AS state").Select("state.subject_id,state.drama_id,sub.publisher_owner_id AS owner_id,state.platform_album_id AS album_id,COALESCE(JSON_UNQUOTE(JSON_EXTRACT(state.published_snapshot,'$.publishStatus'))='published',false) AS online").Joins("JOIN subjects sub ON sub.id=state.subject_id").Where("state.subject_id=? AND state.drama_id=? AND state.platform_album_id<>''", application.SubjectIDFromContext(ctx), id).Scan(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return &row, nil
}

// EnsureDistribution 幂等补齐关系，应用停用或剧目下架时自动撤权。
func (s *Store) EnsureDistribution(ctx context.Context, source operations.DistributionSource, app application.Application) (bool, error) {
	if source.SubjectID != app.SubjectID {
		return false, operations.ErrConflict
	}
	ctx = application.WithSubject(ctx, app.SubjectID)
	changed := false
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var drama dramaModel
		if err := ownedDramaQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&drama, "id = ?", source.DramaID).Error; err != nil {
			return err
		}
		if drama.SubjectID != application.SubjectIDFromContext(tx.Statement.Context) {
			return operations.ErrConflict
		}
		if err := attachSubjectState(tx, &drama); err != nil {
			return err
		}
		var published *domain.Drama
		if err := json.Unmarshal(drama.PublishedSnapshot, &published); err != nil && len(drama.PublishedSnapshot) > 0 {
			return err
		}
		online := published != nil && published.PublishStatus == "published"
		// 新应用只作为可选分发目标出现；跨应用授权必须由运营显式开启。
		// 拥有方无需额外选择，继续随官方上线版本自动可用。
		enabled := published != nil && published.OwnerApplicationID == app.ID
		desired := enabled && online && app.Status == application.StatusActive && app.ClientKey != ""
		status := "revoked"
		if desired {
			status = "pending"
		}
		job := distributionModel{DramaDistribution: operations.DramaDistribution{DramaID: source.DramaID, ApplicationID: app.ID, OwnerApplicationID: source.OwnerID, AlbumID: source.AlbumID, Enabled: enabled, Desired: desired, Status: status, NextAttemptAt: time.Now().UTC(), LeaseExpiresAt: time.Unix(0, 0).UTC()}}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("drama_id=? AND application_id=?", source.DramaID, app.ID).Take(&job).Error; err != nil {
			return err
		}
		desired = online && app.Status == application.StatusActive && app.ClientKey != "" && job.Enabled
		if job.Desired == desired {
			return nil
		}
		if err := tx.Model(&distributionModel{}).Where("drama_id=? AND application_id=?", source.DramaID, app.ID).Updates(map[string]any{"desired": desired, "status": "pending", "attempts": 0, "error_code": "", "revision": job.Revision + 1, "next_attempt_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		changed = true
		if !desired {
			return projectDistribution(tx, &drama, app.ID, false)
		}
		return nil
	})
	return changed, err
}

// ListDistributions 返回某部剧各应用的授权状态。
func (s *Store) ListDistributions(ctx context.Context, id string) ([]operations.DramaDistribution, error) {
	var rows []distributionModel
	q := s.data.DB().WithContext(ctx).Model(&distributionModel{}).Joins("JOIN applications target ON target.id=drama_distributions.application_id")
	if subjectID := application.SubjectIDFromContext(ctx); subjectID != "" {
		q = q.Where("target.subject_id=?", subjectID)
	}
	err := q.Where("drama_distributions.drama_id=?", id).Order("application_id").Find(&rows).Error
	result := make([]operations.DramaDistribution, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.DramaDistribution)
	}
	return result, err
}

// SetDistribution 保存人工目标并立刻移除本地撤权投影，保留在途租约避免相反调用重叠。
func (s *Store) SetDistribution(ctx context.Context, id, appID string, enabled bool) error {
	return s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var drama dramaModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&drama, "id=?", id).Error; err != nil {
			return err
		}
		var row distributionModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("drama_id=? AND application_id=?", id, appID).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrNotFound
			}
			return err
		}
		var app applicationModel
		if err := tx.First(&app, "id=?", appID).Error; err != nil {
			return err
		}
		if selected := application.SubjectIDFromContext(ctx); selected != "" && selected != app.SubjectID {
			return operations.ErrConflict
		}
		tx = tx.WithContext(application.WithSubject(tx.Statement.Context, app.SubjectID))
		if drama.SubjectID != application.SubjectIDFromContext(tx.Statement.Context) {
			return operations.ErrConflict
		}
		if err := attachSubjectState(tx, &drama); err != nil {
			return err
		}
		var published *domain.Drama
		if len(drama.PublishedSnapshot) > 0 {
			if err := json.Unmarshal(drama.PublishedSnapshot, &published); err != nil {
				return err
			}
		}
		desired := enabled && app.Status == application.StatusActive && published != nil && published.PublishStatus == "published"
		if err := tx.Model(&distributionModel{}).Where("drama_id=? AND application_id=?", id, appID).Updates(map[string]any{"enabled": enabled, "desired": desired, "status": "pending", "attempts": 0, "error_code": "", "revision": row.Revision + 1, "next_attempt_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		if err := touchDramaOperator(tx, id); err != nil {
			return err
		}
		if !desired {
			return projectDistribution(tx, &drama, appID, false)
		}
		return nil
	})
}

// QueueApplicationDistributions 仅把选中的已上线剧目一次提交到 MySQL；任一目标不可用时整体回滚。
func (s *Store) QueueApplicationDistributions(ctx context.Context, appID string, dramaIDs []string) (int, error) {
	sources, err := s.DistributionSources(ctx)
	if err != nil {
		return 0, err
	}
	selected := make(map[string]bool, len(dramaIDs))
	for _, id := range dramaIDs {
		selected[id] = false
	}
	for _, source := range sources {
		if source.Online {
			if _, ok := selected[source.DramaID]; ok {
				selected[source.DramaID] = true
			}
		}
	}
	if len(selected) != len(dramaIDs) {
		return 0, operations.ErrConflict
	}
	for _, found := range selected {
		if !found {
			return 0, operations.ErrConflict
		}
	}
	queued := 0
	err = s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var app applicationModel
		if err := tx.Where("id=?", appID).Take(&app).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrNotFound
			}
			return err
		}
		if app.Status != application.StatusActive || strings.TrimSpace(app.ClientKey) == "" {
			return operations.ErrConflict
		}
		for _, source := range sources {
			if source.SubjectID != app.SubjectID {
				return operations.ErrConflict
			}
			if !selected[source.DramaID] {
				continue
			}
			// 与单剧写入使用同一锁顺序，并在锁内复核上线状态，避免并发下架后新增授权。
			var drama dramaModel
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&drama, "id=?", source.DramaID).Error; err != nil {
				return err
			}
			if err := attachSubjectState(tx.WithContext(application.WithSubject(ctx, app.SubjectID)), &drama); err != nil {
				return err
			}
			var published *domain.Drama
			if len(drama.PublishedSnapshot) == 0 {
				return operations.ErrConflict
			}
			if err := json.Unmarshal(drama.PublishedSnapshot, &published); err != nil {
				return err
			}
			if published == nil || published.PublishStatus != "published" || drama.PlatformAlbumID == "" {
				return operations.ErrConflict
			}
			var row distributionModel
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("drama_id=? AND application_id=?", source.DramaID, appID).Take(&row).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				now := time.Now().UTC()
				row = distributionModel{DramaDistribution: operations.DramaDistribution{DramaID: source.DramaID, ApplicationID: appID, OwnerApplicationID: source.OwnerID, AlbumID: drama.PlatformAlbumID, Enabled: true, Desired: true, Status: "pending", NextAttemptAt: now, LeaseExpiresAt: time.Unix(0, 0).UTC()}}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else {
				// 已授权、排队或自动重试中的相同目标无需再次调用官方接口；人工介入项由本次点击恢复。
				if row.Enabled && row.Desired && row.OwnerApplicationID == source.OwnerID && row.AlbumID == drama.PlatformAlbumID && (row.Status == "authorized" || row.Status == "pending" || row.Status == "failed") {
					continue
				}
				if err := tx.Model(&distributionModel{}).Where("drama_id=? AND application_id=?", source.DramaID, appID).Updates(map[string]any{"owner_application_id": source.OwnerID, "album_id": drama.PlatformAlbumID, "enabled": true, "desired": true, "status": "pending", "attempts": 0, "error_code": "", "revision": row.Revision + 1, "next_attempt_at": time.Now().UTC()}).Error; err != nil {
					return err
				}
			}
			if err := touchDramaOperator(tx, source.DramaID); err != nil {
				return err
			}
			queued++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return queued, nil
}

// ClaimDistribution 通过行锁和租约领取待处理关系，过期任务可由其他 Worker 恢复。
func (s *Store) ClaimDistribution(ctx context.Context, worker string, now time.Time) (*operations.DramaDistribution, error) {
	var result *operations.DramaDistribution
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row distributionModel
		err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("status IN ? AND next_attempt_at<=? AND lease_expires_at<=?", []string{"pending", "failed"}, now, now).Order("next_attempt_at,drama_id,application_id").Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		row.Revision++
		row.LeaseOwner = worker
		row.LeaseExpiresAt = now.Add(time.Minute)
		if err := tx.Model(&distributionModel{}).Where("drama_id=? AND application_id=?", row.DramaID, row.ApplicationID).Updates(map[string]any{"revision": row.Revision, "lease_owner": worker, "lease_expires_at": row.LeaseExpiresAt}).Error; err != nil {
			return err
		}
		result = &row.DramaDistribution
		return nil
	})
	return result, err
}

// FinishDistribution 校验租约和修订；平台错误保留自动退避，成功才加入消费者授权投影。
func (s *Store) FinishDistribution(ctx context.Context, job operations.DramaDistribution, cause error) error {
	return s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var drama dramaModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&drama, "id=?", job.DramaID).Error; err != nil {
			return err
		}
		status := "revoked"
		if job.Desired {
			status = "authorized"
		}
		attempts := 0
		code := ""
		next := time.Now().UTC()
		if cause != nil {
			status = "failed"
			attempts = job.Attempts + 1
			code = "platform_authorization_failed"
			next = next.Add(time.Second * 30 * time.Duration(1<<min(attempts-1, 6)))
			if attempts >= 8 {
				status = "attention_required"
				code = "retry_exhausted"
			}
		}
		q := tx.Model(&distributionModel{}).Where("drama_id=? AND application_id=? AND revision=? AND lease_owner=? AND lease_expires_at>?", job.DramaID, job.ApplicationID, job.Revision, job.LeaseOwner, time.Now().UTC()).Updates(map[string]any{"status": status, "attempts": attempts, "error_code": code, "next_attempt_at": next, "lease_owner": "", "lease_expires_at": time.Unix(0, 0).UTC()})
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return operations.ErrConflict
		}
		if cause == nil {
			return projectDistribution(tx, &drama, job.ApplicationID, job.Desired)
		}
		return nil
	})
}

func projectDistribution(tx *gorm.DB, drama *dramaModel, appID string, authorized bool) error {
	var app applicationModel
	if err := tx.Select("subject_id").Where("id=?", appID).Take(&app).Error; err != nil {
		return err
	}
	tx = tx.WithContext(application.WithSubject(tx.Statement.Context, app.SubjectID))
	if err := attachSubjectState(tx, drama); err != nil {
		return err
	}
	var published *domain.Drama
	if len(drama.PublishedSnapshot) == 0 {
		return nil
	}
	if err := json.Unmarshal(drama.PublishedSnapshot, &published); err != nil {
		return err
	}
	if published == nil {
		return nil
	}
	ids := published.AuthorizedApplicationIDs
	ids = slices.DeleteFunc(ids, func(id string) bool { return id == appID })
	if authorized {
		ids = append(ids, appID)
	}
	published.AuthorizedApplicationIDs = ids
	return saveSubjectState(tx, drama.ID, drama.PlatformAlbumID, published)
}
