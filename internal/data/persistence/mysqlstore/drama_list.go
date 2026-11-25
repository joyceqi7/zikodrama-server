package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"encoding/json"
	"strings"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/operations"
)

// ListDramaPage 返回展示字段和统计，批量对比当前内容与渠道快照生成待办标记，不返回分集正文或逐行查询。
func (s *Store) ListDramaPage(ctx context.Context, input operations.DramaListQuery) (operations.DramaListPage, error) {
	result := operations.DramaListPage{Items: []operations.DramaListItem{}}
	// 列表筛选与详情使用相同的中断投影；旧同步排队项只能人工继续，媒体及审核查询保留等待状态。
	readStatus := "(CASE WHEN ((latest_publication.status='queued' AND latest_publication.stage NOT IN ('register_videos','transcode_videos','poll_review','poll_appeal')) OR (latest_publication.status='processing' AND (latest_publication.lease_owner<>'' OR latest_publication.updated_at<=UTC_TIMESTAMP()-INTERVAL 2 MINUTE))) AND (latest_publication.lease_owner='' OR latest_publication.lease_expires_at<=UTC_TIMESTAMP()) THEN 'failed' ELSE latest_publication.status END)"
	query := ownedDramaQuery(s.data.DB().WithContext(ctx)).Model(&dramaModel{}).Joins("LEFT JOIN tiktok_drama_states AS official_state ON official_state.drama_id=dramas.id AND official_state.subject_id=?", application.SubjectIDFromContext(ctx))
	query = query.Joins("LEFT JOIN admin_users AS creator ON creator.id = dramas.created_by").Joins("LEFT JOIN admin_users AS latest_operator ON latest_operator.id = dramas.updated_by")
	query = query.Joins(`LEFT JOIN tiktok_publications AS latest_publication ON latest_publication.id = (
 SELECT publication_history.id FROM tiktok_publications AS publication_history
 WHERE publication_history.drama_id = dramas.id AND publication_history.subject_id = ?
 ORDER BY publication_history.created_at DESC, publication_history.id DESC LIMIT 1
)`, application.SubjectIDFromContext(ctx))
	// 编辑区只有显式 ready 表示已确认就绪，历史渠道状态按草稿筛选，保留原始状态与线上快照。
	if input.ContentStatus == "draft" {
		query = query.Where("dramas.publish_status <> 'ready'")
	} else if input.ContentStatus == "ready" {
		query = query.Where("dramas.publish_status = 'ready'")
	}
	if input.DirectStatus == "unpublished" {
		query = query.Where("NOT EXISTS (SELECT 1 FROM direct_publications dp WHERE dp.drama_id=dramas.id AND dp.subject_id=? AND dp.status IN ('published','offline'))", application.SubjectIDFromContext(ctx))
	} else if input.DirectStatus != "" {
		query = query.Where("EXISTS (SELECT 1 FROM direct_publications dp WHERE dp.drama_id=dramas.id AND dp.subject_id=? AND dp.status=?)", application.SubjectIDFromContext(ctx), input.DirectStatus)
	}
	if input.Publication == "published" {
		query = query.Where("JSON_UNQUOTE(JSON_EXTRACT(official_state.published_snapshot,'$.publishStatus')) = 'published'")
	}
	if input.Publication == "draft" {
		query = query.Where("COALESCE(JSON_UNQUOTE(JSON_EXTRACT(official_state.published_snapshot,'$.publishStatus')),'') <> 'published'")
	}
	switch input.Publication {
	case "needs_action":
		query = query.Where(readStatus+" IN ? OR EXISTS (SELECT 1 FROM drama_distributions dd WHERE dd.drama_id=dramas.id AND EXISTS (SELECT 1 FROM applications da WHERE da.id=dd.application_id AND da.subject_id=?) AND dd.status IN ?)", []string{"synced", "approved", "rejected", "failed"}, application.SubjectIDFromContext(ctx), []string{"failed", "attention_required"})
	case "waiting_platform":
		query = query.Where(readStatus+" IN ?", []string{"queued", "processing", "under_review"})
	case "content_incomplete":
		query = query.Where("dramas.cover_url='' OR NOT EXISTS (SELECT 1 FROM episodes e WHERE e.drama_id=dramas.id AND e.is_draft=1) OR EXISTS (SELECT 1 FROM episodes e WHERE e.drama_id=dramas.id AND e.is_draft=1 AND (e.media_status<>'ready' OR e.vid=''))")
	case "manual_attention":
		query = query.Where(readStatus+" IN ? OR EXISTS (SELECT 1 FROM drama_distributions dd WHERE dd.drama_id=dramas.id AND EXISTS (SELECT 1 FROM applications da WHERE da.id=dd.application_id AND da.subject_id=?) AND dd.status IN ?)", []string{"rejected", "failed"}, application.SubjectIDFromContext(ctx), []string{"failed", "attention_required"})
	case "syncing":
		query = query.Where(readStatus+" IN ? AND latest_publication.stage NOT IN ?", []string{"queued", "processing"}, []string{"submit_review", "poll_review", "appeal_review", "poll_appeal", "withdraw_review", "set_online_version", "list_album", "delist_album"})
	case "await_review":
		query = query.Where(readStatus + "='synced'")
	case "under_review":
		query = query.Where(readStatus + "='under_review'")
	case "rejected":
		query = query.Where(readStatus+" IN ?", []string{"rejected", "failed"})
	case "await_publish":
		query = query.Where(readStatus + "='approved'")
	case "distribution_failed":
		query = query.Where("EXISTS (SELECT 1 FROM drama_distributions dd WHERE dd.drama_id=dramas.id AND EXISTS (SELECT 1 FROM applications da WHERE da.id=dd.application_id AND da.subject_id=?) AND dd.status IN ?)", application.SubjectIDFromContext(ctx), []string{"failed", "attention_required"})
	}
	if input.Query != "" {
		predicate := "LOCATE(?,LOWER(dramas.title)) > 0"
		args := []any{strings.ToLower(input.Query)}
		for _, tag := range domain.TikTokTagCatalog() {
			if strings.Contains(strings.ToLower(tag.OfficialName), strings.ToLower(input.Query)) {
				predicate += " OR JSON_CONTAINS(dramas.tag_ids_json,JSON_QUOTE(?))"
				args = append(args, tag.ID)
			}
		}
		query = query.Where("("+predicate+")", args...)
	}
	if err := query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	type row struct {
		CreatedByID, CreatedByName, UpdatedByID, UpdatedByName                                            string
		ID, Title, CoverURL, Status, PublishStatus                                                        string
		Assignee                                                                                          string
		OperationDueAt                                                                                    *time.Time
		PublicationID, PublicationPriority, PublicationStatus, PublicationStage                           string
		PublicationErrorCode                                                                              string
		PublicationLeaseOwner                                                                             string
		PublicationLeaseExpiresAt, PublicationUpdatedAt                                                   time.Time
		PublicationExternalWriteStarted                                                                   bool
		DeleteBlocked                                                                                     bool
		PublicationContentJSON                                                                            []byte
		Year, OnlineVersion, EpisodeCount, ReadyEpisodeCount, PublicationReviewStatus, PublicationVersion int
		TagIDsJSON                                                                                        []byte
		AuthorizationJSON                                                                                 []byte
	}
	rows := []row{}
	fields := `COALESCE(CAST(dramas.created_by AS CHAR),'') AS created_by_id,
 COALESCE(NULLIF(creator.nickname,''),creator.username,'') AS created_by_name,
 COALESCE(CAST(dramas.updated_by AS CHAR),'') AS updated_by_id,
 COALESCE(NULLIF(latest_operator.nickname,''),latest_operator.username,'') AS updated_by_name,
 EXISTS(SELECT 1 FROM direct_publications dp WHERE dp.drama_id=dramas.id AND dp.current_version_id IS NOT NULL) OR EXISTS(SELECT 1 FROM tiktok_drama_states os WHERE os.drama_id=dramas.id AND os.published_snapshot IS NOT NULL AND JSON_TYPE(os.published_snapshot)<>'NULL') OR EXISTS(SELECT 1 FROM tiktok_publications ap WHERE ap.drama_id=dramas.id AND ap.status IN ('queued','processing','under_review','approved','synced') AND ap.id=(SELECT newest.id FROM tiktok_publications newest WHERE newest.drama_id=ap.drama_id AND newest.subject_id=ap.subject_id ORDER BY newest.created_at DESC,newest.id DESC LIMIT 1)) AS delete_blocked,
 dramas.id,dramas.title,dramas.assignee,dramas.operation_due_at,dramas.cover_url,dramas.status,dramas.year,dramas.publish_status,dramas.tag_ids_json,
 COALESCE(JSON_EXTRACT(official_state.published_snapshot,'$.platformVersion'),0) AS online_version,
 JSON_OBJECT('publishStatus', COALESCE(JSON_UNQUOTE(JSON_EXTRACT(official_state.published_snapshot,'$.publishStatus')),''),
 'ownerApplicationId', COALESCE(JSON_UNQUOTE(JSON_EXTRACT(official_state.published_snapshot,'$.ownerApplicationId')),''),
 'authorizedApplicationIds', COALESCE(JSON_EXTRACT(official_state.published_snapshot,'$.authorizedApplicationIds'),JSON_ARRAY())) AS authorization_json,
 (SELECT COUNT(*) FROM episodes WHERE episodes.drama_id=dramas.id AND is_draft=1) AS episode_count,
 (SELECT COUNT(*) FROM episodes WHERE episodes.drama_id=dramas.id AND is_draft=1 AND media_status='ready' AND vid<>'') AS ready_episode_count,
 COALESCE(latest_publication.id,'') AS publication_id,
 COALESCE(latest_publication.priority,'') AS publication_priority,
 COALESCE(latest_publication.status,'') AS publication_status,
 COALESCE(latest_publication.stage,'') AS publication_stage,
 COALESCE(latest_publication.review_status,0) AS publication_review_status,
 COALESCE(latest_publication.platform_version,0) AS publication_version,
 COALESCE(latest_publication.error_code,'') AS publication_error_code,
 COALESCE(latest_publication.lease_owner,'') AS publication_lease_owner,
 latest_publication.lease_expires_at AS publication_lease_expires_at,
 latest_publication.updated_at AS publication_updated_at,
 COALESCE(JSON_EXTRACT(latest_publication.workflow_json,'$.externalWriteStarted'),false) AS publication_external_write_started,
 JSON_EXTRACT(latest_publication.workflow_json,'$.content') AS publication_content_json`
	if err := query.Select(fields).Order("dramas.created_at DESC,dramas.id ASC").Offset((input.Page - 1) * input.PageSize).Limit(input.PageSize).Scan(&rows).Error; err != nil {
		return result, err
	}
	for _, r := range rows {
		var ids []string
		if err := json.Unmarshal(r.TagIDsJSON, &ids); err != nil {
			return result, err
		}
		tags := []string{}
		for _, id := range ids {
			if tag, ok := domain.FindTikTokTag(id); ok {
				tags = append(tags, tag.OfficialName)
			}
		}
		item := operations.DramaListItem{DeleteBlocked: r.DeleteBlocked, CreatedByID: r.CreatedByID, CreatedByName: r.CreatedByName, UpdatedByID: r.UpdatedByID, UpdatedByName: r.UpdatedByName, ID: r.ID, Title: r.Title, Assignee: r.Assignee, OperationDueAt: r.OperationDueAt, CoverURL: r.CoverURL, Status: r.Status, PublishStatus: r.PublishStatus, Year: r.Year, OnlineVersion: r.OnlineVersion, EpisodeCount: r.EpisodeCount, ReadyEpisodeCount: r.ReadyEpisodeCount, Tags: tags}
		// 只解析授权元数据，列表查询不加载线上分集或为每一行发起平台请求。
		var published domain.Drama
		if err := json.Unmarshal(r.AuthorizationJSON, &published); err != nil {
			return result, err
		}
		item.AuthorizedApplicationCount = published.ConfirmedAuthorizationCount()
		item.OwnerApplicationID = published.OwnerApplicationID
		if r.PublicationID != "" {
			p := operations.PublicationReadState(operations.TikTokPublication{Status: r.PublicationStatus, Stage: r.PublicationStage, ErrorCode: r.PublicationErrorCode, LeaseOwner: r.PublicationLeaseOwner, LeaseExpiresAt: r.PublicationLeaseExpiresAt, UpdatedAt: r.PublicationUpdatedAt, ExternalWriteStarted: r.PublicationExternalWriteStarted}, time.Now().UTC())
			item.Publication = &operations.DramaListPublication{PlatformVersion: r.PublicationVersion, ID: r.PublicationID, Priority: r.PublicationPriority, Status: p.Status, Stage: p.Stage, ReviewStatus: r.PublicationReviewStatus, ErrorCode: p.ErrorCode}
		}
		result.Items = append(result.Items, item)
	}
	ids := []string{}
	for _, item := range result.Items {
		ids = append(ids, item.ID)
	}
	var currentRows []dramaModel
	if err := s.data.DB().WithContext(ctx).Where("id IN ?", ids).Find(&currentRows).Error; err != nil {
		return result, err
	}
	current := make([]domain.Drama, 0, len(currentRows))
	for _, row := range currentRows {
		d, err := row.domain()
		if err != nil {
			return result, err
		}
		current = append(current, d)
	}
	if err := s.attachEpisodes(ctx, current); err != nil {
		return result, err
	}
	digests := map[string]string{}
	for _, d := range current {
		digests[d.ID] = domain.OfficialContentDigest(d)
	}
	direct, err := s.directSummaries(ctx, ids)
	if err != nil {
		return result, err
	}
	directDigests := map[string]string{}
	for _, d := range current {
		directDigests[d.ID] = directplay.Digest(d)
	}
	publicationDigests := map[string]string{}
	for _, r := range rows {
		if len(r.PublicationContentJSON) > 0 && string(r.PublicationContentJSON) != "null" {
			var frozen domain.Drama
			if err := json.Unmarshal(r.PublicationContentJSON, &frozen); err != nil {
				return result, err
			}
			publicationDigests[r.ID] = domain.OfficialContentDigest(frozen)
		}
	}
	for i := range result.Items {
		item := &result.Items[i]
		summary := direct[item.ID]
		summary.PendingChanges = summary.VersionID != "" && directDigests[item.ID] != summary.SourceDigest
		item.DirectPublication = &summary
		if item.Publication != nil {
			item.Publication.CurrentContentChanged = digests[item.ID] != publicationDigests[item.ID]
		}
	}
	if err := s.attachOfficialReleases(ctx, result.Items); err != nil {
		return result, err
	}
	return result, nil
}

// DramaOverview 从权威剧和分集表聚合计数，不读取视频或内容正文。
func (s *Store) DramaOverview(ctx context.Context) (*operations.Overview, error) {
	var dramas, published, episodes, pending int64
	db := s.data.DB().WithContext(ctx)
	if err := ownedDramaQuery(db).Model(&dramaModel{}).Count(&dramas).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&tiktokDramaStateModel{}).Where("subject_id=? AND JSON_UNQUOTE(JSON_EXTRACT(published_snapshot,'$.publishStatus'))='published'", application.SubjectIDFromContext(ctx)).Count(&published).Error; err != nil {
		return nil, err
	}
	if err := ownedEpisodeQuery(db).Model(&episodeModel{}).Where("is_draft=1").Count(&episodes).Error; err != nil {
		return nil, err
	}
	if err := ownedEpisodeQuery(db).Model(&episodeModel{}).Where("is_draft=1 AND media_status<>'ready'").Count(&pending).Error; err != nil {
		return nil, err
	}
	return &operations.Overview{DramaCount: int(dramas), PublishedCount: int(published), EpisodeCount: int(episodes), PendingMediaCount: int(pending)}, nil
}
