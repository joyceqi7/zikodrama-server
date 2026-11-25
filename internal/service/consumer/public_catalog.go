package consumer

import (
	"net/http"
)

// publicCatalog 仅开放目录读取；播放、奖励和个人权益仍走 withVisitor 验证身份。
func (h *Handler) publicCatalog(w http.ResponseWriter, r *http.Request) {
	tiktokAppID, err := readTikTokAppID(r.URL.Query().Get("tiktokAppId"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	input, err := readDiscoveryQuery(r)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	result, err := h.bootstrapUsecase.PublicCatalog(r.Context(), tiktokAppID, input)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, result)
}
