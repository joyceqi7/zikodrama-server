package consumer

import (
	"context"
	"log/slog"

	"drama-server/internal/biz/application"
)

// ReviewHome 只读取已命中审核版本的共享列表，不复用普通首页栏目或目录结果。
func (s *Service) ReviewHome(ctx context.Context) ([]DramaSummary, error) {
	review := application.MiniReviewFromContext(ctx)
	result := []DramaSummary{}
	if !review.Enabled || len(review.DramaIDs) == 0 {
		return result, nil
	}
	byID := map[string]DramaSummary{}
	// 清单最多一百项，沿用发布目录的应用授权过滤，每次最多读取五十条摘要。
	for offset := 0; offset < len(review.DramaIDs); offset += 50 {
		page, err := s.store.DiscoverDramas(ctx, DiscoveryQuery{DramaIDs: review.DramaIDs, ReviewRevision: review.Revision, Offset: offset, Limit: 50})
		if err != nil {
			slog.ErrorContext(ctx, "read review homepage published summaries failed", "error", err)
			return nil, err
		}
		for _, drama := range page.Items {
			byID[drama.ID] = drama
		}
		if len(page.Items) < 50 {
			break
		}
	}
	for _, id := range review.DramaIDs {
		if drama, ok := byID[id]; ok {
			result = append(result, drama)
		}
	}
	return result, nil
}
