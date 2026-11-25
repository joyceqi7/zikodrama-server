package operations

import (
	"net/http"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
)

// registerUnlockStrategyRoutes 注册当前应用的策略面板接口，所有读写都要求可信应用上下文。
func (h *Handler) registerUnlockStrategyRoutes() {
	h.mux.HandleFunc("GET /admin/v1/application-strategy", h.requirePermission(adminrbac.PermissionApplicationRead, h.getApplicationStrategy))
	h.mux.HandleFunc("PUT /admin/v1/application-strategy", h.requirePermission(adminrbac.PermissionApplicationUpdate, h.saveApplicationStrategy))
}

func (h *Handler) getApplicationStrategy(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.MonetizationService.ApplicationStrategy(r.Context())
	writeResult(w, r, http.StatusOK, item, err)
}

func (h *Handler) saveApplicationStrategy(w http.ResponseWriter, r *http.Request) {
	var input monetization.ApplicationStrategyInput
	if !decode(w, r, &input) {
		return
	}
	if monetization.DefaultStrategies().Validate(input.Document) != nil || len(input.Rules) > 4 {
		rejectInvalidInput(w, r, "application strategy input rejected", operations.ErrInvalidInput)
		return
	}
	codes := map[string]bool{}
	for _, rule := range input.Document.Rules {
		if rule.Type != "rewarded_ad" {
			codes[rule.Code] = true
		}
	}
	for code, resource := range input.Rules {
		if !codes[code] || len(resource.ProductID) > 255 || resource.AdUnitID != "" {
			rejectInvalidInput(w, r, "application strategy product input rejected", operations.ErrInvalidInput)
			return
		}
	}
	item, err := h.service.MonetizationService.SaveApplicationStrategy(r.Context(), input)
	writeResult(w, r, http.StatusOK, item, err)
}
