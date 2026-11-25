package operations

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"drama-server/internal/biz/mediaconfig"
	bizoperations "drama-server/internal/biz/operations"
)

const maxVODCallbackBytes int64 = 1 << 20

// registerVODCallbackRoutes 注册无需管理员会话、但必须通过 VOD 官方签名的公网回调。
func (h *Handler) registerVODCallbackRoutes() {
	if h == nil || h.mediaConfig == nil {
		return
	}
	h.mux.HandleFunc("POST /callbacks/v1/byteplus/vod", h.receiveVODCallback)
}

// receiveVODCallback 限制正文大小，先对原始字节验签，再解析并同步提交匹配任务的唤醒操作。
// 只有入口或写入失败才要求平台重试；视频就绪状态由 Worker 查询 VOD 确认。
func (h *Handler) receiveVODCallback(w http.ResponseWriter, r *http.Request) {
	if h.mediaConfig == nil || h.service == nil {
		http.Error(w, "callback unavailable", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxVODCallbackBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.WarnContext(r.Context(), "read VOD callback failed", "error", err)
	}
	if err != nil || len(body) == 0 {
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}
	// 签名针对原始字节，必须先验签再解析；不能先重编码 JSON，否则字段顺序或空白变化会影响签名。
	keyVersion, err := h.mediaConfig.VerifyCallback(
		r.Context(), r.Header.Get("X-VOD-TIMESTAMP"), r.Header.Get("X-VOD-SIGNATURE"), body,
	)
	if err != nil {
		slog.WarnContext(r.Context(), "verify VOD callback failed", "error", err)
		if errors.Is(err, mediaconfig.ErrCallbackAuth) {
			http.Error(w, "invalid callback authentication", http.StatusUnauthorized)
			return
		}
		http.Error(w, "callback unavailable", http.StatusServiceUnavailable)
		return
	}
	event, err := parseVODCallback(r.Context(), body, keyVersion)
	if err != nil {
		slog.WarnContext(r.Context(), "VOD callback input rejected", "error", err)
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}
	if _, err := h.service.DramaService.ReceiveVODCallback(r.Context(), event); err != nil {
		slog.ErrorContext(r.Context(), "receive VOD callback failed", "error", err)
		if errors.Is(err, bizoperations.ErrInvalidInput) {
			http.Error(w, "invalid callback", http.StatusBadRequest)
			return
		}
		http.Error(w, "callback unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(`{"code":0,"message":"success"}`)); err != nil {
		slog.ErrorContext(r.Context(), "write VOD callback response failed", "error", err)
	}
}
