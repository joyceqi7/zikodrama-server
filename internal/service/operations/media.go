package operations

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/operations"
	"drama-server/internal/pkg/apierr"
)

// registerMediaRoutes 注册媒资直传和任务接口。
func (h *Handler) registerMediaRoutes() {
	h.mux.HandleFunc("POST /admin/v1/media-uploads/authorization", h.requirePermission(adminrbac.PermissionMediaUpload, h.mediaUploadAuthorization))
	h.mux.HandleFunc("POST /admin/v1/episodes/{episodeID}/media-uploads", h.requirePermission(adminrbac.PermissionMediaUpload, h.reserveEpisodeMediaUpload))
	h.mux.HandleFunc("POST /admin/v1/episodes/{episodeID}/media-uploads/{uploadID}/authorization", h.requirePermission(adminrbac.PermissionMediaUpload, h.refreshEpisodeMediaUploadAuthorization))
	h.mux.HandleFunc("POST /admin/v1/episodes/{episodeID}/media-uploads/{uploadID}/proxy", h.requirePermission(adminrbac.PermissionMediaUpload, h.proxyEpisodeMediaUpload))
	h.mux.HandleFunc("POST /admin/v1/episodes/{episodeID}/media-uploads/{uploadID}/diagnostics", h.requirePermission(adminrbac.PermissionMediaUpload, h.recordDirectUploadDiagnostic))
	h.mux.HandleFunc("POST /admin/v1/covers", h.requirePermission(adminrbac.PermissionMediaUpload, h.uploadCover))
	h.mux.HandleFunc("POST /admin/v1/episodes/{episodeID}/media/direct", h.requirePermission(adminrbac.PermissionMediaUpload, h.completeDirectEpisodeMedia))
	h.mux.HandleFunc("GET /admin/v1/dramas/{dramaID}/media-jobs", h.requirePermission(adminrbac.PermissionMediaUpload, h.listDramaMediaJobs))
	h.mux.HandleFunc("GET /admin/v1/media-jobs/{jobID}", h.requirePermission(adminrbac.PermissionMediaUpload, h.mediaJob))
	h.mux.HandleFunc("POST /admin/v1/media-jobs/{jobID}/retry", h.requirePermission(adminrbac.PermissionMediaUpload, h.retryMediaJob))
}

// refreshEpisodeMediaUploadAuthorization 为仍属于目标分集的预留任务续发短期授权，不创建重复上传任务。
func (h *Handler) refreshEpisodeMediaUploadAuthorization(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "episodeID", "uploadID") {
		return
	}
	result, err := h.service.DramaService.RefreshEpisodeMediaUploadAuthorization(r.Context(), r.PathValue("episodeID"), r.PathValue("uploadID"))
	writeResult(w, r, http.StatusOK, result, err)
}

// proxyEpisodeMediaUpload 在浏览器无法建立 VOD multipart 会话时接收原始文件，并转交服务端官方 SDK 流式上传。
func (h *Handler) proxyEpisodeMediaUpload(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "episodeID", "uploadID") {
		return
	}
	// 全局配置允许的最大单文件为 100 GiB；业务层仍按预留任务的实际大小精确校验。
	r.Body = http.MaxBytesReader(w, r.Body, 100<<30)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		slog.WarnContext(r.Context(), "parse proxied media upload failed", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		slog.WarnContext(r.Context(), "read proxied media upload file failed", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	input, err := normalizeReservationInput(operations.EpisodeMediaUploadReservationInput{FileName: header.Filename, Size: header.Size})
	if rejectInvalidInput(w, r, "proxied media upload input rejected", err) {
		_ = file.Close()
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		return
	}
	// 大文件接收可能超过 HTTP 请求超时；正文完整落盘后仍需登记任务，避免已传文件因过期 Context 被误判失败。
	job, err := h.service.DramaService.BeginProxiedEpisodeMediaUpload(context.WithoutCancel(r.Context()), r.PathValue("episodeID"), r.PathValue("uploadID"))
	if err != nil {
		_ = file.Close()
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		writeResult(w, r, http.StatusAccepted, nil, err)
		return
	}
	// 请求只负责接收文件并登记任务；远端上传脱离请求生命周期，避免客户端或网关超时中断业务任务。
	episodeID, uploadID := r.PathValue("episodeID"), r.PathValue("uploadID")
	go func() {
		background := context.Background()
		defer func() {
			if closeErr := file.Close(); closeErr != nil {
				slog.ErrorContext(background, "close proxied media upload failed", "error", closeErr)
			}
			if r.MultipartForm != nil {
				if removeErr := r.MultipartForm.RemoveAll(); removeErr != nil {
					slog.ErrorContext(background, "clean up proxied media upload failed", "error", removeErr)
				}
			}
		}()
		if _, uploadErr := h.service.DramaService.UploadEpisodeMedia(background, episodeID, uploadID, operations.Upload{FileName: input.FileName, Size: input.Size, Reader: file}); uploadErr != nil {
			slog.ErrorContext(background, "background proxy episode media upload failed", "error", uploadErr)
			if failErr := h.service.DramaService.FailProxiedEpisodeMediaUpload(background, uploadID, uploadErr); failErr != nil {
				slog.ErrorContext(background, "persist background proxy upload failure failed", "error", failErr)
			}
		}
	}()
	writeResult(w, r, http.StatusAccepted, job, nil)
}

// recordDirectUploadDiagnostic 接收浏览器直传失败的已脱敏摘要，以便服务端日志关联上传任务。
func (h *Handler) recordDirectUploadDiagnostic(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "episodeID", "uploadID") {
		return
	}
	var input operations.DirectUploadDiagnosticInput
	if !decode(w, r, &input) {
		return
	}
	input.Stage, input.Code, input.Reason = strings.TrimSpace(input.Stage), strings.TrimSpace(input.Code), strings.TrimSpace(input.Reason)
	if !validUploadDiagnosticValue(input.Stage) || !validUploadDiagnosticValue(input.Code) || !validUploadDiagnosticReason(input.Reason) || input.Status < 0 || input.Status > 599 {
		rejectInvalidInput(w, r, "direct upload diagnostic input rejected", operations.ErrInvalidInput)
		return
	}
	err := h.service.DramaService.RecordDirectUploadDiagnostic(r.Context(), r.PathValue("episodeID"), r.PathValue("uploadID"), input)
	writeResult(w, r, http.StatusOK, nil, err)
}

// validUploadDiagnosticReason 允许空值，但只保留前端脱敏后的短文本，防止错误正文写入日志。
func validUploadDiagnosticReason(value string) bool {
	if len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == ' ' || character == '_' || character == '-' || character == ':') {
			return false
		}
	}
	return true
}

// validUploadDiagnosticValue 只允许 SDK 的短稳定标识，避免把响应正文或敏感信息写入日志。
func validUploadDiagnosticValue(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

// reserveEpisodeMediaUpload 在浏览器上传文件前创建归属于目标分集的托管媒资。
func (h *Handler) reserveEpisodeMediaUpload(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "episodeID") {
		return
	}
	var input operations.EpisodeMediaUploadReservationInput
	if !decode(w, r, &input) {
		return
	}
	input, err := normalizeReservationInput(input)
	if rejectInvalidInput(w, r, "reserve episode media upload input rejected", err) {
		return
	}

	result, err := h.service.DramaService.ReserveEpisodeMediaUpload(r.Context(), r.PathValue("episodeID"), input)
	writeResult(w, r, http.StatusCreated, result, err)
}

// mediaUploadAuthorization 返回前端直传 BytePlus VOD 所需的短期授权。
func (h *Handler) mediaUploadAuthorization(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.DramaService.MediaUploadAuthorization(r.Context())
	writeResult(w, r, http.StatusOK, result, err)
}

// uploadCover 接收短剧封面并交给媒资业务处理。
func (h *Handler) uploadCover(w http.ResponseWriter, r *http.Request) {
	fileLimit := operations.MaxCoverUploadBytes
	r.Body = http.MaxBytesReader(w, r.Body, fileLimit+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		slog.WarnContext(r.Context(), "parse media upload failed", "error", err)
		writeError(w, r, apierr.OperationsInvalidCover)
		return
	}
	if r.MultipartForm != nil {
		defer func() {
			if err := r.MultipartForm.RemoveAll(); err != nil {
				slog.ErrorContext(r.Context(), "clean up multipart files failed", "error", err)
			}
		}()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		slog.WarnContext(r.Context(), "read media upload file failed", "error", err)
	}
	if err != nil {
		writeError(w, r, apierr.OperationsInvalidCover)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.ErrorContext(r.Context(), "close media upload failed", "error", err)
		}
	}()
	if header.Size <= 0 || header.Size > fileLimit {
		slog.WarnContext(r.Context(), "media upload size rejected", "error", operations.ErrInvalidInput)
		writeError(w, r, apierr.OperationsInvalidCover)
		return
	}
	upload, err := validatedCoverUpload(operations.Upload{FileName: header.Filename, Size: header.Size, Reader: file})
	if err != nil {
		slog.WarnContext(r.Context(), "cover input rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidCover)
		return
	}
	result, err := h.service.DramaService.UploadCover(r.Context(), upload)
	writeResult(w, r, http.StatusCreated, result, err)
}

// completeDirectEpisodeMedia 将已直传的剧集媒资加入处理队列。
func (h *Handler) completeDirectEpisodeMedia(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "episodeID") {
		return
	}
	var input operations.DirectMediaUploadInput
	if !decode(w, r, &input) {
		return
	}
	input, err := normalizeDirectMediaInput(strings.TrimSpace(r.Header.Get("idempotency-key")), input)
	if rejectInvalidInput(w, r, "complete direct episode media input rejected", err) {
		return
	}

	result, err := h.service.DramaService.QueueDirectEpisodeMedia(
		r.Context(), r.PathValue("episodeID"), strings.TrimSpace(r.Header.Get("idempotency-key")), input,
	)
	writeResult(w, r, http.StatusAccepted, result, err)
}

// listDramaMediaJobs 返回指定短剧的媒资任务列表。
func (h *Handler) listDramaMediaJobs(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	result, err := h.service.DramaService.ListDramaMediaJobs(r.Context(), r.PathValue("dramaID"))
	writeResult(w, r, http.StatusOK, result, err)
}

// mediaJob 返回指定媒资任务。
func (h *Handler) mediaJob(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "jobID") {
		return
	}
	result, err := h.service.DramaService.FindMediaJob(r.Context(), r.PathValue("jobID"))
	writeResult(w, r, http.StatusOK, result, err)
}

// retryMediaJob 将允许重试的媒资任务重新入队。
func (h *Handler) retryMediaJob(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "jobID") {
		return
	}
	result, err := h.service.DramaService.RetryMediaJob(r.Context(), r.PathValue("jobID"))
	writeResult(w, r, http.StatusAccepted, result, err)
}
