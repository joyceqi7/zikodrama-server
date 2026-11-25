package operations

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
)

// CheckContentReady 校验持久化内容的就绪条件，供事务和测试仓储一致使用；不依赖 TK 审核或应用配置。
func CheckContentReady(d consumer.Drama) error {
	if strings.TrimSpace(d.Title) == "" || strings.TrimSpace(d.Description) == "" || d.CoverURL == "" || len(d.Episodes) == 0 {
		return ErrPublicationNotReady
	}
	for i, e := range d.Episodes {
		if e.Number != i+1 || e.ID == "" || e.VID == "" || e.MediaStatus != "ready" {
			return ErrPublicationNotReady
		}
	}
	return nil
}

// PrepareDrama 将当前草稿确认就绪，不生成版本或改变渠道任务。
func (s *DramaService) PrepareDrama(ctx context.Context, id string) (*consumer.Drama, error) {
	d, err := s.repositories.Content.Catalog.PrepareDrama(ctx, id, PublicationActor(ctx))
	if err != nil {
		slog.Log(ctx, publicationErrorLevel(err), "prepare drama content failed", "error", err)
		return nil, err
	}
	projectOperationsDrama(d)
	return d, nil
}

// PrepareDramas 将整批内容确认就绪；任一内容未满足条件时不改变整批状态，不调用渠道发布。
func (s *DramaService) PrepareDramas(ctx context.Context, ids []string) error {
	err := s.repositories.Content.Catalog.PrepareDramas(ctx, ids, PublicationActor(ctx))
	if err != nil {
		slog.Log(ctx, publicationErrorLevel(err), "prepare drama contents failed", "error", err)
	}
	return err
}

func publicationErrorLevel(err error) slog.Level {
	if errors.Is(err, ErrPublicationNotReady) || errors.Is(err, ErrConflict) || errors.Is(err, consumer.ErrNotFound) || errors.Is(err, homepage.ErrDramaRecommended) {
		return slog.LevelWarn
	}
	return slog.LevelError
}
