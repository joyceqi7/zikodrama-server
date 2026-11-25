package operations

import (
	"context"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
)

const (
	// PublicationStageAppealReview 提交当前被拒项目的申诉。
	PublicationStageAppealReview = "appeal_review"
	// PublicationStagePollAppeal 等待 Worker 定期查询申诉及整个版本的官方结果。
	PublicationStagePollAppeal = "poll_appeal"
	// PublicationStageWithdrawReview 撤回仍在审核中的平台版本。
	PublicationStageWithdrawReview = "withdraw_review"
	// PublicationPriorityUrgent 对应 TikTok 官方审核 priority_score=1。
	PublicationPriorityUrgent = "urgent"
	// PublicationPriorityNormal 对应 TikTok 官方审核 priority_score=2。
	PublicationPriorityNormal = "normal"

	// PublicationStatusQueued 表示正在等待异步媒体结果，旧排队记录只允许人工继续。
	PublicationStatusQueued = "queued"
	// PublicationStatusProcessing 表示当前请求或媒体查询正在独占执行此阶段。
	PublicationStatusProcessing = "processing"
	// PublicationStatusSynced 表示剧目版本已同步到 TikTok，等待运营明确提交审核。
	PublicationStatusSynced = "synced"
	// PublicationStatusUnderReview 表示平台已经受理审核，Worker 会定期查询结果。
	PublicationStatusUnderReview = "under_review"
	// PublicationStatusApproved 表示已查询到当前版本审核通过，等待运营明确上线。
	PublicationStatusApproved = "approved"
	// PublicationStatusRejected 表示当前版本未通过审核，需要运营修改内容后重新提交。
	PublicationStatusRejected = "rejected"
	// PublicationStatusFailed 表示本次调用失败，需要运营确认后重试；同步写入不自动排队。
	PublicationStatusFailed = "failed"
	// PublicationStatusOnline 表示拥有方版本已确认上线，跨应用授权结果须逐项读取。
	PublicationStatusOnline = "online"
	// PublicationStatusOffline 表示 TikTok 官方下架已经完成。
	PublicationStatusOffline = "offline"

	// PublicationStageUploadCover 通过官方 Image API 获取剧目封面标识。
	PublicationStageUploadCover = "upload_cover"
	// PublicationStageCreateAlbum 创建官方 Album 草稿。
	PublicationStageCreateAlbum = "create_album"
	// PublicationStageRegisterVideos 登记并查询所有 BytePlus VID。
	PublicationStageRegisterVideos = "register_videos"
	// PublicationStageTranscodeVideos 使用官方模板转码并逐视频核对结果。
	PublicationStageTranscodeVideos = "transcode_videos"

	// PublicationStageUpdateAlbum 写入剧目信息、分集 VID 并取得 Episode 映射。
	PublicationStageUpdateAlbum = "update_album"
	// PublicationStageAwaitSubmitReview 暂停已同步版本，只有人工送审指令才能继续。
	PublicationStageAwaitSubmitReview = "await_submit_review"
	// PublicationStageSubmitReview 提交当前平台版本审核。
	PublicationStageSubmitReview = "submit_review"
	// PublicationStagePollReview 等待 Worker 定期查询审核结果。
	PublicationStagePollReview = "poll_review"
	// PublicationStageAwaitPublish 暂停已审核版本，只有人工上线指令才能继续。
	PublicationStageAwaitPublish = "await_publish"
	// PublicationStageSetOnlineVersion 把审核通过的版本设为官方线上版本。
	PublicationStageSetOnlineVersion = "set_online_version"
	// PublicationStageListAlbum 调用官方 Listing 接口上架剧目。
	PublicationStageListAlbum = "list_album"
	// PublicationStageDelistAlbum 调用官方 Listing 接口下架剧目。
	PublicationStageDelistAlbum = "delist_album"
	// PublicationStageDone 表示本次发布或下架编排已经结束。
	PublicationStageDone = "done"
)

// CanEditRejectedAlbumPublication 仅放行已明确被平台拒绝、未送审且没有待确认写入的 Album 更新任务。
// 保存内容时必须在数据库事务中再次核对这些条件并作废旧快照。
func CanEditRejectedAlbumPublication(publication TikTokPublication) bool {
	return publication.Status == PublicationStatusFailed && publication.Stage == PublicationStageUpdateAlbum &&
		publication.ErrorCode == "api_rejected" && !publication.ExternalWriteStarted &&
		publication.ReviewID == "" && publication.SubmittedAt == nil && publication.LeaseOwner == ""
}

// TikTokPublicationInput 是运营人员提交审核时选择的业务参数。
type TikTokPublicationInput struct {
	// Transcode 显式选择多清晰度转码；旧客户端未提供时沿用原转码行为。
	Transcode *bool `json:"transcode,omitempty"`
	// Priority 是 normal 或 urgent，拥有方和分发范围由服务端决定。
	Priority string `json:"priority"`
}

// TikTokPublication 保存一个短剧当前官方版本的可恢复编排事实。
// 所有平台标识即使只包含数字也必须按字符串保存和返回。
type TikTokPublication struct {
	// SubjectID 固定官方版本的发布归属，不随后台选择变化。
	SubjectID string `json:"subjectId"`
	// SkipTranscode 固定本次同步的源视频策略，历史工作流默认继续原转码。
	SkipTranscode bool `json:"-"`
	// CurrentContentChanged 表示当前就绪内容与任务冻结内容不同，不改变任务处理范围。
	CurrentContentChanged bool `json:"currentContentChanged"`
	// PublishRequested 记录运营对当前冻结版本的上线指令；旧记录默认为 false，禁止恢复自动上线。
	PublishRequested bool `json:"-"`
	// Appeal 保存已验证的申诉输入和历史调用事实，不自动重放申诉。
	Appeal *TikTokReviewAppealInput `json:"appeal,omitempty"`
	// ReviewItems 按平台项目标识保存官方单项审核状态。
	ReviewItems map[string]int `json:"reviewItems,omitempty"`
	// ExternalWriteStarted 在非幂等请求发出前持久化，进程中断后禁止直接重放。
	ExternalWriteStarted bool `json:"-"`
	// OwnerScope 冻结官方应用和云媒资身份，凭据轮换可以继续，但不能切换账号或空间。
	OwnerScope PublicationOwnerScope `json:"-"`
	// TranscodeKeySeed 为当前发布生成独立的随机幂等键前缀，旧任务为空时沿用历史键。
	TranscodeKeySeed string `json:"-"`
	// ContentSnapshot 是提交时冻结的内容，后台后续编辑不会改变正在送审的版本。
	ContentSnapshot *domain.Drama `json:"-"`
	// ContentSummary 仅公开本次固定集数与同步前线上范围，不包含媒体地址或内部快照。
	ContentSummary *PublicationContentSummary `json:"contentSummary,omitempty"`
	// Videos 保存逐集登记与查询进度。
	Videos []PublicationVideo `json:"videos"`
	// Transcodes 保存最多 1000 个视频一批的官方转码任务。
	Transcodes []PublicationTranscode `json:"transcodes"`
	// ReviewFailReasons 保存当前版本的官方审核意见。
	ReviewFailReasons []string `json:"reviewFailReasons,omitempty"`
	// Revision 是数据库比较并交换的修订号，阻止过期 Worker 覆盖新状态。
	Revision uint64 `json:"-"`
	// ID 是官方冻结版本记录标识，不是同步接口返回的任务 ID。
	ID string `json:"id"`
	// DramaID 是关联的内部短剧标识。
	DramaID string `json:"dramaId"`
	// OwnerApplicationID 是拥有并调用官方 API 的应用标识。
	OwnerApplicationID string `json:"ownerApplicationId"`
	// Priority 是运营选择的官方审核优先级。
	Priority string `json:"priority"`
	// Status 是管理端展示的稳定业务状态。
	Status string `json:"status"`
	// Stage 是当前或刚失败的官方处理阶段，媒体与审核阶段可等待平台结果。
	Stage string `json:"stage"`
	// PlatformCoverID 是 TikTok Image API 返回的封面标识。
	PlatformCoverID string `json:"platformCoverId,omitempty"`
	// PlatformAlbumID 是 TikTok Album 标识。
	PlatformAlbumID string `json:"platformAlbumId,omitempty"`
	// PlatformVersion 是 TikTok 当前待发布版本号。
	PlatformVersion int `json:"platformVersion,omitempty"`
	// ReviewID 是 TikTok 审核任务标识。
	ReviewID string `json:"reviewId,omitempty"`
	// ReviewStatus 是 TikTok Album Query 返回的原始审核状态整数。
	ReviewStatus int `json:"reviewStatus"`
	// PlatformPublishStatus 是 TikTok 返回的原始上下架状态整数。
	PlatformPublishStatus int `json:"platformPublishStatus"`
	// EpisodeIDsBySequence 按内部分集序号保存已确认的 TikTok Episode 字符串标识；更新前可保存同一官方归属的既有分集，更新成功后保存完整新版本映射。
	EpisodeIDsBySequence map[int]string `json:"episodeIdsBySequence,omitempty"`

	// ErrorCode 是可稳定映射为运营提示的安全错误分类。
	ErrorCode string `json:"errorCode,omitempty"`
	// ErrorMessage 是可直接展示给运营人员的友好说明，不保存平台原始异常。
	ErrorMessage string `json:"errorMessage,omitempty"`
	// DiagnosticCode 是外部写结果未知前的安全失败分类，用于重启后继续定位，不保存平台响应正文。
	DiagnosticCode string `json:"diagnosticCode,omitempty"`
	// Attempts 是当前任务累计执行失败次数。
	Attempts int `json:"attempts"`
	// NextAttemptAt 是 Worker 下一次允许领取任务的时间。
	NextAttemptAt time.Time `json:"nextAttemptAt"`
	// LeaseOwner 是当前数据库任务租约所有者，不通过 API 返回。
	LeaseOwner string `json:"-"`
	// LeaseExpiresAt 是当前数据库任务租约失效时间，不通过 API 返回。
	LeaseExpiresAt time.Time `json:"-"`
	// CreatedAt 是首次提交时间。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 是最近状态更新时间。
	UpdatedAt time.Time `json:"updatedAt"`
	// SubmittedAt 是官方审核受理时间。
	SubmittedAt *time.Time `json:"submittedAt,omitempty"`
	// OnlineAt 是官方上架完成时间。
	OnlineAt *time.Time `json:"onlineAt,omitempty"`
}

// Active 报告任务是否仍在渠道流程内；无执行中的待送审、待上线任务可由新任务替换，编辑区不受锁定。
func (p TikTokPublication) Active() bool {
	return p.Status == PublicationStatusQueued || p.Status == PublicationStatusProcessing || p.Status == PublicationStatusSynced || p.Status == PublicationStatusUnderReview || p.Status == PublicationStatusApproved
}

// RejectsContent 判断审核驳回后是否仍为原内容；撤回可重送，普通驳回要求内容修改。
// 比较任务自身的冻结内容，保留已有任务，不查询直连发布版本。
func (p TikTokPublication) RejectsContent(snapshot domain.Drama) bool {
	if p.Status != PublicationStatusRejected || p.ReviewStatus == 4 {
		return false
	}
	return p.ContentSnapshot != nil && domain.OfficialContentDigest(*p.ContentSnapshot) == domain.OfficialContentDigest(snapshot)
}

// AllowsMediaRestart 只允许明确失败的媒体步骤用同一内容、版本和官方资源归属重建任务；未知写入及活动租约必须先恢复。
func (p TikTokPublication) AllowsMediaRestart(next TikTokPublication) bool {
	return p.Status == PublicationStatusFailed && !p.ExternalWriteStarted && !publicationLeaseActive(p) &&
		(publicationMediaRestartRequired(p) || publicationManualTranscodeRestartRequired(p)) &&
		p.ContentSnapshot != nil && next.ContentSnapshot != nil &&
		p.DramaID == next.DramaID &&
		p.OwnerApplicationID == next.OwnerApplicationID && p.OwnerScope == next.OwnerScope &&
		p.PlatformAlbumID == next.PlatformAlbumID && p.PlatformVersion == next.PlatformVersion &&
		domain.ContentDigest(*p.ContentSnapshot) == domain.ContentDigest(*next.ContentSnapshot)
}

// TikTokPublicationRepository 定义发布任务及其内容可见性原子更新端口。
type TikTokPublicationRepository interface {
	// CheckTikTokUnpublish 从权威配置校验官方轮播与热播已移除该剧，供 Worker 发出下架请求前复核。
	CheckTikTokUnpublish(context.Context, string) error
	// BeginTikTokExternalWrite 核对发布任务的修订号及租约后，持久化非幂等写入已开始标记并回填新修订号；用于阻止进程中断后盲目重放。
	BeginTikTokExternalWrite(context.Context, *TikTokPublication) error
	// FindTikTokPublicationByDramaID 按内部短剧 ID 返回最近创建的官方发布任务；没有任务时返回内容域 ErrNotFound。
	FindTikTokPublicationByDramaID(context.Context, string) (TikTokPublication, error)
	// StartTikTokPublication 在事务中冻结当前就绪内容，创建 TK 任务并撤销旧修订；成功回填任务身份，官方版本由任务流程维护，任务竞争或内容变化返回 ErrConflict。
	StartTikTokPublication(context.Context, *TikTokPublication) error
	// RestartTikTokPublication 核对原失败任务的身份及修订后，用同一冻结内容创建媒体重试任务；不查询直连发布版本或草稿，旧任务保留。
	RestartTikTokPublication(context.Context, TikTokPublication, *TikTokPublication) error
	// ClaimTikTokPublication 领取一个到期且租约可用的发布队列任务；不扫描审核中和待上线记录，无可领任务返回 nil, nil。
	ClaimTikTokPublication(context.Context, string, time.Time, time.Time) (*TikTokPublication, error)
	// ClaimTikTokReview 领取一个审核中且租约可用的任务；由独立十分钟扫描器调用，不参与高频发布队列扫描。
	ClaimTikTokReview(context.Context, string, time.Time, time.Time) (*TikTokPublication, error)
	// UpdateTikTokPublication 核对任务身份、修订及租约后原子保存；下架排队及清除线上快照前检查官方推荐引用，被替换任务返回 ErrConflict。
	UpdateTikTokPublication(context.Context, TikTokPublication, *domain.Drama) error
}

// TikTokMediaProviderFactory 按拥有方应用创建隔离的官方 API 客户端。
// 实现只能从应用的 SecretRef 解析部署密钥，禁止把密钥写回领域对象。
type TikTokMediaProviderFactory func(application.Application) TikTokMediaProvider

// TikTokMediaProvider 封装 TikTok Media Asset Open API 的官方发布能力。
type TikTokMediaProvider interface {
	// CreateImage 以公开封面 URL 调用官方 Image API，返回平台封面 ID；此返回值不是本地封面资源 ID。
	CreateImage(context.Context, string) (string, error)
	// CreateAlbum 在当前拥有方应用创建空 Album，返回官方 Album ID 和初始版本；失败后是否可重放由发布检查点控制。
	CreateAlbum(context.Context) (CreateAlbumResult, error)
	// UpdateAlbum 将冻结的 AlbumDraft 写入指定平台版本，返回新版本及分集序号到官方 Episode ID 的映射。
	UpdateAlbum(context.Context, AlbumDraft) (UpdateAlbumResult, error)
	// SubmitReview 按 Album ID、平台版本和数值 priority_score 提交审核，返回官方审核任务 ID；返回成功仅代表受理。
	SubmitReview(context.Context, string, int, int) (string, error)
	// QueryAlbum 按 Album ID 和目标版本查询审核、线上版本及上下架状态；业务发布状态由 Worker 据此推进。
	QueryAlbum(context.Context, string, int) (AlbumObservation, error)
	// AuthorizeAlbum 把指定 Album 授权给目标应用 Client Key 集合；参数不是内部应用 ID。
	AuthorizeAlbum(context.Context, string, []string) error
	// SetOnlineVersion 将指定 Album 的审核通过版本设为线上版本；实际剧目上架另由 SetAlbumListed 完成。
	SetOnlineVersion(context.Context, string, int) error
	// SetAlbumListed 按 Album ID 设置官方上下架状态，listed 为 true 时上架、false 时下架；官方明确已上架也视为上架成功，其他拒绝返回错误。本方法不选择线上版本。
	SetAlbumListed(context.Context, string, bool) error

	// RegisterVideo 向 TikTok 登记已在 BytePlus 的视频及账号空间，返回可恢复的任务和 VID；须再查询确认视频可用。
	RegisterVideo(context.Context, TikTokVideoInput) (TikTokVideoRegistration, error)
	// QueryVideo 按登记 jobID、BytePlus VID 和账号 ID 查询视频状态；所有平台标识保持字符串。
	QueryVideo(context.Context, string, string, string) (TikTokVideoObservation, error)
	// StartTranscode 按固定幂等键提交一批 VID 的官方转码，返回任务 ID；重试沿用输入中的 IdempotencyKey。
	StartTranscode(context.Context, TikTokTranscodeInput) (string, error)
	// QueryTranscode 按转码 taskID 和 BytePlus 账号 ID 查询任务及逐视频结果；任务成功不能替代逐 VID 成功检查。
	QueryTranscode(context.Context, string, string) (TikTokTranscodeObservation, error)
	// GenerateMinisLink 根据小程序内部路径和展示信息调用官方链接接口，返回官方 Minis 链接；不拼接替代链接。
	GenerateMinisLink(context.Context, MinisLinkInput) (string, error)
}

// TikTokAlbumRecoveryProvider 通过官方查询核实结果未知的版本更新，不再次创建或编辑剧目。
type TikTokAlbumRecoveryProvider interface {
	// ResolveAlbumUpdate 查询指定 Album 的版本，只返回与冻结草稿完整一致、晚于原版本且尚未送审的唯一最新版本及分集映射；无法确认时返回错误，不执行外部写入。
	ResolveAlbumUpdate(context.Context, AlbumDraft) (UpdateAlbumResult, error)
}

// TikTokAlbumRetryProvider 在人工恢复命令中核对未知更新，并使用已确认的原版本保护条件恢复写入。
type TikTokAlbumRetryProvider interface {
	// RetryAlbumUpdate 优先返回已写入且匹配冻结内容的新版本；仅原版本完整可见且未推进时携带原版本号重试，不创建 Album、不送审、不上线。
	RetryAlbumUpdate(context.Context, AlbumDraft) (UpdateAlbumResult, error)
}

const ()

// CreateAlbumResult 是官方创建空 Album 后返回的字符串标识和初始版本。
type CreateAlbumResult struct {
	// AlbumID 是 TikTok Album 标识。
	AlbumID string
	// Version 是初始草稿版本。
	Version int
}

// AlbumDraft 是发送给官方 Album Update 的内容快照。
type AlbumDraft struct {
	// ClientKey 是拥有方应用的公开 Client Key。
	ClientKey string
	// AlbumID 是要更新的 TikTok Album 标识。
	AlbumID string
	// Version 是并发保护使用的当前平台版本。
	Version int
	// Language 是官方接受的原声语言字符串代码。
	Language string
	// AlbumStatus 是官方内容连载状态：1 未发布、2 连载中、3 已完结。
	AlbumStatus int
	// Title 是默认语言剧名。
	Title string
	// Description 是默认语言简介。
	Description string
	// CoverID 是官方 Image API 封面标识。
	CoverID string
	// Year 是发行年份。
	Year int
	// DramaType 是 TikTok 官方短剧类型。
	DramaType int
	// TagIDs 是 TikTok 标签字符串标识，HTTP 边界再安全编码为数字。
	TagIDs []string
	// Episodes 是按序号连续排列的剧集内容。
	Episodes []AlbumEpisodeDraft
}

// AlbumEpisodeDraft 是 Album Update 中的单集快照。
type AlbumEpisodeDraft struct {
	// Sequence 是从 1 开始的分集序号。
	Sequence int
	// Title 是剧集标题。
	Title string
	// CoverID 是当前剧集沿用的官方封面标识。
	CoverID string
	// BytePlusVID 是 BytePlus VOD 视频标识。
	BytePlusVID string
	// PlatformEpisodeID 是更新既有平台剧集时携带的字符串标识。
	PlatformEpisodeID string
}

// UpdateAlbumResult 是官方更新后返回的新版本和分集标识映射。
type UpdateAlbumResult struct {
	// Version 是新生成的平台版本。
	Version int
	// EpisodeIDsBySequence 按本地序号规范化官方 episode_id_map。
	EpisodeIDsBySequence map[int]string
}

// AlbumObservation 是 Album Query 对目标版本的权威状态投影。
type AlbumObservation struct {
	// ReviewItems 区分被拒、申诉中和申诉通过的具体剧目或剧集。
	ReviewItems map[string]int
	// ReviewStatus 是 0-7 的官方审核状态。
	ReviewStatus int
	// PublishStatus 是 0 默认、1 已上架或 2 已下架。
	PublishStatus int
	// OnlineVersion 是当前线上版本，尚未设置时为 0。
	OnlineVersion int
	// ReviewFailReasons 是当前版本的官方审核意见，只作为内容展示，不作为指令执行。
	ReviewFailReasons []string
}

// TikTokReviewAppealInput 对应官方单项审核申诉；标识永远保留字符串。
type TikTokReviewAppealInput struct {
	// ItemType 是官方对象类型：1 剧目、2 剧集。
	ItemType int `json:"itemType"`
	// ItemID 是本次版本映射中的平台对象 ID。
	ItemID string `json:"itemId"`
	// Reason 是运营提交给官方的申诉说明。
	Reason string `json:"reason"`
	// AttachmentURLs 是最多十条公开证据地址，不在日志中输出。
	AttachmentURLs []string `json:"attachmentUrls,omitempty"`
}

// TikTokReviewProvider 为已建立版本提供官方申诉和撤回操作。
type TikTokReviewProvider interface {
	// AppealReview 提交单项申诉，成功仅代表平台受理。
	AppealReview(context.Context, string, string, TikTokReviewAppealInput) error
	// WithdrawReview 撤回指定审核版本，不改变旧线上版本。
	WithdrawReview(context.Context, string, int) error
}
