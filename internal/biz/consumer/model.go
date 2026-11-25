package consumer

import (
	"time"

	"drama-server/internal/biz/monetization"
)

// Drama 同时承载运营内容与线上快照；消费者必须先经 PublishedForApplication 选择可见版本。
type Drama struct {
	// SubjectID 是权威内容归属，仅服务端使用，旧 Mini 无需提交或接收此字段。
	SubjectID string `json:"-"`
	// PlaybackProvider 仅独立发布快照填写，官方响应缺省保持不变。
	PlaybackProvider string `json:"playbackProvider,omitempty"`
	// Assignee 是当前发布链路负责人，供运营工作台协作。
	Assignee string `json:"assignee,omitempty"`
	// OperationNote 保存发布链路交接备注。
	OperationNote string `json:"operationNote,omitempty"`
	// OperationDueAt 是运营期望完成时间；为空表示未设置 SLA。
	OperationDueAt *time.Time `json:"operationDueAt,omitempty"`
	// CoverMID 是剧封面的 VOD 素材标识，随发布快照保存。
	CoverMID string `json:"coverMid,omitempty"`
	// CoverSpaceName 固定封面上传空间，供远端清理。
	CoverSpaceName string `json:"coverSpaceName,omitempty"`
	// CoverFileName 是封面的 StoreUri，供远端删除确认。
	CoverFileName string `json:"coverFileName,omitempty"`

	// Published 是当前已经上架的内容快照，与运营编辑中的字段完全分离。
	Published *Drama `json:"-"`
	// OnlineVersion 仅在运营投影中填写，草稿状态不代表线上版本已下架。
	OnlineVersion int `json:"onlineVersion,omitempty"`
	// OnlineEpisodeNumbers 来自已上线快照，不以编辑区集数推测线上内容；仅运营投影填写。
	OnlineEpisodeNumbers []int `json:"onlineEpisodeNumbers,omitempty"`
	// OwnerApplicationID 是官方 Album 拥有方，自有播放不需要额外授权。
	OwnerApplicationID string `json:"ownerApplicationId,omitempty"`
	// AuthorizedApplicationIDs 是官方确认授权成功的其他小程序。
	AuthorizedApplicationIDs []string `json:"authorizedApplicationIds,omitempty"`
	// PlatformVersion 是该内容对应的官方版本号。
	PlatformVersion int `json:"platformVersion,omitempty"`
	// ID 是当前模型的内部业务标识，始终按字符串传递。
	ID string `json:"id"`
	// Title 是唯一展示标题。
	Title string `json:"title"`
	// Description 是唯一展示简介。
	Description string `json:"description"`
	// CoverURL 是短剧封面展示地址，不作为播放授权依据。
	CoverURL string `json:"coverUrl"`
	// Tags 是供消费者展示和筛选的官方标签名称。
	Tags []string `json:"tags"`
	// TikTokTags 保存按 TagIDs 关联的官方标签，仅供服务端展示投影使用。
	TikTokTags []TikTokTag `json:"-"`
	// Status 表示连载进度，与上架状态 PublishStatus 分开维护。
	Status string `json:"status"`
	// Year 是运营维护的内容年份。
	Year int `json:"year"`
	// Language 是内容原始语言代码，用于官方发布资料。
	Language string `json:"language"`
	// DramaType 是官方发布所需的内容类型编码，对应运营元数据目录。
	DramaType int `json:"dramaType"`
	// TagIDs 保存官方 TikTok 标签标识，顺序用于展示，值按字符串处理。
	TagIDs []string `json:"tagIds"`
	// PublishStatus 在编辑区仅为 draft 或 ready；渠道线上快照使用 published，与编辑状态独立。
	PublishStatus string `json:"publishStatus"`
	// PlatformAlbumID 是 TikTok 审核通过后分配的 Album 标识；始终按字符串保存，
	// 只由官方发布流程写入，普通内容编辑不能覆盖。
	PlatformAlbumID string `json:"platformAlbumId,omitempty"`
	// Episodes 保存该版本剧集列表，摘要取列表首集作为默认入口。
	Episodes []Episode `json:"episodes"`
}

// TikTokTag 保存官方标签身份与展示名称。
type TikTokTag struct {
	// EnglishName 是运营维护的英文展示名；空值回退官方名称。
	EnglishName string `json:"englishName"`
	// ID 是官方 TikTok 标签标识，即使为纯数字也按字符串处理。
	ID string `json:"id"`
	// OfficialName 是官方标签目录中的原始名称。
	OfficialName string `json:"officialName"`
}

const (
	// DramaStatusUnpublished 表示尚未开始连载的内容进度，不等同于发布状态。
	DramaStatusUnpublished = "unpublished"
	// DramaStatusUpdating 表示内容仍在更新，消费者展示为 ongoing。
	DramaStatusUpdating = "updating"
	// DramaStatusCompleted 表示内容已完结。
	DramaStatusCompleted = "completed"
)

// DramaSummary 是当前渠道上线内容的展示摘要，不携带发布版本、媒体凭据和编辑数据。
type DramaSummary struct {
	// TagIDs 是线上快照的官方标签标识，用于按身份而非译名关联分类榜。
	TagIDs []string `json:"tagIds,omitempty"`
	// ID 是当前模型的内部业务标识，始终按字符串传递。
	ID string `json:"id"`
	// FirstEpisodeID 取当前剧集列表首项的内部标识；无剧集时为空。
	FirstEpisodeID string `json:"firstEpisodeId"`
	// FirstEpisodePosterURL 是第一集首帧图；尚未生成或无剧集时为空。
	FirstEpisodePosterURL string `json:"firstEpisodePosterUrl"`
	// Title 是唯一展示标题。
	Title string `json:"title"`
	// Description 是唯一展示简介。
	Description string `json:"description"`
	// CoverURL 是短剧封面展示地址，不作为播放授权依据。
	CoverURL string `json:"coverUrl"`
	// EpisodeCount 是该线上版本包含的剧集数量。
	EpisodeCount int `json:"episodeCount"`
	// Tags 是供消费者展示和筛选的官方标签名称。
	Tags []string `json:"tags"`
	// Status 表示连载进度，与上架状态 PublishStatus 分开维护。
	Status string `json:"status"`
}

// DiscoveryQuery 是入口已规范化并校验的筛选条件；分页位置基于本次过滤后的可见目录。
type DiscoveryQuery struct {
	// Home 仅由首页入口显式设置，不限制搜索或分享直达。
	Home bool
	// ExpectedReviewRevision 来自首页请求与游标，配置变化时拒绝继续旧分页。
	ExpectedReviewRevision *int64
	// DramaIDs 非 nil 时在分页和标签汇总前限制审核内容，空切片表示空首页。
	DramaIDs []string
	// ReviewRevision 隔离审核配置变化前后的目录缓存。
	ReviewRevision int64
	// TagID 仅供服务端分类榜候选查询，按官方字符串标识精确过滤。
	TagID string `json:"-"`
	// Query 是入口去除首尾空白后的搜索词，多词按全部命中匹配。
	Query string
	// Tag 是按展示名称匹配的标签，匹配时忽略大小写。
	Tag string
	// Status 是可选的消费者连载状态筛选值。
	Status string
	// Offset 是入口解析不透明游标后得到的非负分页位置。
	Offset int
	// Limit 是入口已校验的每页最大条数。
	Limit int
}

// DiscoveryPage 返回可见目录的一页摘要、下一页游标和当前目录可用标签。
type DiscoveryPage struct {
	// Items 保存本页短剧摘要，已完成应用可见性与语言投影。
	Items []DramaSummary `json:"items"`
	// NextCursor 是下一页起点的版本化编码；为空表示没有后续结果。
	NextCursor string `json:"nextCursor,omitempty"`
	// Tags 是供消费者展示和筛选的官方标签名称。
	Tags []string `json:"tags"`
	// Metadata 仅由可信管理查询补充；消费者响应保持省略。
	Metadata map[string]PublicationMetadata `json:"metadata,omitempty"`
}

// PublicationMetadata 为直连运营候选提供当前版本和有效应用数，不包含分集或凭据。
type PublicationMetadata struct {
	// Version 是本渠道当前线上版本号。
	Version int64 `json:"version"`
	// Applications 是启用且有效的本渠道应用数。
	Applications int `json:"applications"`
}

// Summary 从当前值生成列表摘要，不自行校验上架和应用授权；调用方须先选择可见快照。
func (d Drama) Summary() DramaSummary {
	firstEpisodeID := ""
	firstEpisodePosterURL := ""
	if len(d.Episodes) > 0 {
		firstEpisodeID = d.Episodes[0].ID
		firstEpisodePosterURL = d.Episodes[0].PosterURL
	}

	return DramaSummary{
		ID: d.ID, FirstEpisodeID: firstEpisodeID, FirstEpisodePosterURL: firstEpisodePosterURL, Title: d.Title,
		Description: d.Description, CoverURL: d.CoverURL, EpisodeCount: len(d.Episodes),
		// Tag 是按展示名称匹配的标签，匹配时忽略大小写。
		Tags: d.Tags, TagIDs: append([]string(nil), d.TagIDs...), Status: d.Status,
	}
}

// Episode 保存内部剧集、VOD 媒资与 TikTok 官方剧集的映射，三者标识不可混用。
type Episode struct {
	// MediaSpaceName 是当前视频上传时的 VOD 空间。
	MediaSpaceName string `json:"mediaSpaceName,omitempty"`

	// ID 是当前模型的内部业务标识，始终按字符串传递。
	ID string `json:"id"`
	// DramaID 是所属短剧的内部业务标识，不是 TikTok AlbumID。
	DramaID string `json:"dramaId"`
	// Number 是短剧内从 1 开始的集号，用于排序及免费集判定。
	Number int `json:"number"`
	// Title 是唯一展示标题。
	Title string `json:"title"`
	// Description 是唯一展示简介。
	Description string `json:"description"`
	// VID 是 BytePlus VOD 视频标识，必须与官方剧集映射匹配。
	VID string `json:"vid"`
	// PlatformEpisodeID 是 TikTok 媒资库中的剧集标识；它与内部 ID 分离，
	// 只有该字段、Album 标识和 VID 同时存在时才允许启动官方 Player。
	PlatformEpisodeID string `json:"platformEpisodeId,omitempty"`
	// PosterURL 是剧集海报地址，供列表及播放器首帧前展示。
	PosterURL string `json:"posterUrl"`
	// DurationSeconds 是运营媒资记录中的预计时长，单位为秒。
	DurationSeconds int `json:"durationSeconds"`
	// MediaStatus 表示媒资处理状态；只有 ready 才能启动官方播放。
	MediaStatus string `json:"mediaStatus"`
}

// EpisodeSummary 是消费者剧集展示摘要，不包含源媒体地址及上传任务。
type EpisodeSummary struct {
	// ID 是当前模型的内部业务标识，始终按字符串传递。
	ID string `json:"id"`
	// DramaID 是所属短剧的内部业务标识，不是 TikTok AlbumID。
	DramaID string `json:"dramaId"`
	// Number 是短剧内从 1 开始的集号，用于排序及免费集判定。
	Number int `json:"number"`
	// Title 是唯一展示标题。
	Title string `json:"title"`
	// Description 是唯一展示简介。
	Description string `json:"description"`
	// PosterURL 是剧集海报地址，供列表及播放器首帧前展示。
	PosterURL string `json:"posterUrl"`
	// DurationSeconds 是运营媒资记录中的预计时长，单位为秒。
	DurationSeconds int `json:"durationSeconds"`
	// AccessMode 描述免费、需激励广告或已解锁的访问方式，空值表示尚未填充。
	AccessMode string `json:"accessMode,omitempty"`
}

// Summary 仅提取剧集展示字段；播放身份和访问方式由相应用例另行计算。
func (e Episode) Summary() EpisodeSummary {
	return EpisodeSummary{
		ID: e.ID, DramaID: e.DramaID, Number: e.Number, Title: e.Title, Description: e.Description,
		PosterURL: e.PosterURL, DurationSeconds: e.DurationSeconds,
	}
}

// PlaybackIdentity 是服务端确认官方媒资就绪后返回的 Player 描述符。
// 用于 Preload 时只允许准备媒体；起播范围由 Playback 或 EntryPreview 决定。
// 该结构不包含直链、DRM KEK 或长期平台凭据。
type PlaybackIdentity struct {
	// Channel 固定为 byteplus，由应用 provider 决定宿主或独立播放器。
	Channel string `json:"channel"`
	// AlbumID 是 TikTok 已审核短剧的官方 Album 标识。
	AlbumID string `json:"albumId,omitempty"`
	// EpisodeID 是官方平台剧集标识；直连省略，内部标识仍在顶层 episode.id。
	EpisodeID string `json:"episodeId,omitempty"`
	// PlayAuthToken 是直连临时凭据，严禁持久化或记录日志。
	PlayAuthToken string `json:"playAuthToken,omitempty"`
	// ExpiresAt 是临时凭据到期时间。
	ExpiresAt string `json:"expiresAt,omitempty"`
	// VID 是已验权线上分集的 BytePlus VOD 标识，官方分支还须匹配平台映射。
	VID string `json:"vid"`
	// PosterURL 是剧集海报地址，供列表及播放器首帧前展示。
	PosterURL string `json:"posterUrl"`
	// Title 是唯一展示标题。
	Title string `json:"title"`
	// EpisodeNumber 是当前剧集在短剧中的顺序编号。
	EpisodeNumber int `json:"episodeNumber"`
	// DurationSecond 是运营媒资记录的预计时长，单位为秒。
	DurationSecond int `json:"durationSeconds"`
}

// EntryPlaybackPreview 是锁定首集的开头播放许可，权益状态仍由广告结算决定。
type EntryPlaybackPreview struct {
	// Seconds 是广告流程开始前最多允许播放的秒数。
	Seconds int `json:"seconds"`
	// Playback 是官方首集标识或独立直连片段的短时许可；为空表示片段准备中。
	Playback *PlaybackIdentity `json:"playback,omitempty"`
}

const (
	// PlayStatusPlayable 表示权益与官方媒资均满足，可启动官方 Player。
	PlayStatusPlayable = "playable"
	// PlayStatusAccessRequired 表示媒资可用但当前权益不足，需展示奖励入口。
	PlayStatusAccessRequired = "access_required"
	// PlayStatusUnavailable 表示当前无法提供可执行的播放或解锁结果。
	PlayStatusUnavailable = "unavailable"
)

// PlayResult 是播放解析的服务端权威结果。
// Status=playable 时才允许返回正片 Playback；Status=access_required 时可返回
// 解锁 Offer、仅供媒体预加载的 Preload 和首集的 EntryPreview。
// VIP 进度沿用消费者合同中的 vipState 字段名。
type PlayResult struct {
	// Offers 按策略顺序返回已接通的获取方式；客户端通过 method 分发处理。
	Offers []monetization.AccessOffer `json:"offers,omitempty"`
	// Status 决定客户端展示播放器、解锁入口或暂不可用提示。
	Status string `json:"status"`
	// Drama 是已通过应用可见性校验的短剧摘要。
	Drama *DramaSummary `json:"drama,omitempty"`
	// Episode 是本次请求目标剧集的展示摘要。
	Episode *EpisodeSummary `json:"episode,omitempty"`
	// Playback 仅在 playable 时提供官方 Player 所需标识，不含直链或播放凭据。
	Playback *PlaybackIdentity `json:"playback,omitempty"`
	// Preload 只在媒资就绪但权益不足且存在解锁入口时提供；客户端不得据此起播。
	Preload *PlaybackIdentity `json:"preload,omitempty"`
	// EntryPreview 仅授权播放锁定首集的开头，不表示用户已有完整剧集权益。
	EntryPreview *EntryPlaybackPreview `json:"entryPreview,omitempty"`
	// Offer 是逐集奖励入口，领取时仍须校验当前配置与广告位。
	Offer *monetization.AccessOffer `json:"offer,omitempty"`
	// VIPOffer 是当前可累计 VIP 进度的奖励入口，已激活时不提供。
	VIPOffer *monetization.AccessOffer `json:"vipOffer,omitempty"`
	// VIPProgress 保存服务器确认的应用自然日 VIP 累计与生效状态。
	VIPProgress *monetization.VIPProgress `json:"vipState,omitempty"`
}

// RewardClaimResult 在幂等奖励事实之外携带本次已确认可播放的当前集。
// Playback 只在逐集奖励成功且官方媒资仍可用时返回；客户端可直接恢复播放器，
// 不必再次调用播放解析接口。连续广告是否继续仍由内嵌 ClaimResult.NextAd 决定。
type RewardClaimResult struct {
	monetization.ClaimResult
	// EntryAdStatus 返回本次入剧结算后的服务端进度；旧客户端可忽略，缺省不代表零次数。
	EntryAdStatus *EntryAdStatus `json:"entryAdStatus,omitempty"`
	// Drama 返回本次逐集结算后的完整展示目录和访问状态，不替代 Playback 的起播授权。
	Drama *RewardDramaState `json:"drama,omitempty"`
	// Playback 是奖励事务确认后生成的当前集权威播放结果，不包含媒体直链或长期凭据。
	Playback *PlayResult `json:"playback,omitempty"`
}

// RewardDramaState 复用短剧详情的响应结构，避免结算后客户端再次查询选集状态。
type RewardDramaState struct {
	DramaSummary
	// Episodes 只包含服务端确认的展示与访问状态，不包含媒体凭据。
	Episodes []EpisodeSummary `json:"episodes"`
}

// LaunchTarget 是已确认属于当前应用线上目录的内部短剧与剧集定位。
type LaunchTarget struct {
	// DramaID 是所属短剧的内部业务标识，不是 TikTok AlbumID。
	DramaID string `json:"dramaId"`
	// EpisodeID 是目标剧集的内部业务标识。
	EpisodeID string `json:"episodeId"`
}

// UnlockRecord 将当前仍有效的分集权益与可见内容合并，供用户查看已解锁记录。
type UnlockRecord struct {
	// Drama 是权益所属短剧的当前可见展示摘要。
	Drama DramaSummary `json:"drama"`
	// Episode 是本条分集权益对应的当前剧集摘要。
	Episode EpisodeSummary `json:"episode"`
	// GrantedAt 记录权益成功授予的时间。
	GrantedAt time.Time `json:"grantedAt"`
	// ExpiresAt 是权益截止时间；零值表示持久层未设置过期时间。
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}
