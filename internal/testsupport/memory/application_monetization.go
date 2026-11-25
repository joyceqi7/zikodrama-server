package memory

import (
	"context"
	"strings"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
)

// 查询并返回指定的应用商业化配置。
func (s *Store) GetApplicationMonetization(ctx context.Context) (monetization.ApplicationMonetizationConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if binding, ok := s.strategyBindings[application.IDFromContext(ctx)]; ok {
		if binding.StrategyID == "" {
			return monetization.ApplicationMonetizationConfig{}, domain.ErrNotFound
		}
		strategy := cloneStrategy(s.unlockStrategies[binding.StrategyID])
		return monetization.ConfigFromStrategy(strategy, binding.WithApplicationAdUnits(strategy.Document, s.applications[binding.ApplicationID].AdUnits)), nil
	}
	config, exists := s.applicationMonetization[recordApplicationID(application.IDFromContext(ctx))]
	if !exists {
		return monetization.ApplicationMonetizationConfig{}, domain.ErrNotFound
	}
	return cloneApplicationMonetization(config), nil
}

// 校验并保存应用商业化配置。
func (s *Store) SaveApplicationMonetization(ctx context.Context, config monetization.ApplicationMonetizationConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	applicationID := recordApplicationID(application.IDFromContext(ctx))
	if config.ApplicationID == "" {
		config.ApplicationID = applicationID
	}
	if strings.TrimSpace(config.ApplicationID) != applicationID {
		return domain.ErrNotFound
	}
	doc, _ := monetization.StrategyFromConfig(config)
	app := s.applications[applicationID]
	if app.MiniReview.AnyEnabled() && app.MonetizationMode == application.MonetizationModeIAA && !monetization.HasEnabledEpisodeRule(doc) {
		return operations.ErrInvalidInput
	}
	if err := monetization.ValidateApplicationMode(s.applications[applicationID].MonetizationMode, doc); err != nil {
		return err
	}
	s.applicationMonetization[applicationID] = cloneApplicationMonetization(config)
	return nil
}

// 执行应用商业化配置对应的业务处理。
func cloneApplicationMonetization(config monetization.ApplicationMonetizationConfig) monetization.ApplicationMonetizationConfig {
	copy := config
	return copy
}
