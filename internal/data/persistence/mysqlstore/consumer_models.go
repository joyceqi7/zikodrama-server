package mysqlstore

import "time"

// consumerUserModel 将内部消费者主体与可选 TikTok 身份统一保存在 users 中。
type consumerUserModel struct {
	// ID 是号段分配器生成的内部用户主键，奖励、权益和客户端凭据引用该值。
	ID string `gorm:"column:id;primaryKey;type:bigint unsigned"`
	// BucketID 是身份哈希确定的稳定逻辑桶。
	BucketID uint16 `gorm:"column:bucket_id;type:smallint unsigned"`
	// ApplicationID 限定用户和 TikTok 身份所属应用。
	ApplicationID string `gorm:"column:application_id;type:bigint unsigned"`
	// OpenIDHash 未绑定时为 NULL；应用与非空摘要的组合唯一。
	OpenIDHash *string `gorm:"column:open_id_hash"`
	// AuthVersion 递增后使该用户已签发的旧 VisitorToken 失效。
	AuthVersion uint64 `gorm:"column:auth_version;type:bigint unsigned;default:1"`
	// CreatedAt 保留业务用户首次创建时间，登录不覆盖。
	CreatedAt time.Time `gorm:"column:created_at"`
	// UpdatedAt 是用户创建或平台身份最近刷新时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 将消费者模型固定映射到统一用户表，与管理员账号表区分。
func (consumerUserModel) TableName() string { return "users_00" }

// entitlementGrantModel 只保存不可变归属、来源、时间和类型 JSON，不按具体玩法增列。
type entitlementGrantModel struct {
	// ID 是当前物理分表内的数据库自增主键；来源唯一索引保证重试返回同一条记录。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// BucketID 是权益持有人的稳定逻辑桶。
	BucketID uint16 `gorm:"column:bucket_id;type:smallint unsigned"`
	// ApplicationID 隔离不同小程序的权益。
	ApplicationID string `gorm:"column:application_id;type:bigint unsigned"`
	// VisitorID 关联持有权益的稳定消费者主体。
	VisitorID string `gorm:"column:visitor_id;type:bigint unsigned"`
	// Type 选择权益数据的解释和访问判定策略。
	Type string `gorm:"column:type"`
	// Data 保存发放时的权益内容快照。
	Data []byte `gorm:"column:data"`
	// SourceType 区分广告、订单和订阅等可信来源。
	SourceType string `gorm:"column:source_type"`
	// SourceID 标识具体履约事件，用于幂等与撤销。
	SourceID string `gorm:"column:source_id"`
	// SourceItem 区分同一事件发放的多个权益项。
	SourceItem string `gorm:"column:source_item"`
	// ValidFrom 是包含在有效区间内的起点。
	ValidFrom time.Time `gorm:"column:valid_from"`
	// ValidUntil 是不包含在有效区间内的终点，空值代表无限期。
	ValidUntil *time.Time `gorm:"column:valid_until"`
	// RevokedAt 标记来源撤销，事件重放不会清除。
	RevokedAt *time.Time `gorm:"column:revoked_at"`
	// GrantedAt 保留首次落账时间。
	GrantedAt time.Time `gorm:"column:granted_at"`
}

// TableName 声明权益授予模型的基准分表，实际读写按用户逻辑桶选择物理表。
func (entitlementGrantModel) TableName() string { return "entitlement_grants_00" }

// entitlementStateModel 是播放鉴权直接读取的按目标展开权益投影。
type entitlementStateModel struct {
	// ID 是当前物理分表内的数据库自增投影标识，不占用用户 UID 号段。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// BucketID 是权益持有人的稳定逻辑桶。
	BucketID uint16 `gorm:"column:bucket_id;type:smallint unsigned"`
	// ApplicationID 隔离不同小程序。
	ApplicationID string `gorm:"column:application_id;type:bigint unsigned"`
	// VisitorID 关联稳定消费者主体。
	VisitorID string `gorm:"column:visitor_id;type:bigint unsigned"`
	// GrantID 指向同一物理分片的权益授予流水，不能脱离分片上下文定位记录。
	GrantID string `gorm:"column:grant_id;type:bigint unsigned"`
	// Type 是权益类型编码。
	Type string `gorm:"column:type"`
	// TargetKey 是规范化鉴权目标键。
	TargetKey string `gorm:"column:target_key"`
	// Data 保存权益策略仍需解释的授予快照。
	Data []byte `gorm:"column:data"`
	// ValidFrom 是投影生效起点。
	ValidFrom time.Time `gorm:"column:valid_from"`
	// ValidUntil 是投影失效终点，NULL 表示无限期。
	ValidUntil *time.Time `gorm:"column:valid_until"`
	// CreatedAt 是投影创建时间。
	CreatedAt time.Time `gorm:"column:created_at"`
}

// TableName 声明权益投影模型的基准分表，与对应授予流水使用相同的分片路由。
func (entitlementStateModel) TableName() string { return "user_entitlement_state_00" }

// entitlementRevocationModel 保存来源撤销事实，防止退款或撤销事件重放后权益复活。
type entitlementRevocationModel struct {
	// ID 是当前物理分表内的数据库自增撤销流水标识。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// BucketID 是权益持有人的稳定逻辑桶。
	BucketID uint16 `gorm:"column:bucket_id;type:smallint unsigned"`
	// ApplicationID 隔离不同小程序。
	ApplicationID string `gorm:"column:application_id;type:bigint unsigned"`
	// VisitorID 关联稳定消费者主体。
	VisitorID string `gorm:"column:visitor_id;type:bigint unsigned"`
	// SourceType 区分广告、订单和订阅来源。
	SourceType string `gorm:"column:source_type"`
	// SourceID 是被撤销的业务来源事件标识。
	SourceID string `gorm:"column:source_id"`
	// RevokedAt 是撤销生效时间。
	RevokedAt time.Time `gorm:"column:revoked_at"`
}

// TableName 声明权益撤销模型的基准分表，实际读写按权益持有人的逻辑桶路由。
func (entitlementRevocationModel) TableName() string { return "entitlement_revocations_00" }

// vipProgressModel 映射访客在单个应用业务周期内完成的 VIP 广告次数。
type vipProgressModel struct {
	// ID 是当前物理分表内的数据库自增进度记录主键。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// BucketID 是访客的稳定逻辑桶。
	BucketID uint16 `gorm:"column:bucket_id;type:smallint unsigned"`
	// ProgressKey 隔离应用策略中不同规则的累计次数。
	ProgressKey string `gorm:"column:progress_key"`
	// ApplicationID 所属小程序自增主键，关联 applications.id。
	ApplicationID string `gorm:"column:application_id;type:bigint unsigned"`
	// VisitorID 消费者标识，关联 users.id。
	VisitorID string `gorm:"column:visitor_id;type:bigint unsigned"`
	// PeriodKey 按应用时区与规则划分的业务周期键。
	PeriodKey string `gorm:"column:period_key"`
	// Completed 本周期已完成的有效广告次数。
	Completed int `gorm:"column:completed_count"`
	// Required 当前进度采用的广告完成门槛。
	Required int `gorm:"column:required_count"`
	// UpdatedAt 记录最近更新时间，使用 UTC。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 声明 VIP 进度模型的基准分表，实际读写按用户逻辑桶路由。
func (vipProgressModel) TableName() string { return "application_vip_progress_00" }

// rewardGrantModel 映射已经成功落账的广告奖励；ClaimID 是跨网络重试的幂等键。
type rewardGrantModel struct {
	// ID 是当前物理分表内的数据库自增流水主键；ClaimID 单独负责网络重试幂等。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// BucketID 是领取人的稳定逻辑桶。
	BucketID uint16 `gorm:"column:bucket_id;type:smallint unsigned"`
	// EntitlementsJSON 保存本次领取生成的实际权益快照。
	EntitlementsJSON []byte `gorm:"column:entitlements_json"`
	// RuleSnapshotJSON 兼容历史表结构；新记录写 JSON null，恢复不读取历史策略条款。
	RuleSnapshotJSON []byte `gorm:"column:rule_snapshot_json"`
	// ProgressKey 固定领取时所属的策略与规则进度空间。
	ProgressKey string `gorm:"column:progress_key"`
	// ClaimID 客户端奖励领取标识，唯一键保证重复提交不重复结算。
	ClaimID string `gorm:"column:claim_id"`
	// ApplicationID 所属小程序自增主键，关联 applications.id。
	ApplicationID string `gorm:"column:application_id;type:bigint unsigned"`
	// CampaignID 本次领取关联的投放活动标识，NULL 表示无归因。
	CampaignID string `gorm:"column:campaign_id;type:bigint unsigned;default:null"`
	// VisitorID 消费者标识，关联 users.id。
	VisitorID string `gorm:"column:visitor_id;type:bigint unsigned"`
	// DramaID 奖励关联的短剧标识，非短剧奖励可为 NULL。
	DramaID *string `gorm:"column:drama_id;type:bigint unsigned;default:null"`
	// EpisodeID 奖励关联的分集标识，非逐集奖励可为 NULL。
	EpisodeID *string `gorm:"column:episode_id;type:bigint unsigned;default:null"`
	// UnlockEpisodeIDsJSON 本次解锁的分集标识列表 JSON。
	UnlockEpisodeIDsJSON []byte `gorm:"column:unlock_episode_ids_json"`
	// AdUnitID 本次广告所用的官方广告位标识。
	AdUnitID string `gorm:"column:ad_unit_id"`
	// Scene 领取广告时的业务场景编码。
	Scene string `gorm:"column:scene"`
	// RewardType 奖励类型编码，用于结算与统计。
	RewardType string `gorm:"column:reward_type"`
	// PeriodKey 按应用时区与规则生成的奖励周期键。
	PeriodKey string `gorm:"column:period_key"`
	// PeriodStartsAt 奖励周期开始时间，使用 UTC，无周期时为 NULL。
	PeriodStartsAt *time.Time `gorm:"column:period_starts_at"`
	// PeriodExpiresAt 奖励周期结束时间，使用 UTC，无周期时为 NULL。
	PeriodExpiresAt *time.Time `gorm:"column:period_expires_at"`
	// RewardRequiredCount 该奖励完成一轮所需的广告次数快照。
	RewardRequiredCount int `gorm:"column:reward_required_count"`
	// DailyRewardLimit 领取时的每日奖励次数上限，0 表示不限。
	DailyRewardLimit int `gorm:"column:daily_reward_limit"`
	// EntitlementScope 奖励授权范围编码，如单集或全目录。
	EntitlementScope string `gorm:"column:entitlement_scope"`
	// GrantedAt 奖励成功结算时间，使用 UTC。
	GrantedAt time.Time `gorm:"column:granted_at"`
}

// TableName 声明奖励流水模型的基准分表，实际读写按领取人的逻辑桶路由。
func (rewardGrantModel) TableName() string { return "reward_grants_00" }
