package data

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/subject"
	"errors"
	"log/slog"
)

// subjectOwner 从选定主体读取发布身份，VOD 账号与共享空间统一读取全局媒资配置。
// 此解析只供官方发布使用，不参与已有直连服务的启动或播放门禁。
func subjectOwner(resources *Resources) func(context.Context, string) (application.Application, error) {
	service := subject.NewService(resources.store)
	return func(ctx context.Context, id string) (application.Application, error) {
		owner, err := service.Owner(ctx, id)
		if err != nil {
			if errors.Is(err, subject.ErrNotConfigured) || errors.Is(err, subject.ErrNotFound) {
				slog.WarnContext(ctx, "load subject owner rejected", "error", err)
			} else {
				slog.ErrorContext(ctx, "load subject owner failed", "error", err)
			}
			return owner, err
		}
		media, err := NewGlobalConfigService(resources).Media(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "load publisher VOD configuration failed", "error", err)
			return owner, err
		}
		if media == nil || media.BytePlusAccountID == "" || media.SpaceName == "" {
			slog.WarnContext(ctx, "publisher VOD account unavailable", "error", subject.ErrNotConfigured)
			return owner, subject.ErrNotConfigured
		}
		owner.BytePlusAccountID, owner.BytePlusSpaceName = media.BytePlusAccountID, media.SpaceName
		return owner, nil
	}
}
