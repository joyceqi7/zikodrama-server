package mysqlstore

import "time"

// dramaModel 映射主体独立草稿；官方字段仅在读取时合并所属主体的投影。
type dramaModel struct {
	// SubjectID 固定短剧、分集与发布记录的所属主体。
	SubjectID string `gorm:"column:subject_id;type:bigint unsigned"`
	// CreatedBy 是首次创建该短剧的可信管理员 ID，历史未知时为 NULL，后续修改不覆盖。
	CreatedBy *string `gorm:"column:created_by;type:bigint unsigned"`
	// UpdatedBy 是最近成功修改短剧的可信管理员 ID，后台任务不覆盖。
	UpdatedBy *string `gorm:"column:updated_by;type:bigint unsigned"`
	// Assignee 是当前发布协作负责人，与创建人和操作人分开维护。
	Assignee string `gorm:"column:assignee"`
	// OperationNote 是发布协作交接备注。
	OperationNote string `gorm:"column:operation_note"`
	// OperationDueAt 是期望完成时间，未设置时为空。
	OperationDueAt *time.Time `gorm:"column:operation_due_at"`
	// CoverMID 保存剧封面的远端标识。
	CoverMID string `gorm:"column:cover_mid"`
	// CoverSpaceName 固定封面上传空间。
	CoverSpaceName string `gorm:"column:cover_space_name"`
	// CoverFileName 保存删除素材所需的 StoreUri。
	CoverFileName string `gorm:"column:cover_file_name"`

	// PublishedSnapshot 只承接当前主体的读取投影，不属于共享草稿数据库列。
	PublishedSnapshot []byte `gorm:"-"`
	// ID 短剧聚合业务标识。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// Title 短剧主标题。
	Title string `gorm:"column:title"`
	// Description 短剧主简介。
	Description string `gorm:"column:description"`
	// CoverURL 短剧公开封面地址。
	CoverURL string `gorm:"column:cover_url"`
	// Status 短剧站内内容状态。
	Status string `gorm:"column:status"`
	// Year 短剧出品年份，0 表示未填写。
	Year int `gorm:"column:year"`
	// Language 原始音频语言代码，不表示界面或字幕语言。
	Language string `gorm:"column:language"`
	// DramaType TikTok 官方短剧类型枚举值。
	DramaType int `gorm:"column:drama_type"`
	// TagIDsJSON TikTok 官方标签标识列表 JSON。
	TagIDsJSON []byte `gorm:"column:tag_ids_json"`
	// PublishStatus 当前草稿的发布流程状态。
	PublishStatus string `gorm:"column:publish_status"`
	// PlatformAlbumID 只承接当前主体的 Album 读取投影，不写入共享草稿。
	PlatformAlbumID string `gorm:"-"`
	// CreatedAt 记录创建时间，使用 UTC。
	CreatedAt time.Time `gorm:"column:created_at"`
	// UpdatedAt 记录最近更新时间，使用 UTC。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 将短剧聚合映射到 dramas 表。
func (dramaModel) TableName() string { return "dramas" }

// episodeModel 映射短剧分集及其权威 VOD 媒资状态。
type episodeModel struct {
	// MediaSpaceName 固定分集视频所属空间。
	MediaSpaceName string `gorm:"column:media_space_name"`

	// IsDraft 表示分集仍属于编辑区；线上引用的旧分集保留主键，维持已授予权益的内容引用。
	IsDraft bool `gorm:"column:is_draft"`
	// ID 分集业务标识，历史上线分集保留该主键。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// DramaID 所属短剧标识，关联 dramas.id。
	DramaID string `gorm:"column:drama_id;type:bigint unsigned"`
	// Number 剧内集序号，编辑替换时历史保留行可使用负数避让。
	Number int `gorm:"column:episode_number"`
	// Title 分集主标题。
	Title string `gorm:"column:title"`
	// Description 分集主简介，NULL 表示未填写。
	Description *string `gorm:"column:description"`
	// VID 当前分集的 BytePlus 视频 VID，按字符串保存。
	VID string `gorm:"column:vid"`
	// PlatformEpisodeID 仅供领域映射使用，官方 ID 固定在主体版本中，不写入共享分集表。
	PlatformEpisodeID string `gorm:"-"`
	// PosterURL 分集公开海报地址，供列表与播放器展示。
	PosterURL string `gorm:"column:poster_url"`
	// DurationSeconds 视频时长，单位秒，0 表示尚未获取。
	DurationSeconds int `gorm:"column:duration_seconds"`
	// MediaStatus 当前分集媒资处理状态。
	MediaStatus string `gorm:"column:media_status"`
	// CreatedAt 记录创建时间，使用 UTC。
	CreatedAt time.Time `gorm:"column:created_at"`
	// UpdatedAt 记录最近更新时间，使用 UTC。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 将当前及历史分集映射到 episodes 表。
func (episodeModel) TableName() string { return "episodes" }
