package memory

import (
	"context"
	"sort"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// 校验并创建媒体任务。
func (s *Store) CreateMediaJob(_ context.Context, job *operations.MediaJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job.ID == "" {
		job.ID = s.allocateIDLocked()
	}
	if _, exists := s.mediaJobs[job.ID]; exists {
		return operations.ErrConflict
	}
	if _, exists := s.mediaJobKeys[job.IdempotencyKey]; job.IdempotencyKey != "" && exists {
		return operations.ErrConflict
	}
	if job.Kind == operations.MediaJobUpload && activeMediaJobStatus(job.Status) {
		for _, existing := range s.mediaJobs {
			if existing.Kind == operations.MediaJobUpload && existing.EpisodeID == job.EpisodeID && activeMediaJobStatus(existing.Status) {
				return operations.ErrConflict
			}
		}
	}
	if job.Kind == operations.MediaJobUpload && job.Status == operations.MediaJobReserved {
		for id, old := range s.mediaJobs {
			if old.Kind == operations.MediaJobUpload && old.EpisodeID == job.EpisodeID && old.Status == operations.MediaJobReserved {
				old.Status = operations.MediaJobFailed
				s.mediaJobs[id] = old
				s.queueResourceDeletionLocked(old)
			}
		}
	}
	s.mediaJobs[job.ID] = *job
	if job.IdempotencyKey != "" {
		s.mediaJobKeys[job.IdempotencyKey] = job.ID
	}
	return nil
}

// 查询并返回指定的媒体任务。
func (s *Store) FindMediaJob(_ context.Context, id string) (operations.MediaJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.mediaJobs[id]
	if !ok {
		return operations.MediaJob{}, domain.ErrNotFound
	}
	return job, nil
}

// 查询并返回指定的媒体任务。
func (s *Store) FindMediaJobByIdempotencyKey(_ context.Context, key string) (operations.MediaJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.mediaJobKeys[key]
	if !ok {
		return operations.MediaJob{}, domain.ErrNotFound
	}
	return s.mediaJobs[id], nil
}

// 查询并返回符合条件的媒体任务剧集标识集合列表。
func (s *Store) ListMediaJobsByEpisodeIDs(_ context.Context, episodeIDs []string) ([]operations.MediaJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	allowed := make(map[string]struct{}, len(episodeIDs))
	for _, episodeID := range episodeIDs {
		allowed[episodeID] = struct{}{}
	}
	jobs := make([]operations.MediaJob, 0)
	for _, job := range s.mediaJobs {
		if _, ok := allowed[job.EpisodeID]; ok && job.Kind == operations.MediaJobUpload {
			jobs = append(jobs, job)
		}
	}
	sort.Slice(jobs, func(left, right int) bool {
		if jobs[left].CreatedAt.Equal(jobs[right].CreatedAt) {
			return jobs[left].ID < jobs[right].ID
		}
		return jobs[left].CreatedAt.Before(jobs[right].CreatedAt)
	})
	return jobs, nil
}

// 查询并返回符合条件的媒体任务状态列表。
func (s *Store) ListMediaJobsByStatus(_ context.Context, statuses []string, limit int) ([]operations.MediaJob, error) {
	return s.listMediaJobsByStatus(statuses, time.Time{}, time.Now().UTC(), limit), nil
}

// 查询并返回符合条件的媒体任务列表。
func (s *Store) ListMediaJobsUpdatedBefore(_ context.Context, statuses []string, before time.Time, limit int) ([]operations.MediaJob, error) {
	return s.listMediaJobsByStatus(statuses, before, time.Time{}, limit), nil
}

// 查询并返回符合条件的媒体任务状态列表。
func (s *Store) listMediaJobsByStatus(statuses []string, before, dueAt time.Time, limit int) []operations.MediaJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	allowed := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		allowed[status] = struct{}{}
	}
	jobs := make([]operations.MediaJob, 0)
	for _, job := range s.mediaJobs {
		if _, ok := allowed[job.Status]; ok && (before.IsZero() || job.UpdatedAt.Before(before)) &&
			(dueAt.IsZero() || !job.NextAttemptAt.After(dueAt)) {
			jobs = append(jobs, job)
		}
	}
	sort.Slice(jobs, func(left, right int) bool {
		if jobs[left].UpdatedAt.Equal(jobs[right].UpdatedAt) {
			if jobs[left].CreatedAt.Equal(jobs[right].CreatedAt) {
				return jobs[left].ID < jobs[right].ID
			}
			return jobs[left].CreatedAt.Before(jobs[right].CreatedAt)
		}
		return jobs[left].UpdatedAt.Before(jobs[right].UpdatedAt)
	})
	if limit > 0 && len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs
}

// 删除指定的媒体任务。
func (s *Store) DeleteMediaJob(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.mediaJobs[id]
	if !ok {
		return domain.ErrNotFound
	}
	switch job.Status {
	case operations.MediaJobSucceeded, operations.MediaJobFailed, operations.MediaJobReserved, operations.MediaJobUploaded:
	default:
		return operations.ErrConflict
	}
	if job.Kind == operations.MediaJobDelete && job.Status != operations.MediaJobSucceeded {
		return operations.ErrConflict
	}
	if job.Kind != operations.MediaJobDelete && job.Status != operations.MediaJobSucceeded {
		s.queueResourceDeletionLocked(job)
	}
	delete(s.mediaJobs, id)
	delete(s.mediaJobKeys, job.IdempotencyKey)
	return nil
}

// 执行领取结果媒体任务租约对应的业务处理。
func (s *Store) ClaimMediaJobLease(_ context.Context, claim operations.MediaJobLeaseClaim) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if claim.ID == "" || claim.ExpectedStatus == "" || claim.LeaseOwner == "" || claim.ClaimedAt.IsZero() || claim.LeaseExpiresAt.IsZero() ||
		!claim.LeaseExpiresAt.After(claim.ClaimedAt) {
		return false, operations.ErrInvalidInput
	}
	job, ok := s.mediaJobs[claim.ID]
	if !ok {
		return false, domain.ErrNotFound
	}
	if job.Status != claim.ExpectedStatus || !job.UpdatedAt.Equal(claim.ExpectedUpdatedAt) {
		return false, nil
	}
	if claim.ExpectedStatus == operations.MediaJobRunning {
		if job.LeaseExpiresAt != nil && job.LeaseExpiresAt.After(claim.ClaimedAt) {
			return false, nil
		}
	} else if job.NextAttemptAt.After(claim.ClaimedAt) {
		return false, nil
	}
	if claim.ExpectedStatus == operations.MediaJobQueued {
		job.Attempts++
	}
	job.Status = operations.MediaJobRunning
	job.Error = ""
	job.LeaseOwner = claim.LeaseOwner
	leaseExpiresAt := claim.LeaseExpiresAt.UTC()
	job.LeaseExpiresAt = &leaseExpiresAt
	job.UpdatedAt = claim.ClaimedAt.UTC()
	s.mediaJobs[claim.ID] = job
	return true, nil
}

// 执行媒体任务租约对应的业务处理。
func (s *Store) RenewMediaJobLease(
	_ context.Context,
	id, leaseOwner string,
	renewedAt, leaseExpiresAt time.Time,
) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || leaseOwner == "" || renewedAt.IsZero() || leaseExpiresAt.IsZero() || !leaseExpiresAt.After(renewedAt) {
		return false, operations.ErrInvalidInput
	}
	job, ok := s.mediaJobs[id]
	if !ok {
		return false, domain.ErrNotFound
	}
	if job.Status != operations.MediaJobRunning || job.LeaseOwner != leaseOwner || job.LeaseExpiresAt == nil ||
		!job.LeaseExpiresAt.After(renewedAt) {
		return false, nil
	}
	expires := leaseExpiresAt.UTC()
	job.LeaseExpiresAt = &expires
	job.UpdatedAt = renewedAt.UTC()
	s.mediaJobs[id] = job
	return true, nil
}

// 校验并更新媒体任务。
func (s *Store) UpdateMediaJob(_ context.Context, job operations.MediaJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.mediaJobs[job.ID]
	if !ok {
		return domain.ErrNotFound
	}
	if job.LeaseOwner != "" && existing.LeaseOwner != job.LeaseOwner {
		return operations.ErrConflict
	}
	if job.Kind == operations.MediaJobUpload && activeMediaJobStatus(job.Status) {
		for id, existing := range s.mediaJobs {
			if id != job.ID && existing.EpisodeID == job.EpisodeID && activeMediaJobStatus(existing.Status) {
				return operations.ErrConflict
			}
		}
	}
	s.mediaJobs[job.ID] = job
	return nil
}

// 执行媒体任务状态对应的业务处理。
func activeMediaJobStatus(status string) bool {
	return status == operations.MediaJobUploading || status == operations.MediaJobQueued || status == operations.MediaJobRunning || status == operations.MediaJobProcessing
}
