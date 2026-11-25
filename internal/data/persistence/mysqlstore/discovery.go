package mysqlstore

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"gorm.io/gorm"
)

// publishedCatalogQuery 选择当前渠道可见的已发布目录，保留官方授权或直连应用门禁。
func (s *Store) publishedCatalogQuery(ctx context.Context) *gorm.DB {
	if application.IsDirect(ctx) {
		return s.directCatalogQuery(ctx)
	}
	query := s.data.DB().WithContext(ctx).Table("tiktok_drama_states AS official_state").Joins("JOIN dramas AS d ON d.id=official_state.drama_id AND d.subject_id=official_state.subject_id").Where("official_state.subject_id=? AND JSON_UNQUOTE(JSON_EXTRACT(official_state.published_snapshot,'$.publishStatus'))='published'", application.SubjectIDFromContext(ctx))
	if homepage.IsManagementContext(ctx) {
		return query
	}
	app := application.IDFromContext(ctx)
	return query.Where("? <> '' AND (JSON_UNQUOTE(JSON_EXTRACT(official_state.published_snapshot,'$.ownerApplicationId'))=? OR JSON_CONTAINS(JSON_EXTRACT(official_state.published_snapshot,'$.authorizedApplicationIds'),JSON_QUOTE(?)))", app, app, app)
}

// DiscoverDramas 在 MySQL 中过滤、计数和分页，只解码当前页快照提取摘要，不逐剧查询分集。
func (s *Store) DiscoverDramas(ctx context.Context, input domain.DiscoveryQuery) (domain.DiscoveryPage, error) {
	catalog, err := s.ListTikTokTags(ctx)
	if err != nil {
		return domain.DiscoveryPage{}, err
	}
	byID := map[string]domain.TikTokTag{}
	for _, tag := range catalog {
		byID[tag.ID] = tag
	}
	source := publishedCatalogSource(ctx)
	catalogQuery := func() *gorm.DB {
		q := s.publishedCatalogQuery(ctx)
		if input.DramaIDs != nil {
			if len(input.DramaIDs) == 0 {
				return q.Where("1=0")
			}
			// 审核清单按当前发布渠道的短剧列筛选。
			return q.Where(source.id+" IN ?", input.DramaIDs)
		}
		return q
	}
	page := domain.DiscoveryPage{Items: []domain.DramaSummary{}, Tags: []string{}}
	tagIDsExpr := "JSON_EXTRACT(" + source.snapshot + ", '$.tagIds')"
	// 标签选项独立查询唯一的标签组合，不加载任何剧详情。
	var tagSets []string
	if err := catalogQuery().Distinct(tagIDsExpr).Pluck(tagIDsExpr, &tagSets).Error; err != nil {
		return page, err
	}
	labels := map[string]string{}
	for _, raw := range tagSets {
		var ids []string
		if err := json.Unmarshal([]byte(raw), &ids); err != nil {
			return page, err
		}
		for _, id := range ids {
			if tag, ok := byID[id]; ok {
				labels[strings.ToLower(tag.DisplayName())] = tag.DisplayName()
			}
		}
	}
	for _, label := range labels {
		page.Tags = append(page.Tags, label)
	}
	sort.Slice(page.Tags, func(i, j int) bool { return strings.ToLower(page.Tags[i]) < strings.ToLower(page.Tags[j]) })
	query := catalogQuery()
	if input.TagID != "" {
		query = query.Where("JSON_CONTAINS("+tagIDsExpr+", JSON_QUOTE(?))", input.TagID)
	}
	if input.Status != "" {
		statusExpr := "CASE JSON_UNQUOTE(JSON_EXTRACT(" + source.snapshot + ", '$.status')) WHEN 'updating' THEN 'ongoing' WHEN 'unpublished' THEN 'ongoing' ELSE JSON_UNQUOTE(JSON_EXTRACT(" + source.snapshot + ", '$.status')) END"
		query = query.Where(statusExpr+" = ?", input.Status)
	}
	if input.Tag != "" {
		predicate := "1 = 0"
		args := []any{}
		for _, tag := range catalog {
			if strings.EqualFold(tag.DisplayName(), input.Tag) {
				predicate += " OR JSON_CONTAINS(" + tagIDsExpr + ", JSON_QUOTE(?))"
				args = append(args, tag.ID)
			}
		}
		query = query.Where("("+predicate+")", args...)
	}
	for _, term := range strings.Fields(strings.ToLower(input.Query)) {
		predicate := "LOCATE(?, LOWER(CONCAT_WS(' ', JSON_UNQUOTE(JSON_EXTRACT(" + source.snapshot + ", '$.title')), JSON_UNQUOTE(JSON_EXTRACT(" + source.snapshot + ", '$.description'))))) > 0"
		args := []any{term}
		for _, tag := range catalog {
			if strings.Contains(strings.ToLower(tag.DisplayName()), term) {
				predicate += " OR JSON_CONTAINS(" + tagIDsExpr + ", JSON_QUOTE(?))"
				args = append(args, tag.ID)
			}
		}
		query = query.Where("("+predicate+")", args...)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return page, err
	}
	if input.Offset > int(total) {
		return page, domain.ErrInvalidDiscovery
	}
	var rows []catalogSnapshotRow
	if err := query.Select(source.snapshotFields()).Order(source.createdAt + " DESC, " + source.id + " ASC").Offset(input.Offset).Limit(input.Limit).Scan(&rows).Error; err != nil {
		return page, err
	}
	for _, r := range rows {
		var snapshot catalogSnapshot
		if err := json.Unmarshal(r.SnapshotJSON, &snapshot); err != nil {
			return page, err
		}
		page.Items = append(page.Items, snapshot.summary(r.ID, byID))
	}
	if input.Offset+len(rows) < int(total) {
		page.NextCursor = domain.EncodeDiscoveryCursor(input.Offset + len(rows))
	}
	return page, nil
}
