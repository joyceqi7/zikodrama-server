package biz

import (
	"context"
	"log/slog"
	"time"

	"drama-server/internal/biz/adminrbac"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/visitor"
	config "drama-server/internal/conf"
)

// NewOperationsService 创建运营接口和后台任务共享的业务服务。
// 媒资任务通过 MySQL 租约协调，所有服务实例可以使用同一套用例。
func NewOperationsService(
	repositories operations.Repositories,
	media operations.MediaProvider,
	tiktokMedia operations.TikTokMediaProviderFactory,
) (*operations.Service, error) {
	service := operations.NewService(repositories, media)
	if err := configureMediaJobs(service); err != nil {
		return nil, err
	}
	if err := service.ConfigureTikTokPublications(tiktokMedia, operations.PublicationJobConfig{}); err != nil {
		return nil, err
	}
	return service, nil
}

// NewAdminRBACService 创建使用 JWT 认证的管理员认证与授权用例。
// 固定权限目录和首个超级管理员由部署 SQL 初始化；运营仅维护用户、角色和分配，服务启动不写 RBAC 数据。
func NewAdminRBACService(adminStore adminrbac.Repository, settings config.Config) (*adminrbac.Service, error) {
	if err := settings.ValidateAdminJWTSecret(); err != nil {
		slog.Error("configure admin JWT signing secret failed", "error", err)
		return nil, err
	}
	// 管理员 JWT 直接使用独立配置，不从消费者密钥派生，也不回退到消费者密钥。
	return adminrbac.NewService(adminStore, adminrbac.Config{SigningKey: []byte(settings.AdminJWTSigningSecret)}), nil
}

// NewConsumerService 注入共享消费者仓储与真实时钟，供目录、播放和奖励用例使用。
func NewConsumerService(store domain.Store, provider domain.DirectPlaybackProvider) *domain.Service {
	s := domain.NewService(store, time.Now)
	s.ConfigureDirectPlayback(provider)
	return s
}

// NewVisitorService 按运行环境装配 Visitor 身份用例：生产和预发要求 TikTok 登录，本地可匿名启动。
// VisitorToken 为客户端自包含凭据；用户和官方身份摘要进入共享 Store，不创建消费者 Session 表。
func NewVisitorService(
	settings config.Config,
	resolver *ApplicationResolver,
	store visitor.Store,
	playback domain.DirectPlaybackProvider,
) (*visitor.Service, error) {
	resolver.ConfigureDirectPlaybackGate(func(ctx context.Context) error {
		if playback == nil {
			slog.WarnContext(ctx, "direct playback provider unavailable", "error", visitor.ErrServiceUnavailable)
			return visitor.ErrServiceUnavailable
		}
		_, err := playback.Configuration(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "configure visitor direct playback failed", "error", err)
		}
		return err
	})
	service, err := visitor.NewService(resolver, store, visitor.Config{
		RequireTikTokLogin:            settings.Environment == "production" || settings.Environment == "staging",
		IdentityTokenEncryptionSecret: settings.IdentityTokenEncryptionSecret,
		VisitorTokenTTL:               settings.VisitorTokenTTL,
	})
	if err != nil {
		slog.Error("configure visitor service failed", "error", err)
	}
	return service, err
}
