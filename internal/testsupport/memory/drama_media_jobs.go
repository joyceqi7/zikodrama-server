package memory

import (
	"context"
	"crypto/sha256"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"fmt"
	"time"
)

// ConfirmMediaUpload 与数据库保持相同的预留和剧归属约束。
func (s *Store) ConfirmMediaUpload(_ context.Context, job operations.MediaJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.mediaJobs[job.ID]
	if !ok {
		return domain.ErrNotFound
	}
	if stored.Kind != operations.MediaJobUpload || stored.DramaID != job.DramaID || stored.EpisodeID != job.EpisodeID || stored.FileName != job.FileName || stored.Size != job.Size || (stored.RemoteVID != "" && stored.RemoteVID != job.RemoteVID) {
		return operations.ErrConflict
	}
	if stored.Status != operations.MediaJobReserved && stored.Status != operations.MediaJobUploading {
		if stored.RemoteVID == job.RemoteVID {
			return nil
		}
		return operations.ErrConflict
	}
	drama, ok := s.dramas[job.DramaID]
	if !ok {
		return domain.ErrNotFound
	}
	found := false
	for _, ep := range drama.Episodes {
		if ep.ID == job.EpisodeID && ep.VID == stored.ExpectedVID {
			found = true
		}
	}
	for id, other := range s.mediaJobs {
		if id != job.ID && other.Kind == operations.MediaJobUpload && other.EpisodeID == job.EpisodeID && activeMediaJobStatus(other.Status) {
			found = false
		}
	}
	if !found {
		stored.Status = operations.MediaJobFailed
		stored.RemoteVID = job.RemoteVID
		s.mediaJobs[stored.ID] = stored
		s.queueResourceDeletionLocked(job)
		return operations.ErrMediaUploadDiscarded
	}
	for id, other := range s.mediaJobs {
		if id != job.ID && other.IdempotencyKey == job.IdempotencyKey {
			return operations.ErrConflict
		}
	}
	delete(s.mediaJobKeys, stored.IdempotencyKey)
	s.mediaJobKeys[job.IdempotencyKey] = job.ID
	s.mediaJobs[job.ID] = job
	return nil
}

// ApplyVODCallback 仅唤醒对应上传，终态不回退。
func (s *Store) ApplyVODCallback(_ context.Context, event operations.VODEvent) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, job := range s.mediaJobs {
		if job.Kind != operations.MediaJobUpload && job.Kind != operations.MediaJobCover {
			continue
		}
		if event.UploadID != "" {
			if id != event.UploadID {
				continue
			}
		} else if job.RemoteVID != event.ProviderAssetID || job.SpaceName != event.SpaceName {
			continue
		}
		if job.ResourceClass != event.ResourceClass || (job.SpaceName != "" && event.SpaceName != "" && job.SpaceName != event.SpaceName) || (job.RemoteVID != "" && job.RemoteVID != event.ProviderAssetID) {
			return false, nil
		}
		if job.Status == operations.MediaJobSucceeded || job.Status == operations.MediaJobRunning {
			return false, nil
		}
		if job.Status == operations.MediaJobFailed {
			if job.RemoteVID == "" {
				job.RemoteVID = event.ProviderAssetID
				s.mediaJobs[job.ID] = job
				s.mediaJobKeys[id] = job.ID
			}
			s.queueResourceDeletionLocked(job)
			return false, nil
		}
		if job.Status == operations.MediaJobReserved && job.RemoteVID == "" {
			job.RemoteVID = event.ProviderAssetID
		}
		if job.Status == operations.MediaJobQueued || job.Status == operations.MediaJobProcessing {
			job.NextAttemptAt = time.Now().UTC()
		}
		s.mediaJobs[id] = job
		return true, nil
	}
	return false, nil
}

// QueueMediaDeletion 将删除快照写入同一任务集合。
func (s *Store) QueueMediaDeletion(_ context.Context, job operations.MediaJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queueResourceDeletionLocked(job)
	return nil
}
func (s *Store) queueResourceDeletionLocked(job operations.MediaJob) {
	if job.RemoteVID == "" {
		return
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("delete_%x", sha256.Sum256([]byte(job.ResourceClass+"\x00"+job.SpaceName+"\x00"+job.RemoteVID)))
	if _, ok := s.mediaJobKeys[id]; ok {
		return
	}
	job.ID = s.allocateIDLocked()
	job.IdempotencyKey = id
	job.Kind = operations.MediaJobDelete
	job.Status = operations.MediaJobQueued
	job.CreatedAt = now
	job.UpdatedAt = now
	job.NextAttemptAt = now
	job.Attempts = 0
	job.LeaseOwner = ""
	job.LeaseExpiresAt = nil
	s.mediaJobs[job.ID] = job
	s.mediaJobKeys[id] = job.ID
}
func (s *Store) queueEpisodeDeletionLocked(ep domain.Episode) {
	s.queueResourceDeletionLocked(operations.MediaJob{DramaID: ep.DramaID, EpisodeID: ep.ID, ResourceClass: operations.MediaResourceVideo, SpaceName: ep.MediaSpaceName, RemoteVID: ep.VID})
}
func (s *Store) queueCoverDeletionLocked(drama domain.Drama) {
	s.queueResourceDeletionLocked(operations.MediaJob{DramaID: drama.ID, ResourceClass: operations.MediaResourceMaterial, SpaceName: drama.CoverSpaceName, RemoteVID: drama.CoverMID, ProviderFileName: drama.CoverFileName})
}

// IsMediaResourceUsed 检查草稿、线上快照、官方任务与未结束上传。
func (s *Store) IsMediaResourceUsed(_ context.Context, job operations.MediaJob) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, app := range s.applications {
		if job.PublicURL != "" && app.LogoURL == job.PublicURL {
			return true, nil
		}
	}
	for _, drama := range s.dramas {
		if dramaUsesResource(drama, job) {
			return true, nil
		}
		if drama.Published != nil && dramaUsesResource(*drama.Published, job) {
			return true, nil
		}
	}
	for _, publication := range s.tiktokPublications {
		if publication.ContentSnapshot != nil && dramaUsesResource(*publication.ContentSnapshot, job) {
			return true, nil
		}
	}
	for _, other := range s.mediaJobs {
		if other.Kind != operations.MediaJobUpload || other.RemoteVID != job.RemoteVID || other.SpaceName != job.SpaceName {
			continue
		}
		switch other.Status {
		case operations.MediaJobReserved, operations.MediaJobQueued, operations.MediaJobRunning, operations.MediaJobProcessing:
			for _, drama := range s.dramas {
				for _, ep := range drama.Episodes {
					if ep.ID == other.EpisodeID {
						return true, nil
					}
				}
			}
		}
	}
	return false, nil
}
func dramaUsesResource(drama domain.Drama, job operations.MediaJob) bool {
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

// CountMediaDeletionJobs 返回未完成删除数量。
func (s *Store) CountMediaDeletionJobs(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, job := range s.mediaJobs {
		if job.Kind == operations.MediaJobDelete && job.Status != operations.MediaJobSucceeded {
			n++
		}
	}
	return n, nil
}
