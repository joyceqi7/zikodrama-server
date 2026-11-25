package consumer

import (
	"context"
	"log/slog"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/pkg/requesttime"
)

// ReadVIPState 仅查询当前服务器自然日进度和已生效权益，不创建广告入口或自动领取。
func (s *Service) ReadVIPState(ctx context.Context, visitorID string) (monetization.VIPProgress, error) {
	ctx = requesttime.Ensure(ctx, s.now)
	policy, err := s.ResolveMonetizationPolicy(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "resolve vip state policy failed", "error", err)
		return monetization.VIPProgress{}, err
	}
	period := monetization.RewardPeriodFor(requesttime.At(ctx, s.now), application.LocationFromContext(ctx))
	state, err := s.store.FindVIPProgress(ctx, visitorID, policy, period, requesttime.At(ctx, s.now))
	if err != nil {
		slog.ErrorContext(ctx, "read vip state failed", "error", err)
		return monetization.VIPProgress{}, rewardDependencyError(err)
	}
	return state, nil
}

// ReadVIPOffer 在用户需要观看时查询当前可用入口，入口故障不影响独立的 VIP 状态查询。
func (s *Service) ReadVIPOffer(ctx context.Context, visitorID string) (*monetization.AccessOffer, error) {
	ctx = requesttime.Ensure(ctx, s.now)
	policy, err := s.ResolveMonetizationPolicy(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "resolve vip offer policy failed", "error", err)
		return nil, err
	}
	if !policy.Enabled() || !policy.VIP.Enabled {
		return nil, nil
	}
	period := monetization.RewardPeriodFor(requesttime.At(ctx, s.now), application.LocationFromContext(ctx))
	state, err := s.store.FindVIPProgress(ctx, visitorID, policy, period, requesttime.At(ctx, s.now))
	if err != nil {
		slog.ErrorContext(ctx, "read vip offer eligibility failed", "error", err)
		return nil, rewardDependencyError(err)
	}
	if state.Active || (state.RequiredCount > 0 && state.AdCount >= state.RequiredCount) {
		return nil, nil
	}
	offer, err := s.RewardOffer(ctx, visitorID, policy, policy.VIPOfferKind(false), &state)
	if err != nil {
		slog.ErrorContext(ctx, "read vip offer failed", "error", err)
		return nil, err
	}
	return offer, nil
}
