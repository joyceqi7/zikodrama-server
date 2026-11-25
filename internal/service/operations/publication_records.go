package operations

import (
	"drama-server/internal/biz/operations"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

func validRecordKind(kind string) bool { return kind == "reviews" || kind == "releases" }

// listPublicationRecords 在入口校验分页、状态和剧目筛选，再查询共享记录。
func (h *Handler) listPublicationRecords(w http.ResponseWriter, r *http.Request) {
	q := operations.PublicationRecordQuery{Kind: r.PathValue("kind"), Page: 1, PageSize: 20, DramaID: strings.TrimSpace(r.URL.Query().Get("dramaId")), Status: strings.TrimSpace(r.URL.Query().Get("status"))}
	if !validRecordKind(q.Kind) {
		rejectInvalidInput(w, r, "invalid publication record kind", operations.ErrInvalidInput)
		return
	}
	for key, target := range map[string]*int{"page": &q.Page, "pageSize": &q.PageSize} {
		if raw := strings.TrimSpace(r.URL.Query().Get(key)); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 1000000 {
				rejectInvalidInput(w, r, "invalid record pagination", operations.ErrInvalidInput)
				return
			}
			*target = value
		}
	}
	allowed := "|queued|processing|under_review|approved|rejected|withdrawn|failed|online|offline|publishing|unpublishing|superseded|"
	if q.PageSize > 100 || (q.Status != "" && !slices.Contains(strings.Split(allowed, "|"), q.Status)) {
		rejectInvalidInput(w, r, "invalid record filter", operations.ErrInvalidInput)
		return
	}
	if q.DramaID != "" {
		r.SetPathValue("dramaID", q.DramaID)
		if !validatePathIDs(w, r, "dramaID") {
			return
		}
	}
	result, err := h.service.DramaService.ListPublicationRecords(r.Context(), q)
	writeResult(w, r, http.StatusOK, result, err)
}

// publicationRecordAction 把操作绑定到列表中的记录ID，旧版本冲突时不操作新任务。
func (h *Handler) publicationRecordAction(w http.ResponseWriter, r *http.Request) {
	kind, action := r.PathValue("kind"), r.PathValue("action")
	allowed := map[string]bool{"review": kind == "reviews", "withdraw": kind == "reviews", "appeal": kind == "reviews", "publish": kind == "reviews", "retry": true, "unpublish": kind == "releases"}
	if !validRecordKind(kind) || !allowed[action] {
		rejectInvalidInput(w, r, "invalid record action", operations.ErrInvalidInput)
		return
	}
	if !validatePathIDs(w, r, "recordID") {
		return
	}
	record, err := h.service.DramaService.FindPublicationRecord(r.Context(), kind, r.PathValue("recordID"))
	if err != nil {
		writeResult(w, r, http.StatusOK, nil, err)
		return
	}
	r = r.WithContext(operations.WithExpectedPublication(r.Context(), record.ID))
	r.SetPathValue("dramaID", record.DramaID)
	r.SetPathValue("publicationID", record.ID)
	switch action {
	case "review":
		h.refreshTikTokReview(w, r)
	case "withdraw":
		h.withdrawTikTokReview(w, r)
	case "appeal":
		h.appealTikTokReview(w, r)
	case "publish":
		h.publishTikTokPublication(w, r)
	case "retry":
		h.retryTikTokPublication(w, r)
	case "unpublish":
		h.unpublishDrama(w, r)
	}
}

// publicationRecordActionFor 将固定路由恢复为统一处理器使用的流程与动作参数，权限在路由层逐项校验。
func (h *Handler) publicationRecordActionFor(kind, action string) authenticatedHandler {
	return func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("kind", kind)
		r.SetPathValue("action", action)
		h.publicationRecordAction(w, r)
	}
}
