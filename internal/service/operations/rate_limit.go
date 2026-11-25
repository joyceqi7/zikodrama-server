package operations

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"drama-server/internal/pkg/apierr"
	"drama-server/internal/pkg/requestmeta"
)

const (
	adminLoginIPLimit      = 30
	adminLoginAccountLimit = 10
	adminMutationLimit     = 30
	adminRateWindow        = time.Minute
)

// isMediaUploadMutation 识别分集媒资的预留、续传、提交与重试请求。
// 批量上传会为每集多次写入，这些已鉴权请求不占用普通管理写操作的分钟配额。
func isMediaUploadMutation(pattern string) bool {
	switch pattern {
	case "POST /admin/v1/media-uploads/authorization",
		"POST /admin/v1/episodes/{episodeID}/media-uploads",
		"POST /admin/v1/episodes/{episodeID}/media-uploads/{uploadID}/authorization",
		"POST /admin/v1/episodes/{episodeID}/media-uploads/{uploadID}/proxy",
		"POST /admin/v1/episodes/{episodeID}/media-uploads/{uploadID}/diagnostics",
		"POST /admin/v1/episodes/{episodeID}/media/direct",
		"POST /admin/v1/media-jobs/{jobID}/retry":
		return true
	default:
		return false
	}
}

// allowRequest 执行运营端请求限流；限流器异常时记录实际错误并继续权威业务校验。
func (h *Handler) allowRequest(
	w http.ResponseWriter,
	r *http.Request,
	scope, subject string,
	limit int,
	window time.Duration,
) bool {
	if h.config.RateLimiter == nil || strings.TrimSpace(subject) == "" {
		return true
	}
	decision, err := h.config.RateLimiter.Allow(r.Context(), scope, subject, limit, window)
	if err != nil {
		slog.ErrorContext(r.Context(), "operations rate limiter unavailable", "error", err)
		return true
	}
	if decision.Allowed {
		return true
	}
	retryAfter := int((decision.RetryAfter + time.Second - 1) / time.Second)
	if retryAfter < 1 {
		retryAfter = 1
	}
	w.Header().Set("retry-after", strconv.Itoa(retryAfter))
	slog.WarnContext(r.Context(), "operations rate limit exceeded", append(requestmeta.LogFields(r.Context()),
		"flow", "operations_rate_limit", "scope", scope, "stage", "decide", "outcome", "rejected",
		"limit", limit, "retry_after_seconds", retryAfter,
	)...)
	writeError(w, r, apierr.OperationsRateLimited)
	return false
}
