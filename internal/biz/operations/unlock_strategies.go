package operations

import (
	"context"
	"errors"
	"log/slog"

	content "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
)

// logStrategySaveError 区分预期的配置或版本拒绝与存储故障，保留实际错误详情。
func logStrategySaveError(ctx context.Context, operation string, err error) {
	level := slog.LevelError
	if errors.Is(err, monetization.ErrApplicationModeConflict) || errors.Is(err, ErrConflict) || errors.Is(err, ErrInvalidInput) || errors.Is(err, content.ErrNotFound) {
		level = slog.LevelWarn
	}
	slog.Log(ctx, level, operation, "error", err)
}

// ApplicationStrategy 返回当前应用的一致策略快照，未配置时保留应用修订供首次保存。
func (s *MonetizationService) ApplicationStrategy(ctx context.Context) (monetization.ApplicationStrategy, error) {
	item, err := s.settings.GetApplicationStrategy(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "get application strategy failed", "error", err)
	}
	return item, err
}

// SaveApplicationStrategy 原子保存当前应用的配置，平台商品映射不接受广告位覆盖。
func (s *MonetizationService) SaveApplicationStrategy(ctx context.Context, input monetization.ApplicationStrategyInput) (monetization.ApplicationStrategy, error) {
	input.Rules = monetization.ProductBindings(input.Rules)
	item, err := s.settings.SaveApplicationStrategy(ctx, input)
	if err != nil {
		logStrategySaveError(ctx, "save application strategy failed", err)
	}
	return item, err
}
