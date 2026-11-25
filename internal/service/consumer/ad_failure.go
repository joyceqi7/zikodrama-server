package consumer

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/visitor"
	"drama-server/internal/pkg/apierr"
)

// adErrorInput 是广告错误专用合同，不允许成功事件或通用日志字段。
type adErrorInput struct {
	// ID 复用广告统计调用键，不接收客户端声明的用户身份。
	ID string `json:"id"`
	// Ad 是广告入口、错误码和失败阶段的有限字段。
	Ad *domain.AdMetricDetails `json:"ad"`
	// Failure 是日志专用的固定现场，不进入业务层或统计库。
	Failure *adFailureFacts `json:"failure"`
}

// withAdErrorRateLimit 在完整鉴权前按凭据限流；Limiter 只保存摘要，不缓存身份或跳过获准请求的鉴权。
func (h *Handler) withAdErrorRateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("authorization"), "Bearer "))
		// 缺少凭据仍由统一鉴权返回 401，不占用其他主体的额度。
		if token != "" && !h.allowAdErrorRequest(w, r, "consumer_ad_error_credential", token) {
			return
		}
		next(w, r)
	}
}

// allowAdErrorRequest 使用既有配置限制单实例内的请求；超限不记录请求或凭据，也不增加拒绝日志。
func (h *Handler) allowAdErrorRequest(w http.ResponseWriter, r *http.Request, scope, subject string) bool {
	if h.config.RateLimiter == nil || h.config.AdErrorRequestsPerMinute <= 0 {
		slog.ErrorContext(r.Context(), "ad error logging unavailable", "error", "rate limiter is not configured")
		writeError(w, r, apierr.ConsumerServiceUnavailable)
		return false
	}
	decision, err := h.config.RateLimiter.Allow(r.Context(), scope, subject, h.config.AdErrorRequestsPerMinute, time.Minute)
	if err != nil {
		slog.ErrorContext(r.Context(), "ad error rate limit failed", "error", err)
		writeError(w, r, apierr.ConsumerServiceUnavailable)
		return false
	}
	if !decision.Allowed {
		// 预期限流只返回重试窗口，不为每个拒绝请求追加错误日志。
		seconds := max(1, int((decision.RetryAfter+time.Second-1)/time.Second))
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeError(w, r, apierr.ConsumerRateLimited)
		return false
	}
	return true
}

// collectAdErrors 鉴权后按用户复核额度，防止更换有效凭据绕过限额；校验后只打印日志，不依赖指标入库。
func (h *Handler) collectAdErrors(w http.ResponseWriter, r *http.Request, subject visitor.Context) {
	if !h.allowAdErrorRequest(w, r, "consumer_ad_errors", application.IDFromContext(r.Context())+":"+subject.VisitorID) {
		return
	}
	var input struct {
		Events []adErrorInput `json:"events"`
	}
	// 五条固定事实最多占 8 KiB；先限流再解码，避免大包和非法请求刷日志。
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if !decode(w, r, &input) {
		return
	}
	valid := len(input.Events) > 0 && len(input.Events) <= 5
	for i := range input.Events {
		event := &input.Events[i]
		event.ID = strings.TrimSpace(event.ID)
		metric := &domain.MetricEvent{ID: event.ID, Kind: "ad_failure", Ad: event.Ad}
		if !metricIDPattern.MatchString(event.ID) || !validateAdMetric(metric) || !validateAdFailure(metric, event.Failure) {
			valid = false
			continue
		}
		// endpoint 本身表示失败，不接收成功、取消、本地跳过或展示证据。
		switch event.Ad.Reason {
		case "sdk_error", "exception", "ad_active_conflict", "container_unavailable":
		default:
			valid = false
		}
		if event.Ad.Evidence != "" {
			valid = false
		}
	}
	if !valid {
		slog.WarnContext(r.Context(), "ad error input rejected")
		writeDomainError(w, r, visitor.ErrInvalidRequest)
		return
	}
	logAdFailures(r.Context(), subject.VisitorID, input.Events)
	writeData(w, r, http.StatusOK, map[string]bool{"accepted": true})
}

// adFailureFacts 只包含一次失败的有限事实，没有历史数组、页面地址或原始 SDK 对象。
type adFailureFacts struct {
	// Error 是实际错误消息，经入口再次脱敏。
	Error string `json:"error"`
	// InnerError 独立保留 SDK 内部错误详情。
	InnerError string `json:"innerError,omitempty"`
	// ShowDocument 是 show 前的网页状态，未调用时省略。
	ShowDocument string `json:"showDocument,omitempty"`
	// ShowNative 是 show 前最后收到的宿主状态，unknown 不代表前台。
	ShowNative string `json:"showNative,omitempty"`
	// ShowPrerender 是 show 前浏览器给出的预渲染信号。
	ShowPrerender *bool `json:"showPrerender,omitempty"`
	// ErrorDocument 是失败当刻的网页状态。
	ErrorDocument string `json:"errorDocument"`
	// ErrorNative 是失败当刻最后收到的宿主状态。
	ErrorNative string `json:"errorNative"`
	// ErrorPrerender 是失败当刻浏览器给出的预渲染信号。
	ErrorPrerender bool `json:"errorPrerender"`
	// Shown 表示本次是否已经确认展示。
	Shown bool `json:"shown"`
	// UserInitiated 区分手动重试与自动触发。
	UserInitiated bool `json:"userInitiated"`
	// LockOwnerID 是处理错误、释放锁之前的本地持有者。
	LockOwnerID string `json:"lockOwnerId,omitempty"`
	// PreviousAttemptID 是同 SDK 前次实际 show 的指标键。
	PreviousAttemptID string `json:"previousAttemptId,omitempty"`
	// PreviousRelease 是前次本地释放原因，不能推断宿主空闲。
	PreviousRelease string `json:"previousRelease,omitempty"`
	// PreviousCloseMS 是失败当刻距前次 onClose 的毫秒数，不代表 show 前间隔；未收到时省略。
	PreviousCloseMS *float64 `json:"previousCloseMs,omitempty"`
}

// validateAdFailure 限定失败类型、枚举、字节数和关联键，所有自由消息在输出前再次脱敏。
func validateAdFailure(event *domain.MetricEvent, p *adFailureFacts) bool {
	if p == nil {
		return false
	}
	if event.Kind != "ad_failure" || event.Ad == nil {
		return false
	}
	for _, message := range []*string{&p.Error, &p.InnerError} {
		if !utf8.ValidString(*message) || len(*message) > 192 {
			return false
		}
		// 不能信任客户端已经脱敏；日志只接收处理后的错误消息。
		*message = postbackURLPattern.ReplaceAllString(*message, "[URL]")
		*message = postbackCookiePattern.ReplaceAllString(*message, "${1}[REDACTED]")
		*message = postbackCredentialPattern.ReplaceAllString(*message, "${1}[REDACTED]")
		// 脱敏占位符可能比原值长，按 UTF-8 边界截断，保持错误现场的消息长度上限。
		for len(*message) > 192 {
			_, size := utf8.DecodeLastRuneInString(*message)
			*message = (*message)[:len(*message)-size]
		}
	}
	if p.Error == "" {
		return false
	}
	document := func(v string) bool { return v == "visible" || v == "hidden" }
	native := func(v string) bool { return document(v) || v == "unknown" }
	if !document(p.ErrorDocument) || !native(p.ErrorNative) {
		return false
	}
	if p.ShowDocument == "" {
		if p.ShowNative != "" || p.ShowPrerender != nil {
			return false
		}
	} else if !document(p.ShowDocument) || !native(p.ShowNative) || p.ShowPrerender == nil {
		return false
	}
	if p.LockOwnerID != "" && !metricIDPattern.MatchString(p.LockOwnerID) {
		return false
	}
	if p.PreviousAttemptID == "" {
		return p.PreviousRelease == "" && p.PreviousCloseMS == nil
	}
	if !metricIDPattern.MatchString(p.PreviousAttemptID) || p.PreviousAttemptID == event.ID {
		return false
	}
	switch p.PreviousRelease {
	case "pending", "create_failed", "setup_failed", "show_rejected", "active_conflict", "closed", "destroyed", "container_unavailable", "not_visible", "stale_intent":
	default:
		return false
	}
	return p.PreviousCloseMS == nil || !math.IsNaN(*p.PreviousCloseMS) && !math.IsInf(*p.PreviousCloseMS, 0) && *p.PreviousCloseMS >= 0 && *p.PreviousCloseMS <= 86400000
}

// logAdFailures 只输出通过校验的广告错误；按调用键批内去重，不写数据库。
func logAdFailures(ctx context.Context, userID string, events []adErrorInput) {
	seen := make(map[string]bool)
	for _, event := range events {
		if event.Failure == nil || seen[event.ID] {
			continue
		}
		seen[event.ID] = true
		ad := event.Ad
		// 只取可信身份及入口白名单；不输出请求体、VisitorToken 或 SDK 对象。
		level := slog.LevelError
		if ad.Reason == "ad_active_conflict" || ad.Reason == "container_unavailable" {
			level = slog.LevelWarn
		}
		slog.Log(ctx, level, "client ad failed", "application_id", application.IDFromContext(ctx), "user_id", userID,
			"event_id", event.ID, "stage", ad.Stage, "reason", ad.Reason, "ad_type", ad.AdType, "ad_unit_id", ad.AdUnitID,
			"scene", ad.Scene, "client_version", ad.ClientVersion, "os", ad.OS, "tiktok_version", ad.TikTokVersion,
			"error_code", ad.ErrorCode, "sub_error_code", ad.SubErrorCode, "inner_error_code", ad.InnerErrorCode, "failure", event.Failure)
	}
}
