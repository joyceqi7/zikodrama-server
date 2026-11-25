package memory

import (
	"context"
	"encoding/json"
	"time"

	"drama-server/internal/biz/application"
	content "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
)

func cloneStrategy[T any](v T) T {
	raw, _ := json.Marshal(v)
	var copy T
	_ = json.Unmarshal(raw, &copy)
	return copy
}

func (s *Store) validateStrategyBinding(doc monetization.StrategyDocument, b monetization.StrategyBinding) error {
	app := s.applications[b.ApplicationID]
	if err := monetization.ValidateApplicationMode(app.MonetizationMode, doc); err != nil {
		return err
	}
	if monetization.RequiresAdUnits(doc) && !app.AdUnits.Complete() {
		return operations.ErrApplicationAdUnitsIncomplete
	}
	return nil
}

// GetApplicationStrategy 返回应用当前策略和商品映射的一致副本。
func (s *Store) GetApplicationStrategy(ctx context.Context) (monetization.ApplicationStrategy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id := application.IDFromContext(ctx)
	if _, ok := s.applications[id]; !ok {
		return monetization.ApplicationStrategy{}, content.ErrNotFound
	}
	b := s.strategyBindings[id]
	result := monetization.ApplicationStrategy{ApplicationID: id, MonetizationMode: s.applications[id].MonetizationMode, Rules: monetization.ProductBindings(b.Rules)}
	if b.StrategyID != "" {
		item, ok := s.unlockStrategies[b.StrategyID]
		if !ok {
			return result, content.ErrNotFound
		}
		item = cloneStrategy(item)
		result.Strategy = &item
	}
	return cloneStrategy(result), nil
}

// SaveApplicationStrategy 在同一把锁内按当前最新配置保存应用独立策略，修订号由服务端递增。
func (s *Store) SaveApplicationStrategy(ctx context.Context, input monetization.ApplicationStrategyInput) (monetization.ApplicationStrategy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := application.IDFromContext(ctx)
	if _, ok := s.applications[id]; !ok {
		return monetization.ApplicationStrategy{}, content.ErrNotFound
	}
	app := s.applications[id]
	if app.MiniReview.AnyEnabled() && app.MonetizationMode == application.MonetizationModeIAA && !monetization.HasEnabledEpisodeRule(input.Document) {
		return monetization.ApplicationStrategy{}, operations.ErrInvalidInput
	}
	b := s.strategyBindings[id]
	if err := s.validateStrategyBinding(input.Document, monetization.StrategyBinding{ApplicationID: id}); err != nil {
		return monetization.ApplicationStrategy{}, err
	}
	if s.applicationStrategyIDs == nil {
		s.applicationStrategyIDs = map[string]string{}
	}
	if b.StrategyID != "" && s.applicationStrategyIDs[id] != b.StrategyID {
		return monetization.ApplicationStrategy{}, operations.ErrConflict
	}
	item, exists := s.unlockStrategies[b.StrategyID]
	now := time.Now().UTC()
	if !exists {
		item = monetization.UnlockStrategy{ID: s.allocateIDLocked(), CreatedAt: now}
	}
	item.Document, item.UpdatedAt = cloneStrategy(input.Document), now
	item.Revision++
	s.applicationStrategyIDs[id] = item.ID
	s.unlockStrategies[item.ID] = cloneStrategy(item)
	b = monetization.StrategyBinding{ApplicationID: id, StrategyID: item.ID, Rules: monetization.ProductBindings(input.Rules)}
	s.strategyBindings[id] = cloneStrategy(b)
	return cloneStrategy(monetization.ApplicationStrategy{ApplicationID: id, MonetizationMode: s.applications[id].MonetizationMode, Strategy: &item, Rules: b.Rules}), nil
}
