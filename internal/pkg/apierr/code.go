// Package apierr 集中定义业务 API 错误，基础错误能力由 Styx 提供。
package apierr

import (
	"context"
	"net/http"

	styxapierror "gitee.com/linpbin/styx/apierror"
	"gitee.com/linpbin/styx/requestmeta"

	"drama-server/internal/pkg/apireply"
)

// Definition 是 Styx API 错误定义的业务别名。
type Definition = styxapierror.Definition

var definitions []Definition

func define(status int, code string) Definition {
	definition := styxapierror.MustCatalog(status, code)
	definitions = append(definitions, definition)
	return definition
}

func defineWithMessageID(status int, code, messageID string) Definition {
	definition := styxapierror.MustCatalogWithMessageID(status, code, messageID)
	definitions = append(definitions, definition)
	return definition
}

// Definitions 返回已注册错误定义的副本。
func Definitions() []Definition { return append([]Definition(nil), definitions...) }

// WriteHTTP 复用 Styx 错误目录解析安全文案，按项目统一响应合同编码。
func WriteHTTP(ctx context.Context, response http.ResponseWriter, definition Definition) {
	// request_id 通过响应头提供给 H5 和运营后台，用户无需看到内部异常即可把
	// 问题与服务端结构化日志关联起来。
	if requestID := requestmeta.RequestID(ctx); requestID != "" {
		response.Header().Set(requestmeta.HeaderName, requestID)
	}
	apireply.Write(ctx, response, definition.Status, apireply.Envelope{Code: definition.Status, Reason: definition.Code, Message: definition.Message(ctx, nil)})
}

var (
	ConsumerUnhealthy           = define(http.StatusServiceUnavailable, "unhealthy")
	ConsumerBootstrapFailed     = define(http.StatusUnauthorized, "bootstrap_failed")
	ConsumerUnauthorized        = define(http.StatusUnauthorized, "unauthorized")
	ConsumerInvalidRequest      = define(http.StatusBadRequest, "invalid_request")
	ConsumerNotFound            = define(http.StatusNotFound, "not_found")
	ConsumerInvalidRewardClaim  = define(http.StatusConflict, "invalid_reward_claim")
	ConsumerRateLimited         = define(http.StatusTooManyRequests, "rate_limited")
	ConsumerPlaybackUnavailable = define(http.StatusServiceUnavailable, "playback_unavailable")
	ConsumerInternal            = define(http.StatusInternalServerError, "internal_error")
	// ConsumerApplicationUnavailable 表示应用已停用或不存在，重新登录不会恢复服务。
	ConsumerApplicationUnavailable = define(http.StatusForbidden, "application_unavailable")
	// ConsumerApplicationForbidden 表示可信访问凭据不能用于当前应用范围。
	ConsumerApplicationForbidden = define(http.StatusForbidden, "application_forbidden")
	// ConsumerServiceUnavailable 表示身份存储等依赖故障，不要求消费者重新登录。
	ConsumerServiceUnavailable = define(http.StatusServiceUnavailable, "service_unavailable")
	// ConsumerIdentityUnavailable 表示平台登录或服务端配置故障，不等同登录码失效。
	ConsumerIdentityUnavailable = define(http.StatusServiceUnavailable, "identity_unavailable")
	// ConsumerRequestTimeout 表示服务端或依赖等待超时；客户端可保留请求等待恢复。
	ConsumerRequestTimeout = define(http.StatusGatewayTimeout, "request_timeout")
	// ConsumerRequestCancelled 单独描述取消，不伪装为超时或身份失效。
	ConsumerRequestCancelled = define(http.StatusRequestTimeout, "request_cancelled")

	OperationsInvalidCredentials = defineWithMessageID(
		http.StatusUnauthorized, "invalid_credentials", "operations_invalid_credentials",
	)
	OperationsInvalidCover = defineWithMessageID(
		http.StatusBadRequest, "invalid_cover", "operations_invalid_cover",
	)
	OperationsUnauthorized = defineWithMessageID(
		http.StatusUnauthorized, "unauthorized", "operations_unauthorized",
	)
	OperationsPermissionDenied = defineWithMessageID(
		http.StatusForbidden, "permission_denied", "operations_permission_denied",
	)
	OperationsRateLimited = defineWithMessageID(
		http.StatusTooManyRequests, "too_many_requests", "operations_rate_limited",
	)
	OperationsAuditUnavailable = defineWithMessageID(
		http.StatusServiceUnavailable, "audit_unavailable", "operations_audit_unavailable",
	)
	OperationsInvalidApplication = defineWithMessageID(
		http.StatusBadRequest, "invalid_application", "operations_invalid_application",
	)
	OperationsInvalidRequest = defineWithMessageID(
		http.StatusBadRequest, "invalid_request", "operations_invalid_request",
	)
	OperationsNotFound = defineWithMessageID(
		http.StatusNotFound, "not_found", "operations_not_found",
	)
	OperationsStateConflict = defineWithMessageID(
		http.StatusConflict, "state_conflict", "operations_state_conflict",
	)
	// OperationsAlbumUpdateUnconfirmed 提示官方更新尚未核实，保留冻结内容和恢复检查点。
	OperationsAlbumUpdateUnconfirmed = defineWithMessageID(
		http.StatusConflict, "album_update_unconfirmed", "operations_album_update_unconfirmed",
	)
	// OperationsPublicationRecommended 提示先从当前渠道的轮播或热播移除，再执行下线。
	OperationsPublicationRecommended = defineWithMessageID(
		http.StatusConflict, "publication_in_recommendations", "operations_publication_in_recommendations",
	)
	// OperationsApplicationModeConflict 提示先调整不属于当前应用模式的规则。
	OperationsApplicationModeConflict = defineWithMessageID(http.StatusConflict, "application_monetization_mode_conflict", "operations_application_mode_conflict")
	// OperationsApplicationAdUnitsIncomplete 提示每个小程序启用前补齐两种广告位。
	OperationsApplicationAdUnitsIncomplete = defineWithMessageID(
		http.StatusConflict, "application_ad_units_incomplete", "operations_application_ad_units_incomplete",
	)
	OperationsLastSuperAdmin = defineWithMessageID(
		http.StatusConflict, "last_super_admin", "operations_last_super_admin",
	)
	OperationsMediaNotReady = defineWithMessageID(
		http.StatusConflict, "media_not_ready", "operations_media_not_ready",
	)
	// OperationsContentNotReady 提示当前内容的资料或视频未就绪，与渠道发布配置无关。
	OperationsContentNotReady = defineWithMessageID(
		http.StatusConflict, "content_not_ready", "operations_content_not_ready",
	)
	// OperationsPublicationNotReady 提示短剧内容或拥有方配置尚未满足 TikTok 同步前置条件。
	OperationsPublicationNotReady = defineWithMessageID(
		http.StatusConflict, "publication_not_ready", "operations_publication_not_ready",
	)
	// OperationsCampaignLinkUnavailable 统一封装官方链接生成失败的提示。
	OperationsCampaignLinkUnavailable = defineWithMessageID(
		http.StatusServiceUnavailable, "campaign_link_unavailable", "operations_campaign_link_unavailable",
	)
	OperationsMediaUploadDiscarded = defineWithMessageID(
		http.StatusConflict, "media_upload_discarded", "operations_media_upload_discarded",
	)
	OperationsProviderNotConfigured = defineWithMessageID(
		http.StatusServiceUnavailable, "provider_not_configured", "operations_provider_not_configured",
	)
	OperationsInternal = defineWithMessageID(
		http.StatusInternalServerError, "internal_error", "operations_internal_error",
	)
)
