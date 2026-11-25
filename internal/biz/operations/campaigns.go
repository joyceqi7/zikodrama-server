package operations

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/campaign"
	content "drama-server/internal/biz/consumer"
	"drama-server/internal/pkg/requestmeta"
)

// Campaigns 返回当前应用的投放活动。
func (s *CampaignService) Campaigns(ctx context.Context) ([]campaign.Campaign, error) {
	if s == nil || s.campaigns == nil || s.registry == nil {
		slog.WarnContext(ctx, "campaigns rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	items, err := s.campaigns.ListCampaigns(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list campaigns failed", "error", err)
		return nil, err
	}
	return items, nil
}

// CreateCampaign 接收入口已校验的配置，为当前应用生成稳定活动编码和官方 Minis Link 后保存。
// 链接生成失败时不保存活动；空 Code 由服务端生成，应用身份取自认证上下文。
func (s *CampaignService) CreateCampaign(ctx context.Context, input CampaignInput) (*campaign.Campaign, error) {
	if s == nil || s.campaigns == nil || s.registry == nil || s.catalog == nil {
		slog.WarnContext(ctx, "create campaign rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	if input.Code == "" {
		code, err := secureID("launch")
		if err != nil {
			slog.ErrorContext(ctx, "secure id failed", "error", err)
			return nil, err
		}
		input.Code = code
	}
	campaign, err := s.campaignFromInput(ctx, input, true)
	if err != nil {
		slog.ErrorContext(ctx, "campaign from input failed", "error", err)
		return nil, err
	}
	campaign.ApplicationID = application.IDFromContext(ctx)
	now := time.Now().UTC()
	campaign.CreatedAt, campaign.UpdatedAt = now, now
	if err := s.generateCampaignLandingURL(ctx, &campaign, false); err != nil {
		slog.ErrorContext(ctx, "generate campaign landing url failed", "error", err)
		return nil, err
	}
	if err := s.campaigns.SaveCampaign(ctx, &campaign); err != nil {
		slog.ErrorContext(ctx, "save campaign failed", "error", err)
		return nil, err
	}
	return &campaign, nil
}

// UpdateCampaign 更新入口已校验的活动配置；编码、目标短剧和分集保持不变，并沿用创建时的官方链接。
// 停用允许跳过目标线上可见性检查，使已下架内容的旧活动仍可关闭。
func (s *CampaignService) UpdateCampaign(ctx context.Context, id string, input CampaignInput) (*campaign.Campaign, error) {
	if s == nil || s.campaigns == nil || s.registry == nil || s.catalog == nil {
		slog.WarnContext(ctx, "update campaign rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	existing, err := s.campaigns.FindCampaign(ctx, strings.TrimSpace(id))
	if err != nil {
		slog.ErrorContext(ctx, "find campaign failed", "error", err)
		return nil, err
	}
	if input.Code == "" {
		input.Code = existing.Code
	}
	campaign, err := s.campaignFromInput(ctx, input, input.Status != campaign.StatusDisabled)
	if err != nil {
		slog.ErrorContext(ctx, "campaign from input failed", "error", err)
		return nil, err
	}
	if campaign.Code != existing.Code || campaign.DramaID != existing.DramaID || campaign.EpisodeID != existing.EpisodeID {
		slog.WarnContext(ctx, "update campaign rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	campaign.ID = existing.ID
	campaign.ApplicationID = existing.ApplicationID
	campaign.CreatedAt = existing.CreatedAt
	campaign.LandingURL, campaign.MinisPath = existing.LandingURL, existing.MinisPath
	campaign.UpdatedAt = time.Now().UTC()
	if err := s.campaigns.SaveCampaign(ctx, &campaign); err != nil {
		slog.ErrorContext(ctx, "save campaign failed", "error", err)
		return nil, err
	}
	return &campaign, nil
}

// campaignFromInput 将入口已校验的活动字段映射为候选，并检查应用状态和目标播放授权。
func (s *CampaignService) campaignFromInput(ctx context.Context, input CampaignInput, validateTarget bool) (campaign.Campaign, error) {
	candidate := campaign.Campaign{
		Code: input.Code, Name: input.Name,
		ExternalCampaignID: input.ExternalCampaignID, ExternalAdGroupID: input.ExternalAdGroupID,
		ExternalAdID: input.ExternalAdID, ExternalCreativeID: input.ExternalCreativeID,
		DramaID: input.DramaID, EpisodeID: input.EpisodeID,
		ExperimentKey: input.ExperimentKey, Status: input.Status,
	}
	app, err := s.registry.FindApplication(ctx, application.IDFromContext(ctx))
	if err != nil {
		slog.ErrorContext(ctx, "find application failed", "error", err)
		return campaign.Campaign{}, err
	}
	if candidate.Status == campaign.StatusActive && app.Status != application.StatusActive {
		slog.WarnContext(ctx, "campaign from input rejected", "error", ErrInvalidInput)
		return campaign.Campaign{}, ErrInvalidInput
	}
	if !validateTarget {
		return candidate, nil
	}
	drama, err := s.catalog.FindDrama(application.ContextForApplication(ctx, app), candidate.DramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find drama failed", "error", err)
		if errors.Is(err, content.ErrNotFound) {
			slog.WarnContext(ctx, "campaign from input rejected", "error", ErrInvalidInput)
			return campaign.Campaign{}, ErrInvalidInput
		}
		return campaign.Campaign{}, err
	}
	published, visible := drama.PublishedForApplication(app.ID)
	if !visible {
		slog.WarnContext(ctx, "campaign from input rejected", "error", ErrInvalidInput)
		return campaign.Campaign{}, ErrInvalidInput
	}
	for _, episode := range published.Episodes {
		if episode.ID == candidate.EpisodeID {
			return candidate, nil
		}
	}
	slog.WarnContext(ctx, "campaign from input rejected", "error", ErrInvalidInput)
	return campaign.Campaign{}, ErrInvalidInput
}

// generateCampaignLandingURL 在创建时调用官方 Link API 并保存原始结果；读取和改名都不重新生成。
func (s *CampaignService) generateCampaignLandingURL(ctx context.Context, item *campaign.Campaign, requireCover bool) error {
	if item == nil {
		slog.WarnContext(ctx, "generate campaign landing url rejected", "error", ErrInvalidInput)
		return ErrInvalidInput
	}
	app, err := s.registry.FindApplication(ctx, application.IDFromContext(ctx))
	if err != nil {
		slog.ErrorContext(ctx, "find application failed", "error", err)
		return err
	}
	if s.providerFactory == nil {
		slog.WarnContext(ctx, "generate campaign landing url rejected", "error", ErrCampaignLinkUnavailable)
		return ErrCampaignLinkUnavailable
	}
	provider := s.providerFactory(app)
	if provider == nil {
		slog.WarnContext(ctx, "generate campaign landing url rejected", "error", ErrCampaignLinkUnavailable)
		return ErrCampaignLinkUnavailable
	}
	drama, err := s.catalog.FindDrama(application.ContextForApplication(ctx, app), item.DramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find drama failed", "error", err)
		return err
	}
	published, visible := drama.PublishedForApplication(app.ID)
	if !visible {
		slog.WarnContext(ctx, "generate campaign landing url rejected", "error", ErrInvalidInput)
		return ErrInvalidInput
	}
	minisPath, hasCover := campaignMinisPath(*item, published, app.Normalized().PlaybackProvider == "tiktok_minis")
	if requireCover && (!hasCover || app.Status != application.StatusActive) {
		slog.WarnContext(ctx, "campaign cover preview unavailable", "error", ErrInvalidInput)
		return ErrInvalidInput
	}
	item.MinisPath = minisPath
	fields := append(requestmeta.LogFields(ctx), "application_id", app.ID, "tiktok_app_id", app.TikTokAppID, "campaign_id", item.ID, "drama_id", item.DramaID)
	link, err := provider.GenerateMinisLink(ctx, MinisLinkInput{Path: item.MinisPath, Title: published.Title, ImageURL: published.CoverURL})
	if err != nil {
		slog.ErrorContext(ctx, "generate minis link failed", append(fields, "error", err)...)
	}
	if err != nil || strings.TrimSpace(link) == "" {
		slog.WarnContext(ctx, "generate campaign landing url rejected", "error", ErrCampaignLinkUnavailable)
		return ErrCampaignLinkUnavailable
	}
	item.LandingURL = link
	slog.InfoContext(ctx, "official Minis link generated", fields...)
	return nil
}
