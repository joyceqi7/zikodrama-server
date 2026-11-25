package data

import (
	"context"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/data/redisstore"
	"log/slog"
	"time"
)

// catalogPersistence 组合剧编辑与发布持久化端口，供统一失效装饰器使用。
type catalogPersistence interface {
	operations.CatalogRepository
	operations.TikTokTagRepository
	operations.TikTokPublicationRepository
}

// invalidatingCatalogStore 在事务提交后清除 C 端缓存；B 端读取直接查询数据库。
// 发布 Worker 与管理 Handler 复用相同写入口。
type invalidatingCatalogStore struct {
	catalogPersistence
	cache *redisstore.CatalogCache
}

func (s *invalidatingCatalogStore) invalidate(ctx context.Context) {
	if s.cache == nil {
		return
	}
	// 客户端取消请求不应跳过已提交变更的失效动作。
	cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		if err := s.cache.Invalidate(cacheCtx); err != nil {
			slog.ErrorContext(cacheCtx, "invalidate consumer catalog after committed change failed", "attempt", attempt+1, "error", err)
		} else {
			return
		}
		if cacheCtx.Err() != nil {
			return
		}
	}
	// 数据库提交不能伪装成回滚；缓存故障由日志告警和有限 TTL 兜底。
}

// SaveDrama 保持原写入与事务语义，仅在成功后使 C 端列表和详情失效。
func (s *invalidatingCatalogStore) SaveDrama(ctx context.Context, drama domain.Drama) error {
	if err := s.catalogPersistence.SaveDrama(ctx, drama); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// SaveDramaAggregate 保持原写入与事务语义，仅在成功后使 C 端列表和详情失效。
func (s *invalidatingCatalogStore) SaveDramaAggregate(ctx context.Context, drama *domain.Drama, cover *operations.MediaJob) error {
	if err := s.catalogPersistence.SaveDramaAggregate(ctx, drama, cover); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// DeleteDrama 保持原写入与事务语义，仅在成功后使 C 端列表和详情失效。
func (s *invalidatingCatalogStore) DeleteDrama(ctx context.Context, id string) error {
	if err := s.catalogPersistence.DeleteDrama(ctx, id); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// SaveEpisode 保持原写入与事务语义，仅在成功后使 C 端列表和详情失效。
func (s *invalidatingCatalogStore) SaveEpisode(ctx context.Context, episode domain.Episode) error {
	if err := s.catalogPersistence.SaveEpisode(ctx, episode); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// SaveEpisodeMedia 保持原写入与事务语义，仅在成功后使 C 端列表和详情失效。
func (s *invalidatingCatalogStore) SaveEpisodeMedia(ctx context.Context, episode domain.Episode, drama domain.Drama, expectedVID, uploadID string) error {
	if err := s.catalogPersistence.SaveEpisodeMedia(ctx, episode, drama, expectedVID, uploadID); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// UpdateEpisodeMediaStatus 保持原写入与事务语义，仅在成功后使 C 端列表和详情失效。
func (s *invalidatingCatalogStore) UpdateEpisodeMediaStatus(ctx context.Context, episodeID, expectedVID, status string) error {
	if err := s.catalogPersistence.UpdateEpisodeMediaStatus(ctx, episodeID, expectedVID, status); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// StartTikTokPublication 保持原写入与事务语义，仅在成功后使 C 端列表和详情失效。
func (s *invalidatingCatalogStore) StartTikTokPublication(ctx context.Context, publication *operations.TikTokPublication) error {
	if err := s.catalogPersistence.StartTikTokPublication(ctx, publication); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// RestartTikTokPublication 保持冻结内容恢复语义，仅在成功后失效可回源目录。
func (s *invalidatingCatalogStore) RestartTikTokPublication(ctx context.Context, previous operations.TikTokPublication, next *operations.TikTokPublication) error {
	if err := s.catalogPersistence.RestartTikTokPublication(ctx, previous, next); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// UpdateTikTokPublication 保持原写入与事务语义，仅在成功后使 C 端列表和详情失效。
func (s *invalidatingCatalogStore) UpdateTikTokPublication(ctx context.Context, publication operations.TikTokPublication, drama *domain.Drama) error {
	if err := s.catalogPersistence.UpdateTikTokPublication(ctx, publication, drama); err != nil {
		return err
	}
	if drama != nil {
		s.invalidate(ctx)
	}
	return nil
}

// SaveTikTokTagEnglishName 提交英文名后使所有应用的列表、详情及首页缓存失效。
func (s *invalidatingCatalogStore) SaveTikTokTagEnglishName(ctx context.Context, id, name string) error {
	if err := s.catalogPersistence.SaveTikTokTagEnglishName(ctx, id, name); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// FindTikTokReleaseState 透传独立上下架读端口，生产组合根的缓存装饰器必须保留此能力。
func (s *invalidatingCatalogStore) FindTikTokReleaseState(ctx context.Context, id string) (*operations.TikTokReleaseState, error) {
	repo, ok := s.catalogPersistence.(operations.TikTokReleaseRepository)
	if !ok {
		return nil, operations.ErrPublicationUnavailable
	}
	return repo.FindTikTokReleaseState(ctx, id)
}

// ExecuteTikTokRelease 在平台确认且数据库提交后失效缓存，不依赖 Worker。
func (s *invalidatingCatalogStore) ExecuteTikTokRelease(ctx context.Context, id string, input operations.TikTokReleaseInput, call operations.TikTokReleaseCall) (*operations.TikTokPublication, error) {
	repo, ok := s.catalogPersistence.(operations.TikTokReleaseRepository)
	if !ok {
		return nil, operations.ErrPublicationUnavailable
	}
	result, err := repo.ExecuteTikTokRelease(ctx, id, input, call)
	if result != nil {
		s.invalidate(ctx)
	}
	return result, err
}

// BeginTikTokPublicationCall 保留请求内同步写入的修订保护能力。
func (s *invalidatingCatalogStore) BeginTikTokPublicationCall(ctx context.Context, p *operations.TikTokPublication) error {
	repo, ok := s.catalogPersistence.(operations.TikTokExecutionRepository)
	if !ok {
		return operations.ErrPublicationUnavailable
	}
	return repo.BeginTikTokPublicationCall(ctx, p)
}
