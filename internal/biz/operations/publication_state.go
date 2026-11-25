package operations

import "time"

// PublicationOwnerScope 是发布期间不可改变的官方资源归属，不含密钥。
type PublicationOwnerScope struct {
	// Region 是上传源视频时使用的 BytePlus 区域，登记时必须一致。
	Region string `json:"region"`
	// ClientKey 是官方剧目拥有方。
	ClientKey string `json:"clientKey"`
	// AccountID 是已授权的 BytePlus 账号。
	AccountID string `json:"accountId"`
	// SpaceName 是存放源视频的空间。
	SpaceName string `json:"spaceName"`
}

// PublicationVideo 是一次发布中单集的官方登记状态。
type PublicationVideo struct {
	// EpisodeID 是内部剧集标识。
	EpisodeID string `json:"episodeId"`
	// VID 是已经上传到 BytePlus 的视频标识。
	VID string `json:"vid"`
	// JobID 是登记接口返回的异步任务标识。
	JobID string `json:"jobId,omitempty"`
	// Status 是 pending、processing 或 ready。
	Status string `json:"status"`
}

// PublicationTranscode 保存稳定批次和幂等键，重启后使用原任务继续查询。
type PublicationTranscode struct {
	// IdempotencyKey 是由当前发布的持久化随机前缀与批次号构成的稳定键；旧任务沿用历史算法。
	IdempotencyKey string `json:"-"`
	// VIDs 是该批视频集合。
	VIDs []string `json:"vids"`
	// TaskID 是官方批量转码任务标识。
	TaskID string `json:"taskId,omitempty"`
	// Ready 表示逐视频结果已经全部核对通过。
	Ready bool `json:"ready"`
	// CompletedVIDs 保存已由官方查询确认成功的分集，用于识别真实进展。
	CompletedVIDs []string `json:"completedVids,omitempty"`
	// StartedAt 是本系统首次取得该任务标识的时间；旧任务从首次观察开始计时。
	StartedAt time.Time `json:"startedAt,omitempty"`
	// LastProgressAt 只在已完成分集增加时更新，不随轮询刷新。
	LastProgressAt time.Time `json:"lastProgressAt,omitempty"`
	// LastCheckedAt 是最近一次取得有效官方结果的时间。
	LastCheckedAt time.Time `json:"lastCheckedAt,omitempty"`
	// MonitoringStartedAt 是本轮等待观察起点；人工恢复只重置观察窗口，不改写历史进展。
	MonitoringStartedAt time.Time `json:"monitoringStartedAt,omitempty"`
}
