package consumer

import (
	"context"
	"drama-server/internal/biz/application"
	"log/slog"
	"strings"
)

const (
	adLifecycleLimit = 8
	maxAdTimelineMS  = 1<<53 - 1
)

type adTimelinePoint struct {
	// AtMS 是相对同一 WebView 时间原点的单调毫秒数。
	AtMS *int64 `json:"atMs"`
	// DocumentVisibility 是观测当时的网页可见状态。
	DocumentVisibility string `json:"documentVisibility"`
	// NativeVisibility 是最后一次宿主可见信号，unknown 表示还未观测到。
	NativeVisibility string `json:"nativeVisibility"`
	// Prerendering 是浏览器报告的预渲染状态，false 与缺失不同。
	Prerendering *bool `json:"prerendering"`
}

type adLifecycleEvent struct {
	adTimelinePoint
	// Source 保留实际宿主回调或网页状态变化的来源。
	Source string `json:"source"`
}

type adTimelineRequest struct {
	// UserInitiated 区分手动重试和自动衔接。
	UserInitiated *bool `json:"userInitiated"`
	// PreviousAttemptID 关联同主体、同 SDK 上一次实际调用 show 的指标键。
	PreviousAttemptID string `json:"previousAttemptId,omitempty"`
	// PreviousCloseAtMS 是该调用收到 onClose 的单调时间，未观察到时省略。
	PreviousCloseAtMS *int64 `json:"previousCloseAtMs,omitempty"`
	// Lifecycle 按观察顺序保存关闭之后、展示之前的信号。
	Lifecycle []adLifecycleEvent `json:"lifecycle,omitempty"`
	// LifecycleTruncated 标记该区间是否存在因缓冲上限被覆盖的信号。
	LifecycleTruncated *bool `json:"lifecycleTruncated,omitempty"`
}

type adTimeline struct {
	adTimelinePoint
	// Request 仅随实际展示请求携带前次关联和关闭后的生命周期。
	Request *adTimelineRequest `json:"request,omitempty"`
}

func validAdTimelinePoint(point adTimelinePoint) bool {
	return point.AtMS != nil && *point.AtMS >= 0 && *point.AtMS <= maxAdTimelineMS && point.Prerendering != nil &&
		(point.DocumentVisibility == "visible" || point.DocumentVisibility == "hidden") &&
		(point.NativeVisibility == "visible" || point.NativeVisibility == "hidden" || point.NativeVisibility == "unknown")
}

// validateAdTimeline 校验实际 show 的观测点及前次关闭后的事件边界，拒绝无关阶段或超限序列。
func validateAdTimeline(event *metricEventInput) bool {
	timeline := event.AdTimeline
	if timeline == nil {
		return true
	}
	ad := event.Ad
	if ad == nil || ad.ShowElapsedMS == nil || !validAdTimelinePoint(timeline.adTimelinePoint) {
		return false
	}
	switch event.Kind {
	case "ad_request":
		return validateAdTimelineRequest(event.ID, *timeline.AtMS, timeline.Request)
	case "ad_shown", "ad_closed":
		return timeline.Request == nil
	case "ad_show_unknown":
		return timeline.Request == nil && (ad.Stage == "show" || ad.Stage == "destroy")
	case "ad_close_unknown":
		return timeline.Request == nil && (ad.Stage == "close" || ad.Stage == "destroy")
	case "ad_failure", "ad_cancelled":
		return timeline.Request == nil && (ad.Stage == "show" || ad.Stage == "callback") &&
			(event.Kind == "ad_failure" || ad.Reason == "user_cancel")
	default:
		return false
	}
}

func validateAdTimelineRequest(id string, showAt int64, request *adTimelineRequest) bool {
	if request == nil || request.UserInitiated == nil {
		return false
	}
	request.PreviousAttemptID = strings.TrimSpace(request.PreviousAttemptID)
	if request.PreviousAttemptID != "" && (!metricIDPattern.MatchString(request.PreviousAttemptID) || request.PreviousAttemptID == id) {
		return false
	}
	if request.PreviousCloseAtMS == nil {
		return len(request.Lifecycle) == 0 && request.LifecycleTruncated == nil
	}
	previousAt := *request.PreviousCloseAtMS
	if request.PreviousAttemptID == "" || previousAt < 0 || previousAt > showAt ||
		request.LifecycleTruncated == nil || len(request.Lifecycle) > adLifecycleLimit {
		return false
	}
	// 单调时间量化到毫秒，允许同毫秒的相邻事件按数组顺序还原。
	for _, transition := range request.Lifecycle {
		if !validAdTimelinePoint(transition.adTimelinePoint) || *transition.AtMS < previousAt || *transition.AtMS > showAt {
			return false
		}
		switch transition.Source {
		case "document":
		case "minis.show":
			if transition.NativeVisibility != "visible" {
				return false
			}
		case "minis.hide", "minis.destroy":
			if transition.NativeVisibility != "hidden" {
				return false
			}
		default:
			return false
		}
		previousAt = *transition.AtMS
	}
	return true
}

// logAdTimelines 输出已验权、已校验的阶段观测；跨请求重试按应用、用户、调用 ID 和 kind 去重查询。
func logAdTimelines(ctx context.Context, userID string, events []metricEventInput) {
	seen := make(map[[2]string]struct{})
	for _, event := range events {
		if event.AdTimeline == nil {
			continue
		}
		key := [2]string{event.ID, event.Kind}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		slog.InfoContext(ctx, "client ad timeline", "application_id", application.IDFromContext(ctx),
			"user_id", userID, "event_id", event.ID, "kind", event.Kind, "ad", event.Ad, "timeline", event.AdTimeline)
	}
}
