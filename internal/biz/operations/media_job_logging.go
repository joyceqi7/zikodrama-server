package operations

import (
	"context"
	"errors"
	"log/slog"
)

// mediaJobErrorLevel 只按实际错误分类；合并错误取最高级别，避免取消掩盖同时发生的依赖故障。
func mediaJobErrorLevel(err error) slog.Level {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		level := slog.LevelDebug
		for _, cause := range joined.Unwrap() {
			if cause != nil {
				level = max(level, mediaJobErrorLevel(cause))
			}
		}
		return level
	}
	if cause := errors.Unwrap(err); cause != nil {
		return mediaJobErrorLevel(cause)
	}
	if errors.Is(err, context.Canceled) {
		return slog.LevelInfo
	}
	if errors.Is(err, ErrMediaProcessing) {
		return slog.LevelDebug
	}
	if errors.Is(err, errMediaJobLeaseLost) || errors.Is(err, ErrConflict) || errors.Is(err, ErrInvalidInput) {
		return slog.LevelWarn
	}
	return slog.LevelError
}

// logMediaJobError 为异步错误补齐任务标识，不记录媒资地址或完整任务对象。
func logMediaJobError(ctx context.Context, operation, jobID string, err error) {
	slog.Log(ctx, mediaJobErrorLevel(err), operation, "job_id", jobID, "error", err)
}

// logMediaJobState 仅在状态提交成功后记录去向，便于区分等待、重试、终态失败和最终恢复。
func logMediaJobState(ctx context.Context, level slog.Level, operation string, job MediaJob, cause error) {
	fields := []any{"job_id", job.ID, "kind", job.Kind, "status", job.Status, "attempt", job.Attempts}
	if cause != nil {
		fields = append(fields, "error", cause)
	}
	if job.Status == MediaJobQueued || job.Status == MediaJobProcessing {
		fields = append(fields, "next_attempt_at", job.NextAttemptAt)
	}
	slog.Log(ctx, level, operation, fields...)
}
