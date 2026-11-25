package operations

import (
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/operations"
	"drama-server/internal/pkg/apierr"
	"drama-server/internal/pkg/identifier"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// directScope 在可信主体内固定直连运营渠道，应用选择器不能切换为官方通道。
func directScope(next http.HandlerFunc) authenticatedHandler {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := application.WithPlayback(homepage.WithManagementContext(r.Context()), "byteplus_web")
		next(w, r.WithContext(ctx))
	}
}

// registerDirectRoutes 给每个独立操作绑定精确权限，复用首页排序规则。
func (h *Handler) registerDirectRoutes() {
	register := func(pattern, permission string, handler http.HandlerFunc) {
		h.mux.HandleFunc(pattern, h.requirePermission(permission, directScope(func(w http.ResponseWriter, r *http.Request) {
			if !h.directReady(w, r) {
				return
			}
			handler(w, r)
		})))
	}
	register("GET /admin/v1/direct/dramas", "operations.direct_publication.read", h.directList)
	register("GET /admin/v1/direct/dramas/{dramaID}/publication", "operations.direct_publication.read", h.directWorkbench)
	h.mux.HandleFunc("POST /admin/v1/direct/dramas/{dramaID}/publication/operations", h.requireSession(directScope(h.directSubmit)))
	register("GET /admin/v1/direct/publication-operations/{operationID}", "operations.direct_publication.read", h.directOperation)
	for _, kind := range []string{"carousel", "hot-dramas"} {
		name := "carousel"
		read := h.carousel
		if kind == "hot-dramas" {
			name = "hot"
			read = h.hotDramas
		}
		register("GET /admin/v1/direct/"+kind, "operations.direct_"+name+".read", read)
		register("GET /admin/v1/direct/"+kind+"/targets", "operations.direct_"+name+".read", h.carouselTargets)
		register("PUT /admin/v1/direct/"+kind, "operations.direct_"+name+".update", h.saveCarousel)
	}
	register("GET /admin/v1/direct/category-rankings", "operations.direct_ranking.read", h.rankingCategories)
	register("GET /admin/v1/direct/category-rankings/{tagID}", "operations.direct_ranking.read", h.categoryRanking)
	register("PUT /admin/v1/direct/category-rankings/{tagID}", "operations.direct_ranking.update", h.saveCategoryRanking)
	register("GET /admin/v1/direct/category-rankings/{tagID}/targets", "operations.direct_ranking.read", h.rankingTargets)
	h.mux.HandleFunc("GET /admin/v1/home-preview", h.requirePermission("content.drama.read", h.homePreview))
}

// directReady 检查当前部署是否启用直连能力，关闭时记录预期配置拒绝并返回 503。
func (h *Handler) directReady(w http.ResponseWriter, r *http.Request) bool {
	if h.config.Direct == nil {
		slog.WarnContext(r.Context(), "direct playback deployment unavailable", "error", operations.ErrPublicationUnavailable)
		writeError(w, r, apierr.OperationsProviderNotConfigured)
		return false
	}
	return true
}
func (h *Handler) directList(w http.ResponseWriter, r *http.Request) {
	if !h.directReady(w, r) {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	status := r.URL.Query().Get("status")
	pending := r.URL.Query().Get("pendingChanges")
	id := r.URL.Query().Get("dramaId")
	if utf8.RuneCountInString(query) > 120 || !map[string]bool{"": true, "unpublished": true, "published": true, "offline": true, "pending": true}[status] || !map[string]bool{"": true, "true": true, "false": true}[pending] || (id != "" && !identifier.ValidDatabaseID(id)) {
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	rows, err := h.config.Direct.List(r.Context())
	// 待上线筛选合并从未上架与已下线，不改数据库发布状态或历史版本指针。
	filteredRows := rows[:0]
	for _, row := range rows {
		if id != "" && row.Draft.ID != id || query != "" && !strings.Contains(strings.ToLower(row.Draft.Title), strings.ToLower(query)) && row.Draft.ID != query || status == "pending" && row.Publication.Status == "published" || status != "" && status != "pending" && row.Publication.Status != status || pending != "" && row.Summary.PendingChanges != (pending == "true") {
			continue
		}
		filteredRows = append(filteredRows, row)
	}
	rows = filteredRows
	writeResult(w, r, 200, rows, err)
}
func (h *Handler) directWorkbench(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") || !h.directReady(w, r) {
		return
	}
	row, err := h.config.Direct.Workbench(r.Context(), r.PathValue("dramaID"))
	writeResult(w, r, 200, row, err)
}
func (h *Handler) directSubmit(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") || !h.directReady(w, r) {
		return
	}
	var c directplay.Command
	if !decode(w, r, &c) {
		return
	}
	c.RequestID = strings.TrimSpace(c.RequestID)
	if !regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`).MatchString(c.RequestID) || c.ExpectedRevision < 0 || c.ExpectedRevision >= 9007199254740991 || (c.Kind != "publish" && c.Kind != "offline" && c.Kind != "relist") {
		slog.WarnContext(r.Context(), "direct publication command input rejected", "error", directplay.ErrInvalid)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	// 动作由命令选择后再次执行精确授权和统一审计，不能由查看权限代替。
	permissionKind := c.Kind
	if permissionKind == "relist" {
		permissionKind = "publish" // 恢复原版本沿用直连上线权限，不授予新的隐式能力。
	}
	h.requirePermission("operations.direct_publication."+permissionKind, func(w http.ResponseWriter, r *http.Request) {
		row, err := h.config.Direct.Submit(r.Context(), r.PathValue("dramaID"), c, operations.PublicationActor(r.Context()))
		writeReadinessResult(w, r, row, err)
	})(w, r)
}
func (h *Handler) directOperation(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "operationID") || !h.directReady(w, r) {
		return
	}
	row, err := h.config.Direct.Operation(r.Context(), r.PathValue("operationID"))
	writeResult(w, r, 200, row, err)
}

// homePreview 使用真实应用过滤预览，运营配置范围仍是固定渠道。
func (h *Handler) homePreview(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("applicationId")
	if !identifier.ValidDatabaseID(id) || h.config.Homepage == nil {
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	app, err := h.service.FindApplication(r.Context(), id)
	if err != nil {
		writeOperationsError(w, r, err)
		return
	}
	direct := strings.Contains(r.URL.Path, "/direct/")
	if app.SubjectID != application.SubjectIDFromContext(r.Context()) || (app.PlaybackProvider == "byteplus_web") != direct {
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	// 从新上下文安装应用，移除管理目录的全渠道授权。
	ctx := application.ContextForApplication(homepage.WithoutManagementContext(r.Context()), *app)
	// 页面读取权限不隐式授予其他栏目；缺少权限时不读取其配置或泄露过滤条目。
	session, _ := r.Context().Value(adminSessionContextKey{}).(adminrbac.Session)
	permissions := []string{adminrbac.PermissionCarouselRead, adminrbac.PermissionHotRead, adminrbac.PermissionRankingRead}
	if direct {
		permissions = []string{"operations.direct_carousel.read", "operations.direct_hot.read", "operations.direct_ranking.read"}
	}
	allowed := make([]bool, 3)
	omitted := []string{}
	for i, permission := range permissions {
		allowed[i] = session.User.IsSuper || slices.Contains(session.Permissions, permission)
		if !allowed[i] {
			omitted = append(omitted, []string{"carousel", "hot", "rankings"}[i])
		}
	}
	carousel, hot := []consumer.DramaSummary{}, []consumer.DramaSummary{}
	rankings := []homepage.RankingSection{}
	if allowed[0] {
		carousel, err = h.config.Homepage.HomeCarousel(ctx)
		if err != nil {
			writeOperationsError(w, r, err)
			return
		}
	}
	if allowed[1] {
		hot, err = h.config.Homepage.HomeHot(ctx)
		if err != nil {
			writeOperationsError(w, r, err)
			return
		}
	}
	if allowed[2] {
		rankings, err = h.config.Homepage.HomeRankings(ctx)
		if err != nil {
			writeOperationsError(w, r, err)
			return
		}
	}
	// 预览保留过滤说明；停用应用始终没有消费者可见内容。
	type exclusion struct {
		Module  string `json:"module"`
		DramaID string `json:"dramaId"`
		Title   string `json:"title"`
		Reason  string `json:"reason"`
	}
	filtered := []exclusion{}
	management := homepage.WithManagementContext(ctx)
	c, hotConfig := homepage.CarouselView{}, homepage.CarouselView{}
	if allowed[0] {
		c, err = h.config.Homepage.Carousel(management)
		if err != nil {
			writeOperationsError(w, r, err)
			return
		}
	}
	if allowed[1] {
		hotConfig, err = h.config.Homepage.Hot(management)
		if err != nil {
			writeOperationsError(w, r, err)
			return
		}
	}
	inspect := func(module string, id, title string, visible bool, channelExists bool, actual bool) {
		reason := ""
		if app.Status != application.StatusActive {
			reason = "application_disabled"
		} else if !visible {
			reason = "hidden"
		} else if !channelExists {
			reason = "offline_or_tag_changed"
		} else if !actual {
			if direct {
				reason = "not_enabled_for_application"
			} else {
				reason = "official_authorization_pending"
			}
		}
		if reason != "" {
			filtered = append(filtered, exclusion{module, id, title, reason})
		}
	}
	contains := func(values []consumer.DramaSummary, id string) bool {
		for _, d := range values {
			if d.ID == id {
				return true
			}
		}
		return false
	}
	for _, section := range []struct {
		name   string
		config homepage.CarouselView
		actual []consumer.DramaSummary
	}{{"carousel", c, carousel}, {"hot", hotConfig, hot}} {
		titles := map[string]string{}
		for _, d := range section.config.Dramas {
			titles[d.ID] = d.Title
		}
		for _, item := range section.config.Items {
			inspect(section.name, item.DramaID, titles[item.DramaID], item.Visible, contains(section.config.Dramas, item.DramaID), contains(section.actual, item.DramaID))
		}
	}
	categories := []homepage.RankingCategory{}
	if allowed[2] {
		categories, err = h.config.Homepage.RankingCategories(management)
		if err != nil {
			writeOperationsError(w, r, err)
			return
		}
	}
	for _, category := range categories {
		if category.Count == 0 {
			continue
		}
		view, err := h.config.Homepage.CategoryRanking(management, category.Tag.ID)
		if err != nil {
			writeOperationsError(w, r, err)
			return
		}
		actual := []consumer.DramaSummary{}
		for _, ranking := range rankings {
			if ranking.TagID == category.Tag.ID {
				actual = ranking.Dramas
			}
		}
		titles := map[string]string{}
		for _, d := range view.Dramas {
			titles[d.ID] = d.Title
		}
		for _, item := range view.Items {
			inspect("category:"+category.Tag.ID, item.DramaID, titles[item.DramaID], item.Visible, contains(view.Dramas, item.DramaID), contains(actual, item.DramaID))
		}
	}
	if app.Status != application.StatusActive {
		carousel = []consumer.DramaSummary{}
		hot = []consumer.DramaSummary{}
		rankings = []homepage.RankingSection{}
	}
	writeResult(w, r, 200, map[string]any{"carousel": carousel, "hot": hot, "rankings": rankings, "filtered": filtered, "omittedSections": omitted}, nil)
}
