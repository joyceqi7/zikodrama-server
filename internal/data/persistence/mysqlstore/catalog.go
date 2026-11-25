package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// ListDramas 返回已经发布、允许消费者访问的短剧完整目录。
// 目录主记录、TikTok 标签和分集分别由对应的 GORM Model 装载，避免在
// Repository 中维护手写列顺序和 Scan 参数顺序。
func (s *Store) ListDramas(ctx context.Context) ([]domain.Drama, error) {
	return s.listDramas(ctx, true)
}

// ListAllDramas 返回运营后台可管理的全部短剧编辑内容，并批量读取分集；不查询渠道版本。
func (s *Store) ListAllDramas(ctx context.Context) ([]domain.Drama, error) {
	return s.listDramas(ctx, false)
}

func (s *Store) listDramas(ctx context.Context, publishedOnly bool) ([]domain.Drama, error) {
	query := ownedDramaQuery(s.data.DB().WithContext(ctx)).Order("created_at DESC, id ASC")
	if publishedOnly && application.IsDirect(ctx) {
		query = query.Where("id IN (?)", s.directCatalogQuery(ctx).Select("p.drama_id"))
	} else if publishedOnly {
		query = query.Where("id IN (SELECT drama_id FROM tiktok_drama_states WHERE subject_id=? AND published_snapshot IS NOT NULL AND JSON_TYPE(published_snapshot)<>'NULL')", application.SubjectIDFromContext(ctx))
	}
	var models []dramaModel
	if err := query.Find(&models).Error; err != nil {
		return nil, err
	}
	if err := attachSubjectStates(s.data.DB().WithContext(ctx), models); err != nil {
		return nil, err
	}
	dramas := make([]domain.Drama, 0, len(models))
	for _, model := range models {
		drama, err := model.domain()
		if err != nil {
			return nil, err
		}
		dramas = append(dramas, drama)
	}
	catalog, err := s.ListTikTokTags(ctx)
	if err != nil {
		return nil, err
	}
	for index := range dramas {
		domain.AttachTikTokTagCatalog(&dramas[index], catalog)
		if dramas[index].Published != nil {
			domain.AttachTikTokTagCatalog(dramas[index].Published, catalog)
		}
		dramas[index].Episodes = []domain.Episode{}
	}
	if err := s.attachEpisodes(ctx, dramas); err != nil {
		return nil, err
	}
	if err := s.hydratePublishedEpisodePosters(ctx, dramas); err != nil {
		return nil, err
	}
	if application.IsDirect(ctx) {
		for i := range dramas {
			if err := s.attachDirectSnapshot(ctx, &dramas[i]); err != nil {
				return nil, err
			}
		}
	}
	return dramas, nil
}

// hydratePublishedEpisodePosters 以线上快照中的 episode ID 与 VID 为引用，从 episodes 表合并官方派生海报，不改写发布内容快照。
func (s *Store) hydratePublishedEpisodePosters(ctx context.Context, dramas []domain.Drama) error {
	episodeIDs := make([]string, 0)
	for index := range dramas {
		if dramas[index].Published == nil {
			continue
		}
		for _, episode := range dramas[index].Published.Episodes {
			episodeIDs = append(episodeIDs, episode.ID)
		}
	}
	if len(episodeIDs) == 0 {
		return nil
	}
	var models []episodeModel
	if err := catalogQuery(s.data.DB().WithContext(ctx)).Select("id", "vid", "poster_url").Where("id IN ?", episodeIDs).Find(&models).Error; err != nil {
		return err
	}
	posters := make(map[string]string, len(models))
	for _, model := range models {
		if posterURL := strings.TrimSpace(model.PosterURL); posterURL != "" {
			posters[model.ID+"\x00"+model.VID] = posterURL
		}
	}
	for dramaIndex := range dramas {
		if dramas[dramaIndex].Published == nil {
			continue
		}
		for episodeIndex := range dramas[dramaIndex].Published.Episodes {
			episode := &dramas[dramaIndex].Published.Episodes[episodeIndex]
			if posterURL := posters[episode.ID+"\x00"+episode.VID]; posterURL != "" {
				episode.PosterURL = posterURL
			}
		}
	}
	return nil
}

// attachEpisodes 一次读取全部短剧的编辑区分集，避免目录和媒资校准按短剧逐条查询。
func (s *Store) attachEpisodes(ctx context.Context, dramas []domain.Drama) error {
	if len(dramas) == 0 {
		return nil
	}
	ids := make([]string, 0, len(dramas))
	indexes := make(map[string]int, len(dramas))
	for index := range dramas {
		ids = append(ids, dramas[index].ID)
		indexes[dramas[index].ID] = index
	}
	var models []episodeModel
	if err := catalogQuery(s.data.DB().WithContext(ctx)).
		Where("drama_id IN ? AND is_draft = ?", ids, true).
		Order("drama_id ASC, episode_number ASC, id ASC").Find(&models).Error; err != nil {
		return err
	}
	for _, model := range models {
		index, ok := indexes[model.DramaID]
		if !ok {
			continue
		}
		dramas[index].Episodes = append(dramas[index].Episodes, model.domain())
	}
	return nil
}

// FindDrama 按主键返回短剧编辑内容、TikTok 标签和分集；保留既有官方线上快照，不查询发布版本。
func (s *Store) FindDrama(ctx context.Context, id string) (*domain.Drama, error) {
	var model dramaModel
	query := ownedDramaQuery(s.data.DB().WithContext(ctx))
	query = query.Where("id=?", id)
	if err := query.First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	if err := attachSubjectState(s.data.DB().WithContext(ctx), &model); err != nil {
		return nil, err
	}
	drama, err := model.domain()
	if err != nil {
		return nil, err
	}
	catalog, err := s.ListTikTokTags(ctx)
	if err != nil {
		return nil, err
	}
	domain.AttachTikTokTagCatalog(&drama, catalog)
	if drama.Published != nil {
		domain.AttachTikTokTagCatalog(drama.Published, catalog)
	}
	drama.Episodes, err = s.listEpisodes(ctx, drama.ID)
	if err != nil {
		return nil, err
	}
	if err := s.hydratePublishedEpisodePosters(ctx, []domain.Drama{drama}); err != nil {
		return nil, err
	}
	if application.IsDirect(ctx) {
		if err := s.attachDirectSnapshot(ctx, &drama); err != nil {
			return nil, err
		}
	}
	return &drama, nil
}

// SaveDrama 在一个事务中保存短剧主记录，并补齐线上快照的全局分类关联。
func (s *Store) SaveDrama(ctx context.Context, drama domain.Drama) error {
	drama.PublishStatus = "draft"
	return catalogQuery(s.data.DB().WithContext(ctx)).Transaction(func(tx *gorm.DB) error {
		if err := saveDramaRecord(tx, &drama); err != nil {
			return err
		}
		return nil
	})
}

// SaveDramaOperations 更新协作元数据，不改变草稿、线上快照或发布状态。
func (s *Store) SaveDramaOperations(ctx context.Context, id, assignee, note string, dueAt *time.Time) error {
	updates := map[string]any{"assignee": assignee, "operation_note": note, "operation_due_at": dueAt, "updated_at": time.Now().UTC()}
	if actor := operations.PublicationActor(ctx); actor != "" {
		updates["updated_by"] = actor
	}
	result := ownedDramaQuery(s.data.DB().WithContext(ctx)).Model(&dramaModel{}).Where("id = ?", id).Updates(updates)
	return gormAffectedOrNotFound(result)
}

// SaveDramaAggregate 原子替换短剧编辑内容，允许无分集草稿；清空分集时保留线上快照引用。
// 保存前先锁定已有分集，防止上传回调和后台编辑同时修改同一条媒资。聚合编辑不
// 接受前端直接替换 VID；如果请求中的 VID 与库内值不同，继续使用数据库中的媒资
// 字段，媒资替换只能经 SaveEpisodeMedia 完成。
// 编辑聚合只维护当前剧和集；渠道发布快照与版本不由该入口生成或覆盖。
func (s *Store) SaveDramaAggregate(ctx context.Context, target *domain.Drama, cover *operations.MediaJob) error {
	drama := *target
	drama.PublishStatus = "draft"
	drama.Episodes = append([]domain.Episode(nil), target.Episodes...)
	err := catalogQuery(s.data.DB().WithContext(ctx)).Transaction(func(tx *gorm.DB) error {
		creating := drama.ID == ""
		if creating {
			if err := saveDramaRecord(tx, &drama); err != nil {
				return err
			}
		}
		var previousDrama dramaModel
		previousDramaErr := ownedDramaQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&previousDrama, "id = ?", drama.ID).Error
		if errors.Is(previousDramaErr, gorm.ErrRecordNotFound) {
			if err := saveDramaRecord(tx, &drama); err != nil {
				return err
			}
			previousDramaErr = ownedDramaQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&previousDrama, "id=?", drama.ID).Error
		}
		if previousDramaErr != nil {
			return previousDramaErr
		}
		// TikTok Album 标识只由官方发布流程维护，普通内容编辑必须保留库内值。
		if previousDramaErr == nil {
			drama.PlatformAlbumID = previousDrama.PlatformAlbumID
		}
		previousEpisodes, err := lockedEpisodes(tx, drama.ID)
		if err != nil {
			return err
		}
		for index := range drama.Episodes {
			if current, exists := previousEpisodes[drama.Episodes[index].ID]; exists {
				// 平台 Episode 标识不能由管理端普通表单覆盖。
				drama.Episodes[index].PlatformEpisodeID = current.PlatformEpisodeID
				{
					preserveEpisodeMedia(&drama.Episodes[index], current)
				}
			}
		}
		if cover != nil {
			var job mediaJobModel
			if err := catalogQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&job, "id = ?", cover.ID).Error; err != nil {
				return err
			}
			if job.SubjectID != application.SubjectIDFromContext(ctx) || job.Kind != operations.MediaJobCover || job.Status != operations.MediaJobUploaded || job.PublicURL != drama.CoverURL {
				return operations.ErrConflict
			}
			drama.CoverMID, drama.CoverSpaceName, drama.CoverFileName = job.RemoteVID, job.SpaceName, job.ProviderFileName
			if err := tx.Model(&job).Updates(map[string]any{"drama_id": drama.ID, "status": operations.MediaJobSucceeded, "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		} else if previousDramaErr == nil && previousDrama.CoverURL == drama.CoverURL {
			drama.CoverMID, drama.CoverSpaceName, drama.CoverFileName = previousDrama.CoverMID, previousDrama.CoverSpaceName, previousDrama.CoverFileName
		} else {
			drama.CoverMID, drama.CoverSpaceName, drama.CoverFileName = "", "", ""
		}
		if err := saveDramaRecord(tx, &drama); err != nil {
			return err
		}

		// 把编辑区编号整体移到现有最小值以下，避免与历史保留行发生唯一键碰撞。
		lowest, highest := 0, 0
		for _, episode := range previousEpisodes {
			lowest = min(lowest, episode.Number)
			highest = max(highest, episode.Number)
		}
		if err := tx.Model(&episodeModel{}).Where("drama_id = ? AND is_draft = ?", drama.ID, true).
			Updates(map[string]any{"episode_number": gorm.Expr("episode_number - ?", highest-lowest+1), "is_draft": false}).Error; err != nil {
			return err
		}
		ids := make([]string, 0, len(drama.Episodes))
		for index := range drama.Episodes {
			episode := &drama.Episodes[index]
			if episode.ID == "" {
				episode.DramaID = drama.ID
			}
			if episode.DramaID != drama.ID {
				return operations.ErrInvalidInput
			}
			if err := saveEpisodeRecord(tx, episode); err != nil {
				return err
			}
			ids = append(ids, episode.ID)
		}
		keepIDs := append([]string(nil), ids...)
		if previousDramaErr == nil {
			previous, err := previousDrama.domain()
			if err != nil {
				return err
			}
			if previous.Published != nil {
				for _, episode := range previous.Published.Episodes {
					keepIDs = append(keepIDs, episode.ID)
				}
			}
		}
		// 已确认版本和官方任务都保留分集身份，草稿修改不能破坏线上播放或审核。
		retained, err := retainedContentEpisodeIDs(tx, drama.ID)
		if err != nil {
			return err
		}
		keepIDs = append(keepIDs, retained...)
		removedIDs := make([]string, 0, len(previousEpisodes))
		kept := make(map[string]bool, len(keepIDs))
		for _, id := range keepIDs {
			kept[id] = true
		}
		for id := range previousEpisodes {
			if !kept[id] {
				removedIDs = append(removedIDs, id)
			}
		}
		if err := deleteEpisodeRecords(tx, removedIDs); err != nil {
			return err
		}
		for _, episode := range drama.Episodes {
			delete(previousEpisodes, episode.ID)
		}
		if previousDramaErr == nil {
			previous, err := previousDrama.domain()
			if err != nil {
				return err
			}
			if previous.CoverMID != drama.CoverMID {
				if err := queueCoverDeletion(tx, previous); err != nil {
					return err
				}
			}
		}
		for _, episode := range previousEpisodes {
			if err := queueEpisodeDeletion(tx, episode); err != nil {
				return err
			}
		}

		return nil
	})
	if err == nil {
		*target = drama
	}
	return err
}

// saveDramaRecord 使用主键冲突更新保存短剧。CreatedAt 只在首次创建时写入，
// UpdatedAt 每次保存都会刷新，避免依赖数据库驱动层的隐式行为。
func saveDramaRecord(db *gorm.DB, drama *domain.Drama) error {
	if application.SubjectIDFromContext(db.Statement.Context) == "" {
		return operations.ErrInvalidInput
	}
	creating := drama.ID == ""
	if !creating {
		var existing dramaModel
		err := db.Select("id", "subject_id").First(&existing, "id=?", drama.ID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			creating = true
		} else if err != nil {
			return err
		} else if existing.SubjectID != application.SubjectIDFromContext(db.Statement.Context) {
			return domain.ErrNotFound
		}
	}
	model, err := newDramaModel(*drama)
	model.SubjectID = application.SubjectIDFromContext(db.Statement.Context)
	if err != nil {
		return err
	}
	columns := []string{"title", "description", "cover_url", "cover_mid", "cover_space_name", "cover_file_name", "status", "year", "language", "drama_type", "tag_ids_json", "publish_status", "updated_at"}
	// 创建人只随 INSERT 保存；冲突更新只写最新人工操作人，禁止表单或后台任务覆盖创建身份。
	if actor := operations.PublicationActor(db.Statement.Context); actor != "" {
		model.CreatedBy, model.UpdatedBy = stringPointer(actor), stringPointer(actor)
		columns = append(columns, "updated_by")
	}
	if creating {
		err = db.Create(&model).Error
	} else {
		err = gormAffectedOrNotFound(ownedDramaQuery(db).Model(&dramaModel{}).Where("id=?", drama.ID).Select(columns).Updates(&model))
	}
	if err == nil {
		drama.ID = model.ID
	} else {
		return err
	}
	return nil
}

// DeleteDrama 在同一事务删除可编辑且无线上快照的短剧、分集、发布与分发记录，保留奖励流水并清空失效引用，同时安排 VOD 清理。
func (s *Store) DeleteDrama(ctx context.Context, id string) error {
	return catalogQuery(s.data.DB().WithContext(ctx)).Transaction(func(tx *gorm.DB) error {
		var stored dramaModel
		if err := ownedDramaQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).Take(&stored).Error; err != nil {
			return err
		}
		current, err := stored.domain()
		if err != nil {
			return err
		}
		// 删除前检查所有历史发布引用，迁移保留的资源也不能提前释放。
		for _, guard := range []struct{ table, predicate string }{
			{"direct_publications", "status='published'"},
			{"tiktok_drama_states", "published_snapshot IS NOT NULL AND JSON_TYPE(published_snapshot)<>'NULL'"},
			{"tiktok_publications", "status IN ('queued','processing','under_review','approved','synced') AND id=(SELECT latest.id FROM tiktok_publications latest WHERE latest.drama_id=tiktok_publications.drama_id AND latest.subject_id=tiktok_publications.subject_id ORDER BY latest.created_at DESC,latest.id DESC LIMIT 1)"},
		} {
			var retained int64
			if err := tx.Table(guard.table).Where("drama_id=?", id).Where(guard.predicate).Count(&retained).Error; err != nil {
				return err
			}
			if retained > 0 {
				return operations.ErrConflict
			}
		}
		snapshots, err := retainedContentSnapshots(tx, id)
		if err != nil {
			return err
		}
		for _, snapshot := range snapshots {
			if err := queueCoverDeletion(tx, snapshot); err != nil {
				return err
			}
			for _, episode := range snapshot.Episodes {
				if err := queueEpisodeDeletion(tx, episode); err != nil {
					return err
				}
			}
		}
		// 删除未上架短剧同时移除版本和渠道历史，清理任务随后确认引用已全部释放。
		for _, table := range []string{"direct_publication_versions", "direct_publication_operations", "direct_publications"} {
			if err := tx.Table(table).Where("drama_id = ?", id).Delete(map[string]any{}).Error; err != nil {
				return err
			}
		}
		episodes, err := lockedEpisodes(tx, id)
		if err != nil {
			return err
		}
		// 显式维护原有清理语义，不能依赖数据库级联删除或 SET NULL。
		episodeIDs := make([]string, 0, len(episodes))
		for episodeID := range episodes {
			episodeIDs = append(episodeIDs, episodeID)
		}
		if err := deleteEpisodeRecords(tx, episodeIDs); err != nil {
			return err
		}
		for shard := uint16(0); shard < 16; shard++ {
			if err := tx.Table(userTable("reward_grants", shard)).Where("drama_id = ?", id).Update("drama_id", nil).Error; err != nil {
				return err
			}
		}
		// 随短剧显式清理关联记录，不依赖数据库外键级联。
		for _, table := range []string{"tiktok_review_records", "tiktok_release_records", "tiktok_drama_states"} {
			if err := tx.Table(table).Where("drama_id = ?", id).Delete(&publicationRecordModel{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("drama_id = ?", id).Delete(&tiktokPublicationModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("drama_id = ?", id).Delete(&distributionModel{}).Error; err != nil {
			return err
		}
		if err := gormAffectedOrNotFound(tx.Delete(&dramaModel{}, "id = ?", id)); err != nil {
			return err
		}
		if err := queueCoverDeletion(tx, current); err != nil {
			return err
		}
		for _, episode := range episodes {
			if err := queueEpisodeDeletion(tx, episode); err != nil {
				return err
			}
		}
		// 已确认但尚未绑定的上传仍持久化为清理任务；未知 VID 的预留任务继续等待回调定位。
		var jobs []mediaJobModel
		if err := tx.Where("drama_id = ? AND kind <> ? AND remote_vid <> ''", id, operations.MediaJobDelete).Find(&jobs).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			if err := queueResourceDeletion(tx, job.mediaJob()); err != nil {
				return err
			}
		}

		return nil
	})
}

// deleteEpisodeRecords 在调用方聚合事务内清空奖励流水的分集引用，再删除指定分集；空集合不执行写入。
func deleteEpisodeRecords(tx *gorm.DB, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	for shard := uint16(0); shard < 16; shard++ {
		if err := tx.Table(userTable("reward_grants", shard)).Where("episode_id IN ?", ids).Update("episode_id", nil).Error; err != nil {
			return err
		}
	}
	return tx.Where("id IN ?", ids).Delete(&episodeModel{}).Error
}

// FindEpisode 按主键返回分集主字段。
func (s *Store) FindEpisode(ctx context.Context, id string) (*domain.Episode, error) {
	var model episodeModel
	if err := ownedEpisodeQuery(s.data.DB().WithContext(ctx)).First(&model, "id = ? AND is_draft = ?", id, true).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	episode := model.domain()
	return &episode, nil
}

// SaveEpisode 在一个事务中保存分集以及被替换媒资的清理任务。
func (s *Store) SaveEpisode(ctx context.Context, episode domain.Episode) error {
	return catalogQuery(s.data.DB().WithContext(ctx)).Transaction(func(tx *gorm.DB) error {
		return saveEpisodeWithCleanup(ctx, tx, episode)
	})
}

// SaveEpisodeMedia 保存媒体上传结果。
// expectedVID 是发起上传时观察到的旧值。只有分集仍属于同一短剧、VID 未被其他
// 请求修改且分集仍处于可编辑草稿时才允许落库，防止过期上传回调覆盖新结果。
func (s *Store) SaveEpisodeMedia(ctx context.Context, episode domain.Episode, drama domain.Drama, expectedVID, assetID string) error {
	if episode.DramaID != drama.ID {
		return operations.ErrInvalidInput
	}
	return catalogQuery(s.data.DB().WithContext(ctx)).Transaction(func(tx *gorm.DB) error {
		// 与保存草稿、发布、下架统一先锁剧目，再锁分集，避免交叉锁顺序。
		var storedDrama dramaModel
		if err := ownedDramaQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&storedDrama, "id = ?", drama.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrNotFound
			}
			return err
		}
		var storedEpisode episodeModel
		if err := catalogQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&storedEpisode, "id = ?", episode.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrNotFound
			}
			return err
		}
		if storedEpisode.DramaID != drama.ID || storedEpisode.VID != expectedVID {
			return operations.ErrConflict
		}
		if !storedEpisode.IsDraft {
			return operations.ErrConflict
		}
		if err := tx.Model(&dramaModel{}).Where("id = ?", drama.ID).Update("publish_status", "draft").Error; err != nil {
			return err
		}
		if assetID != "" {
			var job mediaJobModel
			if err := catalogQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&job, "id = ?", assetID).Error; err != nil {
				return err
			}
			if job.Kind != operations.MediaJobUpload || job.EpisodeID != episode.ID || job.DramaID != drama.ID || job.RemoteVID != episode.VID || job.Status != operations.MediaJobRunning {
				return operations.ErrConflict
			}
		}
		if err := gormAffectedOrNotFound(tx.Model(&episodeModel{}).Where("id = ?", episode.ID).Updates(map[string]any{
			"vid":              episode.VID,
			"media_space_name": episode.MediaSpaceName,
			"poster_url":       episode.PosterURL,
			"duration_seconds": episode.DurationSeconds,
			"media_status":     episode.MediaStatus,
			"updated_at":       time.Now().UTC(),
		})); err != nil {
			return err
		}
		if err := touchDramaOperator(tx, drama.ID); err != nil {
			return err
		}
		if storedEpisode.VID != episode.VID {
			return queueEpisodeDeletion(tx, storedEpisode.domain())
		}

		return nil
	})
}

// UpdateEpisodeMediaStatus 只投影仍绑定同一 VID 的远端观察状态，避免过期校准覆盖新视频。
func (s *Store) UpdateEpisodeMediaStatus(ctx context.Context, episodeID, expectedVID, status string) error {
	result := ownedEpisodeQuery(s.data.DB().WithContext(ctx)).Model(&episodeModel{}).
		Where("id = ? AND vid = ?", strings.TrimSpace(episodeID), strings.TrimSpace(expectedVID)).
		Updates(map[string]any{"media_status": strings.TrimSpace(status), "updated_at": time.Now().UTC()})
	return gormAffectedOrNotFound(result)
}

func saveEpisodeWithCleanup(ctx context.Context, tx *gorm.DB, episode domain.Episode) error {
	var stored episodeModel
	err := catalogQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&stored, "id = ?", episode.ID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err := saveEpisodeRecord(tx, &episode); err != nil {
		return err
	}
	return nil
}

// lockedEpisodes 锁定并返回短剧当前的分集快照，供聚合替换和媒资清理使用。
func lockedEpisodes(tx *gorm.DB, dramaID string) (map[string]domain.Episode, error) {
	var models []episodeModel
	if err := catalogQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("drama_id = ?", dramaID).Find(&models).Error; err != nil {
		return nil, err
	}
	episodes := make(map[string]domain.Episode, len(models))
	for _, model := range models {
		episode := model.domain()
		episodes[episode.ID] = episode
	}
	return episodes, nil
}

// preserveEpisodeMedia 保留数据库中已经确认的媒资字段，阻止普通聚合编辑绕过上传流程。
func preserveEpisodeMedia(target *domain.Episode, current domain.Episode) {
	target.VID = current.VID
	target.MediaSpaceName = current.MediaSpaceName
	target.PlatformEpisodeID = current.PlatformEpisodeID
	target.PosterURL = current.PosterURL
	target.DurationSeconds = current.DurationSeconds
	target.MediaStatus = current.MediaStatus
}

func saveEpisodeRecord(db *gorm.DB, episode *domain.Episode) error {
	if err := requireOwnedDrama(db, episode.DramaID); err != nil {
		return err
	}
	if episode.ID != "" {
		var existing episodeModel
		err := catalogQuery(db).First(&existing, "id=?", episode.ID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && existing.DramaID != episode.DramaID {
			return domain.ErrNotFound
		}
	}
	model := newEpisodeModel(*episode)
	err := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"drama_id", "episode_number", "title", "description", "is_draft",
			"vid", "media_space_name", "poster_url", "duration_seconds", "media_status", "updated_at",
		}),
	}).Create(&model).Error
	var mysqlErr *mysqldriver.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return operations.ErrInvalidInput
	}
	if err == nil {
		episode.ID = model.ID
	}
	return err
}

func (s *Store) listEpisodes(ctx context.Context, dramaID string) ([]domain.Episode, error) {
	var models []episodeModel
	if err := catalogQuery(s.data.DB().WithContext(ctx)).Where("drama_id = ? AND is_draft = ?", dramaID, true).
		Order("episode_number ASC, id ASC").Find(&models).Error; err != nil {
		return nil, err
	}
	episodes := make([]domain.Episode, 0, len(models))
	for _, model := range models {
		episode := model.domain()
		episodes = append(episodes, episode)
	}
	return episodes, nil
}

// newDramaModel 转换短剧主表字段并序列化既有线上快照与字符串标识集合，不维护发布版本。
func newDramaModel(drama domain.Drama) (dramaModel, error) {
	publishedJSON, err := json.Marshal(drama.Published)
	if err != nil {
		return dramaModel{}, err
	}
	tagIDs, err := json.Marshal(nonNilStrings(drama.TagIDs))
	if err != nil {
		return dramaModel{}, err
	}
	now := time.Now().UTC()
	return dramaModel{
		Assignee: drama.Assignee, OperationNote: drama.OperationNote, OperationDueAt: drama.OperationDueAt,
		PublishedSnapshot: publishedJSON,
		CoverMID:          drama.CoverMID, CoverSpaceName: drama.CoverSpaceName, CoverFileName: drama.CoverFileName,
		ID: drama.ID, Title: drama.Title, Description: drama.Description, CoverURL: drama.CoverURL, Status: drama.Status,
		Year: drama.Year, Language: drama.Language, DramaType: drama.DramaType, TagIDsJSON: tagIDs,
		PublishStatus: drama.PublishStatus, PlatformAlbumID: drama.PlatformAlbumID,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// domain 还原编辑内容与已有官方线上快照，不注入任何渠道版本。
func (model dramaModel) domain() (domain.Drama, error) {
	var published *domain.Drama
	if len(model.PublishedSnapshot) > 0 {
		if err := json.Unmarshal(model.PublishedSnapshot, &published); err != nil {
			return domain.Drama{}, err
		}
	}
	tagIDs, err := decodeStringIDs(model.TagIDsJSON)
	if err != nil {
		return domain.Drama{}, err
	}
	drama := domain.Drama{
		SubjectID: model.SubjectID, Assignee: model.Assignee, OperationNote: model.OperationNote, OperationDueAt: model.OperationDueAt,
		Published: published,
		CoverMID:  model.CoverMID, CoverSpaceName: model.CoverSpaceName, CoverFileName: model.CoverFileName,
		ID: model.ID, Title: model.Title, Description: model.Description, CoverURL: model.CoverURL, Tags: []string{},
		Status: model.Status, Year: model.Year, Language: model.Language, DramaType: model.DramaType,
		TagIDs: tagIDs, PublishStatus: model.PublishStatus, PlatformAlbumID: model.PlatformAlbumID,
	}
	return drama, nil
}

func newEpisodeModel(episode domain.Episode) episodeModel {
	description := episode.Description
	now := time.Now().UTC()
	return episodeModel{
		IsDraft:        true,
		MediaSpaceName: episode.MediaSpaceName,
		ID:             episode.ID, DramaID: episode.DramaID, Number: episode.Number, Title: episode.Title,
		Description: &description, VID: episode.VID, PlatformEpisodeID: episode.PlatformEpisodeID,
		PosterURL:       episode.PosterURL,
		DurationSeconds: episode.DurationSeconds,
		MediaStatus:     episode.MediaStatus, CreatedAt: now, UpdatedAt: now,
	}
}

func (model episodeModel) domain() domain.Episode {
	description := ""
	if model.Description != nil {
		description = *model.Description
	}
	return domain.Episode{
		MediaSpaceName: model.MediaSpaceName,
		ID:             model.ID, DramaID: model.DramaID, Number: model.Number, Title: model.Title,
		Description: description, VID: model.VID, PlatformEpisodeID: model.PlatformEpisodeID,
		PosterURL:       model.PosterURL,
		DurationSeconds: model.DurationSeconds,
		MediaStatus:     model.MediaStatus,
	}
}

// nonNilStrings 统一把 nil 保存成 JSON 空数组，避免接口返回 null。
func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// decodeStringIDs 只接受字符串数组。TikTok 标识必须以字符串保存；项目尚未上线，
// 因此不保留旧数字标识的兼容解析分支。
func decodeStringIDs(encoded []byte) ([]string, error) {
	if len(encoded) == 0 {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal(encoded, &values); err != nil {
		return nil, err
	}
	return nonNilStrings(values), nil
}

// FindDramaDetail 为详情读取提供剧内容，缓存由组合根的读取装饰器负责。
func (s *Store) FindDramaDetail(ctx context.Context, id string) (*domain.Drama, error) {
	return s.FindDrama(ctx, id)
}

// FindReconciliationDrama 仅供持有全局巡检租约的 Worker 使用，从权威归属恢复主体后读取聚合。
func (s *Store) FindReconciliationDrama(ctx context.Context, id string) (*domain.Drama, error) {
	var row dramaModel
	if err := catalogQuery(s.data.DB().WithContext(ctx)).Select("id", "subject_id").First(&row, "id=?", id).Error; err != nil {
		return nil, err
	}
	return s.FindDrama(application.WithSubject(ctx, row.SubjectID), id)
}
