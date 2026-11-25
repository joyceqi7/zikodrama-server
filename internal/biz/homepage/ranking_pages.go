package homepage

import (
	"context"
	"log/slog"
	"slices"

	"drama-server/internal/biz/consumer"
)

// RankingMember 是分类筛选所需的最小线上投影，不携带剧目摘要或分集。
type RankingMember struct {
	// DramaID 是当前主体渠道内可见的短剧标识。
	DramaID string
	// TagIDs 来自线上快照，避免已移出分类的旧引用继续展示。
	TagIDs []string
}

// PublicRankingCategory 仅返回非空分类的身份和名称，不附带剧目列表。
type PublicRankingCategory struct {
	// TagID 是官方分类字符串标识，翻译变化不改变身份。
	TagID string `json:"tagId"`
	// Name 优先使用当前英文译名。
	Name string `json:"name"`
}

// RankingPage 按运营顺序返回单个分类的一页可见剧目。
type RankingPage struct {
	// PublicRankingCategory 标识本页所属分类，客户端用来隔离切换后的迟到响应。
	PublicRankingCategory
	// Dramas 仅包含当前页的公开摘要，空页序列化为数组。
	Dramas []consumer.DramaSummary `json:"dramas"`
	// NextCursor 为空表示已到末页，不包含任何身份或播放授权信息。
	NextCursor string `json:"nextCursor,omitempty"`
}

// HomeRankingCategories 按官方目录顺序返回当前应用可见的非空分类，只查询标识和标签。
func (s *Service) HomeRankingCategories(ctx context.Context) ([]PublicRankingCategory, error) {
	configs, err := s.rankings(ctx)
	if err != nil {
		return nil, err
	}
	tags, err := s.rankingTags(ctx)
	if err != nil {
		return nil, err
	}
	members, err := s.rankingMembers(ctx, configs)
	if err != nil {
		return nil, err
	}
	result := []PublicRankingCategory{}
	for _, tag := range tags {
		for _, item := range configs[tag.ID].Items {
			if item.Visible && slices.Contains(members[item.DramaID], tag.ID) {
				result = append(result, PublicRankingCategory{TagID: tag.ID, Name: tag.DisplayName()})
				break
			}
		}
	}
	return result, nil
}

// HomeRankingPage 只筛选所选分类，并在读取剧目摘要前分页；隐藏、下架、授权和线上标签约束始终生效。
// offset 与 limit 已由 HTTP 入口校验，空分类仍返回带分类身份的空页。
func (s *Service) HomeRankingPage(ctx context.Context, tagID string, offset, limit int) (RankingPage, error) {
	tags, err := s.rankingTags(ctx)
	if err != nil {
		return RankingPage{}, err
	}
	page := RankingPage{Dramas: []consumer.DramaSummary{}}
	for _, tag := range tags {
		if tag.ID == tagID {
			page.PublicRankingCategory = PublicRankingCategory{TagID: tag.ID, Name: tag.DisplayName()}
			break
		}
	}
	if page.TagID == "" {
		return page, consumer.ErrNotFound
	}
	configs, err := s.rankings(ctx)
	if err != nil {
		return page, err
	}
	config := configs[tagID]
	members, err := s.rankingMembers(ctx, map[string]Ranking{tagID: config})
	if err != nil {
		return page, err
	}
	ids := []string{}
	for _, item := range config.Items {
		if item.Visible && slices.Contains(members[item.DramaID], tagID) {
			ids = append(ids, item.DramaID)
		}
	}
	// 分类内容在翻页期间可能减少；旧游标超过新末尾时自然结束，避免客户端陷入重试。
	if offset >= len(ids) {
		return page, nil
	}
	end := offset + min(limit, len(ids)-offset)
	rows, err := s.visible(ctx, ids[offset:end])
	if err != nil {
		return page, err
	}
	for _, row := range rows {
		// 两次读取之间发生发布变更时，仍复核本页摘要的线上标签。
		if slices.Contains(row.TagIDs, tagID) {
			page.Dramas = append(page.Dramas, row)
		}
	}
	if end < len(ids) {
		page.NextCursor = consumer.EncodeDiscoveryCursor(end)
	}
	return page, nil
}

// rankingMembers 对引用去重后仅读取最小线上投影，不为隐藏成员查询剧目内容。
func (s *Service) rankingMembers(ctx context.Context, configs map[string]Ranking) (map[string][]string, error) {
	ids := []string{}
	seen := map[string]bool{}
	for _, config := range configs {
		for _, item := range config.Items {
			if item.Visible && !seen[item.DramaID] {
				ids = append(ids, item.DramaID)
				seen[item.DramaID] = true
			}
		}
	}
	result := map[string][]string{}
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.catalog.FindVisibleRankingMembers(ctx, ids)
	if err != nil {
		slog.ErrorContext(ctx, "load homepage ranking member identities failed", "error", err)
		return nil, err
	}
	for _, row := range rows {
		result[row.DramaID] = row.TagIDs
	}
	return result, nil
}
