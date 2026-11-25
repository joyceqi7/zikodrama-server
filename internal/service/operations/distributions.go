package operations

import (
	"drama-server/internal/biz/operations"
	"drama-server/internal/pkg/identifier"
	"net/http"
	"strconv"
	"strings"
)

// listApplicationDramaAuthorizations 分页展示目标应用的未授权和已有授权流程短剧。
func (h *Handler) listApplicationDramaAuthorizations(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "applicationID") {
		return
	}
	input := operations.ApplicationDramaAuthorizationQuery{Page: 1, PageSize: 20, Query: strings.TrimSpace(r.URL.Query().Get("query")), View: r.URL.Query().Get("view")}
	for name, target := range map[string]*int{"page": &input.Page, "pageSize": &input.PageSize} {
		if raw := r.URL.Query().Get(name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 1000000 {
				rejectInvalidInput(w, r, "invalid application drama authorization pagination", operations.ErrInvalidInput)
				return
			}
			*target = value
		}
	}
	if input.PageSize > 100 || len(input.Query) > 200 || (input.View != "available" && input.View != "tracked") {
		rejectInvalidInput(w, r, "invalid application drama authorization filters", operations.ErrInvalidInput)
		return
	}
	page, err := h.service.DramaService.ApplicationDramaAuthorizations(r.Context(), r.PathValue("applicationID"), input)
	writeResult(w, r, http.StatusOK, page, err)
}

func (h *Handler) listDistributions(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	rows, err := h.service.DramaService.DramaDistributions(r.Context(), r.PathValue("dramaID"))
	writeResult(w, r, http.StatusOK, rows, err)
}
func (h *Handler) setDistribution(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID", "applicationID") {
		return
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Enabled == nil {
		rejectInvalidInput(w, r, "distribution enabled required", operations.ErrInvalidInput)
		return
	}
	err := h.service.DramaService.SetDramaDistribution(r.Context(), r.PathValue("dramaID"), r.PathValue("applicationID"), *input.Enabled)
	writeResult(w, r, http.StatusOK, map[string]any{}, err)
}

// authorizeApplicationDramas 在当前请求内授权明确选中的线上短剧，返回平台确认数量。
func (h *Handler) authorizeApplicationDramas(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "applicationID") {
		return
	}
	var input struct {
		DramaIDs []string `json:"dramaIds"`
	}
	if !decode(w, r, &input) {
		return
	}
	invalid := len(input.DramaIDs) == 0 || len(input.DramaIDs) > 1000
	seen := make(map[string]bool, len(input.DramaIDs))
	for i := range input.DramaIDs {
		input.DramaIDs[i] = strings.TrimSpace(input.DramaIDs[i])
		if !identifier.ValidDatabaseID(input.DramaIDs[i]) || seen[input.DramaIDs[i]] {
			invalid = true
		}
		seen[input.DramaIDs[i]] = true
	}
	if invalid {
		rejectInvalidInput(w, r, "application drama authorization selection rejected", operations.ErrInvalidInput)
		return
	}
	count, err := h.service.DramaService.AuthorizeApplicationDramas(r.Context(), r.PathValue("applicationID"), input.DramaIDs)
	writeResult(w, r, http.StatusOK, map[string]int{"authorizedCount": count}, err)
}
