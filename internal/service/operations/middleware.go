package operations

import (
	"context"
	"drama-server/internal/pkg/identifier"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/subject"
	"drama-server/internal/pkg/apierr"
)

// authenticatedHandler 只接收经会话/权限中间件建立可信上下文的管理请求。
type authenticatedHandler func(http.ResponseWriter, *http.Request)

// requireSession 验证 JWT、账号状态和认证版本，并把可信登录主体写入请求上下文。
func (h *Handler) requireSession(next authenticatedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, err := h.rbac.Authenticate(r.Context(), bearerToken(r))
		if err != nil {
			if errors.Is(err, adminrbac.ErrUnauthorized) {
				slog.WarnContext(r.Context(), "operations authentication rejected", "error", err)
				writeError(w, r, apierr.OperationsUnauthorized)
			} else {
				writeOperationsError(w, r, err)
			}
			return
		}
		r = r.WithContext(operations.WithPublicationActor(context.WithValue(r.Context(), adminSessionContextKey{}, session), session.User.ID))
		if scoped, ok := h.withApplicationContext(w, r); ok {
			next(w, scoped)
		}
	}
}

// requirePermission 要求当前管理端用户拥有指定权限。
func (h *Handler) requirePermission(permission string, next authenticatedHandler) http.HandlerFunc {
	return h.requireAnyPermission([]string{permission}, next)
}

// requireAnyPermission 要求当前管理端用户至少拥有一个指定权限。
// 写请求会在业务处理前后分别持久化 started 和最终审计状态。
func (h *Handler) requireAnyPermission(permissions []string, next authenticatedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var session adminrbac.Session
		err := adminrbac.ErrForbidden
		// 这些权限按“任一满足”处理；只有 Forbidden 才继续尝试，认证或依赖错误立即终止。
		for _, permission := range permissions {
			session, err = h.rbac.Authorize(r.Context(), bearerToken(r), permission)
			if err == nil || !errors.Is(err, adminrbac.ErrForbidden) {
				break
			}
		}
		if errors.Is(err, adminrbac.ErrForbidden) {
			slog.WarnContext(r.Context(), "operations permission rejected", "error", err)
			writeError(w, r, apierr.OperationsPermissionDenied)
			return
		}
		if err != nil {
			if errors.Is(err, adminrbac.ErrUnauthorized) {
				slog.WarnContext(r.Context(), "operations authentication rejected", "error", err)
				writeError(w, r, apierr.OperationsUnauthorized)
			} else {
				writeOperationsError(w, r, err)
			}
			return
		}
		r = r.WithContext(operations.WithPublicationActor(context.WithValue(r.Context(), adminSessionContextKey{}, session), session.User.ID))
		var ok bool
		r, ok = h.withApplicationContext(w, r)
		if !ok {
			return
		}
		mutation := r.Method != http.MethodGet && r.Method != http.MethodHead && h.service != nil
		if mutation && !isMediaUploadMutation(r.Pattern) && !h.allowRequest(w, r, "admin_mutation_user", session.User.ID, adminMutationLimit, adminRateWindow) {
			return
		}
		var audit operations.AuditRecord
		// 先写 started 审计，再进入具体 Handler。起始审计失败时尚未执行业务，可以安全拒绝请求。
		if mutation {
			requestID, requestIDErr := operations.NewAuditRequestID()
			if requestIDErr != nil {
				h.reportAuditError(requestIDErr)
				writeError(w, r, apierr.OperationsAuditUnavailable)
				return
			}
			audit = operations.AuditRecord{
				RequestID: requestID, ActorID: session.User.ID, ActorUsername: session.User.Username,
				ApplicationID: application.IDFromContext(r.Context()),
				Action:        auditAction(r), ResourceType: auditResourceType(r), ResourceID: auditResourceID(r),
				Result: "started", HTTPStatus: 0, ClientIP: clientIP(r), CreatedAt: time.Now().UTC(),
			}
			auditContext, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			err = h.service.ReportingService.RecordAudit(auditContext, audit)
			cancel()
			if err != nil {
				h.reportAuditError(err)
				writeError(w, r, apierr.OperationsAuditUnavailable)
				return
			}
		}
		if !mutation {
			next(w, r)
			return
		}
		// 写操作的响应先保存在内存中，以便在业务处理完成后写入准确的状态码和审计结果。
		// 业务处理可能已经提交数据库事务，因此最终审计写入失败时仍必须返回原业务响应；
		// 否则客户端会把已经成功的写操作误判为失败并重试，造成重复创建或重复变更。
		recorder := newAuditResponseWriter()
		next(recorder, r)
		result := "success"
		if recorder.status >= http.StatusBadRequest {
			result = "failed"
		}
		audit.Result = result
		audit.HTTPStatus = recorder.status
		auditContext, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		err = h.service.ReportingService.RecordAudit(auditContext, audit)
		cancel()
		if err != nil {
			h.reportAuditError(err)
		}
		recorder.flushTo(w)
	}
}

// withApplicationContext 建立运营请求的可信应用边界。
// 路径中的 applicationID 优先用于应用子资源；其余应用级接口必须显式发送
// X-Application-ID。全局目录和 RBAC 接口不伪造默认应用上下文。
func (h *Handler) withApplicationContext(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	subjectID := strings.TrimSpace(r.Header.Get("x-subject-id"))
	if subjectID != "" {
		if !identifier.ValidDatabaseID(subjectID) {
			writeError(w, r, apierr.OperationsInvalidRequest)
			return r, false
		}
		r = r.WithContext(application.WithSubject(r.Context(), subjectID))
	}

	if requiresSubjectContext(r.Pattern) {
		if subjectID == "" {
			writeError(w, r, apierr.OperationsInvalidRequest)
			return r, false
		}
		if _, err := h.service.SubjectService.Find(r.Context(), subjectID); err != nil {
			if errors.Is(err, subject.ErrNotFound) {
				writeError(w, r, apierr.OperationsInvalidRequest)
			} else {
				writeOperationsError(w, r, err)
			}
			return r, false
		}
	}

	if isSharedRecommendationRoute(r.Pattern) || strings.Contains(r.Pattern, " /admin/v1/direct/") {
		// 主体运营不以应用头切换内容范围或渠道；同主体下的应用共享此份配置。
		ctx := application.WithContext(r.Context(), "")
		return r.WithContext(homepage.WithManagementContext(ctx)), true
	}
	headerApplicationID := strings.TrimSpace(r.Header.Get("x-application-id"))
	pathApplicationID := strings.TrimSpace(r.PathValue("applicationID"))
	if pathApplicationID != "" && headerApplicationID != "" && pathApplicationID != headerApplicationID {
		writeError(w, r, apierr.OperationsInvalidApplication)
		return r, false
	}
	applicationID := pathApplicationID
	if applicationID == "" {
		applicationID = headerApplicationID
	}
	if applicationID == "" {
		if requiresApplicationContext(r.Pattern) {
			writeError(w, r, apierr.OperationsInvalidApplication)
			return r, false
		}
		return r, true
	}
	if h.service == nil || !identifier.ValidDatabaseID(applicationID) {
		writeError(w, r, apierr.OperationsInvalidApplication)
		return r, false
	}
	app, err := h.service.ApplicationService.FindApplication(r.Context(), applicationID)
	if err != nil {
		slog.ErrorContext(r.Context(), "resolve operations application failed", "error", err)
		writeError(w, r, apierr.OperationsInvalidApplication)
		return r, false
	}
	if subjectID != "" && subjectID != app.SubjectID {
		writeError(w, r, apierr.OperationsInvalidApplication)
		return r, false
	}
	return r.WithContext(application.ContextForApplication(r.Context(), *app)), true
}

// requiresApplicationContext 列出不能脱离应用执行的运营合同。
// 使用路由 Pattern 而不是 URL 前缀，避免未来新增全局接口时被意外纳入某个应用。
func requiresApplicationContext(pattern string) bool {
	switch strings.TrimSpace(pattern) {
	case "GET /admin/v1/review-settings", "PUT /admin/v1/review-settings", "GET /admin/v1/application-strategy", "PUT /admin/v1/application-strategy", "GET /admin/v1/application-monetization", "PUT /admin/v1/application-monetization",
		"POST /admin/v1/dramas/{dramaID}/campaign-link", "GET /admin/v1/campaigns", "POST /admin/v1/campaigns", "PUT /admin/v1/campaigns/{campaignID}", "POST /admin/v1/campaigns/{campaignID}/preview-link",
		"GET /admin/v1/ad-analytics", "GET /admin/v1/consumer-users", "GET /admin/v1/dashboard", "GET /admin/v1/tiktok-postback-logs":
		return true
	default:
		return false
	}
}

// isSharedRecommendationRoute 仅匹配已鉴权的共享推荐路由，不能扩大消费端或其他应用资源的权限。
func isSharedRecommendationRoute(pattern string) bool {
	switch strings.TrimSpace(pattern) {
	case "GET /admin/v1/official/review-list", "PUT /admin/v1/official/review-list", "GET /admin/v1/official/review-list/targets", "GET /admin/v1/direct/review-list", "PUT /admin/v1/direct/review-list", "GET /admin/v1/direct/review-list/targets":
		return true
	case "GET /admin/v1/publication-records/{kind}", "POST /admin/v1/publication-records/{kind}/{recordID}/{action}":
		return true
	case "GET /admin/v1/category-rankings", "GET /admin/v1/category-rankings/{tagID}", "PUT /admin/v1/category-rankings/{tagID}", "GET /admin/v1/category-rankings/{tagID}/targets":
		return true
	case "GET /admin/v1/hot-dramas", "PUT /admin/v1/hot-dramas", "GET /admin/v1/hot-dramas/targets", "GET /admin/v1/carousel", "PUT /admin/v1/carousel", "GET /admin/v1/carousel/targets":
		return true
	default:
		return false
	}
}

// requiresSubjectContext 将内容、标签关联统计和线上运营固定到选定主体，不要求配齐官方发布凭据。
func requiresSubjectContext(pattern string) bool {
	// 应用级接口由可信应用解析主体；投放链接虽位于短剧路径下，也不能要求客户端重复发送主体。
	if requiresApplicationContext(pattern) {
		return false
	}
	// 短剧、分集、封面与上传任务都属于主体；查询和直接传 ID 的操作使用同一边界。
	for _, prefix := range []string{"/admin/v1/dramas", "/admin/v1/episodes", "/admin/v1/media", "/admin/v1/covers", "/admin/v1/overview", "/admin/v1/drama-report", "/admin/v1/campaign-report", "/admin/v1/reward-analysis"} {
		if strings.Contains(pattern, " "+prefix) {
			return true
		}
	}
	if isSharedRecommendationRoute(pattern) || strings.Contains(pattern, " /admin/v1/direct/") {
		return true
	}
	switch strings.TrimSpace(pattern) {
	case "GET /admin/v1/tiktok-tags":
		return true
	case "GET /admin/v1/dramas/{dramaID}/official-release":
		return true
	case "POST /admin/v1/dramas/{dramaID}/official-release":
		return true
	case "GET /admin/v1/publication-records/{kind}":
		return true
	case "POST /admin/v1/publication-records/reviews/{recordID}/review":
		return true
	case "POST /admin/v1/publication-records/reviews/{recordID}/withdraw":
		return true
	case "POST /admin/v1/publication-records/reviews/{recordID}/appeal":
		return true
	case "POST /admin/v1/publication-records/reviews/{recordID}/publish":
		return true
	case "POST /admin/v1/publication-records/reviews/{recordID}/retry":
		return true
	case "POST /admin/v1/publication-records/releases/{recordID}/retry":
		return true
	case "POST /admin/v1/publication-records/releases/{recordID}/unpublish":
		return true
	case "GET /admin/v1/dramas/{dramaID}/distributions":
		return true
	case "POST /admin/v1/dramas/{dramaID}/publications":
		return true
	case "POST /admin/v1/dramas/{dramaID}/publication/submit-review":
		return true
	case "GET /admin/v1/dramas/{dramaID}/publication":
		return true
	case "POST /admin/v1/dramas/{dramaID}/publication/appeal":
		return true
	case "POST /admin/v1/dramas/{dramaID}/publication/withdraw":
		return true
	case "POST /admin/v1/dramas/{dramaID}/publication/refresh-review":
		return true
	case "POST /admin/v1/dramas/{dramaID}/publications/{publicationID}/publish":
		return true
	case "POST /admin/v1/dramas/{dramaID}/publication/retry":
		return true
	case "POST /admin/v1/dramas/{dramaID}/unpublish":
		return true
	}
	return false
}
