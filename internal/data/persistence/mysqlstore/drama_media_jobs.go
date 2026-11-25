package mysqlstore

import (
	"context"
	"crypto/sha256"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
)

// ConfirmMediaUpload 在剧锁内校验预留任务，重复确认不改变已开始或完成的任务。
func (s *Store) ConfirmMediaUpload(ctx context.Context, job operations.MediaJob) error {
	if err := job.ValidateStorageRules(); err != nil {
		return err
	}
	discarded := false
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var drama dramaModel
		if err := ownedDramaQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&drama, "id = ?", job.DramaID).Error; err != nil {
			return err
		}
		var episode episodeModel
		if err := tx.First(&episode, "id = ? AND drama_id = ? AND is_draft = ?", job.EpisodeID, job.DramaID, true).Error; err != nil {
			return err
		}
		var stored mediaJobModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&stored, "id = ?", job.ID).Error; err != nil {
			return err
		}
		if stored.SubjectID != application.SubjectIDFromContext(ctx) || stored.Kind != operations.MediaJobUpload || stored.EpisodeID != job.EpisodeID || stored.DramaID != job.DramaID || stored.FileName != job.FileName || stored.Size != job.Size || (stored.RemoteVID != "" && stored.RemoteVID != job.RemoteVID) {
			return operations.ErrConflict
		}
		if stored.Status != operations.MediaJobReserved && stored.Status != operations.MediaJobUploading {
			if stored.RemoteVID == job.RemoteVID {
				return nil
			}
			return operations.ErrConflict
		}
		var active int64
		if err := tx.Model(&mediaJobModel{}).Where("episode_id = ? AND id <> ? AND kind = ? AND status IN ?", job.EpisodeID, job.ID, operations.MediaJobUpload, []string{operations.MediaJobUploading, operations.MediaJobQueued, operations.MediaJobRunning, operations.MediaJobProcessing}).Count(&active).Error; err != nil {
			return err
		}
		if episode.VID != stored.ExpectedVID || active > 0 {
			if err := tx.Model(&stored).Updates(map[string]any{"status": operations.MediaJobFailed, "remote_vid": job.RemoteVID, "error_message": "upload discarded", "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
			if err := queueResourceDeletion(tx, job); err != nil {
				return err
			}
			discarded = true
			return nil
		}
		updates := mediaJobUpdates(job)
		updates["idempotency_key"] = job.IdempotencyKey
		if err := normalizeMediaJobError(tx.Model(&stored).Updates(updates).Error); err != nil {
			return err
		}
		return touchDramaOperator(tx, job.DramaID)
	})
	if err != nil {
		return err
	}
	if discarded {
		return operations.ErrMediaUploadDiscarded
	}
	return nil
}

// ApplyVODCallback 同步锁定匹配任务，在预留和上传中状态持久记录 VID 或提前轮询；终态和不匹配事件不修改。
func (s *Store) ApplyVODCallback(ctx context.Context, event operations.VODEvent) (bool, error) {
	applied := false
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("kind IN ?", []string{operations.MediaJobUpload, operations.MediaJobCover})
		if event.UploadID != "" {
			q = q.Where("id = ?", event.UploadID)
		} else {
			q = q.Where("remote_vid = ? AND resource_class = ? AND space_name = ?", event.ProviderAssetID, event.ResourceClass, event.SpaceName)
		}
		var job mediaJobModel
		if err := q.First(&job).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if job.ResourceClass != event.ResourceClass || (job.SpaceName != "" && event.SpaceName != "" && job.SpaceName != event.SpaceName) || (job.RemoteVID != "" && job.RemoteVID != event.ProviderAssetID) {
			return nil
		}
		if job.Status == operations.MediaJobSucceeded || job.Status == operations.MediaJobRunning {
			return nil
		}
		if job.Status == operations.MediaJobFailed {
			if job.RemoteVID == "" {
				job.RemoteVID = event.ProviderAssetID
				if err := tx.Model(&job).Update("remote_vid", job.RemoteVID).Error; err != nil {
					return err
				}
			}
			return queueResourceDeletion(tx, job.mediaJob())
		}
		if job.RemoteVID == "" && (job.Status == operations.MediaJobReserved || job.Status == operations.MediaJobUploading) {
			// 仅登记官方资源身份，处理是否成功仍由 Worker 查询 VOD，乱序回调不能回退分集状态。
			if err := tx.Model(&job).Update("remote_vid", event.ProviderAssetID).Error; err != nil {
				return err
			}
		}
		if job.Status == operations.MediaJobQueued || job.Status == operations.MediaJobProcessing {
			if err := tx.Model(&job).Update("next_attempt_at", time.Now().UTC()).Error; err != nil {
				return err
			}
		}
		applied = true
		return nil
	})
	return applied, err
}

// QueueMediaDeletion 将文件定位快照写入统一任务表，不依赖已删除的剧或分集。
func (s *Store) QueueMediaDeletion(ctx context.Context, job operations.MediaJob) error {
	return queueResourceDeletion(s.data.DB().WithContext(ctx), job)
}
func queueResourceDeletion(tx *gorm.DB, job operations.MediaJob) error {
	if job.RemoteVID == "" {
		return nil
	}
	if job.SubjectID == "" {
		job.SubjectID = application.SubjectIDFromContext(tx.Statement.Context)
	}
	if job.SubjectID == "" {
		return operations.ErrInvalidInput
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("delete_%x", sha256.Sum256([]byte(job.ResourceClass+"\x00"+job.SpaceName+"\x00"+job.RemoteVID)))
	record := newMediaJobModel(operations.MediaJob{SubjectID: job.SubjectID, IdempotencyKey: id, Kind: operations.MediaJobDelete,
		DramaID: job.DramaID, EpisodeID: job.EpisodeID, ResourceClass: job.ResourceClass, SpaceName: job.SpaceName, RemoteVID: job.RemoteVID, ProviderFileName: job.ProviderFileName, PublicURL: job.PublicURL,
		Status: operations.MediaJobQueued, CreatedAt: now, UpdatedAt: now, NextAttemptAt: now})
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error
}
func queueEpisodeDeletion(tx *gorm.DB, episode domain.Episode) error {
	return queueResourceDeletion(tx, operations.MediaJob{DramaID: episode.DramaID, EpisodeID: episode.ID, ResourceClass: operations.MediaResourceVideo, SpaceName: episode.MediaSpaceName, RemoteVID: episode.VID})
}
func queueCoverDeletion(tx *gorm.DB, drama domain.Drama) error {
	return queueResourceDeletion(tx, operations.MediaJob{DramaID: drama.ID, ResourceClass: operations.MediaResourceMaterial, SpaceName: drama.CoverSpaceName, RemoteVID: drama.CoverMID, ProviderFileName: drama.CoverFileName})
}

// IsMediaResourceUsed 检查草稿、线上快照、直连版本、官方任务与未结束上传，阻止删除仍有引用的媒资。
func (s *Store) IsMediaResourceUsed(ctx context.Context, job operations.MediaJob) (bool, error) {
	used, err := s.isFrozenContentResourceUsed(ctx, job)
	if err != nil || used {
		return used, err
	}
	var count int64
	if job.PublicURL != "" {
		if err := s.data.DB().WithContext(ctx).Model(&applicationModel{}).Where("logo_url = ?", job.PublicURL).Count(&count).Error; err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	if job.ResourceClass == operations.MediaResourceVideo {
		if err := s.data.DB().WithContext(ctx).Model(&mediaJobModel{}).
			Where("kind = ? AND remote_vid = ? AND space_name = ? AND status IN ?", operations.MediaJobEntryPreview,
				job.RemoteVID, job.SpaceName, []string{operations.MediaJobQueued, operations.MediaJobRunning, operations.MediaJobProcessing, operations.MediaJobSucceeded}).
			Count(&count).Error; err != nil || count > 0 {
			return count > 0, err
		}
		if err := s.data.DB().WithContext(ctx).Model(&episodeModel{}).Where("is_draft = ? AND vid = ? AND media_space_name = ?", true, job.RemoteVID, job.SpaceName).Count(&count).Error; err != nil {
			return false, err
		}
	} else {
		if err := s.data.DB().WithContext(ctx).Model(&dramaModel{}).Where("cover_mid = ? AND cover_space_name = ?", job.RemoteVID, job.SpaceName).Count(&count).Error; err != nil {
			return false, err
		}
	}
	if count > 0 {
		return true, nil
	}
	var states []tiktokDramaStateModel
	if err := s.data.DB().WithContext(ctx).Where("published_snapshot IS NOT NULL AND JSON_TYPE(published_snapshot)<>'NULL'").Find(&states).Error; err != nil {
		return false, err
	}
	for _, state := range states {
		var published domain.Drama
		if err := json.Unmarshal(state.PublishedSnapshot, &published); err != nil {
			return false, err
		}
		if publishedResourceUsed(published, job) {
			return true, nil
		}
	}
	// 未完成上传只在目标分集仍存在时阻止删除；被删除的剧不留下永久阻塞任务。
	err = s.data.DB().WithContext(ctx).Model(&mediaJobModel{}).Where("kind = ? AND remote_vid = ? AND space_name = ? AND status IN ?", operations.MediaJobUpload, job.RemoteVID, job.SpaceName, []string{operations.MediaJobReserved, operations.MediaJobUploading, operations.MediaJobQueued, operations.MediaJobRunning, operations.MediaJobProcessing}).
		Where("EXISTS (SELECT 1 FROM episodes WHERE episodes.id = media_jobs.episode_id AND episodes.is_draft = 1)").Count(&count).Error
	return count > 0, err
}
func publishedResourceUsed(drama domain.Drama, job operations.MediaJob) bool {
	if job.ResourceClass == operations.MediaResourceMaterial {
		return drama.CoverMID == job.RemoteVID && drama.CoverSpaceName == job.SpaceName
	}
	for _, ep := range drama.Episodes {
		if ep.VID == job.RemoteVID && ep.MediaSpaceName == job.SpaceName {
			return true
		}
	}
	return false
}

// CountMediaDeletionJobs 统计等待删除或仍在重试的任务。
func (s *Store) CountMediaDeletionJobs(ctx context.Context) (int, error) {
	var count int64
	err := subjectContentQuery(s.data.DB().WithContext(ctx)).Model(&mediaJobModel{}).Where("kind = ? AND status <> ?", operations.MediaJobDelete, operations.MediaJobSucceeded).Count(&count).Error
	return int(count), err
}

// isFrozenContentResourceUsed 分别检查直连版本和官方任务快照，保留已有 TK 任务的引用保护。
func (s *Store) isFrozenContentResourceUsed(ctx context.Context, job operations.MediaJob) (bool, error) {
	predicate := ""
	args := []any{}
	if job.ResourceClass == operations.MediaResourceVideo {
		raw, _ := json.Marshal(map[string]string{"vid": job.RemoteVID, "mediaSpaceName": job.SpaceName})
		predicate = "JSON_CONTAINS(JSON_EXTRACT(%s,'$.episodes'),?)"
		args = append(args, string(raw))
	} else {
		predicate = "JSON_UNQUOTE(JSON_EXTRACT(%s,'$.coverMid')) = ? AND JSON_UNQUOTE(JSON_EXTRACT(%s,'$.coverSpaceName')) = ?"
		args = append(args, job.RemoteVID, job.SpaceName)
	}
	for _, source := range []struct{ table, snapshot string }{
		{"direct_publication_versions", "snapshot_json"},
		{"tiktok_publications", "JSON_EXTRACT(workflow_json, '$.content')"},
	} {
		expr := strings.ReplaceAll(predicate, "%s", source.snapshot)
		var n int64
		if err := s.data.DB().WithContext(ctx).Table(source.table).Where(expr, args...).Count(&n).Error; err != nil {
			return false, err
		}
		if n > 0 {
			return true, nil
		}
	}
	return false, nil
}
