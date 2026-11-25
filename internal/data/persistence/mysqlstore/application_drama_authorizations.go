package mysqlstore

import (
	"context"
	"strings"

	"drama-server/internal/biz/operations"
)

// ListApplicationDramaAuthorizations 从 MySQL 一次分页读取目标应用的线上短剧和授权状态。
func (s *Store) ListApplicationDramaAuthorizations(ctx context.Context, appID string, input operations.ApplicationDramaAuthorizationQuery) (operations.ApplicationDramaAuthorizationPage, error) {
	result := operations.ApplicationDramaAuthorizationPage{Items: []operations.ApplicationDramaAuthorizationItem{}}
	owner := "COALESCE(JSON_UNQUOTE(JSON_EXTRACT(official_state.published_snapshot,'$.ownerApplicationId')),'')"
	// 只把有效授权意图放在右栏；撤权后的旧 authorized 结果不能显示为已授权。
	activeIntent := "COALESCE(dd.enabled,0)=1 AND COALESCE(dd.desired,0)=1 AND COALESCE(dd.status,'') IN ('pending','authorized','failed','attention_required')"
	tracked := "(" + owner + " = ? OR (" + activeIntent + "))"
	query := s.data.DB().WithContext(ctx).Table("dramas AS d").
		Joins("JOIN applications AS target ON target.id=? AND target.subject_id=d.subject_id", appID).
		Joins("JOIN tiktok_drama_states AS official_state ON official_state.drama_id=d.id AND official_state.subject_id=target.subject_id").
		Joins("LEFT JOIN drama_distributions AS dd ON dd.drama_id=d.id AND dd.application_id=?", appID).
		Where("JSON_UNQUOTE(JSON_EXTRACT(official_state.published_snapshot,'$.publishStatus')) = 'published' AND official_state.platform_album_id <> ''")
	if input.Query != "" {
		query = query.Where("LOCATE(?,LOWER(d.title)) > 0", strings.ToLower(input.Query))
	}
	if input.View == "tracked" {
		query = query.Where(tracked, appID)
	} else {
		query = query.Where("NOT "+tracked, appID)
	}
	if err := query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	if err := query.Select("d.id,d.title,CASE WHEN "+owner+" = ? THEN 'owner' WHEN "+activeIntent+" THEN dd.status ELSE 'none' END AS status", appID).
		Order("d.created_at DESC,d.id ASC").Offset((input.Page - 1) * input.PageSize).Limit(input.PageSize).Scan(&result.Items).Error; err != nil {
		return result, err
	}
	return result, nil
}
