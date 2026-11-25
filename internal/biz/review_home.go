package biz

import (
	"context"
	"log/slog"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
)

// ReviewHomeResult 是独立审核首页合同，未命中时不返回普通首页内容。
type ReviewHomeResult struct {
	// TikTokAppID 供客户端校验响应归属。
	TikTokAppID string `json:"tiktokAppId"`
	// Enabled 仅在应用开关开启且包版本精确命中时为真。
	Enabled bool `json:"enabled"`
	// Revision 对应当前主体渠道的审核配置，避免混用旧清单。
	Revision int64 `json:"revision"`
	// Dramas 按审核列表顺序返回当前应用实际可见的发布摘要。
	Dramas []domain.DramaSummary `json:"dramas"`
}

// ReviewHome 使用独立接口读取审核列表，不改变原首页、分类或短剧目录合同。
func (uc *ConsumerBootstrap) ReviewHome(ctx context.Context, tiktokAppID string) (ReviewHomeResult, error) {
	result := ReviewHomeResult{TikTokAppID: tiktokAppID, Dramas: []domain.DramaSummary{}}
	app, err := uc.visitors.PublicApplication(ctx, tiktokAppID)
	if err != nil {
		slog.ErrorContext(ctx, "resolve review homepage application failed", "error", err)
		return result, err
	}
	ctx = application.WithMiniReview(application.WithSubject(application.WithPlayback(application.WithTimeZone(ctx, app.ID, app.TimeZone), app.PlaybackProvider), app.SubjectID), app.MiniReview, app.MonetizationMode)
	state := application.MiniReviewFromContext(ctx)
	result.Enabled, result.Revision = state.Enabled, state.Revision
	if !state.Enabled {
		return result, nil
	}
	result.Dramas, err = uc.consumer.ReviewHome(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "load review homepage content failed", "error", err)
	}
	return result, err
}
