package mysqlstore

import (
	"context"
	"crypto/sha256"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/operations"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// DirectRepository 维护当前主体的直连版本和上下架状态，共享内容草稿。
type DirectRepository struct{ store *Store }

// NewDirectRepository 创建直连发布仓储。
func NewDirectRepository(store *Store) *DirectRepository { return &DirectRepository{store} }
func publicationDomain(m directPublicationModel) directplay.Publication {
	p := directplay.Publication{DramaID: m.DramaID, Status: m.Status, Revision: m.Revision}
	if m.CurrentVersionID != nil {
		p.CurrentVersionID = *m.CurrentVersionID
	}
	return p
}
func operationDomain(m directPublicationOperationModel) (directplay.Operation, error) {
	o := directplay.Operation{ID: m.ID, DramaID: m.DramaID, Status: m.Status, CreatedAt: m.CreatedAt}
	if m.ResultVersionID != nil {
		o.ResultVersionID = *m.ResultVersionID
	}
	err := json.Unmarshal(m.CommandJSON, &o.Command)
	return o, err
}
func frozenDraft(tx *gorm.DB, id string) (consumer.Drama, error) {
	var row dramaModel
	if err := ownedDramaQuery(tx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return consumer.Drama{}, consumer.ErrNotFound
		}
		return consumer.Drama{}, err
	}
	d, err := row.domain()
	if err != nil {
		return d, err
	}
	d.Published = nil
	var episodes []episodeModel
	if err := tx.Where("drama_id = ? AND is_draft = ?", id, true).Order("episode_number,id").Find(&episodes).Error; err != nil {
		return d, err
	}
	d.Episodes = []consumer.Episode{}
	for _, e := range episodes {
		d.Episodes = append(d.Episodes, e.domain())
	}
	return d, nil
}

// Workbench 提供编辑内容、直连发布版本和直连当前上架指针，不再包含应用分发关系。
func (r *DirectRepository) Workbench(ctx context.Context, id string) (directplay.Workbench, error) {
	w := directplay.Workbench{Versions: []directplay.Version{}}
	d, err := r.store.FindDrama(ctx, id)
	if err != nil {
		return w, err
	}
	w.Draft = *d
	w.SourceDigest = directplay.Digest(*d)
	w.Publication = directplay.Publication{DramaID: id, Status: "unpublished"}
	var p directPublicationModel
	if err := subjectContentQuery(r.store.data.DB().WithContext(ctx)).First(&p, "drama_id = ?", id).Error; err == nil {
		w.Publication = publicationDomain(p)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return w, err
	}
	w.Versions, err = r.store.listDirectPublicationVersions(ctx, id)
	if err != nil {
		return w, err
	}
	summaries, err := r.store.directSummaries(ctx, []string{id})
	if err != nil {
		return w, err
	}
	w.Summary = summaries[id]
	w.Summary.PendingChanges = w.Summary.VersionID != "" && w.SourceDigest != w.Summary.SourceDigest
	return w, nil
}

// List 返回共享草稿及当前主体的直连摘要，历史版本全文仅在详情查询。
func (r *DirectRepository) List(ctx context.Context) ([]directplay.Workbench, error) {
	drafts, err := r.store.ListAllDramas(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(drafts))
	for _, d := range drafts {
		ids = append(ids, d.ID)
	}
	summaries, err := r.store.directSummaries(ctx, ids)
	if err != nil {
		return nil, err
	}
	var pubs []directPublicationModel
	if err := subjectContentQuery(r.store.data.DB().WithContext(ctx)).Where("drama_id IN ?", ids).Find(&pubs).Error; err != nil {
		return nil, err
	}
	byID := map[string]directplay.Publication{}
	for _, p := range pubs {
		byID[p.DramaID] = publicationDomain(p)
	}
	rows := []directplay.Workbench{}
	for _, d := range drafts {
		p, ok := byID[d.ID]
		if !ok {
			p = directplay.Publication{DramaID: d.ID, Status: "unpublished"}
		}
		summary := summaries[d.ID]
		summary.PendingChanges = summary.VersionID != "" && directplay.Digest(d) != summary.SourceDigest
		rows = append(rows, directplay.Workbench{Draft: d, Publication: p, Summary: summary, Versions: []directplay.Version{}})
	}
	return rows, nil
}
func lockDirectPublication(tx *gorm.DB, id string) (directPublicationModel, error) {
	p := directPublicationModel{SubjectID: application.SubjectIDFromContext(tx.Statement.Context), DramaID: id, Status: "unpublished"}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&p).Error; err != nil {
		return p, err
	}
	err := subjectContentQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, "drama_id = ?", id).Error
	return p, err
}

// Accept 同步且幂等地切换已准备内容版本或下架；发布前在同一事务生成或复用版本，不选择应用、不进入后台队列。
func (r *DirectRepository) Accept(ctx context.Context, id string, c directplay.Command, actor string) (directplay.Operation, error) {
	var result directplay.Operation
	if application.SubjectIDFromContext(ctx) == "" {
		return result, directplay.ErrInvalid
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	err = r.store.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var drama dramaModel
		if err := ownedDramaQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&drama, "id = ?", id).Error; err != nil {
			return err
		}
		p, err := lockDirectPublication(tx, id)
		if err != nil {
			return err
		}
		var previous directPublicationOperationModel
		err = subjectContentQuery(tx).Where("drama_id = ? AND request_key = ?", id, c.RequestID).First(&previous).Error
		if err == nil {
			if previous.InputDigest != digest {
				return directplay.ErrConflict
			}
			result, err = operationDomain(previous)
			return err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if p.Revision != c.ExpectedRevision {
			return directplay.ErrConflict
		}
		before := []byte("null")
		if p.Status == "published" && p.CurrentVersionID != nil {
			var v directPublicationVersionModel
			if err := subjectContentQuery(tx).First(&v, "id = ? AND drama_id = ?", *p.CurrentVersionID, id).Error; err != nil {
				return err
			}
			before = v.SnapshotJSON
		}
		versionID, status, after := p.CurrentVersionID, "offline", []byte("null")
		if c.Kind == "publish" {
			v, err := freezeDirectPublicationVersion(tx, drama, actor)
			if errors.Is(err, operations.ErrPublicationNotReady) {
				return errors.Join(directplay.ErrInvalid, err)
			}
			if err != nil {
				return err
			}
			rawSnapshot, err := json.Marshal(v.Snapshot)
			if err != nil {
				return err
			}
			versionID, status, after = &v.ID, "published", rawSnapshot
		} else if c.Kind == "relist" && p.CurrentVersionID != nil {
			// 恢复已发布版本不读取新草稿，允许草稿视频尚未上传完成。
			var v directPublicationVersionModel
			if err := subjectContentQuery(tx).First(&v, "id = ? AND drama_id = ?", *p.CurrentVersionID, id).Error; err != nil {
				return err
			}
			status, after = "published", v.SnapshotJSON
		} else if c.Kind != "offline" || p.CurrentVersionID == nil {
			return directplay.ErrInvalid
		} else if err := ensureDramaNotRecommended(tx, id, true); err != nil {
			// 在发布事务内检查直连配置，拒绝时状态、版本、修订及幂等记录均不改变。
			return err
		}
		if err := syncDirectCategoryRankings(tx, id, before, after); err != nil {
			return err
		}
		if err := tx.Model(&p).Updates(map[string]any{"status": status, "current_version_id": versionID, "revision": p.Revision + 1, "updated_by": actor, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		row := directPublicationOperationModel{SubjectID: application.SubjectIDFromContext(ctx), DramaID: id, RequestKey: c.RequestID, InputDigest: digest, CommandJSON: raw, Status: "succeeded", ResultVersionID: versionID, CreatedBy: actor}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		result, err = operationDomain(row)
		return err
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = consumer.ErrNotFound
	}
	return result, err
}

// Operation 返回已完成操作的幂等结果。
func (r *DirectRepository) Operation(ctx context.Context, id string) (directplay.Operation, error) {
	var row directPublicationOperationModel
	if err := subjectContentQuery(r.store.data.DB().WithContext(ctx)).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return directplay.Operation{}, consumer.ErrNotFound
		}
		return directplay.Operation{}, err
	}
	return operationDomain(row)
}

// attachDirectSnapshot 只附加当前应用可见的直连线上版本，未找到时保留空线上内容。
func (s *Store) attachDirectSnapshot(ctx context.Context, d *consumer.Drama) error {
	d.Published = nil
	var row directPublicationVersionModel
	q := s.directCatalogQuery(ctx).Where("p.drama_id = ?", d.ID).Select("v.id, v.drama_id, v.version_number, v.snapshot_json")
	if err := q.Take(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	var published consumer.Drama
	if err := json.Unmarshal(row.SnapshotJSON, &published); err != nil {
		return err
	}
	published.PlaybackProvider = "byteplus_web"
	published.PublishStatus = "published"
	published.AuthorizedApplicationIDs = []string{application.IDFromContext(ctx)}
	d.Published = &published
	tags, err := s.ListTikTokTags(ctx)
	if err != nil {
		return err
	}
	consumer.AttachTikTokTagCatalog(d.Published, tags)
	return nil
}

// directCatalogQuery 直接关联当前上架版本；应用主键及唯一播放配置保证门禁关联不重复目录行。
func (s *Store) directCatalogQuery(ctx context.Context) *gorm.DB {
	query := s.data.DB().WithContext(ctx).Table("direct_publications AS p").
		Joins("JOIN direct_publication_versions AS v ON v.id=p.current_version_id AND v.subject_id=p.subject_id AND v.drama_id=p.drama_id").
		Joins("JOIN dramas AS owned_drama ON owned_drama.id=p.drama_id AND owned_drama.subject_id=p.subject_id").Where("p.status = ? AND p.subject_id = ?", "published", application.SubjectIDFromContext(ctx))
	if !isDirectManagement(ctx) {
		// 应用、主体和当前播放模式必须一致；同主体应用共用目录，不依赖逐剧应用绑定。
		query = query.Joins("JOIN applications AS a ON a.id=? AND a.status=? AND a.subject_id=p.subject_id", application.IDFromContext(ctx), "active").
			Joins("JOIN application_playback_profiles AS f ON f.application_id=a.id AND f.provider=?", "byteplus_web")
	}
	return query
}

func isDirectManagement(ctx context.Context) bool { return homepage.IsManagementContext(ctx) }
