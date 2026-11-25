package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/directplay"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// freezeDirectPublicationVersion 由直连上架事务冻结就绪内容；版本号只查询本主体直连历史，同内容重复发布复用最近直连版本。
// 调用方须持有剧目行锁；版本、上架指针及幂等记录一起提交，失败时整体回滚。
func freezeDirectPublicationVersion(tx *gorm.DB, row dramaModel, actor string) (directplay.Version, error) {
	snapshot, err := readyPublicationSnapshot(tx, row)
	if err != nil {
		return directplay.Version{}, err
	}
	digest := directplay.Digest(snapshot)
	var current directPublicationVersionModel
	err = subjectContentQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("drama_id = ?", row.ID).Order("version_number DESC").Take(&current).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return directplay.Version{}, err
	}
	if err == nil && current.SourceDigest == digest {
		return directVersionDomain(current)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return directplay.Version{}, err
	}
	version := directPublicationVersionModel{SubjectID: application.SubjectIDFromContext(tx.Statement.Context), DramaID: row.ID, VersionNumber: current.VersionNumber + 1, SourceDigest: digest, SnapshotJSON: raw, CreatedBy: actor, CreatedAt: time.Now().UTC()}
	if err := tx.Create(&version).Error; err != nil {
		return directplay.Version{}, err
	}
	return directVersionDomain(version)
}

// listDirectPublicationVersions 仅返回该剧的直连发布历史，短剧管理及 TK 不调用此查询。
func (s *Store) listDirectPublicationVersions(ctx context.Context, id string) ([]directplay.Version, error) {
	var rows []directPublicationVersionModel
	if err := subjectContentQuery(s.data.DB().WithContext(ctx)).Where("drama_id = ?", id).Order("version_number DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	versions := []directplay.Version{}
	for _, row := range rows {
		v, err := directVersionDomain(row)
		if err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, nil
}

// directVersionDomain 返回直连发布元数据与快照，不把版本字段注入剧和集的编辑对象。
func directVersionDomain(row directPublicationVersionModel) (directplay.Version, error) {
	version := directplay.Version{ID: row.ID, DramaID: row.DramaID, Number: row.VersionNumber, SourceDigest: row.SourceDigest, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt}
	if err := json.Unmarshal(row.SnapshotJSON, &version.Snapshot); err != nil {
		return version, err
	}
	return version, nil
}
