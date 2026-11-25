package operations

import (
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/operations"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// registerDramaReportRoutes 使用独立报表读取权限，不扩大原仪表盘授权。
func (h *Handler) registerDramaReportRoutes() {
	h.mux.HandleFunc("GET /admin/v1/drama-report/{dramaID}/trend", h.requirePermission(adminrbac.PermissionDramaReportRead, h.dramaReportTrend))
	h.mux.HandleFunc("GET /admin/v1/drama-report", h.requirePermission(adminrbac.PermissionDramaReportRead, h.dramaReport))
	h.mux.HandleFunc("GET /admin/v1/drama-report/{dramaID}/episodes", h.requirePermission(adminrbac.PermissionDramaReportRead, h.dramaReportEpisodes))
}

// dramaReportEpisodes 保持分集与列表相同的日期限制，禁止绕过所选日期读取全部历史。
func (h *Handler) dramaReportEpisodes(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	r, from, to, ok := rewardReportInput(w, r, false)
	if !ok {
		return
	}
	result, err := h.service.ReportingService.RewardEpisodes(r.Context(), r.PathValue("dramaID"), from, to)
	writeResult(w, r, http.StatusOK, result, err)
}

// dramaReport 在业务层调用前校验日期、显式应用范围、分页和排序白名单。
func (h *Handler) dramaReport(w http.ResponseWriter, r *http.Request) {
	r, from, to, ok := rewardReportInput(w, r, false)
	if !ok {
		return
	}
	values := r.URL.Query()
	input := operations.DramaReportQuery{From: from, To: to, Query: strings.TrimSpace(values.Get("query")), Page: 1, PageSize: 20, Sort: strings.TrimSpace(values.Get("sort")), Direction: strings.TrimSpace(values.Get("direction"))}
	if input.Sort == "" {
		input.Sort = "unlockCount"
	}
	if input.Direction == "" {
		input.Direction = "desc"
	}
	valid := utf8.RuneCountInString(input.Query) <= 100 && (input.Direction == "asc" || input.Direction == "desc") && (input.Sort == "unlockCount" || input.Sort == "unlockUsers" || input.Sort == "averageUnlocks" || input.Sort == "playUsers" || input.Sort == "firstEpisodeCompletionRate")
	for key, target := range map[string]*int{"page": &input.Page, "pageSize": &input.PageSize} {
		if raw := strings.TrimSpace(values.Get(key)); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				valid = false
			} else {
				*target = value
			}
		}
	}
	if !valid || input.Page < 1 || input.Page > 1000000 || input.PageSize < 1 || input.PageSize > 100 {
		rejectInvalidInput(w, r, "drama report filters rejected", operations.ErrInvalidInput)
		return
	}
	result, err := h.service.ReportingService.DramaReport(r.Context(), input)
	writeResult(w, r, http.StatusOK, result, err)
}

// dramaReportTrend 使用本报表独立权限读取所选对象每日趋势，不要求仪表盘授权。
func (h *Handler) dramaReportTrend(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	r, from, to, ok := rewardReportInput(w, r, false)
	if !ok {
		return
	}
	result, err := h.service.ReportingService.RewardTrend(r.Context(), "drama", r.PathValue("dramaID"), from, to)
	writeResult(w, r, http.StatusOK, result, err)
}
