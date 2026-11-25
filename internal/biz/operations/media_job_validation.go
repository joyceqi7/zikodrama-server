package operations

import "time"

// ValidateStorageRules 校验所有媒资任务写入共用的类型和处理区间，替代数据库 CHECK。
// 预留及删除任务允许尚未设置处理时间，两端齐全时按 MySQL 微秒精度比较。
func (job MediaJob) ValidateStorageRules() error {
	switch job.Kind {
	case MediaJobUpload, MediaJobCover, MediaJobDelete:
	case MediaJobEntryPreview:
		if job.ExpectedVID == "" || job.SpaceName == "" || job.PreviewSeconds < 4 || job.PreviewSeconds > 300 ||
			job.ProcessingStartedAt.IsZero() || job.ProcessingDeadlineAt.IsZero() {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	if !job.ProcessingStartedAt.IsZero() && !job.ProcessingDeadlineAt.IsZero() &&
		!job.ProcessingDeadlineAt.Truncate(time.Microsecond).After(job.ProcessingStartedAt.Truncate(time.Microsecond)) {
		return ErrInvalidInput
	}
	return nil
}
