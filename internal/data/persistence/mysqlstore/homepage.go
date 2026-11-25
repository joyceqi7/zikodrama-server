package mysqlstore

import (
	"context"
	"encoding/json"
	"strings"

	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
)

// FindVisibleRankingMembers 复用主体、渠道和应用授权筛选，只投影 ID 与线上标签，不读取完整快照或分集。
func (s *Store) FindVisibleRankingMembers(ctx context.Context, ids []string) ([]homepage.RankingMember, error) {
	result := []homepage.RankingMember{}
	if len(ids) == 0 {
		return result, nil
	}
	source := publishedCatalogSource(ctx)
	var rows []struct {
		// ID 来自当前渠道的短剧关联列。
		ID string
		// TagIDsJSON 只包含线上快照中的标签数组。
		TagIDsJSON []byte
	}
	fields := source.id + " AS id, JSON_EXTRACT(" + source.snapshot + ", '$.tagIds') AS tag_ids_json"
	if err := s.publishedCatalogQuery(ctx).Where(source.id+" IN ?", ids).Select(fields).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		member := homepage.RankingMember{DramaID: row.ID}
		if len(row.TagIDsJSON) > 0 {
			if err := json.Unmarshal(row.TagIDsJSON, &member.TagIDs); err != nil {
				return nil, err
			}
		}
		result = append(result, member)
	}
	return result, nil
}

// FindVisibleSummaries 在 SQL 中批量过滤已下架或未授权内容，Go 解码匹配快照并按线上首集补齐海报。
func (s *Store) FindVisibleSummaries(ctx context.Context, ids []string) ([]consumer.DramaSummary, error) {
	items := []consumer.DramaSummary{}
	if len(ids) == 0 {
		return items, nil
	}
	catalog, err := s.ListTikTokTags(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]consumer.TikTokTag{}
	for _, tag := range catalog {
		byID[tag.ID] = tag
	}
	source := publishedCatalogSource(ctx)
	var rows []catalogSnapshotRow
	err = s.publishedCatalogQuery(ctx).Where(source.id+" IN ?", ids).Select(source.snapshotFields()).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	// 批量补齐发布后生成的首帧图，只接受与线上首集 ID、VID 同时匹配的媒资。
	episodeIDs := make([]string, 0, len(rows))
	firstEpisodeVIDs := make(map[string]string, len(rows))
	for _, row := range rows {
		var snapshot catalogSnapshot
		if err := json.Unmarshal(row.SnapshotJSON, &snapshot); err != nil {
			return nil, err
		}
		item := snapshot.summary(row.ID, byID)
		if len(snapshot.Episodes) > 0 {
			first := snapshot.Episodes[0]
			item.FirstEpisodePosterURL = first.PosterURL
			firstEpisodeVIDs[item.ID] = first.VID
			if first.ID != "" {
				episodeIDs = append(episodeIDs, first.ID)
			}
		}
		items = append(items, item)
	}
	posters := map[string]string{}
	if len(episodeIDs) > 0 {
		var episodes []episodeModel
		if err := s.data.DB().WithContext(ctx).Select("id", "vid", "poster_url").Where("id IN ?", episodeIDs).Find(&episodes).Error; err != nil {
			return nil, err
		}
		for _, episode := range episodes {
			if poster := strings.TrimSpace(episode.PosterURL); poster != "" {
				posters[episode.ID+"\x00"+episode.VID] = poster
			}
		}
	}
	for index := range items {
		item := &items[index]
		if poster := posters[item.FirstEpisodeID+"\x00"+firstEpisodeVIDs[item.ID]]; poster != "" {
			item.FirstEpisodePosterURL = poster
		}
	}
	return items, nil
}
