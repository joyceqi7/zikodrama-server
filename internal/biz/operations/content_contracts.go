package operations

import (
	"context"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
)

// DramaInput 是创建或整体更新短剧聚合的运营命令。
type DramaInput struct {
	// Title 是默认语言下展示的短剧标题。
	Title string `json:"title"`
	// Description 是默认语言下展示的短剧简介。
	Description string `json:"description"`
	// CoverURL 是已经上传到媒资服务的封面地址。
	CoverURL string `json:"coverUrl"`
	// CoverUploadID 是封面上传返回的 Drama 资源标识；管理端不得提交 Mid 代替。
	CoverUploadID string `json:"coverUploadId,omitempty"`
	// Status 是运营维护的内容状态，不等同于 PublishStatus。
	Status string `json:"status"`
	// Year 是短剧内容对应的年份。
	Year int `json:"year"`
	// Language 是默认内容使用的语言代码。
	Language string `json:"language"`
	// DramaType 是短剧类型字典中的整数值。
	DramaType int `json:"dramaType"`
	// TagIDs 是短剧关联的全局 TikTok 标签标识集合。
	TagIDs []string `json:"tagIds"`

	// Episodes 是短剧聚合内按 Number 连续排列的全部剧集，可为空以保存草稿；更新时整体替换集合。
	Episodes []EpisodeInput `json:"episodes"`
}

// DramaOperationsInput 只更新运营协作信息，不改动内容草稿或发布状态。
type DramaOperationsInput struct {
	Assignee string     `json:"assignee"`
	Note     string     `json:"note"`
	DueAt    *time.Time `json:"dueAt"`
}

// EpisodeInput 是短剧聚合中的剧集输入。
type EpisodeInput struct {
	// ID 是已有剧集标识；创建新剧集时必须留空。
	ID string `json:"id,omitempty"`
	// Number 是剧集在短剧中的顺序编号，必须从 1 连续递增。
	Number int `json:"number"`
	// Title 是默认语言下展示的剧集标题。
	Title string `json:"title"`
	// Description 是默认语言下展示的剧集简介。
	Description string `json:"description"`
	// PosterURL 是剧集海报地址；媒资完成后可以由 VOD 结果更新。
	PosterURL string `json:"posterUrl"`
	// DurationSeconds 是剧集时长；大于零的输入才覆盖已有值。
	DurationSeconds int `json:"durationSeconds"`
}

// Overview 是短剧和媒资任务的运营概览。
type Overview struct {
	// DramaCount 是短剧总数。
	DramaCount int `json:"dramaCount"`
	// EpisodeCount 是所有短剧包含的剧集总数。
	EpisodeCount int `json:"episodeCount"`
	// PublishedCount 是已对消费者发布的短剧数。
	PublishedCount int `json:"publishedCount"`
	// PendingMediaCount 是媒资尚未 ready 的剧集数。
	PendingMediaCount int `json:"pendingMediaCount"`
	// MediaCleanupCount 是共享数据库中仍待处理的远端媒资清理任务数。
	MediaCleanupCount int `json:"mediaCleanupCount"`
}

// CatalogRepository 定义短剧聚合管理用例需要的持久化端口。
type CatalogRepository interface {
	// FindVisibleSummaries 为已鉴权的主体渠道管理提供线上摘要，不读取草稿或跨主体内容。
	FindVisibleSummaries(context.Context, []string) ([]domain.DramaSummary, error)
	// PrepareDrama 锁定当前草稿并确认就绪，不生成版本，内容未就绪返回 ErrPublicationNotReady。
	PrepareDrama(context.Context, string, string) (*domain.Drama, error)
	// PrepareDramas 锁定并校验整批当前内容；任一不存在或未就绪时整体回滚，重复确认幂等且不生成发布版本。
	PrepareDramas(context.Context, []string, string) error
	// DramaOverview 直接聚合剧和分集数量，不加载详情。
	DramaOverview(context.Context) (*Overview, error)
	// ListDramaPage 在存储层分页查询运营摘要。
	ListDramaPage(context.Context, DramaListQuery) (DramaListPage, error)
	// ListAllDramas 列出全局短剧编辑聚合，包含分集；不按应用授权范围筛选消费者可见内容。
	ListAllDramas(context.Context) ([]domain.Drama, error)
	// FindDrama 按内部短剧 ID 返回编辑聚合；不存在时返回内容域 ErrNotFound。
	FindDrama(context.Context, string) (*domain.Drama, error)
	// SaveDrama 保存短剧记录；需要同时替换分集和封面时使用 SaveDramaAggregate。
	SaveDrama(context.Context, domain.Drama) error
	// SaveDramaOperations 独立保存负责人、备注和截止时间，不触发内容重新送审。
	SaveDramaOperations(context.Context, string, string, string, *time.Time) error
	// SaveDramaAggregate 为无 ID 的短剧和新分集分配数据库主键，成功提交后回填字符串 ID。在同一事务替换短剧编辑内容、分集和封面上传结果；保留已发布快照所需分集及平台映射，拒绝不可编辑状态；输入 VID 与库内不同时保留库内媒资，不能用聚合编辑替代媒资绑定。
	// 分集可为空，支持先保存基本资料；空列表清除编辑区分集，并在事务内安排媒资清理，实际删除前仍检查线上引用。
	SaveDramaAggregate(context.Context, *domain.Drama, *MediaJob) error
	// DeleteDrama 删除短剧及关联编辑内容，并在同一事务安排文件清理；远端删除由异步任务完成。
	DeleteDrama(context.Context, string) error
	// FindEpisode 按内部剧集 ID 返回当前分集；不存在时返回内容域 ErrNotFound。
	FindEpisode(context.Context, string) (*domain.Episode, error)
	// SaveEpisode 保存单集字段；该入口不提供 SaveEpisodeMedia 的预期 VID 并发保护。
	SaveEpisode(context.Context, domain.Episode) error
	// SaveEpisodeMedia 以 expectedVID 校验旧视频后，在事务中替换分集媒资并安排旧视频清理；uploadID 是本次上传任务 ID，归属或编辑状态已变化时返回 ErrConflict。
	SaveEpisodeMedia(context.Context, domain.Episode, domain.Drama, string, string) error
	// UpdateEpisodeMediaStatus 按 episodeID 和 expectedVID 条件投影 status；旧视频已被替换时不覆盖新视频，未匹配记录返回内容域 ErrNotFound。
	UpdateEpisodeMediaStatus(context.Context, string, string, string) error
}

// DramaListItem 是运营列表摘要，不携带分集、源地址或上传凭据。
type DramaListItem struct {
	// DirectPublication 展示直连当前版本和修订，与官方审核及线上版本独立。
	DirectPublication *directplay.Summary `json:"directPublication,omitempty"`
	// OfficialRelease 独立显示已选版本及上下架进度，不由最新审核任务推测。
	OfficialRelease *TikTokReleaseState `json:"officialRelease,omitempty"`
	// DeleteBlocked 表示渠道上线引用或活动任务阻止删除，仅提供编辑区删除门禁，不返回发布版本。
	DeleteBlocked bool `json:"deleteBlocked"`
	// CreatedByID 是创建管理员 ID；历史无法追溯时为空。
	CreatedByID string `json:"createdById,omitempty"`
	// CreatedByName 优先展示管理员昵称，其次用户名；管理员已删除时为空。
	CreatedByName string `json:"createdByName,omitempty"`
	// UpdatedByID 是最近成功人工操作的管理员 ID，自动任务不覆盖。
	UpdatedByID string `json:"updatedById,omitempty"`
	// UpdatedByName 是最近操作人的当前昵称或用户名；管理员已删除时为空。
	UpdatedByName string `json:"updatedByName,omitempty"`
	// Assignee 是发布链路负责人，不等同于创建人或最新操作人。
	Assignee string `json:"assignee,omitempty"`
	// OperationDueAt 是运营期望完成时间，未设置时为空。
	OperationDueAt *time.Time `json:"operationDueAt,omitempty"`
	// ID 是短剧内部标识。
	ID string `json:"id"`
	// Title 是短剧标题。
	Title string `json:"title"`
	// CoverURL 用于列表封面。
	CoverURL string `json:"coverUrl"`
	// Status 表示连载进度。
	Status string `json:"status"`
	// Year 是内容年份。
	Year int `json:"year"`
	// Tags 是官方标签名称。
	Tags []string `json:"tags"`
	// PublishStatus 是草稿的发布状态。
	PublishStatus string `json:"publishStatus"`
	// OnlineVersion 是对观众生效的版本。
	OnlineVersion int `json:"onlineVersion,omitempty"`
	// AuthorizedApplicationCount 是当前线上快照确认成功的其他小程序数，去重且不含拥有方。
	AuthorizedApplicationCount int `json:"authorizedApplicationCount"`
	// OwnerApplicationID 是当前线上快照的发布方，用于授权明细展示，不计入授权数。
	OwnerApplicationID string `json:"ownerApplicationId,omitempty"`
	// EpisodeCount 是草稿分集总数。
	EpisodeCount int `json:"episodeCount"`
	// ReadyEpisodeCount 是已就绪分集数。
	ReadyEpisodeCount int `json:"readyEpisodeCount"`
	// Publication 是最新一次官方发布任务的列表摘要；从未送审时为空。
	Publication *DramaListPublication `json:"publication,omitempty"`
}

// DramaListPublication 是列表判断当前官方流程所需的最小任务事实。
type DramaListPublication struct {
	// PlatformVersion 是最新同步/审核版本，与已选线上版本分开展示。
	PlatformVersion int `json:"platformVersion,omitempty"`
	// CurrentContentChanged 表示当前已保存内容与任务冻结内容不同。
	CurrentContentChanged bool `json:"currentContentChanged"`
	// ID 将列表上线确认绑定到所见的审核版本。
	ID string `json:"id"`
	// Priority 是运营提交的普通或加急审核优先级。
	Priority string `json:"priority"`
	// Status 是发布任务当前稳定业务状态。
	Status string `json:"status"`
	// Stage 是 Worker 当前处理或等待的官方阶段。
	Stage string `json:"stage"`
	// ReviewStatus 是 TikTok 最近返回的审核状态整数，零表示尚未取得审核事实。
	ReviewStatus int `json:"reviewStatus"`
	// ErrorCode 是任务暂停或失败时的稳定错误分类。
	ErrorCode string `json:"errorCode,omitempty"`
}

// DramaListQuery 是后台已校验的页码、页大小和筛选条件。
type DramaListQuery struct {
	// DirectStatus 按独立直连上下架状态筛选，不改变官方发布或编辑状态。
	DirectStatus string
	// Page 从 1 开始。
	Page int
	// PageSize 限制单页摘要数量。
	PageSize int
	// Query 匹配标题或标签。
	Query string
	// ContentStatus 仅筛选编辑内容的 draft/ready 状态，不表示渠道上线状态。
	ContentStatus string
	// Publication 筛选官方发布流程，供官方发布菜单使用。
	Publication string
}

// DramaListPage 返回当前页摘要和匹配总数。
type DramaListPage struct {
	// Items 不包含详情。
	Items []DramaListItem `json:"items"`
	// Total 是筛选后的记录数。
	Total int64 `json:"total"`
}
