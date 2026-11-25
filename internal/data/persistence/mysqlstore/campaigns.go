package mysqlstore

import (
	"context"
	"errors"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/campaign"
	content "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

type campaignModel struct {
	// LandingURL 保存 TikTok 官方生成结果，不在读取时拼装。
	LandingURL string `gorm:"column:landing_url"`
	// MinisPath 保存生成该链接使用的内部路径。
	MinisPath string `gorm:"column:minis_path"`
	// ID 投放配置内部标识。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// ApplicationID 所属应用标识，禁止通过更新迁移。
	ApplicationID string `gorm:"column:application_id;type:bigint unsigned"`
	// Code 应用内唯一的投放入口编码。
	Code string `gorm:"column:code"`
	// Name 运营展示的投放配置名称。
	Name string `gorm:"column:name"`
	// ExternalCampaignID 外部广告系列标识，按字符串保存。
	ExternalCampaignID string `gorm:"column:external_campaign_id"`
	// ExternalAdGroupID 外部广告组标识。
	ExternalAdGroupID string `gorm:"column:external_ad_group_id"`
	// ExternalAdID 外部广告标识。
	ExternalAdID string `gorm:"column:external_ad_id"`
	// ExternalCreativeID 外部素材标识。
	ExternalCreativeID string `gorm:"column:external_creative_id"`
	// DramaID 入口指向的业务短剧标识。
	DramaID string `gorm:"column:drama_id;type:bigint unsigned"`
	// EpisodeID 入口指向的业务分集标识。
	EpisodeID string `gorm:"column:episode_id;type:bigint unsigned"`
	// ExperimentKey 可选的归因实验键。
	ExperimentKey string `gorm:"column:experiment_key"`
	// Status 启用或停用状态。
	Status string `gorm:"column:status"`
	// CreatedAt 首次创建的 UTC 时间。
	CreatedAt time.Time `gorm:"column:created_at"`
	// UpdatedAt 最近更新的 UTC 时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 将投放配置映射到应用隔离的数据表。
func (campaignModel) TableName() string { return "application_campaigns" }

func campaignModelFromDomain(campaign domain.Campaign) campaignModel {
	return campaignModel{
		LandingURL: campaign.LandingURL, MinisPath: campaign.MinisPath,
		ID: campaign.ID, ApplicationID: campaign.ApplicationID, Code: campaign.Code, Name: campaign.Name,
		ExternalCampaignID: campaign.ExternalCampaignID, ExternalAdGroupID: campaign.ExternalAdGroupID,
		ExternalAdID: campaign.ExternalAdID, ExternalCreativeID: campaign.ExternalCreativeID,
		DramaID: campaign.DramaID, EpisodeID: campaign.EpisodeID, ExperimentKey: campaign.ExperimentKey,
		Status: campaign.Status, CreatedAt: campaign.CreatedAt.UTC(), UpdatedAt: campaign.UpdatedAt.UTC(),
	}
}

func (model campaignModel) domain() domain.Campaign {
	return domain.Campaign{
		LandingURL: model.LandingURL, MinisPath: model.MinisPath,
		ID: model.ID, ApplicationID: model.ApplicationID, Code: model.Code, Name: model.Name,
		ExternalCampaignID: model.ExternalCampaignID, ExternalAdGroupID: model.ExternalAdGroupID,
		ExternalAdID: model.ExternalAdID, ExternalCreativeID: model.ExternalCreativeID,
		DramaID: model.DramaID, EpisodeID: model.EpisodeID, ExperimentKey: model.ExperimentKey,
		Status: model.Status, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}
}

// ResolveCampaign 结合应用状态和已发布内容解析投放归因。
func (s *Store) ResolveCampaign(ctx context.Context, applicationID, code string) (domain.Attribution, bool, error) {
	var model campaignModel
	err := s.data.DB().WithContext(ctx).
		Where("application_id = ? AND code = ? AND status = ?", applicationID, code, domain.StatusActive).
		First(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Attribution{}, false, nil
	}
	if err != nil {
		return domain.Attribution{}, false, err
	}
	campaign := domain.Attribution{
		ID: model.ID, Code: model.Code, DramaID: model.DramaID, EpisodeID: model.EpisodeID, ExperimentKey: model.ExperimentKey,
	}
	app, err := s.FindApplication(ctx, applicationID)
	if err != nil {
		return domain.Attribution{}, false, err
	}
	if app.Status != "active" {
		return domain.Attribution{}, false, nil
	}
	if campaign.DramaID != "" {
		drama, findErr := s.FindDrama(application.ContextForApplication(ctx, app), campaign.DramaID)
		// 目标失效按自然流量处理；数据库故障必须向上传播，不能伪装为有效归因。
		if errors.Is(findErr, content.ErrNotFound) {
			return domain.Attribution{}, false, nil
		}
		if findErr != nil {
			return domain.Attribution{}, false, findErr
		}
		validEpisode := campaign.EpisodeID == ""
		published, visible := drama.PublishedForApplication(applicationID)
		if visible {
			for _, episode := range published.Episodes {
				if episode.ID == campaign.EpisodeID {
					validEpisode = true
					break
				}
			}
		}
		if !visible || !validEpisode {
			return domain.Attribution{}, false, nil
		}
	}
	return campaign, true, nil
}

// ListCampaigns 返回当前应用的投放配置，按入口编码排序。
func (s *Store) ListCampaigns(ctx context.Context) ([]domain.Campaign, error) {
	var models []campaignModel
	if err := s.data.DB().WithContext(ctx).Where("application_id = ?", application.IDFromContext(ctx)).Order("code").Find(&models).Error; err != nil {
		return nil, err
	}
	items := make([]domain.Campaign, 0, len(models))
	for _, model := range models {
		items = append(items, model.domain())
	}
	return items, nil
}

// FindCampaign 按 ID 查询当前应用的投放配置；不存在或属于其他应用时返回 ErrNotFound。
func (s *Store) FindCampaign(ctx context.Context, id string) (domain.Campaign, error) {
	var model campaignModel
	err := s.data.DB().WithContext(ctx).Where("application_id = ? AND id = ?", application.IDFromContext(ctx), id).First(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Campaign{}, content.ErrNotFound
	}
	return model.domain(), err
}

// SaveCampaign 按 ID 新增或更新当前应用的投放配置；编码唯一键冲突返回 ErrConflict，不覆盖其他配置。
func (s *Store) SaveCampaign(ctx context.Context, campaign *domain.Campaign) error {
	if campaign.ApplicationID != application.IDFromContext(ctx) {
		return content.ErrNotFound
	}
	var existing campaignModel
	err := s.data.DB().WithContext(ctx).Select("application_id").Where("id = ?", campaign.ID).First(&existing).Error
	if err == nil && existing.ApplicationID != campaign.ApplicationID {
		return content.ErrNotFound
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	model := campaignModelFromDomain(*campaign)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 使用普通 INSERT，让数据库唯一索引同时约束并发创建；禁止 upsert 命中编码后覆盖旧行。
		err = s.data.DB().WithContext(ctx).Create(&model).Error
	} else {
		// 显式选择字段以保存空值；更新仅作用于当前应用下已存在的 ID。
		err = s.data.DB().WithContext(ctx).Model(&campaignModel{}).
			Where("application_id = ? AND id = ?", campaign.ApplicationID, campaign.ID).
			Select("code", "name", "external_campaign_id", "external_ad_group_id", "external_ad_id", "external_creative_id",
				"drama_id", "episode_id", "experiment_key", "status", "updated_at", "landing_url", "minis_path").
			Updates(&model).Error
	}
	var mysqlErr *mysqldriver.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return operations.ErrConflict
	}
	if err == nil {
		campaign.ID = model.ID
	}
	return err
}
