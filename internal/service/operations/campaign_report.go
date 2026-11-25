package operations

import (
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/operations"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// registerCampaignReportRoutes 注册独立只读报表权限，不继承投放配置写权限。
func (h *Handler) registerCampaignReportRoutes() {
	h.mux.HandleFunc("GET /admin/v1/campaign-report/{campaignID}/trend", h.requirePermission(adminrbac.PermissionCampaignReportRead, h.campaignReportTrend))
	h.mux.HandleFunc("GET /admin/v1/campaign-report", h.requirePermission(adminrbac.PermissionCampaignReportRead, h.campaignReport))
}

// campaignReport 在调用 Service 前校验显式应用范围、日期、分页与排序白名单。
func (h *Handler) campaignReport(w http.ResponseWriter, r *http.Request) {
	r, from, to, ok := rewardReportInput(w, r, false)
	if !ok {
		return
	}
	values := r.URL.Query()
	input := operations.CampaignReportQuery{From: from, To: to, Query: strings.TrimSpace(values.Get("query")), Page: 1, PageSize: 20, Sort: strings.TrimSpace(values.Get("sort")), Direction: strings.TrimSpace(values.Get("direction"))}
	// 缺省按贡献次数降序，未知字段不能成为数据库排序表达式。
	if input.Sort == "" {
		input.Sort = "rewardCompletions"
	}
	if input.Direction == "" {
		input.Direction = "desc"
	}
	valid := utf8.RuneCountInString(input.Query) <= 100 && (input.Direction == "asc" || input.Direction == "desc") && (input.Sort == "rewardCompletions" || input.Sort == "rewardUsers" || input.Sort == "averageRewards")
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
		rejectInvalidInput(w, r, "campaign report filters rejected", operations.ErrInvalidInput)
		return
	}
	result, err := h.service.ReportingService.CampaignReport(r.Context(), input)
	writeResult(w, r, http.StatusOK, result, err)
}

// campaignReportTrend 使用本报表独立权限读取所选对象每日趋势，不要求仪表盘授权。
func (h *Handler) campaignReportTrend(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "campaignID") {
		return
	}
	r, from, to, ok := rewardReportInput(w, r, false)
	if !ok {
		return
	}
	result, err := h.service.ReportingService.RewardTrend(r.Context(), "campaign", r.PathValue("campaignID"), from, to)
	writeResult(w, r, http.StatusOK, result, err)
}
