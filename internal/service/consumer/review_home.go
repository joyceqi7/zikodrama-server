package consumer

import "net/http"

// reviewHome 是新增审核首页入口，原有首页栏目和目录接口继续提供普通内容。
func (h *Handler) reviewHome(w http.ResponseWriter, r *http.Request) {
	id, err := readTikTokAppID(r.URL.Query().Get("tiktokAppId"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	result, err := h.bootstrapUsecase.ReviewHome(r.Context(), id)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, r, http.StatusOK, result)
}
