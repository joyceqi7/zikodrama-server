package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strconv"
	"time"
)

// confirmedReleasePredicate 兼容旧下线调用覆盖记录状态的情形，只认曾确认上架的原版本。
// 新版本同步失败且没有 OnlineAt 不具备已选版本资格。
func confirmedReleasePredicate(alias string) string {
	return "(" + alias + ".status IN ('online','offline') OR (" + alias + ".status IN ('unpublishing','failed') AND JSON_UNQUOTE(JSON_EXTRACT(" + alias + ".record_json,'$.onlineAt')) IS NOT NULL AND JSON_UNQUOTE(JSON_EXTRACT(" + alias + ".record_json,'$.onlineAt')) <> 'null'))"
}

// selectedRelease 读取独立上架事实，再按原记录取得私有冻结快照；审核进度不能覆盖选定事实。
func selectedRelease(tx *gorm.DB, dramaID string) (*operations.TikTokPublication, error) {
	var record publicationRecordModel
	err := subjectPublicationQuery(tx).Table("tiktok_release_records").Where("drama_id=?", dramaID).Where(confirmedReleasePredicate("tiktok_release_records")).Order("updated_at DESC,id DESC").Take(&record).Error
	if err == nil {
		var row tiktokPublicationModel
		if err := tx.Where("id=? AND drama_id=?", record.ID, dramaID).Take(&row).Error; err != nil {
			return nil, err
		}
		p, err := row.domain()
		if err != nil {
			return nil, err
		}
		var fact operations.TikTokPublication
		if err := json.Unmarshal(record.RecordJSON, &fact); err != nil {
			return nil, err
		}
		fact.SubjectID = p.SubjectID
		fact.ContentSnapshot = p.ContentSnapshot
		fact.OwnerScope = p.OwnerScope
		fact.PublishRequested = true
		return &fact, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return nil, nil
}

// FindTikTokReleaseState 仅从已确认的上架记录读取版本，下架仍保留选择。
func (s *Store) FindTikTokReleaseState(ctx context.Context, dramaID string) (*operations.TikTokReleaseState, error) {
	p, err := selectedRelease(s.data.DB().WithContext(ctx), dramaID)
	if err != nil || p == nil {
		return nil, err
	}
	drama, err := s.FindDrama(ctx, dramaID)
	if err != nil {
		return nil, err
	}
	status := operations.PublicationStatusOffline
	if drama.Published != nil {
		status = operations.PublicationStatusOnline
	}
	return &operations.TikTokReleaseState{SelectedPublicationID: p.ID, Version: p.PlatformVersion, Status: status}, nil
}

// ExecuteTikTokRelease 在短剧锁内同步调用平台，与推荐写入和其他上下架串行，确认后提交消费者投影。
func (s *Store) ExecuteTikTokRelease(ctx context.Context, dramaID string, input operations.TikTokReleaseInput, call operations.TikTokReleaseCall) (*operations.TikTokPublication, error) {
	var result operations.TikTokPublication
	var platformErr error
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row dramaModel
		if err := ownedDramaQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", dramaID).Take(&row).Error; err != nil {
			return err
		}
		if err := attachSubjectState(tx, &row); err != nil {
			return err
		}
		drama, err := row.domain()
		if err != nil {
			return err
		}
		selected, err := selectedRelease(tx, dramaID)
		if err != nil {
			return err
		}
		var p operations.TikTokPublication
		if input.Action == "publish" {
			p, err = lockedLatestPublication(tx, dramaID)
			if err != nil {
				return err
			}
			if p.ID != input.SelectedPublicationID {
				return operations.ErrConflict
			}
			if selected != nil && selected.ID == p.ID && drama.Published != nil {
				result = *selected
				return nil
			}
		} else {
			if selected == nil || selected.ID != input.SelectedPublicationID {
				return operations.ErrConflict
			}
			p = *selected
			if drama.Published == nil {
				p.Status = operations.PublicationStatusOffline
			} else {
				p.Status = operations.PublicationStatusOnline
			}
			if input.Action == "list" && drama.Published != nil || input.Action == "delist" && drama.Published == nil {
				result = p
				return nil
			}
		}
		if err := operations.ValidateSelectedRelease(p, drama, input.Action); err != nil {
			return err
		}
		if input.Action == "delist" {
			if err := ensureDramaNotRecommended(tx, dramaID, false); err != nil {
				return err
			}
		}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		confirmed, published, err := call(callCtx, p, drama)
		if err != nil && (input.Action != "publish" || confirmed.Stage != operations.PublicationStageDone || confirmed.Status != operations.PublicationStatusOnline && confirmed.Status != operations.PublicationStatusOffline) {
			return err
		}
		platformErr = err
		result = confirmed
		if published != nil {
			var ids []string
			if err := tx.Table("drama_distributions AS dd").Joins("JOIN applications a ON a.id=dd.application_id AND a.subject_id=?", application.SubjectIDFromContext(ctx)).Where("dd.drama_id=? AND dd.desired=true AND dd.status='authorized'", dramaID).Pluck("application_id", &ids).Error; err != nil {
				return err
			}
			published.AuthorizedApplicationIDs = ids
			published.OwnerApplicationID = ""
			var owner applicationModel
			err := tx.Where("client_key=? AND subject_id=?", p.OwnerScope.ClientKey, p.SubjectID).Take(&owner).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil {
				published.OwnerApplicationID = strconv.FormatUint(owner.ID, 10)
			}
		}
		raw, err := json.Marshal(published)
		if err != nil {
			return err
		}
		if err := saveSubjectState(tx, dramaID, p.PlatformAlbumID, published); err != nil {
			return err
		}
		if err := syncCategoryRankings(tx, dramaID, row.PublishedSnapshot, raw); err != nil {
			return err
		}
		if err := syncPublicationRecords(tx, confirmed); err != nil {
			return err
		}
		// 仅首次明确选择新版本时更新它自身的流程；独立上下架不写审核记录。
		if input.Action == "publish" {
			model, err := newTikTokPublicationModel(confirmed)
			if err != nil {
				return err
			}
			model.Revision = p.Revision + 1
			if err := gormAffectedOrNotFound(tx.Model(&tiktokPublicationModel{}).Where("id=? AND revision=?", p.ID, p.Revision).Select("*").Omit("created_at").Updates(&model)); err != nil {
				return err
			}
			result.Revision = model.Revision
		}
		return touchDramaOperator(tx, dramaID)
	})
	if err != nil {
		return nil, err
	}
	return &result, platformErr
}

// attachOfficialReleases 批量读取已选上架记录和私有冻结版本，响应仅含安全摘要。
func (s *Store) attachOfficialReleases(ctx context.Context, items []operations.DramaListItem) error {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	var rows []publicationRecordModel
	if err := s.data.DB().WithContext(ctx).Table("tiktok_release_records AS r").Select("r.*").Where("r.subject_id=? AND r.drama_id IN ? AND r.id=(SELECT r2.id FROM tiktok_release_records r2 WHERE r2.drama_id=r.drama_id AND r2.subject_id=r.subject_id AND "+confirmedReleasePredicate("r2")+" ORDER BY r2.updated_at DESC,r2.id DESC LIMIT 1)", application.SubjectIDFromContext(ctx), ids).Find(&rows).Error; err != nil {
		return err
	}
	byID := map[string]operations.TikTokReleaseState{}
	for _, row := range rows {
		var p operations.TikTokPublication
		if err := json.Unmarshal(row.RecordJSON, &p); err != nil {
			return err
		}
		byID[row.DramaID] = operations.TikTokReleaseState{SelectedPublicationID: row.ID, Version: p.PlatformVersion, Status: row.Status}
	}
	// 历史库可能尚未补齐独立上架记录；按页批量回退，避免线上 V1 在新 V2 审核时丢失上下架按钮。
	missing := []string{}
	for _, id := range ids {
		if _, ok := byID[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		var legacy []tiktokPublicationModel
		// 缺少上架记录不能扩大主体边界；外层和最近历史的选择必须属于同一可信主体。
		if err := s.data.DB().WithContext(ctx).Table("tiktok_publications AS p").Select("p.*").Where("p.subject_id=? AND p.drama_id IN ? AND p.id=(SELECT p2.id FROM tiktok_publications p2 WHERE p2.subject_id=p.subject_id AND p2.drama_id=p.drama_id AND p2.online_at IS NOT NULL ORDER BY p2.updated_at DESC,p2.id DESC LIMIT 1)", application.SubjectIDFromContext(ctx), missing).Find(&legacy).Error; err != nil {
			return err
		}
		for _, row := range legacy {
			byID[row.DramaID] = operations.TikTokReleaseState{SelectedPublicationID: row.ID, Version: row.PlatformVersion}
		}
	}
	for i := range items {
		state, ok := byID[items[i].ID]
		if !ok {
			continue
		}
		if items[i].OnlineVersion == 0 {
			state.Status = operations.PublicationStatusOffline
		} else {
			state.Status = operations.PublicationStatusOnline
		}
		items[i].OfficialRelease = &state
	}
	return nil
}
