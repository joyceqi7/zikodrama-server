package mysqlstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

const entryPreviewTouchInterval = 24 * time.Hour

// FindOrQueueEntryPreview 在主体内按片源、空间和时长复用任务；只从成功任务返回独立 VID。
func (s *Store) FindOrQueueEntryPreview(ctx context.Context, episode domain.Episode, seconds int) (string, error) {
	subject := application.SubjectIDFromContext(ctx)
	if subject == "" {
		return "", domain.ErrServiceUnavailable
	}
	key := fmt.Sprintf("entry_preview_%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d",
		subject, episode.MediaSpaceName, episode.VID, seconds))))
	job, err := s.FindMediaJobByIdempotencyKey(ctx, key)
	if errors.Is(err, domain.ErrNotFound) {
		now := time.Now().UTC()
		pending := operations.MediaJob{
			Kind: operations.MediaJobEntryPreview, IdempotencyKey: key, EpisodeID: episode.ID, DramaID: episode.DramaID,
			ResourceClass: operations.MediaResourceVideo, SpaceName: episode.MediaSpaceName, ExpectedVID: episode.VID,
			PreviewSeconds: seconds, Status: operations.MediaJobQueued, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
			ProcessingStartedAt: now, ProcessingDeadlineAt: now.Add(15 * time.Minute),
		}
		createErr := s.CreateMediaJob(ctx, &pending)
		if createErr == nil || errors.Is(createErr, operations.ErrConflict) {
			return "", nil
		}
		return "", fmt.Errorf("%w: queue first episode clip: %w", domain.ErrServiceUnavailable, createErr)
	}
	if err != nil {
		return "", fmt.Errorf("%w: read first episode clip: %w", domain.ErrServiceUnavailable, err)
	}
	if job.Kind != operations.MediaJobEntryPreview || job.SpaceName != episode.MediaSpaceName ||
		job.ExpectedVID != episode.VID || job.PreviewSeconds != seconds {
		return "", fmt.Errorf("%w: unexpected first episode clip binding", domain.ErrServiceUnavailable)
	}
	switch job.Status {
	case operations.MediaJobSucceeded:
		if job.RemoteVID == "" || job.RemoteVID == episode.VID {
			return "", fmt.Errorf("%w: first episode clip missing distinct output", domain.ErrServiceUnavailable)
		}
		// 低频刷新使用时间，让清理任务只回收一段时间未使用的片段。
		now := time.Now().UTC()
		if now.Sub(job.UpdatedAt) >= entryPreviewTouchInterval {
			orm, dbErr := s.mediaJobORM()
			if dbErr != nil {
				return "", fmt.Errorf("%w: open first episode clip store: %w", domain.ErrServiceUnavailable, dbErr)
			}
			update := orm.WithContext(ctx).Model(&mediaJobModel{}).Where("id = ? AND status = ?", job.ID, job.Status).
				Update("updated_at", now)
			if update.Error != nil {
				return "", fmt.Errorf("%w: refresh first episode clip use: %w", domain.ErrServiceUnavailable, update.Error)
			}
			if update.RowsAffected == 0 {
				return "", nil
			}
		}
		return job.RemoteVID, nil
	case operations.MediaJobFailed:
		return "", fmt.Errorf("%w: first episode clip task %s failed: %s", domain.ErrServiceUnavailable, job.ID, job.Error)
	default:
		return "", nil
	}
}
