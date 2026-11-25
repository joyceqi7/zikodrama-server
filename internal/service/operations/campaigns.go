package operations

import (
	"net/http"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/operations"
)

// registerCampaignRoutes 注册应用投放活动接口。
func (h *Handler) registerCampaignRoutes() {
	h.mux.HandleFunc("POST /admin/v1/dramas/{dramaID}/campaign-link", h.requirePermission(adminrbac.PermissionCampaignCreate, h.ensureDramaCampaign))
	h.mux.HandleFunc("GET /admin/v1/campaigns", h.requirePermission(adminrbac.PermissionCampaignRead, h.listCampaigns))
	h.mux.HandleFunc("GET /admin/v1/campaign-targets", h.requirePermission(adminrbac.PermissionCampaignRead, h.campaignTargets))
	h.mux.HandleFunc("POST /admin/v1/campaigns", h.requirePermission(adminrbac.PermissionCampaignCreate, h.createCampaign))
	h.mux.HandleFunc("POST /admin/v1/campaigns/{campaignID}/preview-link", h.requirePermission(adminrbac.PermissionCampaignCreate, h.previewCampaignLink))
	h.mux.HandleFunc("PUT /admin/v1/campaigns/{campaignID}", h.requirePermission(adminrbac.PermissionCampaignUpdate, h.updateCampaign))
}

// previewCampaignLink 生成当前应用启用活动的独立验证链接，不修改原活动。
func (h *Handler) previewCampaignLink(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "campaignID") {
		return
	}
	result, err := h.service.CampaignService.PreviewCampaignLink(r.Context(), r.PathValue("campaignID"))
	writeResult(w, r, http.StatusOK, result, err)
}

// campaignTargets 仅提供当前小程序已获播放授权的线上剧集，不暴露运营草稿或媒资凭据。
func (h *Handler) campaignTargets(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.CampaignService.CampaignTargets(r.Context())
	writeResult(w, r, http.StatusOK, result, err)
}

// listCampaigns 返回当前应用的投放活动列表。
func (h *Handler) listCampaigns(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.CampaignService.Campaigns(r.Context())
	writeResult(w, r, http.StatusOK, result, err)
}

// createCampaign 校验并创建投放活动。
func (h *Handler) createCampaign(w http.ResponseWriter, r *http.Request) {
	var input operations.CampaignInput
	if !decode(w, r, &input) {
		return
	}
	input, err := normalizeCampaignInput(input)
	if rejectInvalidInput(w, r, "create campaign input rejected", err) {
		return
	}

	result, err := h.service.CampaignService.CreateCampaign(r.Context(), input)
	writeResult(w, r, http.StatusCreated, result, err)
}

// updateCampaign 校验并更新投放活动。
func (h *Handler) updateCampaign(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "campaignID") {
		return
	}
	var input operations.CampaignInput
	if !decode(w, r, &input) {
		return
	}
	input, err := normalizeCampaignInput(input)
	if rejectInvalidInput(w, r, "update campaign input rejected", err) {
		return
	}

	result, err := h.service.CampaignService.UpdateCampaign(r.Context(), r.PathValue("campaignID"), input)
	writeResult(w, r, http.StatusOK, result, err)
}

// ensureDramaCampaign 校验短剧路径标识，在当前应用下复用或生成第一集投放链接。
func (h *Handler) ensureDramaCampaign(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	result, err := h.service.CampaignService.EnsureDramaCampaign(r.Context(), r.PathValue("dramaID"))
	writeResult(w, r, http.StatusOK, result, err)
}
