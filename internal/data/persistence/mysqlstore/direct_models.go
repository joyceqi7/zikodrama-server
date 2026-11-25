package mysqlstore

import "time"

// applicationPlaybackProfileModel 保存application_playback_profiles的权威状态。
type applicationPlaybackProfileModel struct {
	// ID 保存主键。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// ApplicationID 保存应用主键。
	ApplicationID string `gorm:"column:application_id"`
	// Provider 保存播放提供方。
	Provider string `gorm:"column:provider"`
	// Revision 保存配置修订。
	Revision int64 `gorm:"column:revision"`
	// CreatedAt 保存创建时间。
	CreatedAt time.Time `gorm:"column:created_at"`
	// UpdatedAt 保存更新时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 指定独立存储表。
func (applicationPlaybackProfileModel) TableName() string { return "application_playback_profiles" }

// directPublicationModel 保存direct_publications的权威状态。
type directPublicationModel struct {
	// SubjectID 固定此主体的直连上架状态。
	SubjectID string `gorm:"column:subject_id;type:bigint unsigned"`
	// ID 保存主键。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// DramaID 保存剧目主键。
	DramaID string `gorm:"column:drama_id"`
	// Status 保存独立发布状态。
	Status string `gorm:"column:status"`
	// CurrentVersionID 保存当前版本指针。
	CurrentVersionID *string `gorm:"column:current_version_id"`
	// Revision 保存发布修订栅栏。
	Revision int64 `gorm:"column:revision"`
	// UpdatedBy 保存操作人。
	UpdatedBy string `gorm:"column:updated_by"`
	// CreatedAt 保存创建时间。
	CreatedAt time.Time `gorm:"column:created_at"`
	// UpdatedAt 保存更新时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 指定独立存储表。
func (directPublicationModel) TableName() string { return "direct_publications" }

// directPublicationVersionModel 保存直连渠道的发布版本与快照。
type directPublicationVersionModel struct {
	// SubjectID 隔离冻结版本历史与剧内版本号。
	SubjectID string `gorm:"column:subject_id;type:bigint unsigned"`
	// ID 保存版本主键。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// DramaID 保存剧目主键。
	DramaID string `gorm:"column:drama_id"`
	// VersionNumber 保存剧内版本号。
	VersionNumber int64 `gorm:"column:version_number"`
	// SourceDigest 保存冻结内容摘要。
	SourceDigest string `gorm:"column:source_digest"`
	// SnapshotJSON 保存直连上架时冻结的内容。
	SnapshotJSON []byte `gorm:"column:snapshot_json"`
	// CreatedBy 保存发布人。
	CreatedBy string `gorm:"column:created_by"`
	// CreatedAt 保存发布时间。
	CreatedAt time.Time `gorm:"column:created_at"`
}

// TableName 指定独立存储表。
func (directPublicationVersionModel) TableName() string { return "direct_publication_versions" }

// directPublicationOperationModel 保存direct_publication_operations的权威状态。
type directPublicationOperationModel struct {
	// SubjectID 隔离幂等键与操作结果。
	SubjectID string `gorm:"column:subject_id;type:bigint unsigned"`
	// ID 保存操作主键。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// DramaID 保存剧目主键。
	DramaID string `gorm:"column:drama_id"`
	// RequestKey 保存幂等请求键。
	RequestKey string `gorm:"column:request_key"`
	// InputDigest 保存幂等输入摘要。
	InputDigest string `gorm:"column:input_digest"`
	// CommandJSON 保存原始操作命令。
	CommandJSON []byte `gorm:"column:command_json"`
	// Status 保存任务状态。
	Status string `gorm:"column:status"`
	// ResultVersionID 保存生效版本。
	ResultVersionID *string `gorm:"column:result_version_id"`
	// CreatedBy 保存操作人。
	CreatedBy string `gorm:"column:created_by"`
	// CreatedAt 保存提交时间。
	CreatedAt time.Time `gorm:"column:created_at"`
	// UpdatedAt 保存更新时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 指定独立存储表。
func (directPublicationOperationModel) TableName() string { return "direct_publication_operations" }

// directHomepageRecommendationModel 复用配置合同并隔离物理表。
type directHomepageRecommendationModel homepageRecommendationModel

// TableName 指定独立配置表。
func (directHomepageRecommendationModel) TableName() string { return "direct_homepage_recommendations" }

// directCategoryRankingModel 复用配置合同并隔离物理表。
type directCategoryRankingModel categoryRankingModel

// TableName 指定独立配置表。
func (directCategoryRankingModel) TableName() string { return "direct_category_rankings" }
