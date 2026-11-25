package operations

import (
	"context"
	"log/slog"

	"drama-server/internal/pkg/requestmeta"
)

// ReceiveVODCallback 同步幂等唤醒匹配上传任务；事务提交成功才确认回调，平台请求号与 HTTP 请求标识分别记录。
func (s *DramaService) ReceiveVODCallback(ctx context.Context, event VODEvent) (bool, error) {
	if s == nil || s.repositories.Media.Jobs == nil {
		return false, ErrMediaNotConfigured
	}
	switch event.EventType {
	case VODEventFileUploadComplete, VODEventExecutionComplete, VODEventTranscodeComplete, VODEventMaterialUploadComplete:
	default:
		return false, nil
	}
	applied, err := s.repositories.Media.Jobs.ApplyVODCallback(ctx, event)
	fields := append(requestmeta.LogFields(ctx), "platform_request_id", event.RequestID, "event_type", event.EventType)
	if err != nil {
		slog.ErrorContext(ctx, "apply vod callback failed", append(fields, "error", err)...)
		return false, err
	}
	if applied {
		s.notifyMediaWorker()
	}
	slog.InfoContext(ctx, "vod callback handled", append(fields, "applied", applied)...)
	return applied, nil
}
