package data

import (
	"context"
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/biz/mediaconfig"
	"log/slog"
)

// NewGlobalConfigService 装配全局配置持久化与管理用例。
func NewGlobalConfigService(resources *Resources) *globalconfig.Service {
	return globalconfig.NewService(resources.store.GlobalConfigs())
}

// NewStoredMediaConfigService 从 MySQL 主库读取 media；未配置时关闭媒资能力，允许管理员进入后台完成配置。
func NewStoredMediaConfigService(ctx context.Context, global *globalconfig.Service) (*mediaconfig.Service, error) {
	_, err := global.Media(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "load stored media configuration failed", "error", err)
		return nil, err
	}
	return mediaconfig.NewDynamicService(global.Media), nil
}
