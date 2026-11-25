package mysqlstore

import "time"

// metricPostbackDetailModel 是七天保留的每阶段脱敏诊断，与计数事实同事务写入。
type metricPostbackDetailModel struct {
	// ApplicationID 来自已认证应用，不接受客户端自报。
	ApplicationID string `gorm:"column:application_id;primaryKey"`
	// UserID 来自已认证访问者。
	UserID string `gorm:"column:user_id;primaryKey"`
	// EventID 关联同一次 SDK 调用的不可变事实。
	EventID string `gorm:"column:event_id;primaryKey"`
	// Kind 区分尝试、结果未知及终态。
	Kind string `gorm:"column:kind;primaryKey"`
	// Scene 是允许筛选的固定入口。
	Scene string `gorm:"column:scene"`
	// ClientVersion 是 Mini 构建修订。
	ClientVersion string `gorm:"column:client_version"`
	// ErrorCode 是数值错误码，空值表示未提供。
	ErrorCode string `gorm:"column:error_code"`
	// Payload 是经 Handler 白名单校验的脱敏 JSON。
	Payload string `gorm:"column:payload;type:json"`
	// ReceivedAt 决定七天保留窗口，重试不延长。
	ReceivedAt time.Time `gorm:"column:received_at"`
}

// TableName 声明独立短期诊断表，避免修改已有日期分表。
func (metricPostbackDetailModel) TableName() string { return "metric_postback_details" }
