package homepage

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"log/slog"
)

// PublicationMetadata 仅用于管理列表，不进入消费者卡片或播放合同。
type PublicationMetadata = consumer.PublicationMetadata

// MetadataReader 为运营条目提供版本与应用数，旧消费者查询不调用该端口。
type MetadataReader interface {
	// HomepageMetadata 批量读取线上元数据，不加载分集或凭据。
	HomepageMetadata(context.Context, []string) (map[string]PublicationMetadata, error)
}

func (s *Service) candidateMetadata(ctx context.Context, page consumer.DiscoveryPage) (consumer.DiscoveryPage, error) {
	ids := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	m, err := s.metadata(ctx, ids)
	page.Metadata = m
	return page, err
}

func (s *Service) metadata(ctx context.Context, ids []string) (map[string]PublicationMetadata, error) {
	if !IsManagementContext(ctx) || !application.IsDirect(ctx) {
		return nil, nil
	}
	reader, ok := s.catalog.(MetadataReader)
	if !ok {
		return nil, nil
	}
	m, err := reader.HomepageMetadata(ctx, ids)
	if err != nil {
		slog.ErrorContext(ctx, "read homepage publication metadata failed", "error", err)
	}
	return m, err
}
