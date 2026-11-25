package operations

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"drama-server/internal/biz/application"
	content "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
)

// ApplicationMonetization 返回所选应用的直接 IAA 配置。
// 调用链为 HTTP X-Application-ID -> domain context -> repository。应用身份绝不作为
// 可变请求数据接收。返回值通过 applicationMonetizationView 收口成运营合同。
func (s *MonetizationService) ApplicationMonetization(ctx context.Context) (*ApplicationMonetizationView, error) {
	if s == nil || s.settings == nil {
		slog.WarnContext(ctx, "application monetization rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	config, err := s.settings.GetApplicationMonetization(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "get application monetization failed", "error", err)
		return nil, err
	}
	return applicationMonetizationView(config), nil
}

// UpdateApplicationMonetization 为所选应用写入新的 IAA 规则内部版本。
// 此入口适配旧广告合同，底层统一写入策略 JSON；新面板通过应用策略接口管理。
//
// Revision 只用于 Offer 校验和并发配置演进，不是运营可发布的业务版本。
func (s *MonetizationService) UpdateApplicationMonetization(ctx context.Context, input ApplicationMonetizationInput) (*ApplicationMonetizationView, error) {
	if s == nil || s.registry == nil || s.settings == nil {
		slog.WarnContext(ctx, "update application monetization rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	applicationID := application.IDFromContext(ctx)
	app, err := s.registry.FindApplication(ctx, applicationID)
	if err != nil {
		slog.ErrorContext(ctx, "find application failed", "error", err)
		return nil, err
	}
	now := time.Now().UTC()
	// ApplicationMonetizationConfig 是旧广告输入的适配表示，存储层转换为规则 JSON。
	// StrongWatchAdCount 对应运营合同的 ForceWatchCount，命名差异来自历史领域模型。
	config := monetization.ApplicationMonetizationConfig{
		ApplicationID:           applicationID,
		Status:                  monetization.MonetizationEnabled,
		Strategy:                monetization.MonetizationStrategyEpisodeUnlock,
		EpisodeRewardAdUnitCode: input.EpisodeUnlockAdUnitCode,
		VIPAdUnitCode:           input.VIPStrategy.AdUnitCode,
		FreeEpisodeCount:        input.FreeEpisodeCount,
		EpisodeReward: monetization.EpisodeRewardPolicy{
			UnlockEpisodeCount: 1,
		},
		VIP: monetization.ApplicationVIPPolicy{
			Enabled:            input.VIPStrategy.Enabled,
			StrongWatchAdCount: input.VIPStrategy.ForceWatchCount,
			RequiredAdCount:    input.VIPStrategy.RequiredAdCount,
			EntryDelaySeconds:  input.VIPStrategy.DelaySeconds,
			Title:              input.VIPStrategy.Title,
			Content:            input.VIPStrategy.Content,
			FailureMessage:     input.VIPStrategy.FailureMessage,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	// 更新时保留首次创建时间；奖励领取始终读取最新配置。
	{
		current, currentErr := s.settings.GetApplicationMonetization(ctx)
		if currentErr != nil {
			slog.ErrorContext(ctx, "get application monetization failed", "error", currentErr)
		}
		if currentErr == nil {
			config.CreatedAt = current.CreatedAt
			// 兼容未传新增字段的客户端，更新其它配置不能清除已有奖励阈值。
			config.EpisodeReward = current.EpisodeReward
		} else if !errors.Is(currentErr, content.ErrNotFound) {
			return nil, currentErr
		}
	}
	if input.UnlockEpisodeCount != nil {
		config.EpisodeReward.UnlockEpisodeCount = *input.UnlockEpisodeCount
	}
	if input.DailyRewardLimit != nil {
		config.EpisodeReward.DailyRewardLimit = *input.DailyRewardLimit
	}
	// 消费设置只能引用当前应用已经登记的激励广告位编码。
	if !app.AdUnits.HasRewardedVideo(config.EpisodeAdUnit()) {
		slog.WarnContext(ctx, "update application monetization rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	if config.VIP.Enabled {
		if !app.AdUnits.HasRewardedVideo(config.VIPAdUnit()) {
			slog.WarnContext(ctx, "update application monetization rejected", "error", ErrConflict)
			return nil, ErrConflict
		}
	}
	if err := s.settings.SaveApplicationMonetization(ctx, config); err != nil {
		slog.ErrorContext(ctx, "save application monetization failed", "error", err)
		return nil, err
	}
	return applicationMonetizationView(config), nil
}

// applicationMonetizationView 将持久化模型收口为当前运营 API 合同。
func applicationMonetizationView(config monetization.ApplicationMonetizationConfig) *ApplicationMonetizationView {
	return &ApplicationMonetizationView{
		ApplicationID:           config.ApplicationID,
		FreeEpisodeCount:        config.FreeEpisodeCount,
		UnlockEpisodeCount:      config.EpisodeReward.UnlockEpisodeCount,
		DailyRewardLimit:        config.EpisodeReward.DailyRewardLimit,
		EpisodeUnlockAdUnitCode: config.EpisodeAdUnit(),
		VIPStrategy: ApplicationVIPStrategyInput{
			Enabled:         config.VIP.Enabled,
			ForceWatchCount: config.VIP.StrongWatchAdCount,
			RequiredAdCount: config.VIP.RequiredAdCount,
			DelaySeconds:    config.VIP.EntryDelaySeconds,
			AdUnitCode:      config.VIPAdUnit(),
			Title:           config.VIP.Title,
			Content:         config.VIP.Content,
			FailureMessage:  config.VIP.FailureMessage,
		},
		CreatedAt: config.CreatedAt,
		UpdatedAt: config.UpdatedAt,
	}
}
