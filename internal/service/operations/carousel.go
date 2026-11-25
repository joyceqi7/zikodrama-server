package operations

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/pkg/apierr"
	"drama-server/internal/pkg/identifier"
)

// registerCarouselRoutes 将轮播和人工热播的读取与修改分别绑定精确操作权限。
func (h *Handler) registerCarouselRoutes() {
	h.registerRankingRoutes()
	h.mux.HandleFunc("GET /admin/v1/carousel", h.requirePermission(adminrbac.PermissionCarouselRead, h.carousel))
	h.mux.HandleFunc("GET /admin/v1/carousel/targets", h.requirePermission(adminrbac.PermissionCarouselRead, h.carouselTargets))
	h.mux.HandleFunc("PUT /admin/v1/carousel", h.requirePermission(adminrbac.PermissionCarouselUpdate, h.saveCarousel))
	h.mux.HandleFunc("GET /admin/v1/hot-dramas", h.requirePermission(adminrbac.PermissionHotRead, h.hotDramas))
	h.mux.HandleFunc("GET /admin/v1/hot-dramas/targets", h.requirePermission(adminrbac.PermissionHotRead, h.carouselTargets))
	h.mux.HandleFunc("PUT /admin/v1/hot-dramas", h.requirePermission(adminrbac.PermissionHotUpdate, h.saveCarousel))
}

func (h *Handler) carousel(w http.ResponseWriter, r *http.Request) {
	if h.config.Homepage == nil {
		writeError(w, r, apierr.OperationsInternal)
		return
	}
	result, err := h.config.Homepage.Carousel(r.Context())
	writeResult(w, r, http.StatusOK, result, err)
}

func (h *Handler) carouselTargets(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	offset, err := readCarouselCursor(strings.TrimSpace(r.URL.Query().Get("cursor")))
	if err != nil || utf8.RuneCountInString(query) > 120 {
		slog.WarnContext(r.Context(), "recommendation targets input rejected", "error", consumer.ErrInvalidDiscovery)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	if h.config.Homepage == nil {
		writeError(w, r, apierr.OperationsInternal)
		return
	}
	result, err := h.config.Homepage.Targets(r.Context(), consumer.DiscoveryQuery{Query: query, Offset: offset, Limit: 20})
	writeResult(w, r, http.StatusOK, result, err)
}

func (h *Handler) saveCarousel(w http.ResponseWriter, r *http.Request) {
	var input homepage.Carousel
	if !decode(w, r, &input) {
		return
	}
	if input.Items != nil {
		input.DramaIDs = make([]string, len(input.Items))
		for i := range input.Items {
			input.Items[i].DramaID = strings.TrimSpace(input.Items[i].DramaID)
			input.Items[i].Title = strings.TrimSpace(input.Items[i].Title)
			if utf8.RuneCountInString(input.Items[i].Title) > 200 {
				slog.WarnContext(r.Context(), "recommendation title too long", "error", adminrbac.ErrInvalidInput)
				writeError(w, r, apierr.OperationsInvalidRequest)
				return
			}
			input.DramaIDs[i] = input.Items[i].DramaID
		}
	}
	limit := 20
	if strings.HasSuffix(r.URL.Path, "/hot-dramas") {
		limit = 15
	}
	invalid := input.Revision < 0 || input.Revision >= 9007199254740991 || input.DramaIDs == nil || len(input.DramaIDs) > limit
	seen := map[string]bool{}
	for index, id := range input.DramaIDs {
		id = strings.TrimSpace(id)
		input.DramaIDs[index] = id
		if !identifier.ValidDatabaseID(id) || seen[id] {
			invalid = true
		}
		seen[id] = true
	}
	if invalid {
		slog.WarnContext(r.Context(), "save recommendation input rejected", "error", adminrbac.ErrInvalidInput)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	if h.config.Homepage == nil {
		writeError(w, r, apierr.OperationsInternal)
		return
	}
	save := h.config.Homepage.SaveCarousel
	if strings.HasSuffix(r.URL.Path, "/hot-dramas") {
		save = h.config.Homepage.SaveHot
	}
	result, err := save(r.Context(), input)
	if errors.Is(err, homepage.ErrConflict) {
		writeError(w, r, apierr.OperationsStateConflict)
		return
	}
	writeResult(w, r, http.StatusOK, result, err)
}

// readCarouselCursor 在 HTTP 入口解析与公共目录一致的分页游标。
func readCarouselCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	if len(cursor) > 64 {
		return 0, consumer.ErrInvalidDiscovery
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || !strings.HasPrefix(string(raw), "v1:") {
		return 0, consumer.ErrInvalidDiscovery
	}
	offset, err := strconv.Atoi(strings.TrimPrefix(string(raw), "v1:"))
	if err != nil || offset < 0 {
		return 0, consumer.ErrInvalidDiscovery
	}
	return offset, nil
}

// hotDramas 返回人工配置，未配置时不从自动排行生成。
func (h *Handler) hotDramas(w http.ResponseWriter, r *http.Request) {
	if h.config.Homepage == nil {
		writeError(w, r, apierr.OperationsInternal)
		return
	}
	result, err := h.config.Homepage.Hot(r.Context())
	writeResult(w, r, http.StatusOK, result, err)
}
