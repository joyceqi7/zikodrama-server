package mysqlstore

import (
	"context"
	"time"

	"gorm.io/gorm/clause"

	"drama-server/internal/biz/operations"
)

type auditRecordModel struct {
	// ID 数据库自增审计记录主键。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// RequestID 请求唯一标识，用于避免重复记录同一操作。
	RequestID string `gorm:"column:request_id"`
	// ActorID 操作管理员标识，关联 admin_users.id。
	ActorID string `gorm:"column:actor_id;type:bigint unsigned"`
	// ActorUsername 操作时的管理员用户名快照。
	ActorUsername string `gorm:"column:actor_username"`
	// ApplicationID 对全局管理操作为 NULL，不使用空字符串伪造默认应用。
	ApplicationID *string `gorm:"column:application_id;type:bigint unsigned"`
	// Action 执行的操作权限编码。
	Action string `gorm:"column:action"`
	// ResourceType 被操作资源的类型编码。
	ResourceType string `gorm:"column:resource_type"`
	// ResourceID 被操作资源的标识。
	ResourceID string `gorm:"column:resource_id"`
	// Result 操作结果分类。
	Result string `gorm:"column:result"`
	// HTTPStatus 操作返回的 HTTP 状态码。
	HTTPStatus int `gorm:"column:http_status"`
	// ClientIP 发起操作的客户端 IP 地址。
	ClientIP string `gorm:"column:client_ip"`
	// CreatedAt 记录创建时间，使用 UTC。
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (auditRecordModel) TableName() string { return "admin_audit_logs" }

// SaveAuditRecord 以 request_id 保证同一管理请求的审计记录幂等。
func (s *Store) SaveAuditRecord(ctx context.Context, record operations.AuditRecord) error {
	model := auditRecordModel{
		RequestID: record.RequestID, ActorID: record.ActorID, ActorUsername: record.ActorUsername,
		ApplicationID: stringPointer(record.ApplicationID), Action: record.Action, ResourceType: record.ResourceType,
		ResourceID: record.ResourceID, Result: record.Result, HTTPStatus: record.HTTPStatus,
		ClientIP: record.ClientIP, CreatedAt: record.CreatedAt.UTC(),
	}
	return s.data.DB().WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "request_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"result", "http_status", "client_ip"}),
	}).Create(&model).Error
}

// ListAuditRecords 按时间倒序读取低频运营审计记录。
func (s *Store) ListAuditRecords(ctx context.Context, limit int) ([]operations.AuditRecord, error) {
	var models []auditRecordModel
	if err := s.data.DB().WithContext(ctx).Order("created_at DESC, id DESC").Limit(limit).Find(&models).Error; err != nil {
		return nil, err
	}
	items := make([]operations.AuditRecord, 0, len(models))
	for _, model := range models {
		applicationID := ""
		if model.ApplicationID != nil {
			applicationID = *model.ApplicationID
		}
		items = append(items, operations.AuditRecord{
			ID: model.ID, RequestID: model.RequestID, ActorID: model.ActorID, ActorUsername: model.ActorUsername,
			ApplicationID: applicationID, Action: model.Action, ResourceType: model.ResourceType,
			ResourceID: model.ResourceID, Result: model.Result, HTTPStatus: model.HTTPStatus,
			ClientIP: model.ClientIP, CreatedAt: model.CreatedAt,
		})
	}
	return items, nil
}
