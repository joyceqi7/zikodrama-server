package monetization

import "time"

const (
	// AdSceneEpisodeUnlock 标记广告用于目标集及后续配置数量的分集解锁。
	AdSceneEpisodeUnlock = "unlock_reward"
	// AdSceneVIPReward 标记广告用于应用自然日 VIP 进度。
	AdSceneVIPReward = "vip_reward"
	// AdSceneEntryReward 标记广告用于当日入剧资格。
	AdSceneEntryReward = "entry_reward"

	// RewardTypeEpisodeUnlock 表示本次结算发放分集权益。
	RewardTypeEpisodeUnlock = "episode_unlock"
	// RewardTypeVIPProgress 表示本次结算累加当日 VIP 广告次数。
	RewardTypeVIPProgress = "vip_progress"
	// RewardTypeEntryProgress 表示本次结算累加当日入剧广告次数。
	RewardTypeEntryProgress = "entry_progress"

	// EpisodeAccessFree 表示集号位于策略规定的免费范围。
	EpisodeAccessFree = "free"
	// EpisodeAccessRewarded 表示该集需要通过激励广告获得访问权益。
	EpisodeAccessRewarded = "rewarded"
	// EpisodeAccessUnlocked 表示访客已持有该集访问权益。
	EpisodeAccessUnlocked = "unlocked"
)

// VIPProgress 返回广告结算、进度读取或手动领取后的当前 VIP 状态。
type VIPProgress struct {
	// Active 表示当前目录级权益是否已经生效。
	Active bool `json:"active"`
	// AdCount 是当前应用自然日内已经成功结算的 VIP 广告数。
	AdCount int `json:"adCount"`
	// RequiredCount 是当前自然日允许手动领取 VIP 所需的成功广告次数。
	RequiredCount int `json:"requiredCount"`
	// ValidUntil 是已激活权益的截止时间；无限期权益省略该值，避免客户端误判为过期。
	ValidUntil time.Time `json:"validUntil,omitempty,omitzero"`
	// ProgressDate 是应用时区下的自然日键。
	ProgressDate string `json:"progressDate"`
	// ApplicationID 是奖励或权益归属的内部应用主键。
	ApplicationID string `json:"-"`
}

// RewardGrant 是服务端确认激励广告完整结束后写入的不可变结算事实。
// ClaimID 是客户端幂等键；归因、实际完成的广告位编码和已授予权益作为事实保存，
// 新领取使用当前规则，后续配置变化不改写已经完成的结算。
type RewardGrant struct {
	// BuildContext 仅在首次领取事务内按最新权益重建授予集合，不持久化。
	BuildContext *GrantContext
	// Entitlements 保存逐集广告的实际权益快照；VIP 和入剧广告只累计各自进度。
	Entitlements []Entitlement
	// BuildRule 仅在本次领取内传递最新规则，供用户锁内重建权益；不持久化或用于恢复旧领取。
	BuildRule *UnlockRule
	// OwnershipEpisodeIDs 限定本次批量授予或连续提示需要读取的当前剧分集权益；空集合无需额外读取。
	OwnershipEpisodeIDs []string
	// ProgressKey 隔离不同策略、规则的自然日广告进度。
	ProgressKey string
	// ClaimID 是客户端生成的领取幂等键，同一广告领取重试须保持不变。
	ClaimID string
	// ApplicationID 是奖励或权益归属的官方 TikTok AppID。
	ApplicationID string
	// CampaignID 是领取时可信上下文中的投放标识，自然流量时为空。
	CampaignID string
	// VisitorID 是领取者的持久业务主体，不是 TikTok OpenID。
	VisitorID string
	// DramaID 是逐集奖励或入剧广告的来源剧目；VIP 奖励可为空。
	DramaID string
	// EpisodeID 是逐集奖励的目标内部剧集标识；VIP 和入剧奖励为空。
	EpisodeID string
	// UnlockEpisodeIDs 固定本次实际授予的内部剧集集合，重试不按新配置重算。
	UnlockEpisodeIDs []string
	// AdUnitID 是本次广告实际完成且已与 Offer 校验一致的公开广告位标识。
	AdUnitID string
	// Scene 保存广告使用场景，区分分集解锁、VIP 与入剧奖励。
	Scene string
	// RewardType 保存本次发放的权益类型，决定事务如何结算。
	RewardType string
	// PeriodKey 是应用时区下奖励自然日的日期键。
	PeriodKey string
	// PeriodStartsAt 是奖励自然日开始时刻，持久化为 UTC。
	PeriodStartsAt time.Time
	// PeriodExpiresAt 是下一应用自然日开始时刻，也是当日进度的截止点。
	PeriodExpiresAt time.Time
	// RequiredCount 固定本次 VIP 或入剧广告所需次数，供事务校验。
	RequiredCount int
	// DailyRewardLimit 固定本次逐集或 VIP 奖励的每日领取上限；入剧事实不设写入上限，固定为 0。
	DailyRewardLimit int
	// EntitlementScope 区分单集集合与应用目录级权益。
	EntitlementScope string
	// GrantedAt 记录奖励或权益成功授予的时间。
	GrantedAt time.Time
}

const (
	// ClaimStatusGranted 表示本次请求首次成功写入奖励事实。
	ClaimStatusGranted = "granted"
	// ClaimStatusAlreadyGranted 表示同一 ClaimID 已结算，本次仅恢复既有结果。
	ClaimStatusAlreadyGranted = "already_granted"
	// ClaimStatusEntrySatisfied 保留旧服务端已达标跳过的响应名称；新版入剧事实均按 granted／already_granted 确认。
	ClaimStatusEntrySatisfied = "entry_satisfied"
)

// ClaimResult 是奖励结算的幂等响应。
type ClaimResult struct {
	// SettledGrant 是事务实际命中的奖励事实，幂等命中时不使用本次请求的目标。
	SettledGrant *RewardGrant `json:"-"`
	// OwnedEpisodeIDs 是 MySQL 用户行锁内按本次需求读取的当前剧权益，供首次结算直接计算连续提示。
	OwnedEpisodeIDs map[string]bool `json:"-"`
	// NextAd 是服务端按最新权益决定的后续广告；null 表示停止提示，不影响已完成结算。
	NextAd *NextAdPrompt `json:"nextAd"`
	// ClaimID 是客户端生成的领取幂等键，同一广告领取重试须保持不变。
	ClaimID string `json:"claimId"`
	// Status 区分首次落账和幂等恢复；客户端同时兼容旧 entry_satisfied，并清除已确认的待领取请求。
	Status string `json:"status"`
	// Unlocked 表示该结果属于逐集解锁奖励，不用来表达 VIP 是否生效。
	Unlocked bool `json:"unlocked"`
	// DramaID 是逐集奖励或入剧广告的来源剧目；VIP 奖励可为空。
	DramaID string `json:"dramaId"`
	// EpisodeID 是逐集奖励的目标内部剧集标识；VIP 和入剧奖励为空。
	EpisodeID string `json:"episodeId"`
	// RewardType 保存本次发放的权益类型，决定事务如何结算。
	RewardType string `json:"rewardType"`
	// VIPProgress 返回结算后或重试恢复时读取的当前 VIP 状态。
	VIPProgress *VIPProgress `json:"vipState,omitempty"`
	// DailyLimitReached 仅供服务端在同一请求内决定是否继续提示广告，不写入消费者响应。
	DailyLimitReached bool `json:"-"`
}

// NextAdPrompt 是领奖后的下一步提示，不是播放授权或新的奖励事实。
type NextAdPrompt struct {
	// DramaID 是下一次广告目标所属短剧。
	DramaID string `json:"dramaId"`
	// EpisodeID 是跳过已解锁集后确定的目标。
	EpisodeID string `json:"episodeId"`
	// EpisodeNumber 是展示用集号。
	EpisodeNumber int `json:"episodeNumber"`
	// UnlockedCount 是计算时已经可访问的分集数量。
	UnlockedCount int `json:"unlockedCount"`
	// TotalCount 是当前已发布目录的分集总数。
	TotalCount int `json:"totalCount"`
	// Offer 含当前可用入口及其原有弹窗文案；领取时仍重新验证。
	Offer AccessOffer `json:"offer"`
}

// ClaimRewardInput 是 TikTok 报告广告完整结束后提交的奖励领取命令。
type ClaimRewardInput struct {
	// ClaimID 是客户端生成的领取幂等键，同一广告领取重试须保持不变。
	ClaimID string `json:"claimId"`
	// OfferID 标识当前应用和规则入口；兼容编码中的修订不决定本次待遇，结算读取最新规则。
	OfferID string `json:"offerId"`
	// DramaID 是逐集奖励或入剧广告的来源剧目；VIP 奖励可为空。
	DramaID string `json:"dramaId"`
	// EpisodeID 是逐集奖励的目标内部剧集标识；VIP 奖励可为空。
	EpisodeID string `json:"episodeId"`
	// CompletedAdUnitID 是 TikTok SDK 实际完成的广告单元。
	CompletedAdUnitID string `json:"completedAdUnitId"`
	// IsEnded 必须直接来自 TikTok SDK；只有 true 才允许结算。
	IsEnded bool `json:"isEnded"`
}

// EntitlementGrant 是奖励结算产生的消费者访问权益。
type EntitlementGrant struct {
	// ApplicationID 是奖励或权益归属的官方 TikTok AppID。
	ApplicationID string `json:"-"`
	// VisitorID 是领取者的持久业务主体，不是 TikTok OpenID。
	VisitorID string `json:"-"`
	// DramaID 是逐集奖励所属的内部短剧标识；VIP 奖励可为空。
	DramaID string `json:"-"`
	// EpisodeID 是逐集奖励的目标内部剧集标识；VIP 奖励可为空。
	EpisodeID string `json:"-"`
	// GrantedAt 记录奖励或权益成功授予的时间。
	GrantedAt time.Time `json:"-"`
	// ExpiresAt 是权益截止时刻；零值表示没有设置过期时间。
	ExpiresAt time.Time `json:"-"`
}
