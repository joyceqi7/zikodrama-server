package operations

import (
	"bytes"
	"drama-server/internal/pkg/identifier"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"drama-server/internal/biz/operations"
)

// validatedCoverUpload 在入口校验大小、扩展名与探测到的 MIME 类型是否一致，并恢复供业务上传的读取器。
// 这里只探测文件头，不进行完整图片解码。
func validatedCoverUpload(upload operations.Upload) (operations.Upload, error) {
	if strings.TrimSpace(upload.FileName) == "" || upload.Reader == nil || upload.Size <= 0 || upload.Size > operations.MaxCoverUploadBytes {
		return operations.Upload{}, operations.ErrInvalidInput
	}
	extension := strings.ToLower(filepath.Ext(filepath.Base(upload.FileName)))
	expectedType := map[string]string{
		".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp",
	}[extension]
	if expectedType == "" {
		return operations.Upload{}, operations.ErrInvalidInput
	}
	content, err := io.ReadAll(io.LimitReader(upload.Reader, operations.MaxCoverUploadBytes+1))
	if err != nil {
		return operations.Upload{}, err
	}
	if int64(len(content)) != upload.Size || int64(len(content)) > operations.MaxCoverUploadBytes || http.DetectContentType(content) != expectedType {
		return operations.Upload{}, operations.ErrInvalidInput
	}
	upload.FileName = filepath.Base(upload.FileName)
	// 校验已经消耗原始流，因此用相同字节重建 Reader；只修改文件展示名，不创建本地文件。
	upload.Reader = bytes.NewReader(content)
	return upload, nil
}

// supportedMediaExtension 检查当前直传合同允许的视频扩展名；文件真实资源信息由 Worker 向 VOD 核对。
func supportedMediaExtension(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4", ".flv", ".wmv", ".rm", ".rmvb", ".mpeg", ".mov", ".avi", ".mts", ".m4s", ".mkv":
		return true
	default:
		return false
	}
}

// normalizeReservationInput 校验浏览器上传声明；授权大小上限与媒资归属仍由业务层检查。
func normalizeReservationInput(input operations.EpisodeMediaUploadReservationInput) (operations.EpisodeMediaUploadReservationInput, error) {
	input.FileName = filepath.Base(strings.TrimSpace(input.FileName))
	if input.FileName == "" || len(input.FileName) > 255 || input.Size <= 0 || !supportedMediaExtension(input.FileName) {
		return input, operations.ErrInvalidInput
	}
	return input, nil
}

// normalizeDirectMediaInput 校验直传完成回执及幂等键，不访问媒资账本。
func normalizeDirectMediaInput(key string, input operations.DirectMediaUploadInput) (operations.DirectMediaUploadInput, error) {
	input.VID = strings.TrimSpace(input.VID)
	input.UploadID = strings.TrimSpace(input.UploadID)
	input.UploadRequestID = strings.TrimSpace(input.UploadRequestID)
	input.FileName = filepath.Base(strings.TrimSpace(input.FileName))
	// 幂等键用于重试同一上传确认；这里只验证形态，是否重复及回执是否一致由 Service 读取任务判断。
	if len(key) < 16 || len(key) > 128 || !identifier.ValidDatabaseID(input.UploadID) || input.VID == "" || len(input.VID) > 255 || strings.ContainsAny(input.VID, ",\r\n\t") || input.FileName == "" || len(input.FileName) > 255 || input.Size <= 0 || !supportedMediaExtension(input.FileName) || len(input.UploadRequestID) > 255 {
		return input, operations.ErrInvalidInput
	}
	return input, nil
}
