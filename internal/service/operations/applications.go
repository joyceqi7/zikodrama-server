package operations

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"drama-server/internal/biz/adminrbac"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/operations"
	"drama-server/internal/pkg/apierr"
)

// registerApplicationRoutes 注册应用及其商业化配置接口。
func (h *Handler) registerApplicationRoutes() {
	h.mux.HandleFunc("GET /admin/v1/review-settings", h.requirePermission(adminrbac.PermissionReviewSettingsRead, h.reviewSettings))
	h.mux.HandleFunc("PUT /admin/v1/review-settings", h.requirePermission(adminrbac.PermissionReviewSettingsUpdate, h.saveReviewSettings))
	h.registerUnlockStrategyRoutes()
	for _, channel := range []struct{ path, provider, read, update string }{
		{"official", "tiktok_minis", adminrbac.PermissionOfficialReviewListRead, adminrbac.PermissionOfficialReviewListUpdate},
		{"direct", "byteplus_web", adminrbac.PermissionDirectReviewListRead, adminrbac.PermissionDirectReviewListUpdate},
	} {
		path := "/admin/v1/" + channel.path + "/review-list"
		h.mux.HandleFunc("GET "+path, h.requirePermission(channel.read, reviewListScope(channel.provider, h.reviewWhitelist)))
		h.mux.HandleFunc("PUT "+path, h.requirePermission(channel.update, reviewListScope(channel.provider, h.saveReviewWhitelist)))
		h.mux.HandleFunc("GET "+path+"/targets", h.requirePermission(channel.read, reviewListScope(channel.provider, h.reviewListTargets)))
	}
	h.mux.HandleFunc("GET /admin/v1/applications/options", h.requireSession(h.applicationOptions))
	h.mux.HandleFunc("GET /admin/v1/applications", h.requirePermission(adminrbac.PermissionApplicationRead, h.listApplications))
	h.mux.HandleFunc("POST /admin/v1/applications", h.requirePermission(adminrbac.PermissionApplicationCreate, h.createApplication))
	h.mux.HandleFunc("PUT /admin/v1/applications/{applicationID}", h.requirePermission(adminrbac.PermissionApplicationUpdate, h.updateApplication))
	h.mux.HandleFunc("GET /admin/v1/application-monetization", h.requireAnyPermission([]string{adminrbac.PermissionApplicationRead, adminrbac.PermissionApplicationUpdate}, h.applicationMonetization))
	h.mux.HandleFunc("PUT /admin/v1/application-monetization", h.requirePermission(adminrbac.PermissionApplicationUpdate, h.updateApplicationMonetization))
}

// listApplications 返回运营端可管理的应用列表。
func (h *Handler) listApplications(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.ApplicationService.Applications(r.Context())
	// 应用读取权限只返回审核设置，内容引用由独立清单查看权限控制。
	for i := range result {
		result[i].MiniReview.DramaIDs = nil
	}
	writeResult(w, r, http.StatusOK, result, err)
}

// createApplication 校验并创建应用。
func (h *Handler) createApplication(w http.ResponseWriter, r *http.Request) {
	var input operations.ApplicationInput
	if !decode(w, r, &input) {
		return
	}
	input, err := normalizeCreateApplicationInput(input)
	if rejectInvalidInput(w, r, "create application input rejected", err) {
		return
	}

	if input.PlaybackProvider == "byteplus_web" && !h.directReady(w, r) {
		return
	}
	result, err := h.service.ApplicationService.CreateApplication(r.Context(), input)
	if result != nil {
		result.MiniReview.DramaIDs = nil
	}
	writeResult(w, r, http.StatusCreated, result, err)
}

// updateApplication 在应用编辑权限下校验资料及播放器选择，保留并发修订和直连能力校验。
func (h *Handler) updateApplication(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "applicationID") {
		return
	}
	var input operations.ApplicationInput
	if !decode(w, r, &input) {
		return
	}
	input, err := normalizeApplicationInput(input)
	if rejectInvalidInput(w, r, "update application input rejected", err) {
		return
	}
	// 兼容旧应用编辑合同，但应用编辑权限不能绕过独立审核开关权限。
	if input.MiniReview != nil {
		session, _ := r.Context().Value(adminSessionContextKey{}).(adminrbac.Session)
		if !session.User.IsSuper && !slices.Contains(session.Permissions, adminrbac.PermissionReviewSettingsUpdate) {
			slog.WarnContext(r.Context(), "application review settings permission rejected", "error", adminrbac.ErrForbidden)
			writeError(w, r, apierr.OperationsPermissionDenied)
			return
		}
	}
	if input.PlaybackProvider == "byteplus_web" && !h.directReady(w, r) {
		return
	}

	result, err := h.service.ApplicationService.UpdateApplication(r.Context(), r.PathValue("applicationID"), input)
	if result != nil {
		result.MiniReview.DramaIDs = nil
	}
	// 播放修订过期或引用不可用属于运营配置拒绝，保留原错误详情与 HTTP 映射，避免刷成系统故障。
	if errors.Is(err, operations.ErrConflict) {
		slog.WarnContext(r.Context(), "update application state rejected", "error", err)
		writeError(w, r, apierr.OperationsStateConflict)
		return
	}
	if errors.Is(err, directplay.ErrInvalid) || errors.Is(err, operations.ErrInvalidInput) {
		slog.WarnContext(r.Context(), "update application configuration rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	writeResult(w, r, http.StatusOK, result, err)
}

// applicationMonetization 返回当前应用的商业化配置。
func (h *Handler) applicationMonetization(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.MonetizationService.ApplicationMonetization(r.Context())
	writeResult(w, r, http.StatusOK, result, err)
}

// updateApplicationMonetization 校验并更新当前应用的商业化配置。
func (h *Handler) updateApplicationMonetization(w http.ResponseWriter, r *http.Request) {
	var input operations.ApplicationMonetizationInput
	if !decode(w, r, &input) {
		return
	}
	input, err := normalizeApplicationMonetizationInput(input)
	if rejectInvalidInput(w, r, "update application monetization input rejected", err) {
		return
	}

	result, err := h.service.MonetizationService.UpdateApplicationMonetization(r.Context(), input)
	writeResult(w, r, http.StatusOK, result, err)
}

// applicationOptions 返回选择与分发候选所需安全摘要，Client Key 仅返回是否已配置，不授予应用资料读取或写入权限。
func (h *Handler) applicationOptions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.service.ApplicationService.Applications(r.Context())
	if err != nil {
		writeResult(w, r, http.StatusOK, nil, err)
		return
	}
	options := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		options = append(options, map[string]any{"id": row.ID, "subjectId": row.SubjectID, "name": row.Name, "tiktokAppId": row.TikTokAppID, "status": row.Status, "playbackProvider": row.Normalized().PlaybackProvider, "clientKeyConfigured": strings.TrimSpace(row.ClientKey) != ""})
	}
	writeResult(w, r, http.StatusOK, options, nil)
}

// reviewListTargets 只返回当前主体指定渠道的上线摘要，应用选择器不改变范围。
func (h *Handler) reviewListTargets(w http.ResponseWriter, r *http.Request) {
	var err error
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if len([]rune(query)) > 120 {
		writeResult(w, r, http.StatusOK, nil, operations.ErrInvalidInput)
		return
	}
	offset := 0
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		offset, err = readCarouselCursor(cursor)
		if err != nil {
			writeResult(w, r, http.StatusOK, nil, operations.ErrInvalidInput)
			return
		}
	}
	if h.config.Homepage == nil {
		writeResult(w, r, http.StatusOK, nil, operations.ErrInvalidInput)
		return
	}
	result, err := h.config.Homepage.Targets(r.Context(), domain.DiscoveryQuery{Query: query, Offset: offset, Limit: 20})
	writeResult(w, r, http.StatusOK, result, err)
}
