package consumer

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/pkg/apierr"
	"drama-server/internal/pkg/identifier"
	"strings"
)

// homeCarousel 独立返回首屏轮播，使客户端无需等待热播和分类榜。
func (h *Handler) homeCarousel(w http.ResponseWriter, r *http.Request) {
	h.readHomepageSection(w, r, func(ctx context.Context) (any, error) {
		return h.config.Homepage.HomeCarousel(ctx)
	})
}

// homeHot 独立返回人工热播，使客户端可与轮播并行渲染。
func (h *Handler) homeHot(w http.ResponseWriter, r *http.Request) {
	h.readHomepageSection(w, r, func(ctx context.Context) (any, error) {
		return h.config.Homepage.HomeHot(ctx)
	})
}

// homeRankings 独立返回分类榜，较慢的榜单读取不再阻塞首屏图片。
func (h *Handler) homeRankings(w http.ResponseWriter, r *http.Request) {
	h.readHomepageSection(w, r, func(ctx context.Context) (any, error) {
		return h.config.Homepage.HomeRankings(ctx)
	})
}

// homeRankingCategories 公开读取分类目录，客户端不再为分类按钮下载全部剧目摘要。
func (h *Handler) homeRankingCategories(w http.ResponseWriter, r *http.Request) {
	h.readHomepageSection(w, r, func(ctx context.Context) (any, error) {
		return h.config.Homepage.HomeRankingCategories(ctx)
	})
}

// homeRankingPage 在入口校验分类标识和分页参数，只加载该分类的一页剧目。
func (h *Handler) homeRankingPage(w http.ResponseWriter, r *http.Request) {
	tagID := strings.TrimSpace(r.PathValue("tagID"))
	if !identifier.ValidDatabaseID(tagID) {
		writeDomainError(w, r, consumer.ErrInvalidDiscovery)
		return
	}
	input, err := readDiscoveryQuery(r)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	h.readHomepageSection(w, r, func(ctx context.Context) (any, error) {
		return h.config.Homepage.HomeRankingPage(ctx, tagID, input.Offset, input.Limit)
	})
}

// readHomepageSection 统一校验公开 AppID、安装应用时区并限制单个首页区域的读取时间。
func (h *Handler) readHomepageSection(w http.ResponseWriter, r *http.Request, load func(context.Context) (any, error)) {
	id, err := readTikTokAppID(r.URL.Query().Get("tiktokAppId"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	app, err := h.visitors.PublicApplication(r.Context(), id)
	if err != nil {
		slog.ErrorContext(r.Context(), "resolve homepage application failed", "error", err)
		writeDomainError(w, r, err)
		return
	}
	if h.config.Homepage == nil {
		writeError(w, r, apierr.ConsumerServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(application.WithSubject(application.WithPlayback(application.WithTimeZone(r.Context(), app.ID, app.TimeZone), app.PlaybackProvider), app.SubjectID), 3*time.Second)
	defer cancel()
	result, err := load(ctx)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, result)
}
