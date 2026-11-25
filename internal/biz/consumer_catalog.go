package biz

import (
	"context"
	"log/slog"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
)

// PublicCatalogResult 只提供已发布目录摘要，不包含用户权益、广告 Offer 或播放参数。
type PublicCatalogResult struct {
	// TikTokAppID 用于H5 校验返回目录是否属于当前官方应用。
	TikTokAppID string `json:"tiktokAppId"`
	// Dramas 仅含当前应用获准展示的线上版本，空目录序列化为数组。
	Dramas []domain.DramaSummary `json:"dramas"`
	// Tags 是当前应用已发布目录的分类，与登录后的发现页采用相同筛选范围。
	Tags []string `json:"tags"`
	// NextCursor 指向下一页，不包含登录或授权信息。
	NextCursor string `json:"nextCursor,omitempty"`
}

// PublicCatalog 在静默登录尚未完成时提供目录；复用正式目录的应用授权和发布过滤。
func (uc *ConsumerBootstrap) PublicCatalog(ctx context.Context, tiktokAppID string, input domain.DiscoveryQuery) (PublicCatalogResult, error) {
	app, err := uc.visitors.PublicApplication(ctx, tiktokAppID)
	if err != nil {
		slog.ErrorContext(ctx, "load public catalog application failed", "error", err)
		return PublicCatalogResult{}, err
	}
	ctx = application.WithSubject(ctx, app.SubjectID)
	ctx = application.WithMiniReview(ctx, app.MiniReview, app.MonetizationMode)
	page, err := uc.consumer.Discover(application.WithPlayback(application.WithTimeZone(ctx, app.ID, app.TimeZone), app.PlaybackProvider), input)
	if err != nil {
		slog.ErrorContext(ctx, "list public catalog summaries failed", "error", err)
		return PublicCatalogResult{}, err
	}
	return PublicCatalogResult{TikTokAppID: app.TikTokAppID, Dramas: page.Items, Tags: page.Tags, NextCursor: page.NextCursor}, nil
}
