package consumer

// AdMetricDetails 是官方广告调用的白名单诊断，不是平台计费曝光或奖励凭据。
type AdMetricDetails struct {
	// AdType 区分激励与插屏。
	AdType string `json:"adType"`
	// AdUnitID 是实际调用的广告位字符串。
	AdUnitID string `json:"adUnitId"`
	// Scene 是固定业务入口，不保存观看位置或剧集历史。
	Scene string `json:"scene"`
	// ClientVersion 是 Mini 构建修订。
	ClientVersion string `json:"clientVersion"`
	// OS 是官方公开系统分类，无法获取时为 unknown。
	OS string `json:"os"`
	// TikTokVersion 是官方客户端版本，无法获取时为 unknown。
	TikTokVersion string `json:"tiktokVersion"`
	// Stage 记录能力检查、创建、展示、回调或关闭所处阶段，不包含独立加载。
	Stage string `json:"stage"`
	// Reason 是固定分类，不推断广告库存原因。
	Reason string `json:"reason,omitempty"`
	// ErrorCode 是官方数值错误码，必须结合阶段解释。
	ErrorCode string `json:"errorCode,omitempty"`
	// SubErrorCode 是官方数值子码。
	SubErrorCode string `json:"subErrorCode,omitempty"`
	// InnerErrorCode 独立保留 SDK 内部码，不覆盖主码或子码。
	InnerErrorCode string `json:"innerErrorCode,omitempty"`
	// ElapsedMS 是业务调用开始后的单调时钟耗时。
	ElapsedMS *float64 `json:"elapsedMs,omitempty"`
	// ShowElapsedMS 是 show 调用后的耗时，展示分位数只取已展示事实。
	ShowElapsedMS *float64 `json:"showElapsedMs,omitempty"`
	// Evidence 区分 SDK 展示成功与完播补证。
	Evidence string `json:"evidence,omitempty"`
}
