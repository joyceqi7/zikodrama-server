package operations

import (
	"context"
	"fmt"
)

// processEntryPreview 将剪辑提交和轮询分开，持久化官方请求 ID 后交回原队列安排下一次观察。
func (runner *mediaJobRunner) processEntryPreview(ctx context.Context, job *MediaJob) error {
	provider, ok := runner.service.media.(EntryPreviewProvider)
	if !ok {
		return ErrMediaNotConfigured
	}
	if job.UploadRequestID == "" {
		requestID, err := provider.SubmitEntryPreview(ctx, job.ExpectedVID, job.PreviewSeconds)
		if err != nil {
			return fmt.Errorf("submit first episode clip: %w", err)
		}
		job.UploadRequestID = requestID
		return ErrMediaProcessing
	}
	vid, err := provider.EntryPreviewResult(ctx, job.UploadRequestID, job.ExpectedVID, job.PreviewSeconds)
	if vid != "" {
		job.RemoteVID = vid
	}
	if err != nil {
		return fmt.Errorf("read first episode clip result: %w", err)
	}
	return nil
}
