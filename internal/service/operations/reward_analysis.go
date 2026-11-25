package operations

import (
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"drama-server/internal/pkg/identifier"
	"net/http"
	"strings"
	"time"
)

// registerRewardAnalysisRoutes 复用运营数据读取权限，所有应用仅用于只读奖励报表。
func (h *Handler) registerRewardAnalysisRoutes() {
	h.mux.HandleFunc("GET /admin/v1/reward-analysis", h.requirePermission(adminrbac.PermissionDashboardRead, h.rewardAnalysis))
	h.mux.HandleFunc("GET /admin/v1/reward-analysis/dramas/{dramaID}/episodes", h.requirePermission(adminrbac.PermissionDashboardRead, h.rewardEpisodes))
	h.mux.HandleFunc("GET /admin/v1/reward-analysis/trend", h.requirePermission(adminrbac.PermissionDashboardRead, h.rewardTrend))
}

// rewardReportInput 要求显式应用范围；全部历史仅适用于排行和分集，趋势最多 90 天。
func rewardReportInput(w http.ResponseWriter, r *http.Request, allowHistory bool) (*http.Request, time.Time, time.Time, bool) {
	q := r.URL.Query()
	scope := strings.TrimSpace(q.Get("scope"))
	app := application.IDFromContext(r.Context())
	if (scope != "all" && scope != "application") || (scope == "application" && app == "") {
		rejectInvalidInput(w, r, "reward analysis scope rejected", operations.ErrInvalidInput)
		return r, time.Time{}, time.Time{}, false
	}
	if scope == "all" {
		r = r.WithContext(application.WithContext(r.Context(), ""))
	}
	from, e1 := time.Parse(time.DateOnly, strings.TrimSpace(q.Get("from")))
	through, e2 := time.Parse(time.DateOnly, strings.TrimSpace(q.Get("to")))
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if e1 != nil || e2 != nil || from.Year() < 1000 || through.Before(from) || through.After(today) || through.Sub(from) >= 90*24*time.Hour {
		rejectInvalidInput(w, r, "reward analysis date range rejected", operations.ErrInvalidInput)
		return r, from, through, false
	}
	rangeValue := strings.TrimSpace(q.Get("range"))
	if rangeValue != "" && rangeValue != "range" && !(allowHistory && rangeValue == "all") {
		rejectInvalidInput(w, r, "reward analysis history range rejected", operations.ErrInvalidInput)
		return r, from, through, false
	}
	to := through.AddDate(0, 0, 1)
	if rangeValue == "all" {
		from = time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC)
		to = time.Now().UTC()
	}
	return r, from, to, true
}

// rewardAnalysis 返回入口已明确选择的应用及结算范围。
func (h *Handler) rewardAnalysis(w http.ResponseWriter, r *http.Request) {
	r, from, to, ok := rewardReportInput(w, r, true)
	if !ok {
		return
	}
	result, err := h.service.ReportingService.RewardAnalysis(r.Context(), from, to)
	writeResult(w, r, http.StatusOK, result, err)
}

// rewardEpisodes 返回合法共享剧的目标集奖励，不把 VIP 当成分集贡献。
func (h *Handler) rewardEpisodes(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("dramaID"))
	if !identifier.ValidDatabaseID(id) {
		rejectInvalidInput(w, r, "reward episode drama rejected", operations.ErrInvalidInput)
		return
	}
	r, from, to, ok := rewardReportInput(w, r, true)
	if !ok {
		return
	}
	result, err := h.service.ReportingService.RewardEpisodes(r.Context(), id, from, to)
	writeResult(w, r, http.StatusOK, result, err)
}

// rewardTrend 白名单校验维度和目标标识，不接受任意数据库字段。
func (h *Handler) rewardTrend(w http.ResponseWriter, r *http.Request) {
	dimension := strings.TrimSpace(r.URL.Query().Get("dimension"))
	id := strings.TrimSpace(r.URL.Query().Get("targetId"))
	if (dimension != "drama" && dimension != "campaign") || !identifier.ValidDatabaseID(id) {
		rejectInvalidInput(w, r, "reward trend target rejected", operations.ErrInvalidInput)
		return
	}
	r, from, to, ok := rewardReportInput(w, r, false)
	if !ok {
		return
	}
	result, err := h.service.ReportingService.RewardTrend(r.Context(), dimension, id, from, to)
	writeResult(w, r, http.StatusOK, result, err)
}
