// Package data 创建持久化资源和外部服务适配器，并实现 biz 层声明的端口。
package data

import (
	"github.com/google/wire"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/visitor"
)

// ProviderSet 声明数据源、外部适配器以及业务仓储端口的绑定。
// 资源生命周期由 Styx Runtime 持有，业务层只接收窄接口。
var ProviderSet = wire.NewSet(
	NewResources,
	NewAnalyticsService,
	NewHomepageService,
	NewGlobalConfigService,
	NewStoredMediaConfigService,
	NewConsumerRepository,
	NewDirectPlaybackProvider,
	NewDirectPublicationService,
	wire.Bind(new(domain.DirectPlaybackProvider), new(*DirectPlaybackProvider)),
	NewOperationsRepositories,
	NewAdminRepository,
	NewVisitorRepository,
	NewApplicationRepository,
	NewHealthCheck,
	NewMediaResources,
	NewMediaProvider,
	NewIdentityProviderFactory,
	NewTikTokMediaClientPool,
	NewTikTokMediaProviderFactory,
	NewRequestRateLimiter,
	wire.Bind(new(domain.Store), new(*ConsumerRepository)),
	wire.Bind(new(adminrbac.Repository), new(*AdminRepository)),
	wire.Bind(new(visitor.Store), new(*VisitorRepository)),
	wire.Bind(new(application.Repository), new(*ApplicationRepository)),
)
