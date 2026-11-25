package operations

import (
	"net/http"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/mediaconfig"
	bizoperations "drama-server/internal/biz/operations"
	"drama-server/internal/pkg/apireply"
	"drama-server/internal/pkg/ratelimit"
)

// Config 定义运营 HTTP 适配器依赖的可选能力。
type Config struct {
	// Direct 提供独立发布工作台。
	Direct *directplay.Service
	// Homepage 按当前应用管理首页轮播。
	Homepage *homepage.Service
	// GlobalConfigs 提供跨应用的配置管理用例。
	GlobalConfigs *globalconfig.Service
	// AuditError 接收无法返回给客户端的最终审计写入错误。
	AuditError func(error)
	// MediaConfig 提供只读 BytePlus VOD 回调验签配置。
	MediaConfig *mediaconfig.Service
	// RateLimiter 提供登录和管理写请求的基础频次保护；当前装配为进程内计数，不保证跨实例配额。
	RateLimiter ratelimit.Limiter
}

// Handler 负责把运营 HTTP 合同适配到业务用例，不承载业务规则。
type Handler struct {
	// service 编排运营业务，HTTP 字段在对应入口校验后传入。
	service *bizoperations.Service
	// mediaConfig 校验 VOD 回调签名，与管理员会话认证相互独立。
	mediaConfig *mediaconfig.Service
	// rbac 校验管理员会话和固定 action 权限。
	rbac *adminrbac.Service
	// config 提供审计错误回调和入口限流能力。
	config Config
	// mux 分派已注册的 HTTP 方法与资源路径。
	mux *http.ServeMux
}

// adminSessionContextKey 是已认证管理端主体的私有上下文键。
type adminSessionContextKey struct{}

// envelope 是所有业务 API 共用的响应合同。
type envelope = apireply.Envelope

// NewHandler 创建运营 HTTP 适配器并注册各领域路由。
func NewHandler(service *bizoperations.Service, rbac *adminrbac.Service, config Config) http.Handler {
	handler := &Handler{
		service: service, mediaConfig: config.MediaConfig, rbac: rbac, config: config,
		mux: http.NewServeMux(),
	}
	handler.routes()
	return handler
}

// ServeHTTP 把请求交给标准库路由器。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// routes 只负责组合各个运营领域的路由。
func (h *Handler) routes() {
	h.registerDirectRoutes()
	h.registerAuthenticationRoutes()
	h.registerApplicationRoutes()
	h.registerSubjectRoutes()
	h.registerCampaignRoutes()
	h.registerCarouselRoutes()
	h.registerContentRoutes()
	h.registerMediaRoutes()
	h.registerVODCallbackRoutes()
	h.registerReportingRoutes()
	h.registerConsumerUserRoutes()
	h.registerRBACRoutes()
	h.registerGlobalConfigRoutes()
}
