package operations

import (
	"drama-server/internal/biz/operations"
	"encoding/base64"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/pkg/identifier"
)

// registerReportingRoutes 注册业务指标和审计查询接口。
func (h *Handler) registerReportingRoutes() {
	h.mux.HandleFunc("GET /admin/v1/ad-analytics", h.requirePermission(adminrbac.PermissionAdAnalyticsRead, h.adAnalytics))
	h.registerRewardAnalysisRoutes()
	h.registerDramaReportRoutes()
	h.registerCampaignReportRoutes()
	h.mux.HandleFunc("GET /admin/v1/dashboard", h.requirePermission(adminrbac.PermissionDashboardRead, h.dashboard))
	h.mux.HandleFunc("GET /admin/v1/tiktok-postback-logs", h.requirePermission(adminrbac.PermissionPostbackLogRead, h.tiktokPostbackLogs))
	h.mux.HandleFunc("GET /admin/v1/audit-logs", h.requirePermission(adminrbac.PermissionAuditRead, h.auditLogs))
}

// tiktokPostbackLogs 在调用业务层前校验页大小、事件、结果及绑定应用和筛选条件的游标。
func (h *Handler) tiktokPostbackLogs(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		rejectInvalidInput(w, r, "invalid TikTok postback log query encoding", err)
		return
	}
	query := operations.TikTokPostbackLogQuery{PageSize: 20, Event: strings.TrimSpace(values.Get("event")), Outcome: strings.TrimSpace(values.Get("outcome")), Scene: strings.TrimSpace(values.Get("scene")), ClientVersion: strings.TrimSpace(values.Get("clientVersion")), ErrorCode: strings.TrimSpace(values.Get("errorCode")), From: strings.TrimSpace(values.Get("from")), Through: strings.TrimSpace(values.Get("to"))}
	if raw := strings.TrimSpace(values.Get("pageSize")); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 1 || value > 100 {
			rejectInvalidInput(w, r, "invalid TikTok postback log page size", operations.ErrInvalidInput)
			return
		}
		query.PageSize = value
	}
	validEvent := query.Event == "" || query.Event == "video_play_request" || query.Event == "ep_play" || query.Event == "minis_ad_show"
	validOutcome := query.Outcome == "" || query.Outcome == "attempt" || query.Outcome == "success" || query.Outcome == "failure" || query.Outcome == "unknown" || query.Outcome == "skipped"
	validDetails := true
	switch query.Scene {
	case "", "playback", "entry_reward", "episode_unlock", "vip_reward", "home_interstitial", "pause_interstitial":
	default:
		validDetails = false
	}
	if query.ClientVersion != "" && !postbackVersionPattern.MatchString(query.ClientVersion) || query.ErrorCode != "" && !postbackCodePattern.MatchString(query.ErrorCode) {
		validDetails = false
	}
	if query.From != "" || query.Through != "" {
		from, e1 := time.Parse(time.DateOnly, query.From)
		through, e2 := time.Parse(time.DateOnly, query.Through)
		today := time.Now().UTC().Truncate(24 * time.Hour)
		if e1 != nil || e2 != nil || through.Before(from) || through.After(today) || through.Sub(from) >= 90*24*time.Hour || from.Before(today.AddDate(0, 0, -89)) {
			validDetails = false
		}
	}
	if !validEvent || !validOutcome || !validDetails || values.Has("page") {
		rejectInvalidInput(w, r, "invalid TikTok postback log filters", operations.ErrInvalidInput)
		return
	}
	if raw := strings.TrimSpace(values.Get("cursor")); raw != "" {
		if len(raw) > 768 {
			rejectInvalidInput(w, r, "TikTok postback log cursor exceeds limit", operations.ErrInvalidInput)
			return
		}
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(raw)
		if decodeErr != nil {
			rejectInvalidInput(w, r, "invalid TikTok postback log cursor encoding", decodeErr)
			return
		}
		parts := strings.Split(string(decoded), "|")
		binding := operations.TikTokPostbackFilterBinding(query)
		validBinding := (len(parts) == 7 && binding == "") || (len(parts) == 8 && binding != "" && parts[7] == binding)
		if !validBinding || parts[0] != application.IDFromContext(r.Context()) || parts[1] != query.Event || parts[2] != query.Outcome || !identifier.ValidDatabaseID(parts[4]) || !metricIDPattern.MatchString(parts[5]) || !validPostbackMetricKind(parts[6]) {
			rejectInvalidInput(w, r, "TikTok postback log cursor does not match query", operations.ErrInvalidInput)
			return
		}
		boundary, parseErr := time.Parse(time.RFC3339Nano, parts[3])
		if parseErr != nil || boundary.Year() < 1000 || boundary.Year() > 9999 || boundary.Nanosecond()%1000 != 0 {
			rejectInvalidInput(w, r, "invalid TikTok postback log cursor time", operations.ErrInvalidInput)
			return
		}
		query.BeforeReceivedAt, query.BeforeUserID, query.BeforeEventID, query.BeforeKind = boundary.UTC(), parts[4], parts[5], parts[6]
	}
	result, resultErr := h.service.TikTokPostbackLogService.ListTikTokPostbackLogs(r.Context(), query)
	writeResult(w, r, http.StatusOK, result, resultErr)
}

// postbackVersionPattern 限制公开构建修订，不接受 URL 或任意查询表达式。
var postbackVersionPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

// postbackCodePattern 只允许官方数值错误码。
var postbackCodePattern = regexp.MustCompile(`^-?[0-9]{1,10}$`)

// metricIDPattern 与消费端事实 ID 合同一致，游标只能引用已校验的事实键。
var metricIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,64}$`)

// validPostbackMetricKind 限制游标中的内部类型，禁止把任意值带入存储查询。
func validPostbackMetricKind(value string) bool {
	switch value {
	case "tt_video_request_attempt", "tt_video_request_success", "tt_video_request_failure",
		"tt_episode_play_attempt", "tt_episode_play_success", "tt_episode_play_failure",
		"tt_ad_show_attempt", "tt_ad_show_success", "tt_ad_show_failure",
		"tt_video_request_unknown", "tt_episode_play_unknown", "tt_ad_show_unknown",
		"tt_video_request_skipped", "tt_episode_play_skipped", "tt_ad_show_skipped":
		return true
	default:
		return false
	}
}

// auditLogs 返回管理端审计记录。
func (h *Handler) auditLogs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			rejectInvalidInput(w, r, "audit limit rejected", err)
			return
		}
		if parsed < 1 || parsed > 200 {
			rejectInvalidInput(w, r, "audit limit rejected", operations.ErrInvalidInput)
			return
		}
		limit = parsed
	}
	result, err := h.service.ReportingService.AuditRecords(r.Context(), limit)
	writeResult(w, r, http.StatusOK, result, err)
}

// dashboard 在入口校验闭区间日期，最大 90 天，禁止未来日期。
func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	from, e1 := time.Parse("2006-01-02", strings.TrimSpace(r.URL.Query().Get("from")))
	through, e2 := time.Parse("2006-01-02", strings.TrimSpace(r.URL.Query().Get("to")))
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if e1 != nil || e2 != nil || through.Before(from) || through.After(today) || through.Sub(from) >= 90*24*time.Hour {
		rejectInvalidInput(w, r, "dashboard date range rejected", operations.ErrInvalidInput)
		return
	}
	rankingRange := strings.TrimSpace(r.URL.Query().Get("rankingRange"))
	if rankingRange != "" && rankingRange != "range" && rankingRange != "all" {
		rejectInvalidInput(w, r, "campaign ranking range rejected", operations.ErrInvalidInput)
		return
	}
	result, err := h.service.ReportingService.Dashboard(r.Context(), from, through.AddDate(0, 0, 1), rankingRange == "all")
	writeResult(w, r, http.StatusOK, result, err)
}
