package visitor

import (
	"context"
	"log/slog"
)

// PublicCampaignTarget 复用旧版投放归因规则确认公开目标，不创建 Visitor 或签发身份凭据。
func (s *Service) PublicCampaignTarget(ctx context.Context, tiktokAppID, code string) (Application, string, string, error) {
	app, err := s.PublicApplication(ctx, tiktokAppID)
	if err != nil {
		slog.ErrorContext(ctx, "load campaign preparation application failed", "error", err)
		return Application{}, "", "", err
	}
	attribution, err := s.resolveCampaign(ctx, app.ID, map[string]string{"campaignCode": code})
	if err != nil {
		slog.ErrorContext(ctx, "load campaign preparation target failed", "error", err)
		return Application{}, "", "", err
	}
	if attribution == nil {
		return app, "", "", nil
	}
	return app, attribution.DramaID, attribution.EpisodeID, nil
}

// PublicApplication 解析当前可用应用，供公开目录建立应用范围；不换码、创建访客或签发凭据。
// tiktokAppID 由入口规范化并校验；应用停用或依赖失败时沿用解析器错误。
func (s *Service) PublicApplication(ctx context.Context, tiktokAppID string) (Application, error) {
	app, _, err := s.resolveApplication(ctx, tiktokAppID)
	if err != nil {
		slog.ErrorContext(ctx, "resolve public catalog application failed", "error", err)
		return Application{}, err
	}
	return app, nil
}
