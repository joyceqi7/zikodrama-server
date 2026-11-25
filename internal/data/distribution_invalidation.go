package data

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"drama-server/internal/data/redisstore"
)

// invalidatingDistributionStore 在授权投影提交后使所有应用目录缓存失效。
type invalidatingDistributionStore struct {
	operations.DistributionRepository
	cache *redisstore.CatalogCache
}

func (s *invalidatingDistributionStore) invalidate(ctx context.Context) {
	(&invalidatingCatalogStore{cache: s.cache}).invalidate(ctx)
}

// EnsureDistribution 同步自动分发意图并失效缓存。
func (s *invalidatingDistributionStore) EnsureDistribution(ctx context.Context, source operations.DistributionSource, app application.Application) (bool, error) {
	changed, err := s.DistributionRepository.EnsureDistribution(ctx, source, app)
	if err == nil && changed {
		s.invalidate(ctx)
	}
	return changed, err
}

// ApplyDistribution 同步确认授权结果后失效消费者缓存，失败撤权同样使本地访问策略生效。
func (s *invalidatingDistributionStore) ApplyDistribution(ctx context.Context, id, appID string, enabled, policy bool, call operations.DistributionCall) error {
	repo, ok := s.DistributionRepository.(operations.SynchronousDistributionRepository)
	if !ok {
		return operations.ErrPublicationUnavailable
	}
	err := repo.ApplyDistribution(ctx, id, appID, enabled, policy, call)
	s.invalidate(ctx)
	return err
}
