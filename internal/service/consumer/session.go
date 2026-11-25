package consumer

import (
	"log/slog"
	"net/http"
	"strings"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/visitor"
	"drama-server/internal/pkg/apierr"
	"drama-server/internal/pkg/identifier"
)

// sessionRoutes 仅追加新版入口，既有路由、校验和响应由原实现保持兼容。
func (h *Handler) sessionRoutes() {
	h.mux.HandleFunc("POST /api/v1/session", h.createSession)
	h.mux.HandleFunc("GET /api/v1/open/app-config", h.appConfig)
	h.mux.HandleFunc("GET /api/v1/open/play/prepare", h.prepareCampaignPlayback)
	h.mux.HandleFunc("POST /api/v1/launch/resolve", h.withVisitor(h.resolveLaunch))
	h.mux.HandleFunc("POST /api/v1/play/entry", h.withVisitor(h.playbackEntry))
	h.mux.HandleFunc("GET /api/v1/me/vip", h.withVisitor(h.vipState))
	h.mux.HandleFunc("GET /api/v1/me/vip/offer", h.withVisitor(h.vipOffer))
	h.mux.HandleFunc("POST /api/v1/me/entry-ad/shows", h.withVisitor(h.confirmEntryAdShow))
}

// prepareCampaignPlayback 提前解析当前应用的投放剧集，仅返回公开展示数据，不建立身份或签发媒体凭据。
func (h *Handler) prepareCampaignPlayback(w http.ResponseWriter, r *http.Request) {
	appID, err := readTikTokAppID(r.URL.Query().Get("tiktokAppId"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("campaignCode"))
	if code == "" || len(code) > 64 {
		rejectSplitInput(w, r)
		return
	}
	result, err := h.bootstrapUsecase.PrepareCampaignPlayback(r.Context(), appID, code)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, r, http.StatusOK, result)
}

// playbackEntry 归一化两种入口，再使用相同的服务端起播决策。自然点选不受会话旧投放归因覆盖。
func (h *Handler) playbackEntry(w http.ResponseWriter, r *http.Request, subject visitor.Context) {
	var input struct {
		// Source 区分可信投放落地和用户显式选择。
		Source string `json:"source"`
		// LaunchQuery 只用于投放落地，可信会话目标优先。
		LaunchQuery map[string]string `json:"launchQuery"`
		// DramaID 是显式点选的内部短剧标识。
		DramaID string `json:"dramaId"`
		// EpisodeID 是显式点选的内部分集标识。
		EpisodeID string `json:"episodeId"`
	}
	if !decode(w, r, &input) {
		return
	}
	dramaID, episodeID := strings.TrimSpace(input.DramaID), strings.TrimSpace(input.EpisodeID)
	switch input.Source {
	case "selection":
		if len(input.LaunchQuery) != 0 || !identifier.ValidDatabaseID(dramaID) || !identifier.ValidDatabaseID(episodeID) {
			rejectSplitInput(w, r)
			return
		}
	case "launch":
		if dramaID != "" || episodeID != "" || len(input.LaunchQuery) > 32 {
			rejectSplitInput(w, r)
			return
		}
		for key, value := range input.LaunchQuery {
			if len(key) > 64 || len(value) > 512 {
				rejectSplitInput(w, r)
				return
			}
		}
		dramaID, episodeID = launchEntryIDs(subject, input.LaunchQuery)
		if !identifier.ValidDatabaseID(dramaID) || (episodeID != "" && !identifier.ValidDatabaseID(episodeID)) {
			dramaID = ""
		}
	default:
		rejectSplitInput(w, r)
		return
	}
	result, err := h.service.ResolvePlaybackEntry(r.Context(), subject.VisitorID, dramaID, episodeID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if result == nil && input.Source == "selection" {
		writeDomainError(w, r, domain.ErrNotFound)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeData(w, r, http.StatusOK, map[string]*domain.PlaybackEntry{"entry": result})
}

// createSession 只建立会话；输入沿用官方换码及可信归因规则，但不调用聚合启动用例。
func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	var input visitor.BootstrapInput
	if !decode(w, r, &input) {
		return
	}
	appID, err := readTikTokAppID(input.TikTokAppID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	input.TikTokAppID = appID
	if len(input.LoginCode) > 4096 || len(input.VisitorToken) > 16384 || len(input.LaunchQuery) > 32 {
		rejectSplitInput(w, r)
		return
	}
	for key, value := range input.LaunchQuery {
		if len(key) > 64 || len(value) > 512 {
			rejectSplitInput(w, r)
			return
		}
	}
	result, err := h.bootstrapUsecase.CreateSession(r.Context(), input)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeData(w, r, http.StatusOK, result)
}

// appConfig 公开读取应用配置，不解析 Authorization，也不创建用户。
func (h *Handler) appConfig(w http.ResponseWriter, r *http.Request) {
	appID, err := readTikTokAppID(r.URL.Query().Get("tiktokAppId"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	result, err := h.bootstrapUsecase.PublicAppConfig(r.Context(), appID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, r, http.StatusOK, result)
}

// resolveLaunch 独立解析可见落地内容；凭据中的可信投放目标优先，客户端不能重写归因。
func (h *Handler) resolveLaunch(w http.ResponseWriter, r *http.Request, subject visitor.Context) {
	var input struct {
		LaunchQuery map[string]string `json:"launchQuery"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.LaunchQuery) > 32 {
		rejectSplitInput(w, r)
		return
	}
	for key, value := range input.LaunchQuery {
		if len(key) > 64 || len(value) > 512 {
			rejectSplitInput(w, r)
			return
		}
	}
	dramaID, episodeID := launchEntryIDs(subject, input.LaunchQuery)
	result, err := h.service.ResolveLaunchTarget(r.Context(), dramaID, episodeID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeData(w, r, http.StatusOK, map[string]*domain.LaunchTarget{"launchTarget": result})
}

// 两代落地请求共用可信归因优先级；客户端渠道值不参与权益判断。
func launchEntryIDs(subject visitor.Context, query map[string]string) (string, string) {
	if subject.Campaign != nil && subject.Campaign.DramaID != "" {
		return strings.TrimSpace(subject.Campaign.DramaID), strings.TrimSpace(subject.Campaign.EpisodeID)
	}
	return strings.TrimSpace(firstQueryValue(query, "dramaId", "drama_id", "albumId", "album_id")),
		strings.TrimSpace(firstQueryValue(query, "episodeId", "episode_id"))
}

// vipState 的读取只返回个人状态，广告位故障不会阻塞此接口。
func (h *Handler) vipState(w http.ResponseWriter, r *http.Request, subject visitor.Context) {
	result, err := h.service.ReadVIPState(r.Context(), subject.VisitorID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeData(w, r, http.StatusOK, result)
}

// vipOffer 在用户需要广告时查询单个可用入口，不累计进度或发放权益。
func (h *Handler) vipOffer(w http.ResponseWriter, r *http.Request, subject visitor.Context) {
	result, err := h.service.ReadVIPOffer(r.Context(), subject.VisitorID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeData(w, r, http.StatusOK, map[string]*monetization.AccessOffer{"offer": result})
}

// confirmEntryAdShow 返回已确认展示及最新进度；补充状态失败不能把已保存事实改成失败。
func (h *Handler) confirmEntryAdShow(w http.ResponseWriter, r *http.Request, subject visitor.Context) {
	var input monetization.AdShowInput
	if !decode(w, r, &input) {
		return
	}
	input.Scene = strings.TrimSpace(input.Scene)
	if input.Scene == "" {
		input.Scene = monetization.AdSceneEntryReward
	}
	input, err := normalizeAdShow(input)
	if err != nil || input.Scene != monetization.AdSceneEntryReward {
		writeDomainError(w, r, monetization.ErrInvalidAdShow)
		return
	}
	if err := h.service.RecordAdShow(r.Context(), subject.VisitorID, input); err != nil {
		writeDomainError(w, r, err)
		return
	}
	result := map[string]any{"accepted": true}
	state, err := h.service.EntryAdStatus(r.Context(), subject.VisitorID, input.DramaID)
	if err != nil {
		slog.ErrorContext(r.Context(), "read confirmed entry show progress failed", "error", err)
	} else {
		result["entryAdStatus"] = state
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeData(w, r, http.StatusOK, result)
}

// rejectSplitInput 记录新入口的参数拒绝，不泄漏登录码或完整启动参数。
func rejectSplitInput(w http.ResponseWriter, r *http.Request) {
	slog.WarnContext(r.Context(), "consumer split request rejected", "error", "invalid_request")
	writeError(w, r, apierr.ConsumerInvalidRequest)
}
