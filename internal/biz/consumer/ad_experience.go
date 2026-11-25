package consumer

import (
	"context"
	"log/slog"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/pkg/requesttime"
)

// AdExperience 是当前已认证观众的只读广告规则与权益，不包含登录凭据或启动归因。
type AdExperience struct {
	// InterstitialEnabled 仅供启动编排控制插屏广告位下发，不新增 Mini 接口字段。
	InterstitialEnabled bool `json:"-"`
	// Monetization 是当前应用公开的变现规则。
	Monetization monetization.ConsumerMonetization `json:"monetization"`
	// VIPState 是服务器接收本次请求所在自然日的进度和实际权益。
	VIPState monetization.VIPProgress `json:"vipState"`
	// VIPOffer 是当前可用的广告入口；不因查询而累计次数或领取 VIP。
	VIPOffer *monetization.AccessOffer `json:"vipOffer"`
}

// ReadAdExperience 在现有登录态下查询权益；启动与前台恢复共享相同规则。
func (s *Service) ReadAdExperience(ctx context.Context, visitorID string) (AdExperience, error) {
	ctx = requesttime.Ensure(ctx, s.now)
	policy, err := s.ResolveMonetizationPolicy(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "resolve ad experience policy failed", "error", err)
		return AdExperience{}, err
	}
	period := monetization.RewardPeriodFor(requesttime.At(ctx, s.now), application.LocationFromContext(ctx))
	state, err := s.store.FindVIPProgress(ctx, visitorID, policy, period, requesttime.At(ctx, s.now))
	if err != nil {
		slog.ErrorContext(ctx, "read ad experience vip progress failed", "error", err)
		return AdExperience{}, rewardDependencyError(err)
	}
	result := AdExperience{InterstitialEnabled: policy.AllowsInterstitial(), Monetization: monetization.ConsumerMonetizationFromPolicy(policy), VIPState: state}
	if policy.Enabled() && policy.VIP.Enabled && !state.Active {
		result.VIPOffer, err = s.RewardOffer(ctx, visitorID, policy, policy.VIPOfferKind(false), &state)
		if err != nil {
			slog.ErrorContext(ctx, "read ad experience offer failed", "error", err)
			return AdExperience{}, err
		}
	}
	return result, nil
}
