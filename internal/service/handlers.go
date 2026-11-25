// Package service 把业务用例适配为传输层可挂载的 HTTP Handler。
package service

import (
	"log/slog"
	"net/http"

	"drama-server/internal/biz"
	"drama-server/internal/biz/adminrbac"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/mediaconfig"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/visitor"
	"drama-server/internal/conf"
	"drama-server/internal/pkg/health"
	"drama-server/internal/pkg/ratelimit"
	httpapi "drama-server/internal/service/consumer"
	operationshttp "drama-server/internal/service/operations"
)

// Handlers 为消费者端和运营端提供有名称的 Handler，避免 Wire 在两个 http.Handler
// 之间产生歧义。它不创建 Server，也不处理监听地址和框架中间件。
type Handlers struct {
	// Consumer 处理消费者公开 API。
	Consumer http.Handler
	// Admin 处理需要管理员认证与权限校验的运营 API。
	Admin http.Handler
}

// NewHandlers 把业务服务装配为现有 HTTP 合同。
func NewHandlers(
	consumerService *domain.Service,
	visitors *visitor.Service,
	bootstrap *biz.ConsumerBootstrap,
	operationsService *operations.Service,
	rbac *adminrbac.Service,
	mediaConfig *mediaconfig.Service,
	globalConfigs *globalconfig.Service,
	healthCheck health.Check,
	rateLimiter ratelimit.Limiter,
	homepageService *homepage.Service,
	analyticsService *domain.AnalyticsService,
	directService *directplay.Service,
	settings config.Config,
) *Handlers {
	// 广告错误使用独立的有界窗口，客户端洪峰不能占满运营入口的限流容量。
	adErrorLimiter := ratelimit.NewMemory(0)
	consumerHandler := httpapi.NewHandler(consumerService, visitors, bootstrap, httpapi.Config{
		HealthCheck: healthCheck, Homepage: homepageService, Analytics: analyticsService,
		RateLimiter: adErrorLimiter, AdErrorRequestsPerMinute: settings.AdErrorRequestsPerMinute,
	})
	adminHandler := operationshttp.NewHandler(operationsService, rbac, operationshttp.Config{
		Direct: directService, Homepage: homepageService, GlobalConfigs: globalConfigs, MediaConfig: mediaConfig, RateLimiter: rateLimiter,
		AuditError: func(err error) {
			slog.Error("admin audit persistence failed", "error", err)
		},
	})
	return &Handlers{Consumer: consumerHandler, Admin: adminHandler}
}
