package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"errors"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// mediaJobModel 只映射由部署 Schema 建立的表结构；禁止使用 GORM 自动建表。
type mediaJobModel struct {
	// SubjectID 固定任务及未绑定封面的主体归属。
	SubjectID string `gorm:"column:subject_id;type:bigint unsigned"`
	// Kind 区分上传、封面、首集片段和删除任务。
	Kind string `gorm:"column:kind"`
	// DramaID 固定任务所属剧，删除剧后仍保留。
	DramaID string `gorm:"column:drama_id;type:bigint unsigned;default:null"`
	// ResourceClass 指定视频或素材接口。
	ResourceClass string `gorm:"column:resource_class"`
	// SpaceName 固定上传空间。
	SpaceName string `gorm:"column:space_name"`
	// ProviderFileName 保存图片 StoreUri。
	ProviderFileName string `gorm:"column:provider_file_name"`
	// PublicURL 是待保存的封面地址。
	PublicURL string `gorm:"column:public_url"`

	// 保存当前对象的唯一标识。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// 保存键。
	IdempotencyKey string `gorm:"column:idempotency_key;default:null"`
	// 保存剧集标识。
	EpisodeID string `gorm:"column:episode_id;type:bigint unsigned;default:null"`
	// 保存文件名称。
	FileName string `gorm:"column:file_name"`
	// 保存大小。
	Size int64 `gorm:"column:file_size"`
	// ExpectedVID 是直传校验的原 VID 或剪辑使用的正片 VID。
	ExpectedVID string `gorm:"column:expected_vid"`
	// PreviewSeconds 限制首集片段时长，其他媒资任务取零。
	PreviewSeconds int `gorm:"column:preview_seconds"`
	// 保存媒体平台最终返回的视频标识。
	RemoteVID string `gorm:"column:remote_vid"`
	// 保存上传请求标识。
	UploadRequestID string `gorm:"column:upload_request_id"`
	// 保存当前业务状态。
	Status string `gorm:"column:status"`
	// 保存媒体任务进入处理阶段的时间。
	ProcessingStartedAt *time.Time `gorm:"column:processing_started_at"`
	// 保存任务创建时固化的媒体处理截止时间。
	ProcessingDeadlineAt *time.Time `gorm:"column:processing_deadline_at"`
	// 保存尝试记录。
	AttemptCount int `gorm:"column:attempt_count"`
	// 保存下次执行时间尝试记录。
	NextAttemptAt time.Time `gorm:"column:next_attempt_at"`
	// 保存租约。
	LeaseOwner string `gorm:"column:lease_owner"`
	// 保存租约过期时间。
	LeaseExpiresAt *time.Time `gorm:"column:lease_expires_at"`
	// 保存可安全返回的错误摘要。
	Error string `gorm:"column:error_message"`
	// 保存记录创建时间。
	CreatedAt time.Time `gorm:"column:created_at"`
	// 保存记录最后更新时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// 返回当前数据模型对应的数据表名称。
func (mediaJobModel) TableName() string {
	return "media_jobs"
}

// 执行媒体任务对应的业务处理。
func (model mediaJobModel) mediaJob() operations.MediaJob {
	return operations.MediaJob{
		SubjectID: model.SubjectID,
		Kind:      model.Kind, DramaID: model.DramaID, ResourceClass: model.ResourceClass, SpaceName: model.SpaceName, ProviderFileName: model.ProviderFileName, PublicURL: model.PublicURL,
		ID: model.ID, IdempotencyKey: model.IdempotencyKey, EpisodeID: model.EpisodeID,
		FileName: model.FileName, Size: model.Size, ExpectedVID: model.ExpectedVID, PreviewSeconds: model.PreviewSeconds,
		RemoteVID: model.RemoteVID, UploadRequestID: model.UploadRequestID, Status: model.Status,
		ProcessingStartedAt: timeValue(model.ProcessingStartedAt), ProcessingDeadlineAt: timeValue(model.ProcessingDeadlineAt), Attempts: model.AttemptCount,
		NextAttemptAt: model.NextAttemptAt.UTC(), LeaseOwner: model.LeaseOwner,
		LeaseExpiresAt: utcTimePointer(model.LeaseExpiresAt), Error: model.Error,
		CreatedAt: model.CreatedAt.UTC(), UpdatedAt: model.UpdatedAt.UTC(),
	}
}

// 创建并返回媒体任务实例。
func newMediaJobModel(job operations.MediaJob) mediaJobModel {
	nextAttemptAt := job.NextAttemptAt
	if nextAttemptAt.IsZero() {
		nextAttemptAt = job.CreatedAt
	}
	if nextAttemptAt.IsZero() {
		nextAttemptAt = time.Now().UTC()
	}
	return mediaJobModel{
		SubjectID: job.SubjectID,
		Kind:      job.Kind, DramaID: job.DramaID, ResourceClass: job.ResourceClass, SpaceName: job.SpaceName, ProviderFileName: job.ProviderFileName, PublicURL: job.PublicURL,
		ID: job.ID, IdempotencyKey: job.IdempotencyKey, EpisodeID: job.EpisodeID,
		FileName: job.FileName, Size: job.Size, ExpectedVID: job.ExpectedVID, PreviewSeconds: job.PreviewSeconds,
		RemoteVID: job.RemoteVID, UploadRequestID: job.UploadRequestID, Status: job.Status,
		ProcessingStartedAt: optionalTime(job.ProcessingStartedAt), ProcessingDeadlineAt: optionalTime(job.ProcessingDeadlineAt), AttemptCount: job.Attempts,
		NextAttemptAt: nextAttemptAt.UTC(), LeaseOwner: job.LeaseOwner,
		LeaseExpiresAt: utcTimePointer(job.LeaseExpiresAt), Error: job.Error,
		CreatedAt: job.CreatedAt.UTC(), UpdatedAt: job.UpdatedAt.UTC(),
	}
}

// 执行协调世界时对应的业务处理。
func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}

// CreateMediaJob 保存入口已校验的媒资任务；分集预留在剧目锁内替换旧预留并更新人工操作人，成功后回填数据库 ID。
func (s *Store) CreateMediaJob(ctx context.Context, job *operations.MediaJob) error {
	job.SubjectID = application.SubjectIDFromContext(ctx)
	if job.SubjectID == "" {
		return operations.ErrInvalidInput
	}
	if err := job.ValidateStorageRules(); err != nil {
		return err
	}
	orm, err := s.mediaJobORM()
	if err != nil {
		return err
	}
	record := newMediaJobModel(*job)
	if job.Kind == operations.MediaJobUpload && job.Status == operations.MediaJobReserved {
		err := orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var drama dramaModel
			if err := ownedDramaQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&drama, "id = ?", job.DramaID).Error; err != nil {
				return err
			}
			var ep episodeModel
			if err := tx.First(&ep, "id = ? AND drama_id = ? AND is_draft = ?", job.EpisodeID, job.DramaID, true).Error; err != nil {
				return err
			}
			record.ExpectedVID = ep.VID
			var previous []mediaJobModel
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("episode_id = ? AND kind = ? AND status = ?", job.EpisodeID, operations.MediaJobUpload, operations.MediaJobReserved).Find(&previous).Error; err != nil {
				return err
			}
			for _, old := range previous {
				if err := tx.Model(&old).Updates(map[string]any{"status": operations.MediaJobFailed, "error_message": "upload superseded", "updated_at": time.Now().UTC()}).Error; err != nil {
					return err
				}
				if err := queueResourceDeletion(tx, old.mediaJob()); err != nil {
					return err
				}
			}
			if err := tx.Create(&record).Error; err != nil {
				return normalizeMediaJobError(err)
			}
			return touchDramaOperator(tx, job.DramaID)
		})
		if err == nil {
			job.ID = record.ID
		}
		return err
	}
	if err := orm.WithContext(ctx).Create(&record).Error; err != nil {
		return normalizeMediaJobError(err)
	}
	job.ID = record.ID
	return nil
}

// FindMediaJob 按内部任务 ID 返回媒资事实；不存在时返回内容域 ErrNotFound。
func (s *Store) FindMediaJob(ctx context.Context, id string) (operations.MediaJob, error) {
	orm, err := s.mediaJobORM()
	if err != nil {
		return operations.MediaJob{}, err
	}
	var record mediaJobModel
	err = subjectContentQuery(orm.WithContext(ctx)).Where("id = ?", strings.TrimSpace(id)).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return operations.MediaJob{}, domain.ErrNotFound
	}
	if err != nil {
		return operations.MediaJob{}, err
	}
	return record.mediaJob(), nil
}

// FindMediaJobByIdempotencyKey 按上传幂等键查找任务；不存在时返回内容域 ErrNotFound。
func (s *Store) FindMediaJobByIdempotencyKey(ctx context.Context, key string) (operations.MediaJob, error) {
	orm, err := s.mediaJobORM()
	if err != nil {
		return operations.MediaJob{}, err
	}
	var record mediaJobModel
	err = subjectContentQuery(orm.WithContext(ctx)).Where("idempotency_key = ?", strings.TrimSpace(key)).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return operations.MediaJob{}, domain.ErrNotFound
	}
	if err != nil {
		return operations.MediaJob{}, err
	}
	return record.mediaJob(), nil
}

// 查询并返回符合条件的媒体任务剧集标识集合列表。
func (s *Store) ListMediaJobsByEpisodeIDs(ctx context.Context, episodeIDs []string) ([]operations.MediaJob, error) {
	if len(episodeIDs) == 0 {
		return []operations.MediaJob{}, nil
	}
	ids := make([]string, 0, len(episodeIDs))
	for _, episodeID := range episodeIDs {
		if id := strings.TrimSpace(episodeID); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return []operations.MediaJob{}, nil
	}
	orm, err := s.mediaJobORM()
	if err != nil {
		return nil, err
	}
	var records []mediaJobModel
	if err := subjectContentQuery(orm.WithContext(ctx)).Where("episode_id IN ? AND kind = ?", ids, operations.MediaJobUpload).Order("created_at ASC").Order("id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	return mediaJobsFromModels(records), nil
}

// 查询并返回符合条件的媒体任务状态列表。
func (s *Store) ListMediaJobsByStatus(ctx context.Context, statuses []string, limit int) ([]operations.MediaJob, error) {
	return s.listMediaJobs(ctx, statuses, time.Time{}, time.Now().UTC(), limit)
}

// 查询并返回符合条件的媒体任务列表。
func (s *Store) ListMediaJobsUpdatedBefore(ctx context.Context, statuses []string, before time.Time, limit int) ([]operations.MediaJob, error) {
	return s.listMediaJobs(ctx, statuses, before.UTC(), time.Time{}, limit)
}

// 查询并返回符合条件的媒体任务列表。
func (s *Store) listMediaJobs(ctx context.Context, statuses []string, before, dueAt time.Time, limit int) ([]operations.MediaJob, error) {
	if len(statuses) == 0 {
		return []operations.MediaJob{}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	orm, err := s.mediaJobORM()
	if err != nil {
		return nil, err
	}
	query := orm.WithContext(ctx).Where("status IN ?", statuses)
	if !before.IsZero() {
		query = query.Where("updated_at < ?", before.UTC())
	}
	if !dueAt.IsZero() {
		query = query.Where("next_attempt_at <= ?", dueAt.UTC())
	}
	var records []mediaJobModel
	if err := query.Order("updated_at ASC").Order("created_at ASC").Limit(limit).Find(&records).Error; err != nil {
		return nil, err
	}
	return mediaJobsFromModels(records), nil
}

// 执行媒体任务对应的业务处理。
func mediaJobsFromModels(records []mediaJobModel) []operations.MediaJob {
	jobs := make([]operations.MediaJob, 0, len(records))
	for _, record := range records {
		jobs = append(jobs, record.mediaJob())
	}
	return jobs
}

// UpdateMediaJob 保存任务事实，指定租约时核对所有者；人工更新与所属短剧操作人在同一事务提交，失败均回滚。
func (s *Store) UpdateMediaJob(ctx context.Context, job operations.MediaJob) error {
	if err := job.ValidateStorageRules(); err != nil {
		return err
	}
	orm, err := s.mediaJobORM()
	if err != nil {
		return err
	}
	update := func(tx *gorm.DB) error {
		query := tx.Model(&mediaJobModel{}).Where("id = ?", strings.TrimSpace(job.ID))
		if strings.TrimSpace(job.LeaseOwner) != "" {
			query = query.Where("lease_owner = ?", job.LeaseOwner)
		}
		result := query.Updates(mediaJobUpdates(job))
		if result.Error != nil {
			return normalizeMediaJobError(result.Error)
		}
		if result.RowsAffected == 0 {
			if strings.TrimSpace(job.LeaseOwner) != "" {
				return operations.ErrConflict
			}
			return domain.ErrNotFound
		}
		return nil
	}
	// 人工重试或上传变更先锁剧目，任务失败时操作人更新一并回滚；Worker 保持原写入路径。
	if operations.PublicationActor(ctx) != "" && job.DramaID != "" {
		return orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := touchDramaOperator(tx, job.DramaID); err != nil {
				return err
			}
			return update(tx)
		})
	}
	return update(orm.WithContext(ctx))
}

// 执行媒体任务对应的业务处理。
func mediaJobUpdates(job operations.MediaJob) map[string]any {
	return map[string]any{
		"remote_vid": job.RemoteVID,
		"space_name": job.SpaceName, "provider_file_name": job.ProviderFileName, "public_url": job.PublicURL,
		"upload_request_id":      job.UploadRequestID,
		"status":                 job.Status,
		"processing_started_at":  optionalTime(job.ProcessingStartedAt),
		"processing_deadline_at": optionalTime(job.ProcessingDeadlineAt),
		"attempt_count":          job.Attempts,
		"next_attempt_at":        job.NextAttemptAt.UTC(),
		"lease_owner":            job.LeaseOwner,
		"lease_expires_at":       utcTimePointer(job.LeaseExpiresAt),
		"error_message":          job.Error,
		"updated_at":             job.UpdatedAt.UTC(),
	}
}

// 执行领取结果媒体任务租约对应的业务处理。
func (s *Store) ClaimMediaJobLease(ctx context.Context, claim operations.MediaJobLeaseClaim) (bool, error) {
	claim.ID = strings.TrimSpace(claim.ID)
	claim.ExpectedStatus = strings.TrimSpace(claim.ExpectedStatus)
	claim.LeaseOwner = strings.TrimSpace(claim.LeaseOwner)
	if claim.ID == "" || claim.ExpectedStatus == "" || claim.LeaseOwner == "" || claim.ClaimedAt.IsZero() || claim.LeaseExpiresAt.IsZero() ||
		!claim.LeaseExpiresAt.After(claim.ClaimedAt) {
		return false, operations.ErrInvalidInput
	}
	orm, err := s.mediaJobORM()
	if err != nil {
		return false, err
	}
	query := orm.WithContext(ctx).Model(&mediaJobModel{}).
		Where("id = ? AND status = ? AND updated_at = ?", claim.ID, claim.ExpectedStatus, claim.ExpectedUpdatedAt.UTC())
	if claim.ExpectedStatus == operations.MediaJobRunning {
		query = query.Where("(lease_expires_at IS NULL OR lease_expires_at <= ?)", claim.ClaimedAt.UTC())
	} else {
		query = query.Where("next_attempt_at <= ?", claim.ClaimedAt.UTC())
	}
	updates := map[string]any{
		"status":           operations.MediaJobRunning,
		"error_message":    "",
		"lease_owner":      claim.LeaseOwner,
		"lease_expires_at": claim.LeaseExpiresAt.UTC(),
		"updated_at":       claim.ClaimedAt.UTC(),
	}
	if claim.ExpectedStatus == operations.MediaJobQueued {
		updates["attempt_count"] = gorm.Expr("attempt_count + 1")
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return false, normalizeMediaJobError(result.Error)
	}
	return result.RowsAffected == 1, nil
}

// 执行媒体任务租约对应的业务处理。
func (s *Store) RenewMediaJobLease(
	ctx context.Context,
	id, leaseOwner string,
	renewedAt, leaseExpiresAt time.Time,
) (bool, error) {
	id = strings.TrimSpace(id)
	leaseOwner = strings.TrimSpace(leaseOwner)
	if id == "" || leaseOwner == "" || renewedAt.IsZero() || leaseExpiresAt.IsZero() || !leaseExpiresAt.After(renewedAt) {
		return false, operations.ErrInvalidInput
	}
	orm, err := s.mediaJobORM()
	if err != nil {
		return false, err
	}
	result := orm.WithContext(ctx).Model(&mediaJobModel{}).
		Where("id = ? AND status = ? AND lease_owner = ? AND lease_expires_at > ?", id, operations.MediaJobRunning, leaseOwner, renewedAt.UTC()).
		Updates(map[string]any{"lease_expires_at": leaseExpiresAt.UTC(), "updated_at": renewedAt.UTC()})
	if result.Error != nil {
		return false, normalizeMediaJobError(result.Error)
	}
	return result.RowsAffected == 1, nil
}

// 删除指定的媒体任务。
// DeleteMediaJob 原子回收终态或过期预留，未绑定的文件先写入删除任务，防止与封面提交竞争。
func (s *Store) DeleteMediaJob(ctx context.Context, id string) error {
	return s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job mediaJobModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&job, "id = ?", id).Error; err != nil {
			return err
		}
		switch job.Status {
		case operations.MediaJobSucceeded, operations.MediaJobFailed, operations.MediaJobReserved, operations.MediaJobUploaded:
		default:
			return operations.ErrConflict
		}
		if job.Kind == operations.MediaJobDelete && job.Status != operations.MediaJobSucceeded {
			return operations.ErrConflict
		}
		if job.Kind == operations.MediaJobEntryPreview && job.Status == operations.MediaJobSucceeded &&
			time.Since(job.UpdatedAt) < entryPreviewTouchInterval {
			// 扫描旧任务后若被新的播放请求续用，保留该片段和已发放的短期凭据。
			return operations.ErrConflict
		}
		if job.Kind == operations.MediaJobEntryPreview || (job.Kind != operations.MediaJobDelete && job.Status != operations.MediaJobSucceeded) {
			if err := queueResourceDeletion(tx, job.mediaJob()); err != nil {
				return err
			}
		}
		return tx.Delete(&job).Error
	})
}

// 执行媒体任务对应的业务处理。
func (s *Store) mediaJobORM() (*gorm.DB, error) {
	if s == nil || s.data.DB() == nil {
		return nil, errors.New("gorm mysql store is not configured")
	}
	return s.data.DB(), nil
}

// 规范化媒体任务错误，便于后续比较和持久化。
func normalizeMediaJobError(err error) error {
	var mysqlErr *mysqldriver.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return operations.ErrConflict
	}
	return err
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}
func timeValue(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}
