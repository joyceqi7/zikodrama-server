package operations

import (
	"context"
	"time"
)

// AuditRecord 是管理端写操作的审计记录。
type AuditRecord struct {
	// ID 是数据库自增审计主键的十进制字符串。
	ID string `json:"id"`
	// RequestID 关联同一次写操作的 started 和最终状态记录。
	RequestID string `json:"requestId"`
	// ActorID 是执行操作的管理员标识。
	ActorID string `json:"actorId"`
	// ActorUsername 是操作发生时的管理员用户名快照。
	ActorUsername string `json:"actorUsername"`
	// ApplicationID 是应用级操作对应的可信应用上下文；全局操作留空。
	ApplicationID string `json:"applicationId,omitempty"`
	// Action 是匹配到的 HTTP 方法和路由 Pattern。
	Action string `json:"action"`
	// ResourceType 是运营路径中的首段资源类型。
	ResourceType string `json:"resourceType"`
	// ResourceID 是路由中的目标资源标识，敏感身份只记录摘要。
	ResourceID string `json:"resourceId"`
	// Result 是 started、success 或 failed。
	Result string `json:"result"`
	// HTTPStatus 是最终响应状态码；started 记录为零。
	HTTPStatus int `json:"httpStatus"`
	// ClientIP 是经入口治理后观察到的客户端地址。
	ClientIP string `json:"clientIp"`
	// CreatedAt 是审计状态写入时间。
	CreatedAt time.Time `json:"createdAt"`
}

// MetricsRepository 定义运营统计查询端口。
type MetricsRepository interface {
	// CampaignRewardRanking 按应用和结算时间统计投放链接奖励排行，排除自然流量，最多返回 50 项。
	CampaignRewardRanking(context.Context, time.Time, time.Time) ([]CampaignRewardRank, error)
	// DashboardAudience 查询截至 to（不含）的过去 30 日活跃去重及应用首次采集时间。
	DashboardAudience(context.Context, time.Time) (DashboardAudience, error)
	// DashboardDays 按 UTC 日期聚合当前应用的用户、奖励权益账本及客户端计数信号，范围为 [from,to)。
	DashboardDays(context.Context, time.Time, time.Time) ([]DashboardDay, error)
}

// AuditRepository 定义管理员操作审计端口。
type AuditRepository interface {
	// SaveAuditRecord 以 RequestID 合并同一请求的 started 与最终结果；数据库生成 ID，重复写入更新结果、HTTP 状态和客户端地址。
	SaveAuditRecord(context.Context, AuditRecord) error
	// ListAuditRecords 按创建时间倒序返回最多 limit 条全局管理员审计记录；调用方负责限制 limit。
	ListAuditRecords(context.Context, int) ([]AuditRecord, error)
}
