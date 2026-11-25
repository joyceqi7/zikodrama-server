package mysqlstore

import (
	"drama-server/internal/biz/consumer"
	"time"
)

// metricAdEventModel 是指标事务内保存的广告阶段事实，按请求日期保留九十天。
type metricAdEventModel struct {
	// ApplicationID 只取认证应用。
	ApplicationID string `gorm:"primaryKey"`
	// UserID 只取认证主体，不接收 OpenID。
	UserID string `gorm:"primaryKey"`
	// EventID 是本次广告调用的随机事实键。
	EventID string `gorm:"primaryKey"`
	// Kind 区分同一次调用的不可变阶段。
	Kind string `gorm:"primaryKey"`
	// AdMetricDetails 只保留入口已校验的有界字段。
	consumer.AdMetricDetails `gorm:"embedded"`
	// OccurredAt 固定为本次业务调用发起日，迟到结果不改归属。
	OccurredAt time.Time
	// ReceivedAt 是服务端首次接收该阶段的时间。
	ReceivedAt time.Time
}

// TableName 声明广告指标侧表，不建设广告控制或领奖服务。
func (metricAdEventModel) TableName() string { return "metric_ad_events" }
