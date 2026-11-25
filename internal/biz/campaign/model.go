// Package campaign 定义 TikTok 投放链接及可信归因上下文。
package campaign

import "time"

const (
	// StatusActive 表示该投放编码可被当前应用解析为有效启动归因。
	StatusActive = "active"
	// StatusDisabled 表示停止使用该编码建立新的启动归因。
	StatusDisabled = "disabled"
)

// Campaign 标识严格归属于一个 Application 的 TikTok 获客投放。
// 外部广告标识符即使只包含数字，也始终保持字符串类型。
type Campaign struct {
	// ID 是投放记录的内部业务标识，奖励结算保存该值用于归因。
	ID string `json:"id"`
	// ApplicationID 限定投放所属应用，其他应用不能复用该投放编码建立归因。
	ApplicationID string `json:"applicationId"`
	// Code 是链接携带的稳定启动编码，服务端据此读取可信归因。
	Code string `json:"code"`
	// Name 是管理端用于区分投放的展示名称。
	Name string `json:"name"`
	// LandingURL 是 TikTok 官方生成的 Minis Link，不由客户端自行拼接。
	LandingURL string `json:"landingUrl,omitempty"`
	// MinisPath 是生成官方链接时使用的小程序内部路径，仅由服务端构造。
	MinisPath string `json:"minisPath"`
	// ExternalCampaignID 是广告平台的 Campaign 标识，仅作为归因关联数据保存。
	ExternalCampaignID string `json:"externalCampaignId"`
	// ExternalAdGroupID 是广告平台的 Ad Group 标识，按字符串保留。
	ExternalAdGroupID string `json:"externalAdGroupId"`
	// ExternalAdID 是广告平台的 Ad 标识，按字符串保留。
	ExternalAdID string `json:"externalAdId"`
	// ExternalCreativeID 是广告平台的 Creative 标识，按字符串保留。
	ExternalCreativeID string `json:"externalCreativeId"`
	// DramaID 是落地目标短剧的内部标识，不是官方 AlbumID。
	DramaID string `json:"dramaId"`
	// EpisodeID 是落地目标剧集的内部标识，不是官方 EpisodeID。
	EpisodeID string `json:"episodeId"`
	// ExperimentKey 是运营关联实验或分组使用的稳定键，不参与播放授权。
	ExperimentKey string `json:"experimentKey"`
	// Status 控制投放是否可建立新的启动归因，不直接决定内容上架状态。
	Status string `json:"status"`
	// CreatedAt 记录投放首次创建时间。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 记录投放资料最近修改时间。
	UpdatedAt time.Time `json:"updatedAt"`
}
