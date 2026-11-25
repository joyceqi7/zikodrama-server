package monetization

import (
	"context"
	"time"
)

// ConfigurationRepository 提供当前应用消费设置的权威快照。
type ConfigurationRepository interface {
	// GetApplicationMonetization 读取上下文当前应用的权威配置，不接受客户端指定的配置修订。
	GetApplicationMonetization(context.Context) (ApplicationMonetizationConfig, error)
}

// BenefitRepository 负责 VIP 进度、逐集权益以及广告奖励的原子结算。
// 新奖励必须与其权益在同一事务内写入；入剧只记录完播事实，达标后仍按原键幂等保存。
type BenefitRepository interface {
	// FindVIPProgress 按访客、策略、应用自然日和判定时间读取进度及有效目录权益；无当日进度时返回零计数。
	FindVIPProgress(context.Context, string, MonetizationPolicy, RewardPeriod, time.Time) (VIPProgress, error)
	// ReconcileVIPProgress 仅供显式领取，在事务中核对访客当日进度与当前阈值并幂等发放目录权益。
	ReconcileVIPProgress(context.Context, string, MonetizationPolicy, RewardPeriod, time.Time) (VIPProgress, error)
	// HasCatalogEntitlement 按访客和判定时间检查当前应用仍有效的目录级权益。
	HasCatalogEntitlement(context.Context, string, time.Time) (bool, error)
	// CountRewardGrants 统计当前应用指定访客、奖励类型自起始时刻起已落账次数；提供 progressKey 时只统计该规则，省略时统计全部。
	CountRewardGrants(context.Context, string, string, time.Time, ...string) (int, error)
	// FindRewardGrant 按访客和 ClaimID 恢复当前应用结算；不存在返回 found=false，幂等键属于另一访客时拒绝。
	FindRewardGrant(context.Context, string, string) (RewardGrant, bool, error)
	// GrantReward 在用户行锁内检查幂等、规则日限额和权益并原子落账；入剧达标不停止保存完播事实，已入库的重复领取不重算。
	GrantReward(context.Context, RewardGrant, time.Time) (ClaimResult, error)
	// HasEntitlement 按访客、内部短剧、内部剧集及判定时间通过类型处理器检查当前应用的分集、整剧或 VIP 访问权益。
	HasEntitlement(context.Context, string, string, string, time.Time) (bool, error)
}

// EntitlementRepository 为可信广告、订单和订阅履约提供统一的来源账本端口。
// 不向 C 端开放直接发放或撤销接口；业务状态确认与履约需要共享事务或可靠事件处理。
type EntitlementRepository interface {
	// GrantEntitlements 原子写入同一用户的来源项，成功后回填数据库主键；重放不覆盖已有权益或复活撤销记录。
	GrantEntitlements(context.Context, string, []Entitlement) error
	// RevokeEntitlementSource 只撤销指定来源，其他来源的有效权益继续生效。
	RevokeEntitlementSource(context.Context, string, string, string, time.Time) error
}
