package consumer

import (
	"context"
	"drama-server/internal/pkg/identifier"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	bizapp "drama-server/internal/biz"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/campaign"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/sharding"
	"drama-server/internal/biz/visitor"
	"drama-server/internal/pkg/apierr"
	"drama-server/internal/pkg/apireply"
	"drama-server/internal/pkg/ratelimit"
	"drama-server/internal/pkg/requestmeta"
	"drama-server/internal/pkg/requesttime"
)

// Config 声明消费者 HTTP 入口依赖及广告日志限流配置。
type Config struct {
	// RateLimiter 复用进程内有界实现并隔离运营额度，鉴权前保存凭据摘要，鉴权后保存用户额度。
	RateLimiter ratelimit.Limiter
	// AdErrorRequestsPerMinute 同时限制单实例内同凭据的鉴权请求及同应用、同用户的广告日志请求数。
	AdErrorRequestsPerMinute int
	// Analytics 提供与核心业务独立的运营指标采集。
	Analytics *domain.AnalyticsService
	// Homepage 提供与播放鉴权独立的首页配置和访问计数。
	Homepage *homepage.Service
	// HealthCheck 检查实例依赖是否可用；失败时健康路由返回非就绪状态。
	HealthCheck func(context.Context) error
}

// Handler 组合 HTTP 路由、Visitor 身份恢复与消费者业务用例。
type Handler struct {
	// service 提供目录查询、播放决策和奖励结算用例。
	service *domain.Service
	// bootstrapUsecase 编排应用解析、登录身份和首页启动数据。
	bootstrapUsecase *bizapp.ConsumerBootstrap
	// visitors 校验服务端凭据；生产身份由官方静默登录建立，不要求资料授权。
	visitors *visitor.Service
	// config 保存入口依赖，不持有业务运行状态。
	config Config
	// mux 按 HTTP 方法与路径分发到领域入口。
	mux *http.ServeMux
}

// envelope 是所有业务 API 共用的响应合同。
type envelope = apireply.Envelope

// dramaDetailDTO 在短剧摘要上增加已结合权益计算的分集列表。
type dramaDetailDTO struct {
	// DramaSummary 保留目录摘要字段，使列表和详情使用相同基础合同。
	domain.DramaSummary
	// Episodes 按剧集顺序提供展示信息及访问方式。
	Episodes []domain.EpisodeSummary `json:"episodes"`
}

// NewHandler 使用注入的业务用例注册消费者路由；身份、启动编排和播放业务分别由对应 Service 处理。
func NewHandler(
	service *domain.Service,
	visitors *visitor.Service,
	bootstrapUsecase *bizapp.ConsumerBootstrap,
	config Config,
) http.Handler {
	h := &Handler{
		service: service, bootstrapUsecase: bootstrapUsecase,
		visitors: visitors, config: config, mux: http.NewServeMux(),
	}
	h.routes()
	h.sessionRoutes()
	return h
}

// ServeHTTP 将请求交给已注册路由；每条受保护路由通过 withVisitor 恢复可信身份。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	version := strings.TrimSpace(r.Header.Get("X-Mini-Version"))
	if version != "" && !application.ValidMiniVersion(version) {
		slog.WarnContext(r.Context(), "mini version rejected", "error", visitor.ErrInvalidRequest)
		writeError(w, r, apierr.ConsumerInvalidRequest)
		return
	}
	r = r.WithContext(application.WithMiniVersion(requesttime.Ensure(r.Context(), time.Now), version))
	w.Header().Set("Cache-Control", "private, no-store")
	h.mux.ServeHTTP(w, r)
}

// routes 注册健康检查、公开启动和需要 VisitorToken 的消费接口；不提供观看进度、收藏或历史写接口。
func (h *Handler) routes() {
	h.mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		if h.config.HealthCheck != nil {
			if err := h.config.HealthCheck(r.Context()); err != nil {
				slog.ErrorContext(r.Context(), "consumer health check failed", "error", err)
				writeError(w, r, apierr.ConsumerUnhealthy)
				return
			}
		}
		writeData(w, r, http.StatusOK, map[string]string{"status": "ok"})
	})
	h.mux.HandleFunc("POST /api/v1/bootstrap", h.bootstrap)
	// open 命名空间明确标识无需 VisitorToken、可在 SDK 与登录完成前请求的公开资源。
	h.mux.HandleFunc("GET /api/v1/open/catalog", h.publicCatalog)
	h.mux.HandleFunc("GET /api/v1/open/review-home", h.reviewHome)
	h.mux.HandleFunc("GET /api/v1/open/home/carousel", h.homeCarousel)
	h.mux.HandleFunc("GET /api/v1/open/home/hot", h.homeHot)
	h.mux.HandleFunc("GET /api/v1/open/home/rankings", h.homeRankings)
	h.mux.HandleFunc("GET /api/v1/open/home/ranking-categories", h.homeRankingCategories)
	h.mux.HandleFunc("GET /api/v1/open/home/rankings/{tagID}", h.homeRankingPage)
	h.mux.HandleFunc("GET /api/v1/home", h.withVisitor(h.home))
	h.mux.HandleFunc("GET /api/v1/discovery", h.withVisitor(h.discovery))
	h.mux.HandleFunc("GET /api/v1/dramas/{dramaID}", h.withVisitor(h.drama))
	h.mux.HandleFunc("POST /api/v1/play/resolve", h.withVisitor(h.resolvePlay))
	h.mux.HandleFunc("POST /api/v1/play/resolve-batch", h.withVisitor(h.resolvePlayBatch))
	h.mux.HandleFunc("POST /api/v1/metrics", h.withVisitor(h.collectMetrics))
	h.mux.HandleFunc("POST /api/v1/ad-errors", h.withAdErrorRateLimit(h.withVisitor(h.collectAdErrors)))
	h.mux.HandleFunc("POST /api/v1/reward-grants", h.withVisitor(h.claimReward))
	h.mux.HandleFunc("GET /api/v1/me/entry-ad", h.withVisitor(h.entryAdStatus))
	h.mux.HandleFunc("GET /api/v1/me/ad-experience", h.withVisitor(h.adExperience))
	h.mux.HandleFunc("GET /api/v1/me/ad-shows", h.withVisitor(h.adShowStatus))
	h.mux.HandleFunc("POST /api/v1/me/ad-shows", h.withVisitor(h.recordAdShow))
	h.mux.HandleFunc("POST /api/v1/vip/claim", h.withVisitor(h.claimVIP))
	h.mux.HandleFunc("GET /api/v1/me/unlocks", h.withVisitor(h.unlocks))
}

// adExperience 复用有效 VisitorToken 查询规则与权益，不调用 TikTok 换码或签发新凭据。
func (h *Handler) adExperience(w http.ResponseWriter, r *http.Request, current visitor.Context) {
	w.Header().Set("Cache-Control", "private, no-store")
	result, err := h.service.ReadAdExperience(r.Context(), current.VisitorID)
	if err != nil {
		slog.ErrorContext(r.Context(), "read ad experience failed", "error", err)
		writeDomainError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, result)
}

// entryAdStatus 校验可选来源剧目后查询事实；范围和模式来自策略配置，不由请求指定。
func (h *Handler) entryAdStatus(w http.ResponseWriter, r *http.Request, visitorContext visitor.Context) {
	dramaID := strings.TrimSpace(r.URL.Query().Get("dramaId"))
	if dramaID != "" && !identifier.ValidDatabaseID(dramaID) {
		slog.WarnContext(r.Context(), "entry ad status query rejected", "error", visitor.ErrInvalidRequest)
		writeDomainError(w, r, visitor.ErrInvalidRequest)
		return
	}
	status, err := h.service.EntryAdStatus(r.Context(), visitorContext.VisitorID, dramaID)
	if err != nil {
		slog.ErrorContext(r.Context(), "read entry ad status failed", "error", err)
		writeDomainError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, status)
}

// adShowStatus 查询场景的当前自然日事实，HTTP 入口校验可选内容范围；不接收计数模式或 N。
func (h *Handler) adShowStatus(w http.ResponseWriter, r *http.Request, visitorContext visitor.Context) {
	scene := strings.TrimSpace(r.URL.Query().Get("scene"))
	dramaID, episodeID := strings.TrimSpace(r.URL.Query().Get("dramaId")), strings.TrimSpace(r.URL.Query().Get("episodeId"))
	if !adShowScenePattern.MatchString(scene) || !validAdShowTarget(dramaID, episodeID) {
		slog.WarnContext(r.Context(), "ad show query rejected", "error", monetization.ErrInvalidAdShow)
		writeDomainError(w, r, monetization.ErrInvalidAdShow)
		return
	}
	status, err := h.service.AdShowStatus(r.Context(), visitorContext.VisitorID, scene, dramaID, episodeID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, status)
}

// recordAdShow 在调用业务层前校验通用事实；主体身份不接受客户端覆盖。
func (h *Handler) recordAdShow(w http.ResponseWriter, r *http.Request, visitorContext visitor.Context) {
	var input monetization.AdShowInput
	if !decode(w, r, &input) {
		return
	}
	input, err := normalizeAdShow(input)
	if err != nil {
		slog.WarnContext(r.Context(), "ad show input rejected", "error", err)
		writeDomainError(w, r, err)
		return
	}
	if err := h.service.RecordAdShow(r.Context(), visitorContext.VisitorID, input); err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, map[string]bool{"accepted": true})
}

// bootstrap 校验官方 TikTok AppID 并调用启动用例，返回 VisitorToken、身份状态及初始业务数据。
// 仅认证被拒绝时映射为启动失败；平台或数据库不可用保留独立错误分类。
func (h *Handler) bootstrap(w http.ResponseWriter, r *http.Request) {
	var input visitor.BootstrapInput
	if !decode(w, r, &input) {
		return
	}
	tiktokAppID, err := readTikTokAppID(input.TikTokAppID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	input.TikTokAppID = tiktokAppID
	logFields := append(requestmeta.LogFields(r.Context()),
		"flow", "consumer_bootstrap", "tiktok_app_id", input.TikTokAppID,
	)
	slog.DebugContext(r.Context(), "consumer bootstrap started", append(logFields, "stage", "resolve_application")...)
	result, err := h.bootstrapUsecase.Execute(r.Context(), input)
	if err != nil {
		definition := consumerRequestErrorDefinition(r.Context(), err)
		slog.WarnContext(r.Context(), "consumer bootstrap failed", "error", err)
		if definition.Code == apierr.ConsumerUnauthorized.Code {
			writeError(w, r, apierr.ConsumerBootstrapFailed)
		} else {
			writeError(w, r, definition)
		}
		return
	}
	slog.InfoContext(r.Context(), "consumer bootstrap succeeded", append(logFields,
		"stage", "complete", "outcome", "success",
		"application_id", result.ApplicationID, "visitor_id", result.VisitorID,
		"identity_state", result.IdentityState,
	)...)
	writeData(w, r, http.StatusOK, result)
}

// 按候选键顺序读取第一个非空查询参数。
func firstQueryValue(query map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(query[key]); value != "" {
			return value
		}
	}
	return ""
}

// 按顺序返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// home 返回当前应用可见的线上短剧摘要；授权过滤与展示投影在 consumer Service 中完成。
func (h *Handler) home(w http.ResponseWriter, r *http.Request, _ visitor.Context) {
	input, err := readDiscoveryQuery(r)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	input.Home = true
	page, err := h.service.Discover(r.Context(), input)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, map[string]any{"dramas": page.Items, "nextCursor": page.NextCursor})
}

// discovery 校验筛选条件与游标后查询可见目录，返回当前页、可用标签和下一页游标。
func (h *Handler) discovery(w http.ResponseWriter, r *http.Request, _ visitor.Context) {
	input, err := readDiscoveryQuery(r)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	result, err := h.service.Discover(r.Context(), input)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, result)
}

// drama 读取当前应用可见的短剧，再结合免费集数、VIP 状态及逐集权益构造分集展示状态。
// 这里的 accessMode 用于 UI 展示；真正播放时仍通过播放用例重新检查权益。
func (h *Handler) drama(w http.ResponseWriter, r *http.Request, visitorContext visitor.Context) {
	id := strings.TrimSpace(r.PathValue("dramaID"))
	if !identifier.ValidDatabaseID(id) {
		writeDomainError(w, r, visitor.ErrInvalidRequest)
		return
	}
	r.SetPathValue("dramaID", id)
	drama, err := h.service.FindDrama(r.Context(), r.PathValue("dramaID"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	policy, err := h.service.ResolveMonetizationPolicy(r.Context())
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	vipProgress, err := h.service.VIPProgress(r.Context(), visitorContext.VisitorID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	unlockedEpisodeIDs, err := h.service.UnlockedEpisodeIDs(r.Context(), visitorContext.VisitorID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	unlocked := make(map[string]struct{}, len(unlockedEpisodeIDs))
	for _, episodeID := range unlockedEpisodeIDs {
		unlocked[episodeID] = struct{}{}
	}
	episodes := make([]domain.EpisodeSummary, 0, len(drama.Episodes))
	for _, episode := range drama.Episodes {
		summary := episode.Summary()
		summary.AccessMode = monetization.EpisodeAccessRewarded
		if episode.Number <= policy.FreeEpisodeCount {
			summary.AccessMode = monetization.EpisodeAccessFree
		} else if vipProgress.Active {
			summary.AccessMode = monetization.EpisodeAccessUnlocked
		} else if _, ok := unlocked[episode.ID]; ok {
			summary.AccessMode = monetization.EpisodeAccessUnlocked
		}
		episodes = append(episodes, summary)
	}
	writeData(w, r, http.StatusOK, dramaDetailDTO{DramaSummary: drama.Summary(), Episodes: episodes})
}

// resolvePlay 校验短剧/分集标识，用已认证 Visitor 身份请求播放决策；返回官方 Player 参数或奖励方案。
func (h *Handler) resolvePlay(w http.ResponseWriter, r *http.Request, visitorContext visitor.Context) {
	var input struct {
		// 保存请求指定的短剧标识。
		DramaID string `json:"dramaId"`
		// 保存请求指定的剧集标识。
		EpisodeID string `json:"episodeId"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.DramaID = strings.TrimSpace(input.DramaID)
	input.EpisodeID = strings.TrimSpace(input.EpisodeID)
	if !identifier.ValidDatabaseID(input.DramaID) || !identifier.ValidDatabaseID(input.EpisodeID) {
		writeDomainError(w, r, visitor.ErrInvalidRequest)
		return
	}
	result, err := h.service.ResolveOfficialPlay(
		playResolveContext(r), visitorContext.VisitorID, input.DramaID, input.EpisodeID,
	)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, result)
}

// resolvePlayBatch 一次解析同一短剧最多五个相邻剧集，减少连续播放准备阶段的网络往返。
func (h *Handler) resolvePlayBatch(w http.ResponseWriter, r *http.Request, visitorContext visitor.Context) {
	var input struct {
		// DramaID 是全部目标剧集所属的内部短剧标识。
		DramaID string `json:"dramaId"`
		// EpisodeIDs 按播放器预加载窗口顺序提交内部剧集标识。
		EpisodeIDs []string `json:"episodeIds"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.DramaID = strings.TrimSpace(input.DramaID)
	if !identifier.ValidDatabaseID(input.DramaID) || len(input.EpisodeIDs) == 0 || len(input.EpisodeIDs) > 5 {
		writeDomainError(w, r, visitor.ErrInvalidRequest)
		return
	}
	seen := make(map[string]struct{}, len(input.EpisodeIDs))
	for index := range input.EpisodeIDs {
		input.EpisodeIDs[index] = strings.TrimSpace(input.EpisodeIDs[index])
		if !identifier.ValidDatabaseID(input.EpisodeIDs[index]) {
			writeDomainError(w, r, visitor.ErrInvalidRequest)
			return
		}
		if _, duplicate := seen[input.EpisodeIDs[index]]; duplicate {
			writeDomainError(w, r, visitor.ErrInvalidRequest)
			return
		}
		seen[input.EpisodeIDs[index]] = struct{}{}
	}
	results, err := h.service.ResolveOfficialPlays(playResolveContext(r), visitorContext.VisitorID, input.DramaID, input.EpisodeIDs)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, results)
}

// playResolveContext 只有主动声明支持的 Mini 才接收片段，旧请求沿用原锁定响应。
func playResolveContext(r *http.Request) context.Context {
	if r.URL.Query().Get("entryPreview") == "1" {
		return domain.WithEntryPreview(r.Context())
	}
	return r.Context()
}

// claimReward 只接收 TikTok 已报告完整结束的广告领取请求。ClaimID 由客户端在广告展示
// 前生成并在重试时复用；服务端不创建或保存“广告展示尝试”。
func (h *Handler) claimReward(w http.ResponseWriter, r *http.Request, visitorContext visitor.Context) {
	var input monetization.ClaimRewardInput
	if !decode(w, r, &input) {
		return
	}
	input, err := normalizeRewardClaim(input)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	result, err := h.service.ClaimReward(r.Context(), visitorContext.VisitorID, input)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	// 首次事实落账用 201，原键幂等恢复用 200；继续兼容历史服务结果中的其他成功确认状态。
	status := http.StatusCreated
	if result.Status != monetization.ClaimStatusGranted {
		status = http.StatusOK
	}
	writeData(w, r, status, result)
}

// claimVIP 仅凭已认证的访客身份领取当日达标权益，不接受客户端指定进度或身份。
func (h *Handler) claimVIP(w http.ResponseWriter, r *http.Request, visitorContext visitor.Context) {
	state, err := h.service.ClaimVIP(r.Context(), visitorContext.VisitorID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, state)
}

// unlocks 返回已认证 Visitor 在当前应用中的解锁记录，不接受客户端指定其他 Visitor。
func (h *Handler) unlocks(w http.ResponseWriter, r *http.Request, visitorContext visitor.Context) {
	items, err := h.service.UnlockRecords(r.Context(), visitorContext.VisitorID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, items)
}

// visitorHandler 接收已由 withVisitor 校验的身份，不能从正文重写应用或 Visitor。
type visitorHandler func(http.ResponseWriter, *http.Request, visitor.Context)

// withVisitor 恢复可信应用和 Visitor；只有真正凭据无效才返回 401。
func (h *Handler) withVisitor(next visitorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("authorization"), "Bearer "))
		visitorContext, err := h.visitors.Resolve(r.Context(), token)
		if err != nil {
			definition := consumerRequestErrorDefinition(r.Context(), err)
			slog.WarnContext(r.Context(), "consumer authentication stopped", "error", err)
			writeError(w, r, definition)
			return
		}
		// 应用、时区和归因来自服务端解析的凭据与记录；后续 DAO 从 context 取得应用范围。
		ctx := application.WithTimeZone(r.Context(), visitorContext.ApplicationID, visitorContext.TimeZone)
		ctx = application.WithPlayback(ctx, visitorContext.PlaybackProvider)
		ctx = application.WithSubject(ctx, visitorContext.SubjectID)
		ctx = application.WithMiniReview(ctx, visitorContext.MiniReview, visitorContext.MonetizationMode)
		ctx = sharding.WithBucketID(ctx, visitorContext.BucketID)
		if visitorContext.Campaign != nil {
			ctx = campaign.WithAttribution(ctx, visitorContext.Campaign.ID)
		}
		r = r.WithContext(ctx)
		next(w, r, visitorContext)
	}
}

// 解码单个结构化请求正文并拒绝多余内容。
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		slog.WarnContext(r.Context(), "consumer request decode rejected", "error", err)
		writeError(w, r, apierr.ConsumerInvalidRequest)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = visitor.ErrInvalidRequest
		}
		slog.WarnContext(r.Context(), "consumer request trailing content rejected", "error", err)
		writeError(w, r, apierr.ConsumerInvalidRequest)
		return false
	}
	return true
}

// writeData 输出消费者成功响应，并将当前请求 Context 传给编码及写入故障日志。
func writeData(w http.ResponseWriter, r *http.Request, status int, data any) {
	apireply.Success(r.Context(), w, status, data)
}

// writeError 通过统一错误目录生成稳定 code 和按请求语言选择的 message。
func writeError(w http.ResponseWriter, r *http.Request, definition apierr.Definition) {
	apierr.WriteHTTP(r.Context(), w, definition)
}

// writeDomainError 记录业务返回的实际错误，再映射为安全的 HTTP 错误，避免把依赖详情返回客户端。
func writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	definition := consumerRequestErrorDefinition(r.Context(), err)
	slog.WarnContext(r.Context(), "consumer request stopped", "error", err)
	writeError(w, r, definition)
}

// consumerRequestErrorDefinition 用请求状态兜底识别取消，避免数据库合并回滚错误后丢失取消错误链。
// 只改变失败响应的分类；调用方仍记录原始错误，不把成功提交改成取消结果。
func consumerRequestErrorDefinition(ctx context.Context, err error) apierr.Definition {
	if err != nil && ctx.Err() != nil {
		return consumerErrorDefinition(ctx.Err())
	}
	return consumerErrorDefinition(err)
}

// consumerErrorDefinition 在 HTTP 边界统一分类；取消/超时优先于包装它们的依赖错误。
func consumerErrorDefinition(err error) apierr.Definition {
	var timeout interface{ Timeout() bool }
	switch {
	case errors.Is(err, context.Canceled):
		return apierr.ConsumerRequestCancelled
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return apierr.ConsumerRequestTimeout
	case errors.Is(err, visitor.ErrServiceUnavailable), errors.Is(err, domain.ErrServiceUnavailable):
		return apierr.ConsumerServiceUnavailable
	case errors.Is(err, visitor.ErrIdentityUnavailable):
		return apierr.ConsumerIdentityUnavailable
	case errors.Is(err, application.ErrUnavailable):
		return apierr.ConsumerApplicationUnavailable
	case errors.Is(err, visitor.ErrApplicationForbidden):
		return apierr.ConsumerApplicationForbidden
	case errors.Is(err, visitor.ErrUnauthorized):
		return apierr.ConsumerUnauthorized
	case errors.Is(err, visitor.ErrInvalidRequest):
		return apierr.ConsumerInvalidRequest
	case errors.Is(err, monetization.ErrInvalidAdShow):
		return apierr.ConsumerInvalidRequest
	case errors.Is(err, domain.ErrNotFound):
		return apierr.ConsumerNotFound
	case errors.Is(err, monetization.ErrInvalidRewardClaim):
		return apierr.ConsumerInvalidRewardClaim
	case errors.Is(err, domain.ErrInvalidDiscovery):
		return apierr.ConsumerInvalidRequest
	case errors.Is(err, domain.ErrRateLimited):
		return apierr.ConsumerRateLimited
	case errors.Is(err, domain.ErrPlaybackCredential):
		return apierr.ConsumerPlaybackUnavailable
	default:
		return apierr.ConsumerInternal
	}
}

// readDiscoveryQuery 统一公共目录、首页和筛选页的游标与页大小校验。
func readDiscoveryQuery(r *http.Request) (domain.DiscoveryQuery, error) {
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 50 {
			return domain.DiscoveryQuery{}, domain.ErrInvalidDiscovery
		}
		limit = value
	}
	return normalizeDiscoveryQuery(domain.DiscoveryQuery{Query: r.URL.Query().Get("query"), Tag: r.URL.Query().Get("tag"), Status: r.URL.Query().Get("status"), Limit: limit}, r.URL.Query().Get("cursor"))
}

// readReviewRevision 在首页入口校验配置修订，不接受负值、溢出或任意查询表达式。
func readReviewRevision(raw string) (*int64, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 || value >= 9007199254740991 {
		return nil, domain.ErrInvalidDiscovery
	}
	return &value, nil
}
