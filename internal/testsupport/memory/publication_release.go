package memory

import (
	"context"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

func (s *Store) releaseTasksLocked(dramaID string) []operations.TikTokPublication {
	var rows []operations.TikTokPublication
	for _, p := range s.tiktokPublications {
		if p.DramaID == dramaID {
			rows = append(rows, cloneTikTokPublication(p))
		}
	}
	for _, p := range s.tiktokReleases {
		if p.DramaID == dramaID {
			rows = append(rows, cloneTikTokPublication(p))
		}
	}
	return rows
}

// FindTikTokReleaseState 复用独立上架事实，保证新审核不改变已选版本。
func (s *Store) FindTikTokReleaseState(_ context.Context, id string) (*operations.TikTokReleaseState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := operations.ReleaseStateFromTasks(s.releaseTasksLocked(id))
	if state != nil {
		if s.dramas[id].Published == nil {
			state.Status = operations.PublicationStatusOffline
		} else {
			state.Status = operations.PublicationStatusOnline
		}
	}
	return state, nil
}

// ExecuteTikTokRelease 串行确认平台结果，不创建发布任务。
func (s *Store) ExecuteTikTokRelease(ctx context.Context, id string, input operations.TikTokReleaseInput, call operations.TikTokReleaseCall) (*operations.TikTokPublication, error) {
	// 全库内存锁只用于事实读写；平台工厂可能读取应用仓储，不能持锁调用。
	s.releaseMu.Lock()
	defer s.releaseMu.Unlock()
	s.mu.RLock()
	drama, ok := s.dramas[id]
	state := operations.ReleaseStateFromTasks(s.releaseTasksLocked(id))
	p := s.tiktokPublications[input.SelectedPublicationID]
	if saved, ok := s.tiktokReleases[input.SelectedPublicationID]; ok && input.Action != "publish" {
		p = saved
	}
	latest, exists := s.latestPublicationLocked(id)
	s.mu.RUnlock()
	if !ok {
		return nil, domain.ErrNotFound
	}
	if input.Action == "publish" {
		if !exists || latest.ID != p.ID {
			return nil, operations.ErrConflict
		}
		if state != nil && state.SelectedPublicationID == p.ID && drama.Published != nil {
			return &p, nil
		}
	} else {
		if state == nil || state.SelectedPublicationID != p.ID {
			return nil, operations.ErrConflict
		}
		if input.Action == "list" && drama.Published != nil || input.Action == "delist" && drama.Published == nil {
			return &p, nil
		}
	}
	if err := operations.ValidateSelectedRelease(p, drama, input.Action); err != nil {
		return nil, err
	}
	result, published, err := call(ctx, cloneTikTokPublication(p), cloneDrama(drama))
	if err != nil && (input.Action != "publish" || result.Stage != operations.PublicationStageDone || result.Status != operations.PublicationStatusOnline && result.Status != operations.PublicationStatusOffline) {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.dramas[id]
	stored.Published = published
	s.dramas[id] = cloneDrama(stored)
	if s.tiktokReleases == nil {
		s.tiktokReleases = map[string]operations.TikTokPublication{}
	}
	s.tiktokReleases[p.ID] = cloneTikTokPublication(result)
	if input.Action == "publish" {
		result.Revision++
		s.tiktokPublications[p.ID] = cloneTikTokPublication(result)
	}
	return &result, err
}
