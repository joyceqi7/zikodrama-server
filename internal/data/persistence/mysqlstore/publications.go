package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// tiktokPublicationModel 映射每部短剧当前可恢复的官方发布编排。
type tiktokPublicationModel struct {
	// SubjectID 固定官方发布主体。
	SubjectID string `gorm:"column:subject_id;type:bigint unsigned"`
	// WorkflowJSON 保存冻结内容及逐应用、逐媒体的任务事实。
	WorkflowJSON []byte `gorm:"column:workflow_json"`
	// Revision 用于任务领取和保存的比较并交换，拒绝过期进程回写。
	Revision uint64 `gorm:"column:revision"`
	// ID 可恢复发布任务标识。
	ID string `gorm:"column:id;primaryKey;autoIncrement;type:bigint unsigned"`
	// DramaID 所属短剧标识，关联 dramas.id。
	DramaID string `gorm:"column:drama_id;type:bigint unsigned"`
	// OwnerApplicationID 是媒资拥有方全局分发配置键，不是 applications.id。
	OwnerApplicationID string `gorm:"column:owner_application_id"`
	// Priority 发布优先级编码。
	Priority string `gorm:"column:priority"`
	// Status 发布任务状态。
	Status string `gorm:"column:status"`
	// Stage 当前官方发布流程阶段。
	Stage string `gorm:"column:stage"`
	// PlatformCoverID TikTok Image API 返回的封面标识。
	PlatformCoverID string `gorm:"column:platform_cover_id"`
	// PlatformAlbumID TikTok 官方 Album 标识，按字符串保存。
	PlatformAlbumID string `gorm:"column:platform_album_id"`
	// PlatformVersion TikTok 官方剧目版本号。
	PlatformVersion int `gorm:"column:platform_version"`
	// ReviewID TikTok 官方审核记录标识。
	ReviewID string `gorm:"column:review_id"`
	// ReviewStatus TikTok 官方审核状态枚举值。
	ReviewStatus int `gorm:"column:review_status"`
	// PlatformPublishStatus TikTok 官方发布状态枚举值。
	PlatformPublishStatus int `gorm:"column:platform_publish_status"`
	// EpisodeIDsJSON 本次发布固定的分集标识列表 JSON。
	EpisodeIDsJSON []byte `gorm:"column:episode_ids_json"`
	// ErrorCode 最近一次失败的稳定错误分类。
	ErrorCode string `gorm:"column:error_code"`
	// ErrorMessage 最近一次失败的可公开错误摘要，不含凭据或媒体地址。
	ErrorMessage string `gorm:"column:error_message"`
	// Attempts 任务已执行的重试次数。
	Attempts int `gorm:"column:attempts"`
	// NextAttemptAt 任务下次允许领取或重试的时间，使用 UTC。
	NextAttemptAt time.Time `gorm:"column:next_attempt_at"`
	// LeaseOwner 当前领取任务的 Worker 标识，空字符串表示未领取。
	LeaseOwner string `gorm:"column:lease_owner"`
	// LeaseExpiresAt Worker 租约到期时间，过期后允许其他 Worker 领取。
	LeaseExpiresAt time.Time `gorm:"column:lease_expires_at"`
	// SubmittedAt 提交官方审核时间，未提交时为 NULL。
	SubmittedAt *time.Time `gorm:"column:submitted_at"`
	// OnlineAt 官方上线完成时间，未上线时为 NULL。
	OnlineAt *time.Time `gorm:"column:online_at"`
	// CreatedAt 记录创建时间，使用 UTC。
	CreatedAt time.Time `gorm:"column:created_at"`
	// UpdatedAt 记录最近更新时间，使用 UTC。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (tiktokPublicationModel) TableName() string { return "tiktok_publications" }

// FindTikTokPublicationByDramaID 返回短剧当前发布任务。
func (s *Store) FindTikTokPublicationByDramaID(ctx context.Context, dramaID string) (operations.TikTokPublication, error) {
	var model tiktokPublicationModel
	if err := subjectPublicationQuery(s.data.DB().WithContext(ctx)).Where("drama_id = ?", dramaID).Order("created_at DESC, id DESC").Take(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return operations.TikTokPublication{}, domain.ErrNotFound
		}
		return operations.TikTokPublication{}, err
	}
	return model.domain()
}

// lockedLatestPublication 在剧目锁之后读取并锁定最新任务，供编辑与媒资绑定核对冻结边界。
func lockedLatestPublication(tx *gorm.DB, dramaID string) (operations.TikTokPublication, error) {
	var model tiktokPublicationModel
	if err := subjectPublicationQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("drama_id = ?", dramaID).Order("created_at DESC, id DESC").Take(&model).Error; err != nil {
		return operations.TikTokPublication{}, err
	}
	return model.domain()
}

// StartTikTokPublication 原子冻结就绪内容并创建 TK 任务，平台版本由官方同步流程维护，不影响两个渠道的线上快照。
func (s *Store) StartTikTokPublication(ctx context.Context, publication *operations.TikTokPublication) error {
	return s.startTikTokPublication(ctx, publication, nil)
}

// RestartTikTokPublication 仅重建指定失败任务的媒体计划，保留原任务和其冻结内容。
func (s *Store) RestartTikTokPublication(ctx context.Context, previous operations.TikTokPublication, next *operations.TikTokPublication) error {
	return s.startTikTokPublication(ctx, next, &previous)
}

// startTikTokPublication 在同一剧目锁中区分新内容发布与失败任务恢复，防止草稿或迟到请求改变冻结边界。
func (s *Store) startTikTokPublication(ctx context.Context, publication *operations.TikTokPublication, previous *operations.TikTokPublication) error {
	created := *publication
	ctx = application.WithSubject(ctx, publication.SubjectID)
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var drama dramaModel
		if err := ownedDramaQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", publication.DramaID).Take(&drama).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrNotFound
			}
			return err
		}
		if err := attachSubjectState(tx, &drama); err != nil {
			return err
		}
		var current tiktokPublicationModel
		currentErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("drama_id = ? AND subject_id=?", publication.DramaID, publication.SubjectID).Order("created_at DESC, id DESC").Take(&current).Error
		if currentErr == nil {
			currentDomain, err := current.domain()
			if err != nil {
				return err
			}
			if previous != nil {
				if currentDomain.ID != previous.ID || currentDomain.Revision != previous.Revision || currentDomain.LeaseOwner != previous.LeaseOwner || !currentDomain.AllowsMediaRestart(created) {
					return operations.ErrConflict
				}
			} else {
				if created.ContentSnapshot == nil || currentDomain.RejectsContent(*created.ContentSnapshot) || currentDomain.ExternalWriteStarted || (currentDomain.Active() && !(currentDomain.LeaseOwner == "" && (currentDomain.Status == operations.PublicationStatusSynced || currentDomain.Status == operations.PublicationStatusApproved))) {
					return operations.ErrConflict
				}
			}
			// 只撤销旧任务的写入修订；显式保留时间，避免 MySQL ON UPDATE 把它误判为最近完成的上下架。
			if err := tx.Model(&current).UpdateColumns(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": current.UpdatedAt}).Error; err != nil {
				return err
			}
		} else if !errors.Is(currentErr, gorm.ErrRecordNotFound) {
			return currentErr
		} else if previous != nil {
			return operations.ErrConflict
		}
		if previous == nil {
			snapshot, err := readyPublicationSnapshot(tx, drama)
			if err != nil {
				return err
			}
			// 媒体计划必须对应锁内的同一内容；编辑并发发生时整体回滚。
			if created.ContentSnapshot == nil || domain.ContentDigest(*created.ContentSnapshot) != domain.ContentDigest(snapshot) {
				return operations.ErrConflict
			}
			created.ContentSnapshot = &snapshot
		}
		model, err := newTikTokPublicationModel(created)
		if err != nil {
			return err
		}
		if err := tx.Create(&model).Error; err != nil {
			return err
		}
		created.ID = model.ID
		if err := syncPublicationRecords(tx, created); err != nil {
			return err
		}
		return touchDramaOperator(tx, created.DramaID)
	})
	if err == nil {
		*publication = created
	}
	return err
}

// ClaimTikTokPublication 使用行锁和 SKIP LOCKED 领取到期队列任务，不扫描审核中记录。
func (s *Store) ClaimTikTokPublication(ctx context.Context, workerID string, now, leaseExpiresAt time.Time) (*operations.TikTokPublication, error) {
	return s.claimTikTokPublication(ctx, workerID, now, leaseExpiresAt, false)
}

// ClaimTikTokReview 使用行锁和 SKIP LOCKED 领取审核中任务，由低频审核扫描器批量调用。
func (s *Store) ClaimTikTokReview(ctx context.Context, workerID string, now, leaseExpiresAt time.Time) (*operations.TikTokPublication, error) {
	return s.claimTikTokPublication(ctx, workerID, now, leaseExpiresAt, true)
}

// claimTikTokPublication 在同一租约协议下分别领取发布队列或审核查询，避免两类扫描互相放大频率。
func (s *Store) claimTikTokPublication(ctx context.Context, workerID string, now, leaseExpiresAt time.Time, review bool) (*operations.TikTokPublication, error) {
	var claimed *operations.TikTokPublication
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model tiktokPublicationModel
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		if review {
			query = query.Where("status = ? AND stage IN ? AND updated_at < ? AND (lease_owner = '' OR lease_expires_at <= ?)",
				operations.PublicationStatusUnderReview, []string{operations.PublicationStagePollReview, operations.PublicationStagePollAppeal}, now, now)
		} else {
			query = query.Where("status = ? AND stage IN ? AND next_attempt_at <= ? AND (lease_owner = '' OR lease_expires_at <= ?)",
				operations.PublicationStatusQueued, []string{operations.PublicationStageRegisterVideos, operations.PublicationStageTranscodeVideos}, now, now)
		}
		err := query.Order("next_attempt_at ASC, id ASC").Take(&model).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		model.LeaseOwner = workerID
		model.Revision++
		model.LeaseExpiresAt = leaseExpiresAt
		model.UpdatedAt = now
		if err := tx.Model(&tiktokPublicationModel{}).Where("id = ?", model.ID).Updates(map[string]any{
			"lease_owner": workerID, "lease_expires_at": leaseExpiresAt, "updated_at": now, "revision": model.Revision,
		}).Error; err != nil {
			return err
		}
		publication, err := model.domain()
		if err != nil {
			return err
		}
		claimed = &publication
		return nil
	})
	return claimed, err
}

// UpdateTikTokPublication 只保存当前任务的匹配修订，并可原子更新消费者可见性、全局分类和平台映射。
func (s *Store) UpdateTikTokPublication(ctx context.Context, publication operations.TikTokPublication, drama *domain.Drama) error {
	ctx = application.WithSubject(ctx, publication.SubjectID)
	model, err := newTikTokPublicationModel(publication)
	if err != nil {
		return err
	}
	// 先校验领取时修订号和租约所有者，再释放租约；不能先清空领域租约再写库。
	model.Revision = publication.Revision + 1
	if publication.Status != operations.PublicationStatusProcessing {
		model.LeaseOwner = ""
		model.LeaseExpiresAt = time.Unix(0, 0).UTC()
	}
	return s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 与 StartTikTokPublication 保持同一顺序：剧目锁在前，任务锁在后。
		var previous dramaModel
		if drama != nil && drama.ID != publication.DramaID {
			return operations.ErrConflict
		}
		if err := ownedDramaQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", publication.DramaID).Take(&previous).Error; err != nil {
			return err
		}
		if err := attachSubjectState(tx, &previous); err != nil {
			return err
		}
		latest, err := lockedLatestPublication(tx, publication.DramaID)
		if err != nil {
			return err
		}
		if latest.ID != publication.ID {
			return operations.ErrConflict
		}

		// 官方下架排队、重试和最终清除快照均受推荐引用保护，失败记录自身仍可正常保存。
		if publication.Stage == operations.PublicationStageDelistAlbum && publication.Status == operations.PublicationStatusQueued ||
			publication.Status == operations.PublicationStatusOffline && drama != nil && drama.Published == nil {
			if err := ensureDramaNotRecommended(tx, publication.DramaID, false); err != nil {
				return err
			}
		}
		result := tx.Model(&tiktokPublicationModel{}).Where("id = ? AND drama_id = ? AND revision = ? AND lease_owner = ?", publication.ID, publication.DramaID, publication.Revision, publication.LeaseOwner).
			Select("*").Omit("created_at").Updates(&model)
		if err := gormAffectedOrNotFound(result); err != nil {
			return err
		}
		if err := syncPublicationRecords(tx, publication); err != nil {
			return err
		}
		if err := touchDramaOperator(tx, publication.DramaID); err != nil {
			return err
		}
		if drama == nil {
			return nil
		}
		releaseChanged := publication.Stage == operations.PublicationStageDone &&
			(publication.Status == operations.PublicationStatusOnline || publication.Status == operations.PublicationStatusOffline)
		// 同步、审核只维护媒资映射，绝不能把调用前读到的旧线上指针写回。
		if releaseChanged {
			if drama.Published != nil {
				var ids []string
				if err := tx.Table("drama_distributions AS dd").Joins("JOIN applications a ON a.id=dd.application_id AND a.subject_id=?", publication.SubjectID).Where("dd.drama_id=? AND dd.desired=true AND dd.status='authorized'", drama.ID).Pluck("application_id", &ids).Error; err != nil {
					return err
				}
				drama.Published.AuthorizedApplicationIDs = ids
				drama.Published.OwnerApplicationID = ""
				if publication.OwnerScope.ClientKey != "" {
					var owner applicationModel
					err := tx.Where("client_key = ? AND subject_id=?", publication.OwnerScope.ClientKey, publication.SubjectID).Take(&owner).Error
					if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
						return err
					}
					if err == nil {
						drama.Published.OwnerApplicationID = strconv.FormatUint(owner.ID, 10)
					}
				}
			}
			publishedJSON, marshalErr := json.Marshal(drama.Published)
			if marshalErr != nil {
				return marshalErr
			}
			if err := saveSubjectState(tx, drama.ID, drama.PlatformAlbumID, drama.Published); err != nil {
				return err
			}
			if err := syncCategoryRankings(tx, drama.ID, previous.PublishedSnapshot, publishedJSON); err != nil {
				return err
			}
		} else {
			var published *domain.Drama
			if len(previous.PublishedSnapshot) > 0 {
				if err := json.Unmarshal(previous.PublishedSnapshot, &published); err != nil {
					return err
				}
			}
			if err := saveSubjectState(tx, drama.ID, publication.PlatformAlbumID, published); err != nil {
				return err
			}
		}
		// 官方分集映射已固定在主体发布版本中，不回写共享草稿分集。
		return nil
	})
}

func newTikTokPublicationModel(publication operations.TikTokPublication) (tiktokPublicationModel, error) {
	workflowJSON, err := json.Marshal(publicationWorkflow{
		ContentSummary:   publication.ContentSummary,
		PublishRequested: publication.PublishRequested,
		Appeal:           publication.Appeal, ReviewItems: publication.ReviewItems,
		OwnerScope:           publication.OwnerScope,
		TranscodeKeySeed:     publication.TranscodeKeySeed,
		SkipTranscode:        publication.SkipTranscode,
		ExternalWriteStarted: publication.ExternalWriteStarted,
		DiagnosticCode:       publication.DiagnosticCode,
		Content:              publication.ContentSnapshot, Videos: publication.Videos, Transcodes: publication.Transcodes,
		ReviewFailReasons: publication.ReviewFailReasons,
	})
	if err != nil {
		return tiktokPublicationModel{}, err
	}
	episodeIDs := make(map[string]string, len(publication.EpisodeIDsBySequence))
	for sequence, episodeID := range publication.EpisodeIDsBySequence {
		episodeIDs[strconv.Itoa(sequence)] = episodeID
	}
	episodeIDsJSON, err := json.Marshal(episodeIDs)
	if err != nil {
		return tiktokPublicationModel{}, err
	}
	leaseExpiresAt := publication.LeaseExpiresAt
	if leaseExpiresAt.IsZero() {
		// MySQL DATETIME 不接受 Go 的零值年份，未领取任务统一保存为 Unix epoch。
		leaseExpiresAt = time.Unix(0, 0).UTC()
	}
	nextAttemptAt := publication.NextAttemptAt
	if nextAttemptAt.IsZero() {
		// 非排队状态不再参与领取，但持久化列仍为非空；使用 Unix epoch 避免写入非法零日期。
		nextAttemptAt = time.Unix(0, 0).UTC()
	}
	return tiktokPublicationModel{
		WorkflowJSON: workflowJSON, Revision: publication.Revision,
		SubjectID: publication.SubjectID, ID: publication.ID, DramaID: publication.DramaID, OwnerApplicationID: publication.OwnerApplicationID,
		Priority: publication.Priority, Status: publication.Status, Stage: publication.Stage,
		PlatformCoverID: publication.PlatformCoverID, PlatformAlbumID: publication.PlatformAlbumID,
		PlatformVersion: publication.PlatformVersion, ReviewID: publication.ReviewID,
		ReviewStatus: publication.ReviewStatus, PlatformPublishStatus: publication.PlatformPublishStatus,
		EpisodeIDsJSON: episodeIDsJSON,
		ErrorCode:      publication.ErrorCode, ErrorMessage: publication.ErrorMessage,
		Attempts: publication.Attempts, NextAttemptAt: nextAttemptAt,
		LeaseOwner: publication.LeaseOwner, LeaseExpiresAt: leaseExpiresAt,
		SubmittedAt: publication.SubmittedAt, OnlineAt: publication.OnlineAt,
		CreatedAt: publication.CreatedAt, UpdatedAt: publication.UpdatedAt,
	}, nil
}

func (model tiktokPublicationModel) domain() (operations.TikTokPublication, error) {
	publication := operations.TikTokPublication{
		Revision:  model.Revision,
		SubjectID: model.SubjectID, ID: model.ID, DramaID: model.DramaID, OwnerApplicationID: model.OwnerApplicationID,
		Priority: model.Priority, Status: model.Status, Stage: model.Stage,
		PlatformCoverID: model.PlatformCoverID, PlatformAlbumID: model.PlatformAlbumID,
		PlatformVersion: model.PlatformVersion, ReviewID: model.ReviewID,
		ReviewStatus: model.ReviewStatus, PlatformPublishStatus: model.PlatformPublishStatus,
		EpisodeIDsBySequence: make(map[int]string), ErrorCode: model.ErrorCode, ErrorMessage: model.ErrorMessage,
		Attempts: model.Attempts, NextAttemptAt: model.NextAttemptAt, LeaseOwner: model.LeaseOwner,
		LeaseExpiresAt: model.LeaseExpiresAt, SubmittedAt: model.SubmittedAt, OnlineAt: model.OnlineAt,
		CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}
	var workflow publicationWorkflow
	if err := json.Unmarshal(model.WorkflowJSON, &workflow); err != nil {
		return operations.TikTokPublication{}, err
	}
	publication.ContentSnapshot, publication.Videos, publication.Transcodes = workflow.Content, workflow.Videos, workflow.Transcodes
	publication.ContentSummary = workflow.ContentSummary
	// 历史任务可还原本次内容，但不能用今天的线上内容推测当时的增量。
	if publication.ContentSummary == nil && workflow.Content != nil {
		publication.ContentSummary = operations.NewPublicationContentSummary(*workflow.Content)
		publication.ContentSummary.PreviousEpisodeNumbers = nil
	}
	publication.Appeal, publication.ReviewItems = workflow.Appeal, workflow.ReviewItems
	publication.ExternalWriteStarted = workflow.ExternalWriteStarted
	publication.DiagnosticCode = workflow.DiagnosticCode
	publication.OwnerScope = workflow.OwnerScope
	publication.TranscodeKeySeed = workflow.TranscodeKeySeed
	publication.SkipTranscode = workflow.SkipTranscode
	publication.PublishRequested = workflow.PublishRequested
	publication.ReviewFailReasons = workflow.ReviewFailReasons
	var episodeIDs map[string]string
	if err := json.Unmarshal(model.EpisodeIDsJSON, &episodeIDs); err != nil {
		return operations.TikTokPublication{}, err
	}
	for sequenceValue, episodeID := range episodeIDs {
		sequence, err := strconv.Atoi(sequenceValue)
		if err != nil || sequence <= 0 {
			return operations.TikTokPublication{}, operations.ErrInvalidInput
		}
		publication.EpisodeIDsBySequence[sequence] = episodeID
	}
	return publication, nil
}

// publicationWorkflow 是仅在服务端持久化的版本快照，不直接作为管理 API 响应。
type publicationWorkflow struct {
	// SkipTranscode 固定本次同步选择；旧记录未提供时继续原转码流程。
	SkipTranscode bool `json:"skipTranscode,omitempty"`
	// ContentSummary 保存本次内容与同步前的线上范围，重试或下架不会重算。
	ContentSummary *operations.PublicationContentSummary `json:"contentSummary,omitempty"`
	// PublishRequested 保存当前版本的人工上线指令；历史 JSON 缺省时不允许自动上线。
	PublishRequested bool `json:"publishRequested,omitempty"`
	// Appeal 和 ReviewItems 保存单项申诉输入及已查询的官方事实。
	Appeal *operations.TikTokReviewAppealInput `json:"appeal,omitempty"`
	// ReviewItems 按平台项目隔离审核状态。
	ReviewItems map[string]int `json:"reviewItems,omitempty"`
	// OwnerScope 防止处理过程中切换官方资源归属。
	OwnerScope operations.PublicationOwnerScope `json:"ownerScope"`
	// TranscodeKeySeed 保存新发布的随机幂等键前缀，不进入管理 API 响应。
	TranscodeKeySeed string `json:"transcodeKeySeed,omitempty"`
	// ExternalWriteStarted 记录非幂等外部调用是否尚未确认结果。
	ExternalWriteStarted bool `json:"externalWriteStarted"`
	// DiagnosticCode 保存结果未知前的安全失败分类，不保存平台响应正文。
	DiagnosticCode string `json:"diagnosticCode,omitempty"`
	// Content 保存提交时的内容。
	Content *domain.Drama `json:"content"`
	// Videos 保存视频登记事实。
	Videos []operations.PublicationVideo `json:"videos"`
	// Transcodes 保存批量任务进度，具体幂等键由随机前缀或旧发布 ID 与批次确定。
	Transcodes []operations.PublicationTranscode `json:"transcodes"`
	// ReviewFailReasons 保存官方当前版本审核意见。
	ReviewFailReasons []string `json:"reviewFailReasons"`
}
