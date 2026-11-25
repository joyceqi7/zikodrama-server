package memory

import (
	"context"
	"strings"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
)

// FindAdShow 读取当前应用用户的原始展示快照，不随新的请求日期改变时间。
func (s *Store) FindAdShow(ctx context.Context, visitorID, eventID string) (monetization.AdShow, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	show, found := s.adShows[recordApplicationID(application.IDFromContext(ctx))+":"+visitorID+":"+eventID]
	return show, found, nil
}

// RecordAdShow 模拟已认证主体的不可变展示事实，保存少量通用字段及可选剧目或剧集来源。
func (s *Store) RecordAdShow(ctx context.Context, visitorID string, show monetization.AdShow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := recordApplicationID(application.IDFromContext(ctx)) + ":" + visitorID + ":" + show.EventID
	if saved, ok := s.adShows[key]; ok {
		if saved.Scene != show.Scene || saved.AdUnitID != show.AdUnitID || saved.DramaID != show.DramaID || saved.EpisodeID != show.EpisodeID {
			return monetization.ErrInvalidAdShow
		}
		return nil
	}
	s.adShows[key] = show
	return nil
}

// CountAdShows 模拟通用维度和日期过滤；按用户、场景和可选剧目或剧集隔离。
func (s *Store) CountAdShows(ctx context.Context, visitorID string, query monetization.AdShowQuery) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.countAdShowsLocked(ctx, visitorID, query), nil
}

// countAdShowsLocked 供持有读锁或写锁的调用方复用相同展示范围。
func (s *Store) countAdShowsLocked(ctx context.Context, visitorID string, query monetization.AdShowQuery) int {
	prefix := recordApplicationID(application.IDFromContext(ctx)) + ":" + visitorID + ":"
	count := 0
	for key, show := range s.adShows {
		if strings.HasPrefix(key, prefix) && show.Scene == query.Scene && (query.DramaID == "" || show.DramaID == query.DramaID) && (query.EpisodeID == "" || show.EpisodeID == query.EpisodeID) && !show.OccurredAt.Before(query.From) && show.OccurredAt.Before(query.Until) {
			count++
		}
	}
	return count
}

// CountEntryAdCompletions 仅模拟奖励账本的完整观看计数，展示存储错误不能影响该路径。
func (s *Store) CountEntryAdCompletions(ctx context.Context, visitorID, progressKey, dramaID string, from, until time.Time) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.countEntryAdCompletionsLocked(ctx, visitorID, progressKey, dramaID, from, until), nil
}

// countEntryAdCompletionsLocked 保持查询与结算事务中的完整观看范围一致。
func (s *Store) countEntryAdCompletionsLocked(ctx context.Context, visitorID, progressKey, dramaID string, from, until time.Time) int {
	appID := recordApplicationID(application.IDFromContext(ctx))
	count := 0
	for _, grant := range s.rewardGrants {
		if recordApplicationID(grant.ApplicationID) == appID && grant.VisitorID == visitorID && grant.ProgressKey == progressKey && grant.RewardType == monetization.RewardTypeEntryProgress && (dramaID == "" || grant.DramaID == dramaID) && !grant.GrantedAt.Before(from) && grant.GrantedAt.Before(until) {
			count++
		}
	}
	return count
}
