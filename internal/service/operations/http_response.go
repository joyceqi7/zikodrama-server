package operations

import (
	"context"
	"drama-server/internal/biz/subject"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"drama-server/internal/biz/adminrbac"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"drama-server/internal/pkg/apierr"
	"drama-server/internal/pkg/apireply"
)

// bearerToken 从标准 Authorization 请求头读取 Bearer Token。
func bearerToken(r *http.Request) string {
	authorization := strings.TrimSpace(r.Header.Get("authorization"))
	if !strings.HasPrefix(authorization, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
}

// decode 解码单个结构化请求正文，并拒绝未知字段和多余内容。
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		slog.WarnContext(r.Context(), "decode operations request failed", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return false
	}
	// 只允许一个顶层 JSON 值；必须读到 EOF，防止正文后面藏有第二份输入。
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = operations.ErrInvalidInput
		}
		slog.WarnContext(r.Context(), "decode operations trailing content failed", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return false
	}
	return true
}

// writeResult 根据业务结果写出成功数据或标准错误。
func writeResult(w http.ResponseWriter, r *http.Request, successStatus int, data any, err error) {
	if err != nil {
		writeOperationsError(w, r, err)
		return
	}
	writeData(w, r, successStatus, data)
}

// writeData 写出统一 JSON 成功响应，并将请求 Context 传给编码及写入故障日志。
func writeData(w http.ResponseWriter, r *http.Request, status int, data any) {
	apireply.Success(r.Context(), w, status, data)
}

// writeError 通过统一错误组件写出稳定错误合同。
func writeError(w http.ResponseWriter, r *http.Request, definition apierr.Definition) {
	apierr.WriteHTTP(r.Context(), w, definition)
}

// writeOperationsError 记录实际错误并保持既有 HTTP 映射，正常业务拒绝使用 WARN。
func writeOperationsError(w http.ResponseWriter, r *http.Request, err error) {
	definition := operationsErrorDefinition(err)
	level := slog.LevelError
	if definition.Status < http.StatusInternalServerError {
		level = slog.LevelWarn
	}
	if errors.Is(err, context.Canceled) {
		level = slog.LevelInfo
	}
	slog.Log(r.Context(), level, "operations request failed", "error", err)
	writeError(w, r, definition)
}

// operationsErrorDefinition 只提取原有错误映射，判断顺序与业务 HTTP 合同保持一致。
func operationsErrorDefinition(err error) apierr.Definition {
	var platformErr *operations.PlatformCallError
	unconfirmedAlbum := errors.As(err, &platformErr) && platformErr.Code == "album_update_unconfirmed"
	switch {
	case unconfirmedAlbum:
		return apierr.OperationsAlbumUpdateUnconfirmed
	case errors.Is(err, homepage.ErrDramaRecommended):
		return apierr.OperationsPublicationRecommended
	case errors.Is(err, directplay.ErrConflict) || errors.Is(err, homepage.ErrConflict):
		return apierr.OperationsStateConflict
	case errors.Is(err, directplay.ErrInvalid):
		return apierr.OperationsInvalidRequest
	case errors.Is(err, globalconfig.ErrInvalid):
		return apierr.OperationsInvalidRequest
	case errors.Is(err, globalconfig.ErrNotFound):
		return apierr.OperationsNotFound
	case errors.Is(err, adminrbac.ErrNotFound):
		return apierr.OperationsNotFound
	case errors.Is(err, adminrbac.ErrInvalidInput):
		return apierr.OperationsInvalidRequest
	case errors.Is(err, adminrbac.ErrConflict):
		return apierr.OperationsStateConflict
	case errors.Is(err, adminrbac.ErrLastSuperAdmin):
		return apierr.OperationsLastSuperAdmin
	case errors.Is(err, adminrbac.ErrUnauthorized):
		return apierr.OperationsUnauthorized
	case errors.Is(err, adminrbac.ErrForbidden):
		return apierr.OperationsPermissionDenied
	case errors.Is(err, domain.ErrNotFound):
		return apierr.OperationsNotFound
	case errors.Is(err, operations.ErrInvalidInput):
		return apierr.OperationsInvalidRequest
	case errors.Is(err, operations.ErrMediaNotReady):
		return apierr.OperationsMediaNotReady
	case errors.Is(err, operations.ErrPublicationNotReady):
		return apierr.OperationsPublicationNotReady
	case errors.Is(err, operations.ErrMediaUploadDiscarded):
		return apierr.OperationsMediaUploadDiscarded
	case errors.Is(err, operations.ErrApplicationAdUnitsIncomplete):
		return apierr.OperationsApplicationAdUnitsIncomplete
	case errors.Is(err, monetization.ErrApplicationModeConflict):
		return apierr.OperationsApplicationModeConflict
	case errors.Is(err, operations.ErrConflict):
		return apierr.OperationsStateConflict
	case errors.Is(err, subject.ErrNotConfigured):
		return apierr.OperationsProviderNotConfigured
	case errors.Is(err, subject.ErrNotFound), errors.Is(err, subject.ErrInvalid):
		return apierr.OperationsInvalidRequest
	case errors.Is(err, subject.ErrConflict):
		return apierr.OperationsStateConflict
	case errors.Is(err, operations.ErrMediaNotConfigured):
		return apierr.OperationsProviderNotConfigured
	case errors.Is(err, operations.ErrPublicationUnavailable):
		return apierr.OperationsProviderNotConfigured
	case errors.Is(err, operations.ErrCampaignLinkUnavailable):
		return apierr.OperationsCampaignLinkUnavailable
	default:
		return apierr.OperationsInternal
	}
}
