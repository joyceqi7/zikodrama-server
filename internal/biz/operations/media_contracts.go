package operations

import (
	"context"
	"io"
	"time"

	domain "drama-server/internal/biz/consumer"
)

// Upload 是传给媒资 Provider 的受限上传内容。
type Upload struct {
	// FileName 是入口去除目录部分并校验扩展名后的文件名。
	FileName string
	// Size 是协议层声明的文件字节数，必须与实际读取内容一致。
	Size int64
	// Reader 提供入口校验后重新构造的文件流，供 Provider 消费；读取后不会自动重置。
	Reader io.Reader
	// CallbackArgs 是随上传写入 VOD 回调的业务透传参数。
	CallbackArgs string
}

// MediaAsset 是 BytePlus VOD 媒资处理结果。
type MediaAsset struct {
	// VID 是 BytePlus VOD 为视频分配的字符串标识。
	VID string `json:"vid"`
	// PosterURL 是媒资处理产出的海报地址。
	PosterURL string `json:"posterUrl"`
	// DurationSeconds 是 VOD 探测到的视频时长。
	DurationSeconds int `json:"durationSeconds"`
	// Status 是远端媒资处理状态。
	Status string `json:"status"`
	// UploadRequestID 是 VOD 本次接收上传的请求标识，仅供任务确认关联，不向客户端暴露。
	UploadRequestID string `json:"-"`
}

// MaxCoverUploadBytes 是入口允许的封面文件上限，按实际读取字节检查。
const MaxCoverUploadBytes int64 = 10 << 20

// CoverAsset 是上传后的封面媒资。
type CoverAsset struct {
	// UploadID 是 封面上传任务标识。
	UploadID string `json:"uploadId"`
	// MID 是 BytePlus VOD 素材管理中的图片标识。
	MID string `json:"mid"`
	// ID 保留旧管理端合同，值与 MID 相同。
	ID string `json:"id"`
	// URL 是可保存到短剧聚合的封面地址。
	URL string `json:"url"`
	// SpaceName 是资源所在 VOD 空间，仅供服务端持久化。
	SpaceName string `json:"-"`
	// ProviderFileName 是 VOD 素材的 StoreUri，仅用于删除后的权威存在性确认。
	ProviderFileName string `json:"-"`
}

// MediaUploadModeDirect 标记由浏览器直接上传到 VOD 的模式。
const MediaUploadModeDirect = "direct"

// MediaUploadToken 是前端直传使用的短期凭据。
type MediaUploadToken struct {
	// AccessKeyID 是短期访问密钥标识，不得持久化。
	AccessKeyID string `json:"AccessKeyID"`
	// SecretAccessKey 是短期访问密钥，不得记录日志或持久化。
	SecretAccessKey string `json:"SecretAccessKey"`
	// SessionToken 是短期会话令牌，不得记录日志或持久化。
	SessionToken string `json:"SessionToken"`
	// ExpiredTime 是上传凭据的过期时间。
	ExpiredTime string `json:"ExpiredTime"`
	// CurrentTime 是上传凭据签发时的服务端时间。
	CurrentTime string `json:"CurrentTime"`
}

// MediaUploadAction 是上传后由 BytePlus VOD 执行的处理动作。
type MediaUploadAction struct {
	// Name 是 BytePlus VOD 上传后处理动作名称。
	Name string `json:"Name"`
	// Input 是动作需要的字符串参数。
	Input map[string]string `json:"input,omitempty"`
}

// MediaUploadAuthorization 是前端直传 BytePlus VOD 的授权结果。
type MediaUploadAuthorization struct {
	// Mode 固定为 direct，表示浏览器直接上传到 VOD。
	Mode string `json:"mode"`
	// AppID 是 BytePlus VOD 应用标识，按字符串下发。
	AppID string `json:"appId,omitempty"`
	// SpaceName 是凭据被限制使用的 VOD 空间。
	SpaceName string `json:"spaceName,omitempty"`
	// Region 是 VOD 空间所在地域。
	Region string `json:"region,omitempty"`
	// MaxFileBytes 是单个视频允许上传的最大字节数。
	MaxFileBytes int64 `json:"maxFileBytes"`
	// Token 是仅供本次直传使用的短期凭据。
	Token MediaUploadToken `json:"token,omitempty"`
	// ProcessActions 是上传完成后由 VOD 执行的处理动作。
	ProcessActions []MediaUploadAction `json:"processActions,omitempty"`
	// CallbackArgs 是随单个文件上传写入 VOD 的业务透传信息。
	CallbackArgs string `json:"callbackArgs,omitempty"`
}

const (
	// MediaResourceVideo 表示以 VID 标识的 VOD 视频资源。
	MediaResourceVideo = "video"
	// MediaResourceMaterial 表示以 MID 或 StoreUri 定位的 VOD 素材资源。
	MediaResourceMaterial = "material"

	// MediaProviderUnknown 表示尚无足够远端观察确定资源状态。
	MediaProviderUnknown = "Unknown"
	// MediaProviderPublished 表示远端资源已发布。
	MediaProviderPublished = "Published"
	// MediaProviderMissing 表示权威观察确认远端资源不存在。
	MediaProviderMissing = "Missing"
)

// EpisodeMediaUploadReservation 是分集上传预留任务及专属直传配置。
type EpisodeMediaUploadReservation struct {
	// UploadID 是上传前创建的上传任务 ID，完成回传须携带同一值。
	UploadID string `json:"uploadId"`
	// Authorization 是绑定本次预留回调参数的短期 VOD 直传配置。
	Authorization MediaUploadAuthorization `json:"authorization"`
}

// EpisodeMediaUploadReservationInput 描述待上传文件，文件内容仍由浏览器直接发送到 VOD。
type EpisodeMediaUploadReservationInput struct {
	// FileName 是待上传视频文件名；入口去除目录并校验允许的扩展名。
	FileName string `json:"fileName"`
	// Size 是待上传文件字节数，还须满足提供方配置的最大值。
	Size int64 `json:"size"`
}

// DirectUploadDiagnosticInput 是浏览器直传失败时可安全回传的诊断摘要。
// 不包含上传地址、文件内容或任何 STS 凭据，仅用于服务端记录可定位错误。
type DirectUploadDiagnosticInput struct {
	// Stage 是官方 SDK 报告的失败阶段。
	Stage string `json:"stage"`
	// Code 是官方 SDK 的稳定错误码。
	Code string `json:"code"`
	// Status 是请求存在响应时的 HTTP 状态；0 表示未取得响应。
	Status int `json:"status"`
	// Reason 是剔除 URL 和响应正文后的 SDK 简短失败原因。
	Reason string `json:"reason"`
}

// MediaObservation 是对单个 VOD 视频存在性和发布状态的只读观察结果。
type MediaObservation struct {
	// VID 是此次查询的视频标识，应用观察前须与预期 VID 一致。
	VID string
	// Exists 表示成功查询是否确认远端仍存在；查询异常通过 error 返回。
	Exists bool
	// PublishStatus 是 BytePlus 当前视频发布状态。
	PublishStatus string
	// Title 是远端标题快照，用于判断是否需要同步本地内容。
	Title string
	// Description 是远端简介快照。
	Description string
	// Tags 是远端标签集合，用于与运营内容比较。
	Tags []string
	// ClassificationID 是远端分类 ID，按字符串保存。
	ClassificationID string
}

// VODEpisodeMetadata 是由运营后台生成、需要投影到 VOD 的分集业务元数据。
// VOD 只保存该投影，不反向成为短剧内容的数据源。
type VODEpisodeMetadata struct {
	// VID 是投影目标视频；准备上传分类时尚可为空，同步时必须已知。
	VID string
	// Title 是从运营短剧和分集生成的远端标题。
	Title string
	// Description 是运营维护的分集简介投影。
	Description string
	// Tags 是投影到 VOD 的业务标签。
	Tags []string
	// PrimaryClassificationName 是第一级分类的展示名称。
	PrimaryClassificationName string
	// PrimaryClassificationKey 是第一级分类的稳定业务键，用于识别已有分类。
	PrimaryClassificationKey string
	// DramaClassificationName 是短剧二级分类的展示名称。
	DramaClassificationName string
	// DramaClassificationKey 是短剧二级分类的稳定业务键，避免同名短剧混用分类。
	DramaClassificationKey string
}

// MediaResourceObservation 是 Vid 或 Mid 的权威存在性观察；查询失败必须通过 error 返回，
// 不能把空响应或依赖异常解释为远端不存在。
type MediaResourceObservation struct {
	// ResourceClass 标明被观察对象是 video 还是 material。
	ResourceClass string
	// ProviderAssetID 是实际观察的 VID 或 MID，需与待删除资源匹配。
	ProviderAssetID string
	// Exists 表示成功查询确认的存在性；false 不能用于表达请求失败。
	Exists bool
}

// DirectMediaUploadInput 是完成前端直传后的入队命令。
type DirectMediaUploadInput struct {
	// UploadID 是文件上传前由服务端创建的 分集上传任务标识。
	UploadID string `json:"uploadId"`
	// VID 是浏览器直传成功后返回的 VOD 视频标识。
	VID string `json:"vid"`
	// UploadRequestID 是 VOD 上传请求标识，用于完成确认和问题追踪。
	UploadRequestID string `json:"uploadRequestId"`
	// FileName 是客户端选择的视频文件名，仅用于格式校验和运营展示。
	FileName string `json:"fileName"`
	// Size 是客户端上传的视频字节数。
	Size int64 `json:"size"`
}

const (
	// MediaJobQueued 表示处理任务等待 Worker 领取。
	MediaJobQueued = "queued"
	// MediaJobRunning 表示任务已取得租约，正在执行一次处理。
	MediaJobRunning = "running"
	// MediaJobProcessing 表示远端尚在处理，等待下一次轮询。
	MediaJobProcessing = "processing"
	// MediaJobSucceeded 表示媒资确认与分集绑定已成功。
	MediaJobSucceeded = "succeeded"
	// MediaJobFailed 表示处理失败，是否再次处理由重试策略决定。
	MediaJobFailed = "failed"
	// MediaJobUploading 表示服务端正在把浏览器提交的临时文件上传到 BytePlus。
	MediaJobUploading = "uploading"

	// VODEventFileUploadComplete 对应 BytePlus 视频上传完成事件。
	VODEventFileUploadComplete = "FileUploadComplete"
	// VODEventMaterialUploadComplete 对应 BytePlus 素材上传完成事件。
	VODEventMaterialUploadComplete = "MaterialUploadComplete"
	// VODEventDeleteMediaComplete 对应 BytePlus 媒资删除完成事件。
	VODEventDeleteMediaComplete = "DeleteMediaComplete"
	// VODEventExecutionComplete 对应 BytePlus 工作流执行完成事件。
	VODEventExecutionComplete = "ExecutionComplete"
	// VODEventTranscodeComplete 对应 BytePlus 转码完成事件。
	VODEventTranscodeComplete = "TranscodeComplete"
)

// VODEvent 是验签后的官方事件，用于同步唤醒对应上传任务。
type VODEvent struct {
	// RequestID 是平台回调请求标识，仅用于诊断。
	RequestID string
	// Version 是平台协议版本。
	Version string
	// EventType 是平台事件类型。
	EventType string
	// EventTime 是平台发生时间。
	EventTime time.Time
	// ResourceClass 区分视频和素材。
	ResourceClass string
	// ProviderAssetID 是远端资源标识。
	ProviderAssetID string
	// SpaceName 是远端空间。
	SpaceName string
	// UploadID 定位上传任务，不能代替归属校验。
	UploadID string
	// CallbackArgs 是已验签的业务透传参数。
	CallbackArgs string
	// Code 是官方处理结果。
	Code string
	// Message 是平台说明，不直接记录日志。
	Message string
	// PosterURI 是平台返回的海报信息。
	PosterURI string
}

// MediaResource 是剧拥有的远端文件定位值，不具有独立生命周期或仓储。
type MediaResource struct {
	// ResourceClass 区分视频和图片删除接口。
	ResourceClass string
	// SpaceName 固定上传时使用的 VOD 空间。
	SpaceName string
	// ProviderAssetID 是远端 VID 或 MID。
	ProviderAssetID string
	// ProviderFileName 是图片 StoreUri。
	ProviderFileName string
}

const (
	// MediaJobUpload 表示分集视频上传确认任务。
	MediaJobUpload = "upload"
	// MediaJobCover 表示封面上传任务，保存提交表单前的临时上传结果。
	MediaJobCover = "cover"
	// MediaJobDelete 表示剧释放文件后的远端删除任务。
	MediaJobDelete = "delete"
	// MediaJobEntryPreview 表示为已发布直连首集生成开头片段。
	MediaJobEntryPreview = "entry_preview"
	// MediaJobReserved 表示已签发上传授权，等待远端文件标识。
	MediaJobReserved = "reserved"
	// MediaJobUploaded 表示封面上传完成，等待保存内容。
	MediaJobUploaded = "uploaded"
)

// MediaJob 是可被多实例 Worker 竞争处理的持久化媒资任务。
type MediaJob struct {
	// SubjectID 是任务创建时冻结的主体，供 Worker 恢复时重建可信内容上下文。
	SubjectID string `json:"-"`
	// Kind 区分视频上传、封面上传、首集片段生成与删除。
	Kind string `json:"kind"`
	// DramaID 是所属剧；删除剧后仍保留定位信息，不使用级联外键。
	DramaID string `json:"dramaId"`
	// ResourceClass 指定远端资源类型。
	ResourceClass string `json:"-"`
	// SpaceName 固定本次上传或删除使用的空间。
	SpaceName string `json:"-"`
	// ProviderFileName 是封面文件的 StoreUri。
	ProviderFileName string `json:"-"`
	// PublicURL 是尚未提交的封面上传结果。
	PublicURL string `json:"-"`

	// ID 是数据库自增的媒资任务主键，以字符串返回。
	ID string `json:"jobId"`
	// IdempotencyKey 保证同一完成请求只创建一个任务，不对客户端回显。
	IdempotencyKey string `json:"-"`
	// EpisodeID 是任务最终需要绑定的剧集标识。
	EpisodeID string `json:"episodeId"`
	// FileName 是直传视频的原始文件名。
	FileName string `json:"fileName"`
	// Size 是直传视频字节数。
	Size int64 `json:"size"`
	// ExpectedVID 是上传并发校验的原 VID，或生成首集片段时使用的正片 VID。
	ExpectedVID string `json:"-"`
	// PreviewSeconds 仅用于首集片段任务，限制派生产物时长。
	PreviewSeconds int `json:"-"`
	// RemoteVID 是本次直传在 BytePlus VOD 中的目标 VID。
	RemoteVID string `json:"remoteVid,omitempty"`
	// UploadRequestID 是 BytePlus 上传或剪辑请求标识，供 Worker 恢复远端任务。
	UploadRequestID string `json:"uploadRequestId,omitempty"`
	// Status 是 queued、running、processing、succeeded 或 failed。
	Status string `json:"status"`
	// ProcessingStartedAt 是首次观察到远端仍在处理的时间。
	ProcessingStartedAt time.Time `json:"-"`
	// ProcessingDeadlineAt 是任务创建时固化的 VOD 处理截止时间。
	ProcessingDeadlineAt time.Time `json:"-"`
	// Attempts 是已经领取并处理该任务的次数。
	Attempts int `json:"attempts"`
	// NextAttemptAt 是失败后允许再次领取任务的最早时间。
	NextAttemptAt time.Time `json:"nextAttemptAt"`
	// LeaseOwner 是当前领取任务的实例标识。
	LeaseOwner string `json:"-"`
	// LeaseExpiresAt 是当前任务租约到期时间，过期后其他实例可以接管。
	LeaseExpiresAt *time.Time `json:"-"`
	// Error 是截断后可供运营诊断的最近一次错误。
	Error string `json:"error,omitempty"`
	// Episode 是查询任务时按需补充的剧集视图，不参与任务持久化。
	Episode *domain.Episode `json:"episode,omitempty"`
	// CreatedAt 是任务创建时间。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 是任务最后一次状态变更时间，同时用于乐观领取。
	UpdatedAt time.Time `json:"updatedAt"`
}

// MediaJobLeaseClaim 描述 Worker 对媒资任务的带时限领取。
type MediaJobLeaseClaim struct {
	// ID 是准备领取的任务标识。
	ID string
	// ExpectedStatus 是领取时必须仍然匹配的任务状态。
	ExpectedStatus string
	// ExpectedUpdatedAt 是领取时必须仍然匹配的乐观锁时间。
	ExpectedUpdatedAt time.Time
	// LeaseOwner 是本次领取使用的实例标识。
	LeaseOwner string
	// ClaimedAt 是任务领取时间。
	ClaimedAt time.Time
	// LeaseExpiresAt 是其他实例允许接管任务的时间。
	LeaseExpiresAt time.Time
}

// MediaJobRepository 定义分布式媒资任务的持久化和租约端口。
type MediaJobRepository interface {
	// CreateMediaJob 创建持久化媒资任务，成功后回填数据库分配的字符串 ID；完成请求幂等键重复时返回 ErrConflict，由 Service 读取已有任务处理重放。
	CreateMediaJob(context.Context, *MediaJob) error
	// FindMediaJob 按内部任务 ID 查询处理状态；不存在时返回内容域 ErrNotFound。
	FindMediaJob(context.Context, string) (MediaJob, error)
	// FindMediaJobByIdempotencyKey 按上传完成请求的幂等键查询已有任务；未找到返回内容域 ErrNotFound，调用方仍须核对原任务的目标和输入。
	FindMediaJobByIdempotencyKey(context.Context, string) (MediaJob, error)
	// ListMediaJobsByEpisodeIDs 返回指定内部剧集 ID 集合的媒资任务，用于管理端补充各集处理进度。
	ListMediaJobsByEpisodeIDs(context.Context, []string) ([]MediaJob, error)
	// ListMediaJobsByStatus 按状态集合查询到期可处理任务，并限制结果数量；读取不代表已取得租约。
	ListMediaJobsByStatus(context.Context, []string, int) ([]MediaJob, error)
	// ListMediaJobsUpdatedBefore 按状态集合、更新时间上界和数量查询旧任务，供 Worker 恢复中断的处理；领取仍须比较快照。
	ListMediaJobsUpdatedBefore(context.Context, []string, time.Time, int) ([]MediaJob, error)
	// ClaimMediaJobLease 以任务状态和更新时间快照竞争租约；true 表示领取成功，false 表示已变化或尚不可领，不是数据库故障。
	ClaimMediaJobLease(context.Context, MediaJobLeaseClaim) (bool, error)
	// RenewMediaJobLease 按任务 ID、leaseOwner、续租时刻和新截止时刻延长尚未过期的 running 租约；返回 false 表示租约已失效或不再属于该实例。
	RenewMediaJobLease(context.Context, string, string, time.Time, time.Time) (bool, error)
	// UpdateMediaJob 保存任务进度；LeaseOwner 非空时要求数据库所有者仍匹配，否则返回 ErrConflict，不带租约且无任务时返回内容域 ErrNotFound。
	UpdateMediaJob(context.Context, MediaJob) error
	// DeleteMediaJob 原子回收结束或过期预留任务，未绑定的产物先写入删除任务；活动任务返回冲突。
	DeleteMediaJob(context.Context, string) error
	// ConfirmMediaUpload 锁定剧和预留任务，按文件、目标、远端 ID 核对并幂等确认上传。
	ConfirmMediaUpload(context.Context, MediaJob) error
	// ApplyVODCallback 仅唤醒匹配的上传任务；不以回调内容覆盖分集状态。
	ApplyVODCallback(context.Context, VODEvent) (bool, error)
	// QueueMediaDeletion 保存独立于内容行生命周期的删除任务，重复请求不重复创建。
	QueueMediaDeletion(context.Context, MediaJob) error
	// IsMediaResourceUsed 检查草稿、线上快照、直连版本、官方任务和未结束上传，阻止误删仍使用的文件。
	IsMediaResourceUsed(context.Context, MediaJob) (bool, error)
	// CountMediaDeletionJobs 统计未完成的删除任务。
	CountMediaDeletionJobs(context.Context) (int, error)
}

// MediaProvider 是所有媒资能力共享的最小生命周期端口。
type MediaProvider interface {
	// Delete 按资源类别、Space、远端 VID/MID 和素材 StoreUri 提交删除；返回成功不保证资源已消失，须再观察或等待回调确认。
	Delete(context.Context, string, string, string, string) error
}

// EntryPreviewProvider 使用官方剪辑服务处理独立首集片段。
type EntryPreviewProvider interface {
	// SubmitEntryPreview 用原 VID 和时长提交剪辑，返回可供轮询的官方请求 ID。
	SubmitEntryPreview(ctx context.Context, vid string, seconds int) (string, error)
	// EntryPreviewResult 返回经空间和时长校验并发布的产物 VID；未完成时返回 ErrMediaProcessing。
	EntryPreviewResult(ctx context.Context, requestID, originalVID string, seconds int) (string, error)
}

// CoverProvider 提供封面上传能力。
type CoverProvider interface {
	// UploadCover 上传已经入口校验的图片，返回 BytePlus MID 与可保存的封面地址；上传任务 ID 由业务登记补充。
	UploadCover(context.Context, Upload) (CoverAsset, error)
}

// DirectMediaProvider 提供浏览器直传和上传完成确认能力。
type DirectMediaProvider interface {
	// UploadAuthorization 签发限定 VOD 空间的短期直传凭据，并返回业务文件大小上限；调用方不得记录或持久化 Token。
	UploadAuthorization(context.Context) (MediaUploadAuthorization, error)
	// FinalizeUpload 以 VID 查询原片与空间并提交 VOD 发布设置；上传请求 ID 用于返回结果的任务关联，仍处理中返回 ErrMediaProcessing，不能凭客户端提示直接绑定。
	FinalizeUpload(context.Context, string, string) (MediaAsset, error)
}

// ProxiedMediaUploadProvider 提供服务端代传视频能力，用于浏览器无法建立 VOD multipart 会话时的自动恢复。
type ProxiedMediaUploadProvider interface {
	// UploadEpisodeMedia 使用受限文件流上传视频并返回 VOD 分配的 VID；调用方须先校验预留任务归属和文件大小。
	UploadEpisodeMedia(context.Context, Upload) (MediaAsset, error)
}

// MediaObservationProvider 提供已绑定视频的只读存在性和发布状态观察。
type MediaObservationProvider interface {
	// ObserveMedia 按 VID 读取远端存在性、发布状态和元数据；依赖故障返回 error，不按不存在处理。
	ObserveMedia(context.Context, string) (MediaObservation, error)
}

// MediaReconciliationRepository 以 MySQL 检查点和租约分批巡检剧目，供生产仓储实现。
type MediaReconciliationRepository interface {
	// ClaimMediaReconciliation 领取下一部剧；claimed=false 表示其他实例持有有效租约，空 ID 表示本轮到尾。
	ClaimMediaReconciliation(context.Context, string) (dramaID string, claimed bool, err error)
	// RenewMediaReconciliation 在处理分集期间续租；失去所有权返回错误并停止本轮。
	RenewMediaReconciliation(context.Context, string, string) error
	// FinishMediaReconciliation 成功处理时推进检查点，失败或取消时只释放租约。
	FinishMediaReconciliation(context.Context, string, string, bool) error
}

// VODCatalogProvider 把运营后台的分集元数据投影到 VOD，不管理 VOD 分类。
type VODCatalogProvider interface {
	// SyncVODEpisodeMetadata 对照同一 VID 的权威观察，只把变化的标题、简介和标签投影到 VOD；观察不存在或 VID 不符时拒绝。
	SyncVODEpisodeMetadata(context.Context, VODEpisodeMetadata, MediaObservation) error
}

// MediaResourceObservationProvider 为删除任务查询 Vid/Mid 是否仍存在。
type MediaResourceObservationProvider interface {
	// ObserveMediaResource 按资源定位值中的类型和远端身份确认 VID/MID 是否存在；只有成功观察可作为删除完成依据。
	ObserveMediaResource(context.Context, MediaResource) (MediaResourceObservation, error)
}

// MediaPublicationScopeProvider 提供源视频所在的只读空间与区域，不返回访问凭据。
type MediaPublicationScopeProvider interface {
	// PublicationMediaScope 返回源视频配置的区域和 Space，供发布时核对拥有方授权范围；不返回任何凭据。
	PublicationMediaScope(context.Context) (region string, spaceName string, err error)
}

// MediaProcessingPolicyProvider 提供创建媒资任务时需要固化的 VOD 处理策略。
type MediaProcessingPolicyProvider interface {
	// ProcessingTimeout 返回新建媒资任务需要冻结的最大远端处理时长；配置错误通过 error 返回。
	ProcessingTimeout(context.Context) (time.Duration, error)
}
