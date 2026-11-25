package operations

import "time"

// ApplicationMonetizationInput 是运营 API 接收的应用级变现配置。
// 应用 ID 由 X-Application-ID 写入 context，不允许通过请求体跨应用修改配置。
type ApplicationMonetizationInput struct {
	// FreeEpisodeCount 是每部短剧开头可直接观看的免费集数，其后的分集由解锁或 VIP 权益判定。
	FreeEpisodeCount int `json:"freeEpisodeCount"`
	// UnlockEpisodeCount 是每次完整广告从目标集起解锁的集数；省略时保留旧值，首次默认 1。
	UnlockEpisodeCount *int `json:"unlockEpisodeCount,omitempty"`
	// DailyRewardLimit 是每个用户在应用自然日内的逐集奖励上限；0 不限，省略时保留旧值。
	DailyRewardLimit *int `json:"dailyRewardLimit,omitempty"`
	// EpisodeUnlockAdUnitCode 是逐集解锁使用的应用激励广告位编码。
	EpisodeUnlockAdUnitCode string `json:"episodeUnlockAdUnitCode"`
	// VIPStrategy 配置应用级当日 VIP 广告目标及投放启动门禁；Enabled 为 false 时关闭该策略。
	VIPStrategy ApplicationVIPStrategyInput `json:"vipStrategy"`
}

// ApplicationVIPStrategyInput 同时描述自愿 VIP 领取和投放启动强看规则。
// RequiredAdCount 决定当日 VIP 激活阈值；ForceWatchCount 只约束一次有效投放启动。
type ApplicationVIPStrategyInput struct {
	// Enabled 决定是否提供本应用的当日 VIP 广告策略。
	Enabled bool `json:"enabled"`
	// ForceWatchCount 是一次有效投放启动必须完成的广告次数，不等同于当日 VIP 激活目标。
	ForceWatchCount int `json:"forceWatchCount"`
	// RequiredAdCount 是访问者当日获得 VIP 所需累计完成的广告次数。
	RequiredAdCount int `json:"requiredAdCount"`
	// DelaySeconds 是投放启动门禁首次展示前的等待秒数。
	DelaySeconds int `json:"delaySeconds"`
	// AdUnitCode 是 VIP 进度使用的应用激励广告位编码。
	AdUnitCode string `json:"adUnitCode"`
	// Title 是 VIP 引导的展示标题。
	Title string `json:"title"`
	// Content 是 VIP 引导的规则说明文案。
	Content string `json:"content"`
	// FailureMessage 是广告未能完成时向访问者展示的提示文案。
	FailureMessage string `json:"failureMessage"`
}

// ApplicationMonetizationView 是运营端读取到的应用级配置投影。
type ApplicationMonetizationView struct {
	// ApplicationID 是当前配置所属的官方 TikTok AppID，以字符串传输。
	ApplicationID string `json:"applicationId"`
	// FreeEpisodeCount 是每部短剧开头可直接观看的免费集数，其后的分集由解锁或 VIP 权益判定。
	FreeEpisodeCount int `json:"freeEpisodeCount"`
	// UnlockEpisodeCount 是一次逐集奖励从目标集起覆盖的集数。
	UnlockEpisodeCount int `json:"unlockEpisodeCount"`
	// DailyRewardLimit 是应用自然日内每个用户的逐集奖励次数上限，0 表示不限。
	DailyRewardLimit int `json:"dailyRewardLimit"`
	// EpisodeUnlockAdUnitCode 是逐集解锁使用的应用激励广告位编码。
	EpisodeUnlockAdUnitCode string `json:"episodeUnlockAdUnitCode"`
	// VIPStrategy 配置应用级当日 VIP 广告目标及投放启动门禁；Enabled 为 false 时关闭该策略。
	VIPStrategy ApplicationVIPStrategyInput `json:"vipStrategy"`

	// CreatedAt 是本应用首次创建变现配置的 UTC 时间，后续更新保留。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 是当前配置修订的 UTC 保存时间。
	UpdatedAt time.Time `json:"updatedAt"`
}
