package monetization

import (
	"context"
	"errors"
	"time"
)

// ErrInvalidAdShow 表示展示字段或主体关联无效，或者同一事件的事实发生冲突。
var ErrInvalidAdShow = errors.New("invalid ad show")

// AdShowInput 是跨场景的成功展示事实；无剧目标的场景不要求内容标识，也不绑定奖励 Offer。
type AdShowInput struct {
	// EventID 固定一次成功展示的幂等键，同一事件重试不得重新生成。
	EventID string `json:"eventId"`
	// Scene 是稳定用途编码，例如 entry_reward 或 home_entry；计数按场景隔离。
	Scene string `json:"scene"`
	// AdUnitID 是 SDK 实际成功展示的公开广告位，必须属于当前认证应用。
	AdUnitID string `json:"adUnitId"`
	// DramaID 是可选来源剧目；无剧目标时省略，存在时须对当前应用可见。
	DramaID string `json:"dramaId,omitempty"`
	// EpisodeID 是可选来源剧集；存在时同时提供 DramaID 并校验关联。
	EpisodeID string `json:"episodeId,omitempty"`
	// OccurredAt 兼容接收旧 Mini 的客户端观察时间；新事实在业务入口覆盖为服务器接收时间。
	OccurredAt time.Time `json:"occurredAt"`
}

// AdShow 是保存到认证主体分片的不可变展示事实，不决定播放授权或奖励。
type AdShow = AdShowInput

// AdShowQuery 按通用维度读取 [From,Until) 内的展示，不依赖策略配置或计数门槛。
type AdShowQuery struct {
	// Scene 隔离不同用途，首页或 VIP 展示不得推进入剧次数。
	Scene string
	// DramaID 非空时只统计该剧；为空时汇总该用户该场景内全部展示。
	DramaID string
	// EpisodeID 非空时只统计该集；为空时不限定分集。
	EpisodeID string
	// From 是包含的 UTC 起始时刻。
	From time.Time
	// Until 是不包含的 UTC 结束时刻。
	Until time.Time
}

// AdShowRepository 按已认证主体保存和读取事实，不决定触发、重拉、N 或门禁。
type AdShowRepository interface {
	// FindAdShow 按可信应用、用户及事件键读取首次事实；未找到返回 found=false，不读取当前广告配置。
	FindAdShow(context.Context, string, string) (AdShow, bool, error)
	// RecordAdShow 幂等保存每条有效展示，不因入剧达标丢弃事实；同键改写业务目标仍拒绝。
	RecordAdShow(context.Context, string, AdShow) error
	// CountAdShows 读取当前应用用户在场景、可选内容及时间范围内的成功展示次数。
	CountAdShows(context.Context, string, AdShowQuery) (int, error)
}
