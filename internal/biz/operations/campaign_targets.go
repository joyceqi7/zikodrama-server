package operations

import (
	"context"
	"drama-server/internal/biz/application"
	"log/slog"
)

// CampaignTarget 是当前应用可用于官方落地链接的线上短剧摘要。
type CampaignTarget struct {
	// ID 是内部短剧标识。
	ID string `json:"id"`
	// Title 来自线上版本，不随草稿编辑变化。
	Title string `json:"title"`
	// Version 是当前官方线上版本。
	Version int `json:"version"`
	// Episodes 只包含已上线的分集，不返回 VID、源文件或授权信息。
	Episodes []CampaignTargetEpisode `json:"episodes"`
}

// CampaignTargetEpisode 是投放选择器所需的最小分集信息。
type CampaignTargetEpisode struct {
	// ID 是内部集标识。
	ID string `json:"id"`
	// Number 是线上分集序号。
	Number int `json:"number"`
	// Title 是线上分集名称。
	Title string `json:"title"`
}

// CampaignTargets 独立于内容编辑权限，并按当前小程序的正式授权结果筛选。
func (s *CampaignService) CampaignTargets(ctx context.Context) ([]CampaignTarget, error) {
	appID := application.IDFromContext(ctx)
	if s == nil || s.catalog == nil || appID == "" {
		slog.WarnContext(ctx, "campaign targets rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	app, err := s.registry.FindApplication(ctx, appID)
	if err != nil {
		slog.ErrorContext(ctx, "read campaign target application failed", "application_id", appID, "error", err)
		return nil, err
	}
	ctx = application.ContextForApplication(ctx, app)
	dramas, err := s.catalog.ListAllDramas(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list all dramas failed", "error", err)
		return nil, err
	}
	items := []CampaignTarget{}
	for _, drama := range dramas {
		published, visible := drama.PublishedForApplication(appID)
		if !visible {
			continue
		}
		item := CampaignTarget{ID: published.ID, Title: published.Title, Version: published.PlatformVersion, Episodes: []CampaignTargetEpisode{}}
		for _, episode := range published.Episodes {
			item.Episodes = append(item.Episodes, CampaignTargetEpisode{ID: episode.ID, Number: episode.Number, Title: episode.Title})
		}
		items = append(items, item)
	}
	return items, nil
}
