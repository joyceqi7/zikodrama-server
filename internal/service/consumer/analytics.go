package consumer

import (
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/visitor"
	"drama-server/internal/pkg/requesttime"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var metricIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,64}$`)

type metricEventInput struct {
	// MetricEvent 是原有统计事实，独立传给指标服务。
	domain.MetricEvent
	// AdTimeline 只进入诊断日志，不传给统计存储。
	AdTimeline *adTimeline `json:"adTimeline,omitempty"`
}

// collectMetrics 校验有限批量及事件字段；统计时间取请求接收时刻，身份仅来自已经验证的 VisitorToken。
func (h *Handler) collectMetrics(w http.ResponseWriter, r *http.Request, subject visitor.Context) {
	var input struct {
		Events []metricEventInput `json:"events"`
	}
	if !decode(w, r, &input) {
		return
	}
	now := requesttime.At(r.Context(), time.Now)
	valid := len(input.Events) > 0 && len(input.Events) <= 50
	for i := range input.Events {
		entry := &input.Events[i]
		event := &entry.MetricEvent
		event.ID = strings.TrimSpace(event.ID)
		event.Kind = strings.TrimSpace(event.Kind)
		switch event.Kind {
		case "active", "launch", "play_start", "play_complete", "rewarded_show", "rewarded_complete", "interstitial_show",
			"player_first_frame", "player_switch", "player_stall", "player_autoplay_blocked", "player_error",
			"tt_video_request_attempt", "tt_video_request_success", "tt_video_request_failure",
			"tt_episode_play_attempt", "tt_episode_play_success", "tt_episode_play_failure",
			"tt_ad_show_attempt", "tt_ad_show_success", "tt_ad_show_failure",
			"tt_video_request_unknown", "tt_episode_play_unknown", "tt_ad_show_unknown",
			"tt_video_request_skipped", "tt_episode_play_skipped", "tt_ad_show_skipped",
			"ad_trigger", "ad_create", "ad_created", "ad_request", "ad_shown", "ad_complete", "ad_closed", "ad_cancelled", "ad_failure", "ad_skipped", "ad_show_unknown", "ad_close_unknown":
		default:
			valid = false
		}
		if !metricIDPattern.MatchString(event.ID) {
			valid = false
		}
		if !validatePostback(event) || !validateAdMetric(event) || !validateAdTimeline(entry) {
			valid = false
		}
		event.OccurredAt = now
	}
	if !valid {
		slog.WarnContext(r.Context(), "consumer metric input rejected")
		writeDomainError(w, r, visitor.ErrInvalidRequest)
		return
	}
	if h.config.Analytics == nil {
		slog.ErrorContext(r.Context(), "consumer metric service unavailable", "error", domain.ErrServiceUnavailable)
		writeDomainError(w, r, domain.ErrServiceUnavailable)
		return
	}
	events := make([]domain.MetricEvent, len(input.Events))
	for i, entry := range input.Events {
		events[i] = entry.MetricEvent
	}
	if err := h.config.Analytics.Record(r.Context(), subject.VisitorID, events); err != nil {
		writeDomainError(w, r, err)
		return
	}
	logAdTimelines(r.Context(), subject.VisitorID, input.Events)
	writeData(w, r, http.StatusOK, map[string]bool{"accepted": true})
}
