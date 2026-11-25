package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/homepage"
)

// directSummaries 只查询线上版本元数据和最近任务，不载入历史或播放凭据。
func (s *Store) directSummaries(ctx context.Context, ids []string) (map[string]directplay.Summary, error) {
	result := map[string]directplay.Summary{}
	for _, id := range ids {
		result[id] = directplay.Summary{Status: "unpublished"}
	}
	if len(ids) == 0 {
		return result, nil
	}
	db := s.data.DB().WithContext(ctx)
	var pubs []directPublicationModel
	if err := subjectContentQuery(db).Where("drama_id IN ?", ids).Find(&pubs).Error; err != nil {
		return nil, err
	}
	var versions []directPublicationVersionModel
	if err := subjectContentQuery(db).Select("id,drama_id,version_number,source_digest,created_at,created_by").Where("id IN (SELECT current_version_id FROM direct_publications WHERE subject_id=? AND drama_id IN ?)", application.SubjectIDFromContext(ctx), ids).Find(&versions).Error; err != nil {
		return nil, err
	}
	byVersion := map[string]directPublicationVersionModel{}
	for _, v := range versions {
		byVersion[v.ID] = v
	}
	var appCount int64
	if err := db.Table("applications a").Joins("JOIN application_playback_profiles f ON f.application_id=a.id AND f.provider='byteplus_web'").Where("a.status='active' AND a.subject_id=?", application.SubjectIDFromContext(ctx)).Count(&appCount).Error; err != nil {
		return nil, err
	}
	var operations []directPublicationOperationModel
	if err := subjectContentQuery(db).Select("id,drama_id,command_json,status,result_version_id,created_at").Where("id IN (SELECT MAX(id) FROM direct_publication_operations WHERE subject_id=? AND drama_id IN ? GROUP BY drama_id)", application.SubjectIDFromContext(ctx), ids).Find(&operations).Error; err != nil {
		return nil, err
	}
	last := map[string]*directplay.Operation{}
	for _, row := range operations {
		o, err := operationDomain(row)
		if err != nil {
			return nil, err
		}
		last[row.DramaID] = &o
	}
	for _, p := range pubs {
		v := directPublicationVersionModel{}
		if p.CurrentVersionID != nil {
			v = byVersion[*p.CurrentVersionID]
		}
		summary := directplay.Summary{Revision: p.Revision, SourceDigest: v.SourceDigest, Status: p.Status, Version: v.VersionNumber, VersionID: v.ID, EnabledApplications: int(appCount), LastOperation: last[p.DramaID], PublishedBy: v.CreatedBy}
		if v.ID != "" {
			at := v.CreatedAt
			summary.PublishedAt = &at
		}
		result[p.DramaID] = summary
	}
	return result, nil
}

// HomepageMetadata 批量读取独立当前版本与有效应用数，不读取分集快照。
func (s *Store) HomepageMetadata(ctx context.Context, ids []string) (map[string]homepage.PublicationMetadata, error) {
	summaries, err := s.directSummaries(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := map[string]homepage.PublicationMetadata{}
	for id, p := range summaries {
		if p.Status == "published" {
			result[id] = homepage.PublicationMetadata{Version: p.Version, Applications: p.EnabledApplications}
		}
	}
	return result, nil
}
