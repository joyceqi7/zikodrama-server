package operations

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/operations"
	"drama-server/internal/pkg/apierr"
	"drama-server/internal/pkg/identifier"
)

// registerContentRoutes 注册短剧、标签、发布和运营概览接口。
func (h *Handler) registerContentRoutes() {
	h.mux.HandleFunc("GET /admin/v1/dramas/{dramaID}/official-release", h.requirePermission(adminrbac.PermissionDramaRead, h.findTikTokReleaseState))
	h.mux.HandleFunc("POST /admin/v1/dramas/{dramaID}/official-release", h.requireAnyPermission([]string{adminrbac.PermissionDramaRelease, adminrbac.PermissionDramaPublish}, h.changeTikTokRelease))
	h.mux.HandleFunc("GET /admin/v1/publication-records/{kind}", h.requirePermission(adminrbac.PermissionDramaRead, h.listPublicationRecords))
	h.mux.HandleFunc("POST /admin/v1/publication-records/reviews/{recordID}/review", h.requireAnyPermission([]string{adminrbac.PermissionDramaReview, adminrbac.PermissionDramaPublish}, h.publicationRecordActionFor("reviews", "review")))
	h.mux.HandleFunc("POST /admin/v1/publication-records/reviews/{recordID}/withdraw", h.requireAnyPermission([]string{adminrbac.PermissionDramaReview, adminrbac.PermissionDramaPublish}, h.publicationRecordActionFor("reviews", "withdraw")))
	h.mux.HandleFunc("POST /admin/v1/publication-records/reviews/{recordID}/appeal", h.requireAnyPermission([]string{adminrbac.PermissionDramaReview, adminrbac.PermissionDramaPublish}, h.publicationRecordActionFor("reviews", "appeal")))
	h.mux.HandleFunc("POST /admin/v1/publication-records/reviews/{recordID}/publish", h.requireAnyPermission([]string{adminrbac.PermissionDramaRelease, adminrbac.PermissionDramaPublish}, h.publicationRecordActionFor("reviews", "publish")))
	h.mux.HandleFunc("POST /admin/v1/publication-records/reviews/{recordID}/retry", h.requireAnyPermission([]string{adminrbac.PermissionDramaSync, adminrbac.PermissionDramaReview, adminrbac.PermissionDramaPublish}, h.publicationRecordActionFor("reviews", "retry")))
	h.mux.HandleFunc("POST /admin/v1/publication-records/releases/{recordID}/retry", h.requireAnyPermission([]string{adminrbac.PermissionDramaRelease, adminrbac.PermissionDramaPublish}, h.publicationRecordActionFor("releases", "retry")))
	h.mux.HandleFunc("POST /admin/v1/publication-records/releases/{recordID}/unpublish", h.requireAnyPermission([]string{adminrbac.PermissionDramaRelease, adminrbac.PermissionDramaPublish}, h.publicationRecordActionFor("releases", "unpublish")))
	h.mux.HandleFunc("GET /admin/v1/dramas/{dramaID}/distributions", h.requirePermission(adminrbac.PermissionDramaRead, h.listDistributions))
	h.mux.HandleFunc("PUT /admin/v1/dramas/{dramaID}/distributions/{applicationID}", h.requireAnyPermission([]string{adminrbac.PermissionDramaDistribute, adminrbac.PermissionDramaPublish}, h.setDistribution))
	h.mux.HandleFunc("POST /admin/v1/applications/{applicationID}/drama-authorizations", h.requireAnyPermission([]string{adminrbac.PermissionDramaDistribute, adminrbac.PermissionDramaPublish}, h.authorizeApplicationDramas))
	h.mux.HandleFunc("GET /admin/v1/applications/{applicationID}/drama-authorizations", h.requirePermission(adminrbac.PermissionDramaRead, h.listApplicationDramaAuthorizations))
	h.mux.HandleFunc("GET /admin/v1/overview", h.requirePermission(adminrbac.PermissionDramaRead, h.overview))
	h.mux.HandleFunc("GET /admin/v1/drama-reference-data", h.requirePermission(adminrbac.PermissionDramaRead, h.dramaReferenceData))
	h.mux.HandleFunc("PUT /admin/v1/tiktok-tags/{tagID}", h.requirePermission(adminrbac.PermissionTikTokTagUpdate, h.updateTikTokTag))
	h.mux.HandleFunc("GET /admin/v1/tiktok-tags", h.requirePermission(adminrbac.PermissionTikTokTagRead, h.listTikTokTags))
	h.mux.HandleFunc("GET /admin/v1/dramas", h.requirePermission(adminrbac.PermissionDramaRead, h.listDramas))
	h.mux.HandleFunc("POST /admin/v1/dramas", h.requirePermission(adminrbac.PermissionDramaCreate, h.createDrama))
	h.mux.HandleFunc("GET /admin/v1/dramas/{dramaID}", h.requirePermission(adminrbac.PermissionDramaRead, h.findDrama))
	h.mux.HandleFunc("PUT /admin/v1/dramas/{dramaID}", h.requirePermission(adminrbac.PermissionDramaUpdate, h.updateDrama))
	h.mux.HandleFunc("PUT /admin/v1/dramas/{dramaID}/operations", h.requireAnyPermission([]string{adminrbac.PermissionDramaSync, adminrbac.PermissionDramaReview, adminrbac.PermissionDramaRelease, adminrbac.PermissionDramaDistribute, adminrbac.PermissionDramaPublish}, h.updateDramaOperations))
	h.mux.HandleFunc("POST /admin/v1/dramas/ready", h.requirePermission(adminrbac.PermissionDramaUpdate, h.prepareDramas))
	h.mux.HandleFunc("POST /admin/v1/dramas/{dramaID}/ready", h.requirePermission(adminrbac.PermissionDramaUpdate, h.prepareDrama))
	h.mux.HandleFunc("DELETE /admin/v1/dramas/{dramaID}", h.requirePermission(adminrbac.PermissionDramaDelete, h.deleteDrama))
	h.mux.HandleFunc("POST /admin/v1/dramas/{dramaID}/publications", h.requireAnyPermission([]string{adminrbac.PermissionDramaSync, adminrbac.PermissionDramaPublish}, h.submitTikTokPublication))
	h.mux.HandleFunc("POST /admin/v1/dramas/{dramaID}/publication/submit-review", h.requireAnyPermission([]string{adminrbac.PermissionDramaReview, adminrbac.PermissionDramaPublish}, h.submitTikTokReview))
	h.mux.HandleFunc("GET /admin/v1/dramas/{dramaID}/publication", h.requirePermission(adminrbac.PermissionDramaRead, h.findTikTokPublication))
	h.mux.HandleFunc("POST /admin/v1/dramas/{dramaID}/publication/appeal", h.requireAnyPermission([]string{adminrbac.PermissionDramaReview, adminrbac.PermissionDramaPublish}, h.appealTikTokReview))
	h.mux.HandleFunc("POST /admin/v1/dramas/{dramaID}/publication/withdraw", h.requireAnyPermission([]string{adminrbac.PermissionDramaReview, adminrbac.PermissionDramaPublish}, h.withdrawTikTokReview))
	h.mux.HandleFunc("POST /admin/v1/dramas/{dramaID}/publication/refresh-review", h.requireAnyPermission([]string{adminrbac.PermissionDramaReview, adminrbac.PermissionDramaPublish}, h.refreshTikTokReview))
	h.mux.HandleFunc("POST /admin/v1/dramas/{dramaID}/publications/{publicationID}/publish", h.requireAnyPermission([]string{adminrbac.PermissionDramaRelease, adminrbac.PermissionDramaPublish}, h.publishTikTokPublication))
	h.mux.HandleFunc("POST /admin/v1/dramas/{dramaID}/publication/retry", h.requireAnyPermission([]string{adminrbac.PermissionDramaSync, adminrbac.PermissionDramaReview, adminrbac.PermissionDramaRelease, adminrbac.PermissionDramaPublish}, h.retryTikTokPublication))
	h.mux.HandleFunc("POST /admin/v1/dramas/{dramaID}/unpublish", h.requireAnyPermission([]string{adminrbac.PermissionDramaRelease, adminrbac.PermissionDramaPublish}, h.unpublishDrama))
}

// overview 返回运营概览。
func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.DramaService.Overview(r.Context())
	writeResult(w, r, http.StatusOK, result, err)
}

// dramaReferenceData 返回创建和编辑短剧所需的引用数据。
func (h *Handler) dramaReferenceData(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.DramaService.DramaReferenceData(r.Context())
	writeResult(w, r, http.StatusOK, result, err)
}

// listTikTokTags 返回全局 TikTok 标签及当前主体编辑内容关联的短剧数量。
func (h *Handler) listTikTokTags(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.DramaService.ListTikTokTagSummaries(r.Context())
	writeResult(w, r, http.StatusOK, result, err)
}

// listDramas 返回短剧列表。
func (h *Handler) listDramas(w http.ResponseWriter, r *http.Request) {
	input := operations.DramaListQuery{DirectStatus: strings.TrimSpace(r.URL.Query().Get("directStatus")), Page: 1, PageSize: 20, Query: strings.TrimSpace(r.URL.Query().Get("query")), Publication: strings.TrimSpace(r.URL.Query().Get("publication")), ContentStatus: strings.TrimSpace(r.URL.Query().Get("contentStatus"))}
	for name, target := range map[string]*int{"page": &input.Page, "pageSize": &input.PageSize} {
		if raw := r.URL.Query().Get(name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 1000000 {
				rejectInvalidInput(w, r, "invalid drama pagination", operations.ErrInvalidInput)
				return
			}
			*target = value
		}
	}
	validPublication := map[string]bool{"": true, "draft": true, "published": true, "needs_action": true, "waiting_platform": true, "content_incomplete": true, "manual_attention": true, "syncing": true, "await_review": true, "under_review": true, "rejected": true, "await_publish": true, "distribution_failed": true}
	if !slices.Contains([]string{"", "unpublished", "published", "offline"}, input.DirectStatus) || input.PageSize > 100 || len(input.Query) > 200 || !validPublication[input.Publication] || !slices.Contains([]string{"", "draft", "ready"}, input.ContentStatus) {
		rejectInvalidInput(w, r, "invalid drama filters", operations.ErrInvalidInput)
		return
	}
	result, err := h.service.DramaService.ListDramaPage(r.Context(), input)
	writeResult(w, r, http.StatusOK, result, err)
}

// findDrama 返回指定短剧。
func (h *Handler) findDrama(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	result, err := h.service.DramaService.FindDrama(r.Context(), r.PathValue("dramaID"))
	writeResult(w, r, http.StatusOK, result, err)
}

// createDrama 校验并创建短剧。
func (h *Handler) createDrama(w http.ResponseWriter, r *http.Request) {
	var input operations.DramaInput
	if !decode(w, r, &input) {
		return
	}
	input, err := normalizeDramaInput(input, true)
	if rejectInvalidInput(w, r, "create drama input rejected", err) {
		return
	}

	result, err := h.service.DramaService.CreateDrama(r.Context(), input)
	writeResult(w, r, http.StatusCreated, result, err)
}

// updateDrama 校验并更新短剧。
func (h *Handler) updateDrama(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	var input operations.DramaInput
	if !decode(w, r, &input) {
		return
	}
	input, err := normalizeDramaInput(input, false)
	if rejectInvalidInput(w, r, "update drama input rejected", err) {
		return
	}

	result, err := h.service.DramaService.UpdateDrama(r.Context(), r.PathValue("dramaID"), input)
	writeResult(w, r, http.StatusOK, result, err)
}

// updateDramaOperations 独立维护发布负责人、备注和截止时间。
func (h *Handler) updateDramaOperations(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	var input operations.DramaOperationsInput
	if !decode(w, r, &input) {
		return
	}
	input.Assignee = strings.TrimSpace(input.Assignee)
	input.Note = strings.TrimSpace(input.Note)
	if len(input.Assignee) > 128 || len(input.Note) > 4000 {
		rejectInvalidInput(w, r, "update drama operations rejected", operations.ErrInvalidInput)
		return
	}
	if input.DueAt != nil {
		value := input.DueAt.UTC()
		input.DueAt = &value
	}
	result, err := h.service.DramaService.UpdateDramaOperations(r.Context(), r.PathValue("dramaID"), input)
	writeResult(w, r, http.StatusOK, result, err)
}

// deleteDrama 删除指定短剧。
func (h *Handler) deleteDrama(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	err := h.service.DramaService.DeleteDrama(r.Context(), r.PathValue("dramaID"))
	writeResult(w, r, http.StatusOK, map[string]any{}, err)
}

// submitTikTokPublication 校验并冻结保存内容，按可选转码策略同步官方；送审与上线另行发起。
func (h *Handler) submitTikTokPublication(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	var input struct {
		// Transcode 仅控制本次媒体策略，客户端不能指定拥有方或送审优先级。
		Transcode *bool `json:"transcode,omitempty"`
	}
	if !decode(w, r, &input) {
		return
	}
	result, err := h.service.DramaService.SubmitTikTokPublication(r.Context(), r.PathValue("dramaID"), operations.TikTokPublicationInput{Transcode: input.Transcode})
	writeResult(w, r, http.StatusOK, result, err)
}

// findTikTokPublication 返回短剧当前的官方发布进度。
func (h *Handler) findTikTokPublication(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	result, err := h.service.DramaService.FindTikTokPublication(r.Context(), r.PathValue("dramaID"))
	writeResult(w, r, http.StatusOK, result, err)
}

// retryTikTokPublication 人工恢复失败任务；未知 Album 更新先核对，原版本完整且未推进时由官方版本保护恢复冻结内容。
func (h *Handler) retryTikTokPublication(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	var input operations.TikTokPublicationInput
	// 旧客户端可以不传请求体；显式优先级必须通过入口枚举校验。
	if r.Body != nil && r.ContentLength != 0 {
		if !decode(w, r, &input) {
			return
		}
	}
	if input.Priority != "" {
		normalized, err := normalizePublicationInput(input)
		if err != nil {
			rejectInvalidInput(w, r, "invalid retry review priority", err)
			return
		}
		input = normalized
	}
	p, err := h.service.DramaService.FindTikTokPublication(r.Context(), r.PathValue("dramaID"))
	if err != nil {
		writeResult(w, r, http.StatusOK, nil, err)
		return
	}
	session, _ := r.Context().Value(adminSessionContextKey{}).(adminrbac.Session)
	if !canRetryPublication(session, p.Stage) {
		slog.WarnContext(r.Context(), "retry publication stage permission rejected", "error", adminrbac.ErrForbidden)
		writeError(w, r, apierr.OperationsPermissionDenied)
		return
	}
	// 核对权限后绑定同一任务，避免期间开始了新的同步而把重试作用到新内容。
	ctx := operations.WithExpectedPublication(r.Context(), p.ID)
	result, err := h.service.DramaService.RetryTikTokPublication(ctx, r.PathValue("dramaID"), input)
	writeResult(w, r, http.StatusOK, result, err)
}

// canRetryPublication 按实际故障环节检查可信会话，旧聚合发布权限保留兼容。
func canRetryPublication(session adminrbac.Session, stage string) bool {
	permission := adminrbac.PermissionDramaSync
	switch stage {
	case operations.PublicationStageSetOnlineVersion, operations.PublicationStageListAlbum, operations.PublicationStageDelistAlbum:
		permission = adminrbac.PermissionDramaRelease
	case operations.PublicationStageSubmitReview, operations.PublicationStagePollReview, operations.PublicationStageAwaitPublish, operations.PublicationStageAppealReview, operations.PublicationStagePollAppeal, operations.PublicationStageWithdrawReview:
		permission = adminrbac.PermissionDramaReview
	}
	return session.User.IsSuper || slices.Contains(session.Permissions, adminrbac.PermissionDramaPublish) || slices.Contains(session.Permissions, permission)
}

// unpublishDrama 创建 TikTok 官方下架任务，不提前移除平台事实。
func (h *Handler) unpublishDrama(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	result, err := h.service.DramaService.QueueTikTokUnpublish(r.Context(), r.PathValue("dramaID"))
	writeResult(w, r, http.StatusOK, result, err)
}

// updateTikTokTag 在 HTTP 边界校验官方 ID 格式及英文名长度，空串允许清除翻译。
func (h *Handler) updateTikTokTag(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "tagID") {
		return
	}
	var body struct {
		// EnglishName 必须显式提供，避免遗漏字段误清空已有翻译。
		EnglishName *string `json:"englishName"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.EnglishName == nil {
		rejectInvalidInput(w, r, "missing TikTok tag English name", operations.ErrInvalidInput)
		return
	}
	input := operations.TikTokTagInput{EnglishName: strings.TrimSpace(*body.EnglishName)}
	invalid := !utf8.ValidString(input.EnglishName) || utf8.RuneCountInString(input.EnglishName) > 100
	for _, ch := range input.EnglishName {
		if unicode.IsControl(ch) {
			invalid = true
		}
	}
	if invalid {
		rejectInvalidInput(w, r, "invalid TikTok tag English name", operations.ErrInvalidInput)
		return
	}
	result, err := h.service.DramaService.UpdateTikTokTag(r.Context(), r.PathValue("tagID"), input)
	writeResult(w, r, http.StatusOK, result, err)
}

// prepareDrama 校验当前保存的内容并确认就绪；任何请求都不生成版本。
func (h *Handler) prepareDrama(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	d, err := h.service.DramaService.PrepareDrama(r.Context(), r.PathValue("dramaID"))
	writeReadinessResult(w, r, d, err)
}

// prepareDramas 在入口规范化并校验整批主键，业务事务保证整批成功或整批不变。
func (h *Handler) prepareDramas(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DramaIDs []string `json:"dramaIds"`
	}
	if !decode(w, r, &input) {
		return
	}
	seen := map[string]bool{}
	valid := len(input.DramaIDs) > 0 && len(input.DramaIDs) <= 100
	for i, id := range input.DramaIDs {
		id = strings.TrimSpace(id)
		input.DramaIDs[i] = id
		if !identifier.ValidDatabaseID(id) || seen[id] {
			valid = false
		}
		seen[id] = true
	}
	if !valid {
		rejectInvalidInput(w, r, "invalid batch drama readiness", operations.ErrInvalidInput)
		return
	}
	err := h.service.DramaService.PrepareDramas(r.Context(), input.DramaIDs)
	writeReadinessResult(w, r, struct {
		ReadyCount int `json:"readyCount"`
	}{len(input.DramaIDs)}, err)
}

// writeReadinessResult 就绪拒绝只提示内容校验，不把 TK 应用配置当作草稿就绪条件。
func writeReadinessResult(w http.ResponseWriter, r *http.Request, result any, err error) {
	if errors.Is(err, operations.ErrPublicationNotReady) {
		writeError(w, r, apierr.OperationsContentNotReady)
		return
	}
	writeResult(w, r, http.StatusOK, result, err)
}
