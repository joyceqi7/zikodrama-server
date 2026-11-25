package operations

import (
	"drama-server/internal/biz/application"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/operations"
	"drama-server/internal/pkg/identifier"
)

// registerConsumerUserRoutes 注册观众查询，使用独立读取权限与应用隔离。
func (h *Handler) registerConsumerUserRoutes() {
	h.mux.HandleFunc("GET /admin/v1/consumer-users", h.requirePermission(adminrbac.PermissionConsumerUserRead, h.listConsumerUsers))
}

// listConsumerUsers 在调用业务层前校验页大小、精确主键和绑定应用/筛选条件的游标。
func (h *Handler) listConsumerUsers(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		rejectInvalidInput(w, r, "invalid consumer user query encoding", err)
		return
	}
	input := operations.ConsumerUserQuery{PageSize: 20, UserID: strings.TrimSpace(values.Get("userId"))}
	if values.Has("page") {
		rejectInvalidInput(w, r, "consumer user page numbers are no longer supported", operations.ErrInvalidInput)
		return
	}
	if raw := strings.TrimSpace(values.Get("pageSize")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			rejectInvalidInput(w, r, "invalid consumer user page size", operations.ErrInvalidInput)
			return
		}
		input.PageSize = value
	}
	if input.UserID != "" && !identifier.ValidDatabaseID(input.UserID) {
		rejectInvalidInput(w, r, "invalid consumer user filters", operations.ErrInvalidInput)
		return
	}
	if raw := strings.TrimSpace(values.Get("cursor")); raw != "" {
		if len(raw) > 512 {
			rejectInvalidInput(w, r, "consumer user cursor exceeds limit", operations.ErrInvalidInput)
			return
		}
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			rejectInvalidInput(w, r, "invalid consumer user cursor encoding", err)
			return
		}
		parts := strings.Split(string(decoded), "|")
		if len(parts) != 4 || parts[0] != application.IDFromContext(r.Context()) || parts[1] != input.UserID || !identifier.ValidDatabaseID(parts[3]) {
			rejectInvalidInput(w, r, "consumer user cursor does not match query", operations.ErrInvalidInput)
			return
		}
		boundary, err := time.Parse(time.RFC3339Nano, parts[2])
		if err != nil {
			// 保留具体时间解析错误，不记录完整游标。
			rejectInvalidInput(w, r, "invalid consumer user cursor time", err)
			return
		}
		if boundary.Year() < 1000 || boundary.Year() > 9999 || boundary.Nanosecond()%1000 != 0 {
			rejectInvalidInput(w, r, "consumer user cursor time out of range", operations.ErrInvalidInput)
			return
		}
		input.AfterCreatedAt, input.AfterID = boundary.UTC(), parts[3]
	}
	result, err := h.service.ConsumerUserService.ListConsumerUsers(r.Context(), input)
	writeResult(w, r, http.StatusOK, result, err)
}
