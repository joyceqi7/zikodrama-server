package operations

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/campaign"
)

// EnsureDramaCampaign 返回当前应用指向线上第一集的启用链接，存在时复用，否则生成并持久化。
// 不复活已停用的默认活动，也不改写历史活动目标；并发创建由应用内编码唯一键约束，冲突后读取胜出记录。
func (s *CampaignService) EnsureDramaCampaign(ctx context.Context, dramaID string) (*campaign.Campaign, error) {
	if s == nil || s.campaigns == nil || s.registry == nil || s.catalog == nil {
		slog.WarnContext(ctx, "ensure drama campaign rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	app, err := s.registry.FindApplication(ctx, application.IDFromContext(ctx))
	if err != nil {
		slog.ErrorContext(ctx, "find campaign application failed", "error", err)
		return nil, err
	}
	if app.Status != application.StatusActive {
		slog.WarnContext(ctx, "campaign application inactive", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	drama, err := s.catalog.FindDrama(application.ContextForApplication(ctx, app), dramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find campaign drama failed", "error", err)
		return nil, err
	}
	published, visible := drama.PublishedForApplication(app.ID)
	episodeID := ""
	if visible {
		for _, ep := range published.Episodes {
			if ep.Number == 1 {
				episodeID = ep.ID
				break
			}
		}
	}
	if episodeID == "" {
		slog.WarnContext(ctx, "campaign first episode unavailable", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	items, err := s.campaigns.ListCampaigns(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list reusable campaigns failed", "error", err)
		return nil, err
	}
	if existing := reusableDramaCampaign(items, dramaID, episodeID); existing != nil {
		return existing, nil
	}
	// 编码由业务标识构造，保证不同进程同时生成时也不能新增多条默认记录。
	code := "drama_" + dramaID + "_episode_" + episodeID
	for _, item := range items {
		if item.Code == code {
			slog.WarnContext(ctx, "default campaign unavailable", "error", ErrConflict)
			return nil, ErrConflict
		}
	}
	title := []rune(published.Title)
	if len(title) > 110 {
		title = title[:110]
	}
	now := time.Now().UTC()
	item := campaign.Campaign{ApplicationID: app.ID, Code: code, Name: string(title) + " · 第 1 集", DramaID: dramaID, EpisodeID: episodeID, Status: campaign.StatusActive, CreatedAt: now, UpdatedAt: now}
	if err := s.generateCampaignLandingURL(ctx, &item, false); err != nil {
		slog.ErrorContext(ctx, "generate default campaign link failed", "error", err)
		return nil, err
	}
	if err := s.campaigns.SaveCampaign(ctx, &item); err != nil {
		if !errors.Is(err, ErrConflict) {
			slog.ErrorContext(ctx, "save default campaign failed", "error", err)
			return nil, err
		}
		slog.WarnContext(ctx, "default campaign concurrently created", "error", err)
		items, readErr := s.campaigns.ListCampaigns(ctx)
		if readErr != nil {
			slog.ErrorContext(ctx, "read concurrent campaign failed", "error", readErr)
			return nil, readErr
		}
		if existing := reusableDramaCampaign(items, dramaID, episodeID); existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return &item, nil
}

// reusableDramaCampaign 仅复用同一剧集的有效官方链接，不把其他集数或停用活动当作默认入口。
func reusableDramaCampaign(items []campaign.Campaign, dramaID, episodeID string) *campaign.Campaign {
	for _, item := range items {
		if item.DramaID == dramaID && item.EpisodeID == episodeID && item.Status == campaign.StatusActive && strings.TrimSpace(item.LandingURL) != "" {
			return &item
		}
	}
	return nil
}
