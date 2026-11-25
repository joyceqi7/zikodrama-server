package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
)

// applicationModel 保存数据库行，ID 用于内部关联，TikTokAppID 用于官方平台定位。
type applicationModel struct {
	// SubjectID 是应用所属发布主体，不影响原有直连配置。
	SubjectID string `gorm:"column:subject_id;type:bigint unsigned"`
	// ID 是数据库自增主键，对外以十进制字符串表示。
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	// 保存面向用户展示的名称。
	Name string `gorm:"column:name"`
	// 保存地址。
	LogoURL string `gorm:"column:logo_url"`
	// 保存应用标识。
	TikTokAppID string `gorm:"column:tiktok_app_id"`
	// IdentityHashNamespace 只用于迁移历史登录摘要，不作为业务标识。
	IdentityHashNamespace string `gorm:"column:identity_hash_namespace"`
	// 保存客户端键。
	ClientKey string `gorm:"column:client_key"`
	// ClientSecret 保存仅供服务端调用当前应用接口的密钥，不对外序列化。
	ClientSecret string `gorm:"column:client_secret"`
	// SecretRef 保存历史环境变量引用名。
	SecretRef string `gorm:"column:secret_ref"`
	// 保存地域编码。
	RegionCode string `gorm:"column:region_code"`
	// 保存时间。
	TimeZone string `gorm:"column:time_zone"`

	// 保存合规基础地址。
	ComplianceBaseURL string `gorm:"column:compliance_base_url"`
	// AdUnitsJSON 保存应用登记的广告位编码集合。
	AdUnitsJSON []byte `gorm:"column:ad_units_json"`
	// MonetizationMode 保存应用变现方式，应用登记入口要求明确选择。
	MonetizationMode application.MonetizationMode `gorm:"column:monetization_mode;default:IAA"`
	// 保存当前业务状态。
	Status string `gorm:"column:status"`
	// 保存记录创建时间。
	CreatedAt time.Time `gorm:"column:created_at"`
	// 保存记录最后更新时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// 返回当前数据模型对应的数据表名称。
func (applicationModel) TableName() string {
	return "applications"
}

// ListApplications 按官方 AppID 排序返回应用，同时返回内部主键，不暴露身份摘要兼容字段。
func (s *Store) ListApplications(ctx context.Context) ([]application.Application, error) {
	return s.readApplications(ctx, func(tx *gorm.DB) *gorm.DB { return tx.Order("tiktok_app_id ASC") })
}

// readApplications 在同一快照中读取应用、审核版本与共享清单，列表批量加载避免逐应用查询。
func (s *Store) readApplications(ctx context.Context, query func(*gorm.DB) *gorm.DB) ([]application.Application, error) {
	if s == nil || s.data.DB() == nil {
		return nil, errors.New("gorm mysql store is not configured")
	}
	items := []application.Application{}
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var records []applicationModel
		if err := query(tx).Find(&records).Error; err != nil {
			return err
		}
		for _, record := range records {
			app, err := record.application()
			if err != nil {
				return err
			}
			items = append(items, app)
		}
		if err := attachPlaybackProfiles(tx, items); err != nil {
			return err
		}
		reviews, err := readMiniReviews(tx, items, false)
		if err != nil {
			return err
		}
		for i := range items {
			items[i].MiniReview = reviews[items[i].ID]
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

// FindApplication 按数据库自增 ID 精确读取应用；不存在时返回 ErrNotFound。
func (s *Store) FindApplication(ctx context.Context, id string) (application.Application, error) {
	items, err := s.readApplications(ctx, func(tx *gorm.DB) *gorm.DB { return tx.Where("id=?", id).Limit(1) })
	if err != nil {
		return application.Application{}, err
	}
	if len(items) == 0 {
		return application.Application{}, domain.ErrNotFound
	}
	return items[0], nil
}

// FindApplicationByTikTokAppID 精确匹配官方 AppID；不存在返回 ErrUnavailable，重复配置返回错误，避免任意选择应用。
func (s *Store) FindApplicationByTikTokAppID(ctx context.Context, tiktokAppID string) (application.Application, error) {
	// 应用 ID 是不透明字符串；数据库默认排序规则不能折叠大小写或选择重复配置的第一条。
	items, err := s.readApplications(ctx, func(tx *gorm.DB) *gorm.DB { return tx.Where("tiktok_app_id=?", tiktokAppID).Limit(2) })
	if err != nil {
		return application.Application{}, err
	}
	if len(items) == 0 {
		return application.Application{}, application.ErrUnavailable
	}
	if len(items) != 1 {
		return application.Application{}, errors.New("multiple applications share tiktok app id")
	}
	return items[0], nil
}

// application 解析广告位 JSON 并返回应用资料，JSON 损坏时返回解码错误。
func (model applicationModel) application() (application.Application, error) {
	var adUnits application.AdUnits
	if err := json.Unmarshal(model.AdUnitsJSON, &adUnits); err != nil {
		return application.Application{}, err
	}
	return application.Application{
		ID: strconv.FormatUint(model.ID, 10), Name: model.Name, LogoURL: model.LogoURL,
		SubjectID: model.SubjectID, TikTokAppID: model.TikTokAppID, IdentityHashNamespace: model.IdentityHashNamespace, ClientKey: model.ClientKey, SecretRef: model.SecretRef, ClientSecret: model.ClientSecret, ClientSecretConfigured: model.ClientSecret != "",
		RegionCode: model.RegionCode, TimeZone: model.TimeZone,
		AdUnits: adUnits.Normalized(), ComplianceBaseURL: model.ComplianceBaseURL,
		MonetizationMode: model.MonetizationMode,
		Status:           model.Status, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}, nil
}

// CreateApplication 让数据库分配内部主键；官方 AppID 冲突时拒绝创建，不覆盖已有配置。
func (s *Store) CreateApplication(ctx context.Context, app *application.Application) error {
	return s.saveApplication(ctx, app, true)
}

// SaveApplication 原子保存应用与播放模式；过期播放修订或无效全局直连参数拒绝整笔更新，变现模式或广告位变化递增消费修订。
func (s *Store) SaveApplication(ctx context.Context, app application.Application) error {
	return s.saveApplication(ctx, &app, false)
}

// saveApplication 区分严格新增与已有配置写入，成功创建后回填数据库自增主键。
func (s *Store) saveApplication(ctx context.Context, app *application.Application, creating bool) error {
	if s == nil || s.data.DB() == nil {
		return errors.New("gorm mysql store is not configured")
	}
	adUnitsJSON, err := json.Marshal(app.AdUnits.Normalized())
	if err != nil {
		return err
	}
	record := applicationModel{
		SubjectID: app.SubjectID,
		Name:      app.Name, LogoURL: app.LogoURL, TikTokAppID: app.TikTokAppID, IdentityHashNamespace: app.IdentityHashNamespace,
		ClientKey: app.ClientKey, SecretRef: app.SecretRef, ClientSecret: app.ClientSecret,
		RegionCode: app.RegionCode, TimeZone: app.TimeZone,
		ComplianceBaseURL: app.ComplianceBaseURL, AdUnitsJSON: adUnitsJSON,
		MonetizationMode: app.MonetizationMode,
		Status:           string(app.Status), CreatedAt: app.CreatedAt.UTC(), UpdatedAt: app.UpdatedAt.UTC(),
	}
	if app.ID != "" {
		record.ID, err = strconv.ParseUint(app.ID, 10, 64)
		if err != nil {
			return operations.ErrInvalidInput
		}
	}
	// 写入语句包含密钥，禁用此事务的 SQL 参数日志；错误仍由业务层记录。
	query := s.data.DB().WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	if !creating {
		columns := []string{"name", "logo_url", "tiktok_app_id", "client_key", "secret_ref",
			"region_code", "time_zone", "compliance_base_url", "ad_units_json", "monetization_mode", "status", "updated_at"}
		// 不从旧快照回写密钥，避免无关配置更新覆盖并发轮换后的值。
		if app.ClientSecret != "" {
			columns = append(columns, "client_secret")
		}
		query = query.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tiktok_app_id"}},
			DoUpdates: clause.AssignmentColumns(columns),
		})
	}
	err = query.Transaction(func(tx *gorm.DB) error {
		var oldScope application.Application
		playbackChanged := false
		var target subjectModel
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Select("id").Where("id=?", app.SubjectID).Take(&target).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return operations.ErrInvalidInput
			}
			return err
		}
		if !creating {
			var stored applicationModel
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "subject_id").Where("id=?", app.ID).Take(&stored).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			// 仓储兼容历史内部 upsert；应用首次写入尚无实际渠道记录，不能当作渠道切换。
			oldScope = *app
			if stored.ID != 0 {
				oldScope, err = reviewScopeApplication(tx, app.ID)
				if err != nil {
					return err
				}
			}
			playbackChanged = oldScope.Normalized().PlaybackProvider != app.Normalized().PlaybackProvider
			if playbackChanged {
				if app.MiniReviewWrite {
					return operations.ErrConflict
				}
				if err := lockReviewScopes(tx, oldScope, *app); err != nil {
					return err
				}
			}
			reviews, err := readMiniReviews(tx, []application.Application{*app}, true)
			if err != nil {
				return err
			}
			previous := reviews[app.ID]
			if !app.MiniReviewWrite {
				app.MiniReview = previous
			}
			if app.MiniReviewWrite {
				if previous.Revision != app.MiniReview.Revision {
					return operations.ErrConflict
				}
				next := previous.WithSettings(app.MiniReview)
				next.Revision++
				app.MiniReview = next
			}
			if stored.ID != 0 && stored.SubjectID != app.SubjectID {
				return operations.ErrConflict
			}
			if playbackChanged && app.MiniReview.AnyEnabled() && len(app.MiniReview.DramaIDs) == 0 {
				return operations.ErrInvalidInput
			}
		}
		if !creating {
			var current strategyBindingModel
			readErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", app.ID).Error
			if readErr != nil && !errors.Is(readErr, gorm.ErrRecordNotFound) {
				return readErr
			}
			if readErr == nil {
				// 与策略启用、绑定操作串行化；允许更换编码，模式必须兼容完整规则且启用广告入口不能缺少资源。
				if current.StrategyID == nil && app.MiniReview.AnyEnabled() && app.MonetizationMode == application.MonetizationModeIAA {
					return operations.ErrInvalidInput
				}
				if current.StrategyID != nil {
					var strategy unlockStrategyModel
					if err := tx.First(&strategy, "id = ?", *current.StrategyID).Error; err != nil {
						return err
					}
					item, err := strategy.domain()
					if err != nil {
						return err
					}
					if err := validateMiniReviewStrategy(app.MiniReview, app.MonetizationMode, item.Document); err != nil {
						return err
					}
					if err := monetization.ValidateApplicationMode(app.MonetizationMode, item.Document); err != nil {
						return err
					}
					if monetization.RequiresAdUnits(item.Document) && !app.AdUnits.Complete() {
						return operations.ErrApplicationAdUnitsIncomplete
					}
				}
			}
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		// 新应用初始化共享清单；普通应用编辑不回写审核表，兼容旧入口时两者同事务提交。
		if app.MiniReviewWrite {
			if err := persistMiniReview(tx, strconv.FormatUint(record.ID, 10), app.MiniReview, true); err != nil {
				return err
			}
		}
		if !creating {
			if err := saveApplicationPlayback(tx, *app); err != nil {
				return err
			}
			if playbackChanged {
				for _, scope := range []application.Application{oldScope, *app} {
					if err := tx.Session(&gorm.Session{NewDB: true}).Model(&reviewContentListModel{}).Where("subject_id=? AND playback_provider=?", scope.SubjectID, scope.Normalized().PlaybackProvider).Updates(map[string]any{"revision": gorm.Expr("revision+1"), "updated_at": time.Now().UTC()}).Error; err != nil {
						return err
					}
				}
				app.MiniReview.Revision++
			}
			return nil
		}
		if creating && app.PlaybackProvider == "byteplus_web" {
			return tx.Create(&applicationPlaybackProfileModel{ApplicationID: strconv.FormatUint(record.ID, 10), Provider: app.PlaybackProvider, Revision: 1}).Error
		}
		return nil
	})
	var mysqlErr *mysqldriver.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return operations.ErrConflict
	}
	if err == nil {
		app.ID = strconv.FormatUint(record.ID, 10)
		if creating && app.PlaybackProvider == "byteplus_web" {
			app.PlaybackRevision = 1
		}
	}
	return err
}

// saveApplicationPlayback 在已锁定应用的事务中比较播放修订；切回官方保留递增修订，避免旧表单在往返切换后重新生效。
func saveApplicationPlayback(tx *gorm.DB, app application.Application) error {
	// 继续使用当前事务连接，但移除应用 upsert 的列清单，首次插入播放绑定不能沿用 applications 的 ON CONFLICT 子句。
	tx = tx.Session(&gorm.Session{NewDB: true})
	var profile applicationPlaybackProfileModel
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("application_id = ?", app.ID).Take(&profile).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	currentProvider := profile.Provider
	if currentProvider == "" {
		currentProvider = "tiktok_minis"
	}
	if profile.Revision != app.PlaybackRevision {
		return operations.ErrConflict
	}
	provider := app.Normalized().PlaybackProvider
	changed := provider != currentProvider
	if !changed {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Create(&applicationPlaybackProfileModel{ApplicationID: app.ID, Provider: provider, Revision: 1}).Error
	}
	return tx.Model(&profile).Updates(map[string]any{"provider": provider, "revision": profile.Revision + 1, "updated_at": time.Now().UTC()}).Error
}

// HasRewardedAdUnit 判断广告位编码是否登记在当前应用的激励视频集合中。
func (s *Store) HasRewardedAdUnit(ctx context.Context, code string) (bool, error) {
	app, err := s.FindApplication(ctx, application.IDFromContext(ctx))
	if err != nil {
		return false, err
	}
	return app.AdUnits.HasRewardedVideo(code), nil
}

// HasAdUnit 校验当前应用登记的通用广告位，不要求关联任何奖励 Offer。
func (s *Store) HasAdUnit(ctx context.Context, code string) (bool, error) {
	app, err := s.FindApplication(ctx, application.IDFromContext(ctx))
	if err != nil {
		return false, err
	}
	return app.AdUnits.HasCode(code), nil
}
