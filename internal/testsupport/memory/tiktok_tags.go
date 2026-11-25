package memory

import (
	"context"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
)

func (s *Store) tikTokTagsLocked() []domain.TikTokTag {
	tags := domain.TikTokTagCatalog()
	for i := range tags {
		tags[i].EnglishName = s.tagEnglishNames[tags[i].ID]
	}
	return tags
}

// ListTikTokTags 返回内存目录及翻译副本。
func (s *Store) ListTikTokTags(context.Context) ([]domain.TikTokTag, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tikTokTagsLocked(), nil
}

// SaveTikTokTagEnglishName 模拟单条英文翻译的幂等覆盖。
func (s *Store) SaveTikTokTagEnglishName(_ context.Context, id, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tagEnglishNames == nil {
		s.tagEnglishNames = map[string]string{}
	}
	s.tagEnglishNames[id] = name
	return nil
}

// CountTikTokTagDramas 按当前主体的编辑标签统计短剧，每部剧的重复标签只计一次。
func (s *Store) CountTikTokTagDramas(ctx context.Context) (map[string]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	counts := map[string]int64{}
	for _, drama := range s.dramas {
		if id := application.SubjectIDFromContext(ctx); id == "" || drama.SubjectID != id {
			continue
		}
		seen := map[string]bool{}
		for _, id := range drama.TagIDs {
			if !seen[id] {
				counts[id]++
				seen[id] = true
			}
		}
	}
	return counts, nil
}
