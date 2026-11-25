package operations

import (
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/pkg/apierr"
	"drama-server/internal/pkg/identifier"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"
)

func (h *Handler) registerRankingRoutes() {
	h.mux.HandleFunc("GET /admin/v1/category-rankings", h.requirePermission(adminrbac.PermissionRankingRead, h.rankingCategories))
	h.mux.HandleFunc("GET /admin/v1/category-rankings/{tagID}", h.requirePermission(adminrbac.PermissionRankingRead, h.categoryRanking))
	h.mux.HandleFunc("PUT /admin/v1/category-rankings/{tagID}", h.requirePermission(adminrbac.PermissionRankingUpdate, h.saveCategoryRanking))
	h.mux.HandleFunc("GET /admin/v1/category-rankings/{tagID}/targets", h.requirePermission(adminrbac.PermissionRankingRead, h.rankingTargets))
}
func (h *Handler) rankingReady(w http.ResponseWriter, r *http.Request) bool {
	if h.config.Homepage == nil {
		slog.ErrorContext(r.Context(), "category ranking service unavailable", "error", "homepage service is not configured")
		writeError(w, r, apierr.OperationsInternal)
		return false
	}
	return true
}
func (h *Handler) rankingCategories(w http.ResponseWriter, r *http.Request) {
	if !h.rankingReady(w, r) {
		return
	}
	result, err := h.config.Homepage.RankingCategories(r.Context())
	writeResult(w, r, 200, result, err)
}
func (h *Handler) categoryRanking(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "tagID") || !h.rankingReady(w, r) {
		return
	}
	result, err := h.config.Homepage.CategoryRanking(r.Context(), r.PathValue("tagID"))
	writeResult(w, r, 200, result, err)
}
func (h *Handler) rankingTargets(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "tagID") || !h.rankingReady(w, r) {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	offset, err := readCarouselCursor(strings.TrimSpace(r.URL.Query().Get("cursor")))
	if err != nil {
		rejectInvalidInput(w, r, "invalid ranking candidate cursor", err)
		return
	}
	if utf8.RuneCountInString(query) > 120 {
		rejectInvalidInput(w, r, "invalid ranking candidate query", consumer.ErrInvalidDiscovery)
		return
	}
	result, err := h.config.Homepage.RankingTargets(r.Context(), r.PathValue("tagID"), consumer.DiscoveryQuery{Query: query, Offset: offset, Limit: 20})
	writeResult(w, r, 200, result, err)
}
func (h *Handler) saveCategoryRanking(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "tagID") || !h.rankingReady(w, r) {
		return
	}
	var input homepage.Ranking
	if !decode(w, r, &input) {
		return
	}
	invalid := input.Revision < 0 || input.Revision >= 9007199254740991 || input.Items == nil
	seen := map[string]bool{}
	for i := range input.Items {
		item := &input.Items[i]
		item.DramaID = strings.TrimSpace(item.DramaID)
		if !identifier.ValidDatabaseID(item.DramaID) || seen[item.DramaID] {
			invalid = true
		}
		seen[item.DramaID] = true
	}
	if invalid {
		slog.WarnContext(r.Context(), "invalid category ranking input", "error", adminrbac.ErrInvalidInput)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	result, err := h.config.Homepage.SaveCategoryRanking(r.Context(), r.PathValue("tagID"), input)
	if errors.Is(err, homepage.ErrConflict) {
		writeError(w, r, apierr.OperationsStateConflict)
		return
	}
	writeResult(w, r, 200, result, err)
}
