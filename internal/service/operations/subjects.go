package operations

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/subject"
	"drama-server/internal/pkg/apierr"
)

// registerSubjectRoutes 使用统一 RBAC 维护主体；顶部选项不返回发布方配置。
func (h *Handler) registerSubjectRoutes() {
	h.mux.HandleFunc("GET /admin/v1/subjects/options", h.requireSession(h.subjectOptions))
	h.mux.HandleFunc("GET /admin/v1/subjects", h.requirePermission(adminrbac.PermissionSubjectRead, h.listSubjects))
	h.mux.HandleFunc("POST /admin/v1/subjects", h.requirePermission(adminrbac.PermissionSubjectCreate, h.saveSubject))
	h.mux.HandleFunc("PUT /admin/v1/subjects/{subjectID}", h.requirePermission(adminrbac.PermissionSubjectUpdate, h.saveSubject))
}

func (h *Handler) listSubjects(w http.ResponseWriter, r *http.Request) {
	rows, err := h.service.SubjectService.List(r.Context())
	h.writeSubjects(w, r, http.StatusOK, rows, err)
}

func (h *Handler) subjectOptions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.service.SubjectService.List(r.Context())
	if err != nil {
		h.writeSubjects(w, r, http.StatusOK, nil, err)
		return
	}
	options := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		options = append(options, map[string]string{"id": row.ID, "name": row.Name})
	}
	writeResult(w, r, http.StatusOK, options, nil)
}

var publisherAppIDPattern = regexp.MustCompile(`^[0-9]{1,64}$`)

// normalizeSubjectInput 只规范标识和资料，密钥不裁剪；允许完全空白的发布方配置。
func normalizeSubjectInput(input subject.Input) (subject.Input, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Remark = strings.TrimSpace(input.Remark)
	input.TikTokAppID = strings.TrimSpace(input.TikTokAppID)
	input.ClientKey = strings.TrimSpace(input.ClientKey)
	if input.Name == "" || !utf8.ValidString(input.Name+input.Remark) || len([]rune(input.Name)) > 128 || len([]rune(input.Remark)) > 500 {
		return input, subject.ErrInvalid
	}
	if (input.ClientKey == "") != (input.TikTokAppID == "") || len(input.ClientKey) > 255 || strings.ContainsAny(input.ClientKey, "\r\n\t\x00") {
		return input, subject.ErrInvalid
	}
	if input.TikTokAppID != "" && !publisherAppIDPattern.MatchString(input.TikTokAppID) {
		return input, subject.ErrInvalid
	}
	if len(input.ClientSecret) > 255 || strings.ContainsAny(input.ClientSecret, "\r\n\t\x00") || (input.ClientSecret != "" && strings.TrimSpace(input.ClientSecret) == "") || input.ClientKey == "" && input.ClientSecret != "" {
		return input, subject.ErrInvalid
	}
	return input, nil
}

func (h *Handler) saveSubject(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut && !validatePathIDs(w, r, "subjectID") {
		return
	}
	var input subject.Input
	if !decode(w, r, &input) {
		return
	}
	input, err := normalizeSubjectInput(input)
	if err != nil {
		slog.WarnContext(r.Context(), "subject input rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	row, err := h.service.SubjectService.Save(r.Context(), r.PathValue("subjectID"), input)
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	h.writeSubjects(w, r, status, row, err)
}

func (h *Handler) writeSubjects(w http.ResponseWriter, r *http.Request, status int, data any, err error) {
	switch {
	case errors.Is(err, subject.ErrNotFound):
		writeError(w, r, apierr.OperationsInvalidRequest)
	case errors.Is(err, subject.ErrConflict):
		writeError(w, r, apierr.OperationsStateConflict)
	case errors.Is(err, subject.ErrNotConfigured):
		writeError(w, r, apierr.OperationsProviderNotConfigured)
	default:
		writeResult(w, r, status, data, err)
	}
}
