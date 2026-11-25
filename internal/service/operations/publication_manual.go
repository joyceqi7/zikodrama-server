package operations

import (
	"drama-server/internal/pkg/identifier"
	"net/http"
)

import biz "drama-server/internal/biz/operations"

// submitTikTokReview queues a previously synchronized version for review.
func (h *Handler) submitTikTokReview(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	var input biz.TikTokPublicationInput
	if !decode(w, r, &input) {
		return
	}
	input, err := normalizePublicationInput(input)
	if rejectInvalidInput(w, r, "publication review input rejected", err) {
		return
	}
	result, err := h.service.DramaService.SubmitTikTokReview(r.Context(), r.PathValue("dramaID"), input)
	writeResult(w, r, http.StatusOK, result, err)
}

// refreshTikTokReview 查询并保存当前冻结版本的官方审核事实，成功不代表已经上线。
func (h *Handler) refreshTikTokReview(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	result, err := h.service.DramaService.RefreshTikTokReview(r.Context(), r.PathValue("dramaID"))
	writeResult(w, r, http.StatusOK, result, err)
}

// publishTikTokPublication 将当前页面明确确认的任务标识交给业务门禁，返回排队结果。
func (h *Handler) publishTikTokPublication(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID", "publicationID") {
		return
	}
	result, err := h.service.DramaService.PublishTikTokPublication(r.Context(), r.PathValue("dramaID"), r.PathValue("publicationID"))
	writeResult(w, r, http.StatusOK, result, err)
}

// findTikTokReleaseState 读取与最新同步任务独立的已选版本和上下架状态。
func (h *Handler) findTikTokReleaseState(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	result, err := h.service.DramaService.FindTikTokReleaseState(r.Context(), r.PathValue("dramaID"))
	writeResult(w, r, http.StatusOK, result, err)
}

// changeTikTokRelease 只接受已选记录及动作，由数据库核验版本和轮播热播引用。
func (h *Handler) changeTikTokRelease(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	var input biz.TikTokReleaseInput
	if !decode(w, r, &input) {
		return
	}
	if input.SelectedPublicationID == "" || len(input.SelectedPublicationID) > 20 || input.Action != "list" && input.Action != "delist" {
		rejectInvalidInput(w, r, "official release input rejected", biz.ErrInvalidInput)
		return
	}
	if !identifier.ValidDatabaseID(input.SelectedPublicationID) {
		rejectInvalidInput(w, r, "official release identifier rejected", biz.ErrInvalidInput)
		return
	}

	result, err := h.service.DramaService.ChangeTikTokRelease(r.Context(), r.PathValue("dramaID"), input)
	writeResult(w, r, http.StatusOK, result, err)
}
