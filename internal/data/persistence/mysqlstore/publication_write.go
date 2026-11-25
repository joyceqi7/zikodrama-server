package mysqlstore

import (
	"context"
	"drama-server/internal/biz/operations"
	"time"
)

// BeginTikTokExternalWrite 保留当前租约，先写入安全检查点再允许发送不可幂等重放的请求。
func (s *Store) BeginTikTokExternalWrite(ctx context.Context, publication *operations.TikTokPublication) error {
	if publication == nil || publication.LeaseOwner == "" || publication.ExternalWriteStarted {
		return operations.ErrConflict
	}
	next := *publication
	next.ExternalWriteStarted = true
	model, err := newTikTokPublicationModel(next)
	if err != nil {
		return err
	}
	result := s.data.DB().WithContext(ctx).Model(&tiktokPublicationModel{}).
		Where("id = ? AND revision = ? AND lease_owner = ? AND lease_expires_at > ?", next.ID, next.Revision, next.LeaseOwner, time.Now().UTC()).
		Updates(map[string]any{"workflow_json": model.WorkflowJSON, "revision": next.Revision + 1})
	if err := gormAffectedOrNotFound(result); err != nil {
		return err
	}
	next.Revision++
	*publication = next
	return nil
}
