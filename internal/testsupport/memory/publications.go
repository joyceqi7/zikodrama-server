package memory

import (
	"context"
	"sort"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// CheckTikTokUnpublish 核对剧目存在；该内存仓储未配置首页推荐，真实配置门禁由 MySQL 集成覆盖。
func (s *Store) CheckTikTokUnpublish(_ context.Context, dramaID string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.dramas[dramaID]; !ok {
		return domain.ErrNotFound
	}
	return nil
}

// FindTikTokPublicationByDramaID 返回短剧当前的官方发布任务副本。
func (s *Store) FindTikTokPublicationByDramaID(_ context.Context, dramaID string) (operations.TikTokPublication, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	publication, ok := s.latestPublicationLocked(dramaID)
	if !ok {
		return operations.TikTokPublication{}, domain.ErrNotFound
	}
	return cloneTikTokPublication(publication), nil
}

// SeedTikTokPublication 为测试装入已有官方任务，不执行新内容发布；仅限测试仓储。
func (s *Store) SeedTikTokPublication(publication *operations.TikTokPublication) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if publication.ID == "" {
		publication.ID = s.allocateIDLocked()
	}
	s.tiktokPublications[publication.ID] = cloneTikTokPublication(*publication)
}

// StartTikTokPublication 在发布临界区冻结就绪内容并创建 TK 任务。
func (s *Store) StartTikTokPublication(ctx context.Context, publication *operations.TikTokPublication) error {
	return s.startTikTokPublication(ctx, publication, nil)
}

// RestartTikTokPublication 按失败任务的冻结内容创建新媒体计划，不读取当前草稿。
func (s *Store) RestartTikTokPublication(ctx context.Context, previous operations.TikTokPublication, next *operations.TikTokPublication) error {
	return s.startTikTokPublication(ctx, next, &previous)
}

func (s *Store) startTikTokPublication(ctx context.Context, publication *operations.TikTokPublication, previous *operations.TikTokPublication) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	drama, ok := s.dramas[publication.DramaID]
	if !ok {
		return domain.ErrNotFound
	}
	current, exists := s.latestPublicationLocked(publication.DramaID)
	if previous != nil {
		if !exists || current.ID != previous.ID || current.Revision != previous.Revision || current.LeaseOwner != previous.LeaseOwner || !current.AllowsMediaRestart(*publication) {
			return operations.ErrConflict
		}
	} else {
		if err := operations.CheckContentReady(drama); err != nil {
			return err
		}
		if publication.ContentSnapshot == nil || domain.ContentDigest(*publication.ContentSnapshot) != domain.ContentDigest(drama) {
			return operations.ErrConflict
		}
		if exists && (current.RejectsContent(*publication.ContentSnapshot) || current.ExternalWriteStarted || (current.Active() && (current.LeaseOwner != "" || (current.Status != operations.PublicationStatusSynced && current.Status != operations.PublicationStatusApproved)))) {
			return operations.ErrConflict
		}
		snapshot := domain.FreezeContent(cloneDrama(drama))
		publication.ContentSnapshot = &snapshot
	}
	if exists {
		current.Revision++
		s.tiktokPublications[current.ID] = cloneTikTokPublication(current)
	}
	if publication.ID == "" {
		publication.ID = s.allocateIDLocked()
	}
	s.tiktokPublications[publication.ID] = cloneTikTokPublication(*publication)
	return nil
}

// ClaimTikTokPublication 领取到期队列任务，不扫描审核中记录。
func (s *Store) ClaimTikTokPublication(_ context.Context, workerID string, now, leaseExpiresAt time.Time) (*operations.TikTokPublication, error) {
	return s.claimTikTokPublication(workerID, now, leaseExpiresAt, false)
}

// ClaimTikTokReview 领取审核中任务，由低频审核扫描器调用。
func (s *Store) ClaimTikTokReview(_ context.Context, workerID string, now, leaseExpiresAt time.Time) (*operations.TikTokPublication, error) {
	return s.claimTikTokPublication(workerID, now, leaseExpiresAt, true)
}

// claimTikTokPublication 在内存仓储中复现生产租约与两类扫描边界。
func (s *Store) claimTikTokPublication(workerID string, now, leaseExpiresAt time.Time, review bool) (*operations.TikTokPublication, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidates := make([]operations.TikTokPublication, 0)
	for _, publication := range s.tiktokPublications {
		claimable := (publication.Stage == operations.PublicationStageRegisterVideos || publication.Stage == operations.PublicationStageTranscodeVideos) && publication.Status == operations.PublicationStatusQueued && !publication.NextAttemptAt.After(now)
		if review {
			claimable = publication.Status == operations.PublicationStatusUnderReview &&
				(publication.Stage == operations.PublicationStagePollReview || publication.Stage == operations.PublicationStagePollAppeal) &&
				publication.UpdatedAt.Before(now)
		}
		leaseAvailable := publication.LeaseOwner == "" || !publication.LeaseExpiresAt.After(now)
		if claimable && leaseAvailable {
			candidates = append(candidates, publication)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].NextAttemptAt.Equal(candidates[right].NextAttemptAt) {
			return candidates[left].ID < candidates[right].ID
		}
		return candidates[left].NextAttemptAt.Before(candidates[right].NextAttemptAt)
	})
	publication := candidates[0]
	publication.LeaseOwner = workerID
	publication.Revision++
	publication.LeaseExpiresAt = leaseExpiresAt
	publication.UpdatedAt = now
	s.tiktokPublications[publication.ID] = cloneTikTokPublication(publication)
	result := cloneTikTokPublication(publication)
	return &result, nil
}

// BeginTikTokExternalWrite 模拟保留租约的检查点，拒绝过期 Worker。
func (s *Store) BeginTikTokExternalWrite(_ context.Context, publication *operations.TikTokPublication) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if publication == nil || publication.LeaseOwner == "" || publication.ExternalWriteStarted {
		return operations.ErrConflict
	}
	current, ok := s.tiktokPublications[publication.ID]
	if !ok || current.Revision != publication.Revision || current.LeaseOwner != publication.LeaseOwner || !current.LeaseExpiresAt.After(time.Now().UTC()) {
		return operations.ErrConflict
	}
	publication.ExternalWriteStarted = true
	publication.Revision++
	s.tiktokPublications[publication.ID] = cloneTikTokPublication(*publication)
	return nil
}

// UpdateTikTokPublication 只保存当前任务的匹配修订，并可原子投影短剧和分集的平台标识。
func (s *Store) UpdateTikTokPublication(_ context.Context, publication operations.TikTokPublication, drama *domain.Drama) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.tiktokPublications[publication.ID]
	if !ok || current.ID != publication.ID {
		return domain.ErrNotFound
	}
	latest, exists := s.latestPublicationLocked(publication.DramaID)
	if !exists || latest.ID != publication.ID {
		return operations.ErrConflict
	}
	if current.Revision != publication.Revision || current.LeaseOwner != publication.LeaseOwner {
		return operations.ErrConflict
	}
	publication.Revision++
	if publication.Status != operations.PublicationStatusProcessing {
		publication.LeaseOwner, publication.LeaseExpiresAt = "", time.Time{}
	}
	s.tiktokPublications[publication.ID] = cloneTikTokPublication(publication)
	if drama == nil {
		return nil
	}
	stored, ok := s.dramas[drama.ID]
	if !ok {
		return domain.ErrNotFound
	}
	if publication.Stage == operations.PublicationStageDone && (publication.Status == operations.PublicationStatusOnline || publication.Status == operations.PublicationStatusOffline) {
		stored.Published = drama.Published
	}
	stored.PlatformAlbumID = drama.PlatformAlbumID
	platformEpisodes := make(map[string]string, len(drama.Episodes))
	for _, episode := range drama.Episodes {
		platformEpisodes[episode.ID] = episode.PlatformEpisodeID
	}
	for index := range stored.Episodes {
		if publication.Stage != operations.PublicationStageSubmitReview && publication.Stage != operations.PublicationStageAwaitSubmitReview {
			break
		}
		if platformID := platformEpisodes[stored.Episodes[index].ID]; platformID != "" {
			stored.Episodes[index].PlatformEpisodeID = platformID
		}
	}
	s.dramas[stored.ID] = cloneDrama(stored)
	return nil
}

func cloneTikTokPublication(publication operations.TikTokPublication) operations.TikTokPublication {
	if publication.ContentSummary != nil {
		summary := *publication.ContentSummary
		summary.EpisodeNumbers = append([]int{}, summary.EpisodeNumbers...)
		if summary.PreviousEpisodeNumbers != nil {
			summary.PreviousEpisodeNumbers = append([]int{}, summary.PreviousEpisodeNumbers...)
		}
		publication.ContentSummary = &summary
	}
	if publication.Appeal != nil {
		value := *publication.Appeal
		value.AttachmentURLs = append([]string(nil), value.AttachmentURLs...)
		publication.Appeal = &value
	}
	items := make(map[string]int, len(publication.ReviewItems))
	for id, status := range publication.ReviewItems {
		items[id] = status
	}
	publication.ReviewItems = items

	if publication.ContentSnapshot != nil {
		snapshot := cloneDrama(*publication.ContentSnapshot)
		publication.ContentSnapshot = &snapshot
	}
	publication.Videos = append([]operations.PublicationVideo(nil), publication.Videos...)
	publication.Transcodes = append([]operations.PublicationTranscode(nil), publication.Transcodes...)
	for index := range publication.Transcodes {
		publication.Transcodes[index].VIDs = append([]string(nil), publication.Transcodes[index].VIDs...)
		publication.Transcodes[index].CompletedVIDs = append([]string(nil), publication.Transcodes[index].CompletedVIDs...)
	}
	publication.ReviewFailReasons = append([]string(nil), publication.ReviewFailReasons...)
	mapping := make(map[int]string, len(publication.EpisodeIDsBySequence))
	for sequence, episodeID := range publication.EpisodeIDsBySequence {
		mapping[sequence] = episodeID
	}
	publication.EpisodeIDsBySequence = mapping
	return publication
}

// latestPublicationLocked 保留历史任务，并确定当前最新的提交记录；调用方必须持有锁。
func (s *Store) latestPublicationLocked(dramaID string) (operations.TikTokPublication, bool) {
	var latest operations.TikTokPublication
	found := false
	for _, publication := range s.tiktokPublications {
		if publication.DramaID == dramaID && (!found || publication.CreatedAt.After(latest.CreatedAt) || (publication.CreatedAt.Equal(latest.CreatedAt) && publication.ID > latest.ID)) {
			latest, found = publication, true
		}
	}
	return latest, found
}

// BeginTikTokPublicationCall 直接请求核对修订并占用冻结版本，Worker 不参与。
func (s *Store) BeginTikTokPublicationCall(_ context.Context, p *operations.TikTokPublication) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.latestPublicationLocked(p.DramaID)
	if !ok || current.ID != p.ID || current.Revision != p.Revision || current.LeaseOwner != "" && current.LeaseExpiresAt.After(time.Now().UTC()) {
		return operations.ErrConflict
	}
	p.Revision++
	s.tiktokPublications[p.ID] = cloneTikTokPublication(*p)
	return nil
}
