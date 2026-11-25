package operations

// TikTokVideoInput 登记已经上传到 BytePlus 的视频，不重复传输媒体文件。
type TikTokVideoInput struct {
	// VID 是 BytePlus 视频标识；必须经 TikTok 查询成功后才能送审。
	VID string
	// Title 是分集名称。
	Title string
	// SpaceName 是所属的 BytePlus 空间。
	SpaceName string
	// BytePlusAccountID 是已完成平台关联的 BytePlus 账号标识。
	BytePlusAccountID string
	// Region 是 BytePlus 区域；为空时使用官方默认值。
	Region string
}

// TikTokVideoRegistration 是官方视频登记操作返回的可恢复标识。
type TikTokVideoRegistration struct {
	// JobID 是异步任务标识；同步成功时可以为空。
	JobID string
	// VID 是官方返回的视频标识，尚不能代替查询成功的事实。
	VID string
}

// TikTokVideoObservation 保存官方查询的处理状态与视频身份。
type TikTokVideoObservation struct {
	// Status 是 1 处理中、2 成功、3 失败。
	Status int
	// VID 是 TikTok 确认可以使用的 BytePlus 视频标识。
	VID string
}

// TikTokTranscodeInput 按官方 default_0 模板提交可幂等的转码任务。
type TikTokTranscodeInput struct {
	// IdempotencyKey 由发布任务和批次确定，重试不得重新生成。
	IdempotencyKey string
	// VIDs 是本批视频标识，最多 1000 个。
	VIDs []string
	// SpaceName 是 BytePlus 视频空间。
	SpaceName string
	// BytePlusAccountID 是已关联的 BytePlus 账号标识。
	BytePlusAccountID string
}

// TikTokTranscodeObservation 同时保存任务状态和逐视频结果，避免只看 SUCCESS 就放行。
type TikTokTranscodeObservation struct {
	// TaskID 是官方任务标识。
	TaskID string
	// Status 是官方 PROCESSING、SUCCESS 或失败状态。
	Status string
	// TotalCount 是任务包含的视频数。
	TotalCount int
	// SuccessVIDs 是已转码成功的视频集合。
	SuccessVIDs []string
	// FailedVIDs 是转码失败的视频集合。
	FailedVIDs []string
	// PendingVIDs 是仍在处理的视频集合。
	PendingVIDs []string
}

// MinisLinkInput 使用官方路径和展示信息创建可供投放的 Minis 链接。
type MinisLinkInput struct {
	// Path 是小程序内部路径，包含系统生成的投放标识。
	Path string
	// Title 是链接展示标题。
	Title string
	// ImageURL 是链接展示封面，可为空。
	ImageURL string
}
