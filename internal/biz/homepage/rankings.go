package homepage

import (
	"context"
	"drama-server/internal/biz/consumer"
	"errors"
	"log/slog"
	"slices"
)

// RankingItem 是自动关联的分类成员，数组顺序决定名次。
type RankingItem struct {
	// DramaID 是短剧内部字符串标识，不接受官方 AlbumID。
	DramaID string `json:"dramaId"`
	// Visible 控制该分类下的展示，隐藏仍保留名次与引用。
	Visible bool `json:"visible"`
}

// Ranking 保存一个官方标签下的完整榜单。
type Ranking struct {
	// Revision 在自动成员或运营配置改变时递增；零表示尚无关联。
	Revision int64 `json:"revision"`
	// Items 是自动关联的有序成员，包含隐藏项；省略成员视为隐藏。
	Items []RankingItem `json:"items"`
}

// RankingRepository 按官方标签读取自动成员并保存运营配置。
type RankingRepository interface {
	// GetRankings 返回全局分类表中的榜单映射；发布事务自动维护成员。
	GetRankings(context.Context) (map[string]Ranking, error)
	// ReplaceRanking 按官方标签字符串 ID 和修订原子更新已有成员配置，冲突返回 ErrConflict，不能新增关联。
	ReplaceRanking(context.Context, string, Ranking) (Ranking, error)
}

// RankingCategory 是可选择的官方标签与当前配置数量。
type RankingCategory struct {
	// Tag 是只读的官方身份及当前英文名。
	Tag consumer.TikTokTag `json:"tag"`
	// Count 是当前分类全部关联数，包含隐藏项。
	Count int `json:"count"`
}

// RankingView 保留完整配置和当前仍属于该分类的可见线上短剧。
type RankingView struct {
	// Metadata 仅在直连运营范围提供版本与有效应用数。
	Metadata map[string]PublicationMetadata `json:"metadata,omitempty"`
	// Ranking 是并发版本和人工顺序。
	Ranking
	// Dramas 不包含已下架或已移出该标签的短剧；消费者另按应用授权过滤。
	Dramas []consumer.DramaSummary `json:"dramas"`
}

// RankingSection 是消费者可见的分类榜，名次以数组顺序从一开始。
type RankingSection struct {
	// TagID 是官方字符串标签标识，翻译变化不改变分类身份。
	TagID string `json:"tagId"`
	// Name 优先使用当前英文名，未维护则回退官方原名。
	Name string `json:"name"`
	// Dramas 只包含展示开关开启且仍满足发布、授权及标签约束的短剧。
	Dramas []consumer.DramaSummary `json:"dramas"`
}

func (s *Service) rankings(ctx context.Context) (map[string]Ranking, error) {
	configs, err := s.rankingRepository.GetRankings(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "load category rankings failed", "error", err)
	}
	return configs, err
}
func (s *Service) rankingTags(ctx context.Context) ([]consumer.TikTokTag, error) {
	tags, err := s.catalog.ListTikTokTags(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "load ranking TikTok tags failed", "error", err)
	}
	return tags, err
}

// RankingCategories 返回全部官方标签及自动关联数量；隐藏不减少数量。
func (s *Service) RankingCategories(ctx context.Context) ([]RankingCategory, error) {
	tags, err := s.rankingTags(ctx)
	if err != nil {
		return nil, err
	}
	configs, err := s.rankings(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]RankingCategory, 0, len(tags))
	for _, tag := range tags {
		result = append(result, RankingCategory{Tag: tag, Count: rankingCount(configs[tag.ID].Items)})
	}
	return result, nil
}
func knownRankingTag(ctx context.Context, id string) error {
	if _, ok := consumer.FindTikTokTag(id); !ok {
		slog.WarnContext(ctx, "category ranking unknown tag rejected", "error", consumer.ErrNotFound)
		return consumer.ErrNotFound
	}
	return nil
}

// CategoryRanking 读取全局分类及运营顺序，尚无上线关联时返回空数组。
func (s *Service) CategoryRanking(ctx context.Context, id string) (RankingView, error) {
	if err := knownRankingTag(ctx, id); err != nil {
		return RankingView{}, err
	}
	configs, err := s.rankings(ctx)
	if err != nil {
		return RankingView{}, err
	}
	config := configs[id]
	if config.Items == nil {
		config.Items = []RankingItem{}
	}
	ids := []string{}
	for _, item := range config.Items {
		ids = append(ids, item.DramaID)
	}
	dramas, err := s.visible(ctx, ids)
	if err != nil {
		slog.ErrorContext(ctx, "load category ranking dramas failed", "error", err)
		return RankingView{}, err
	}
	metadata, err := s.metadata(ctx, ids)
	if err != nil {
		return RankingView{}, err
	}
	return RankingView{Ranking: config, Dramas: rankedTagDramas(dramas, id), Metadata: metadata}, nil
}
func rankedTagDramas(dramas []consumer.DramaSummary, id string) []consumer.DramaSummary {
	result := []consumer.DramaSummary{}
	for _, drama := range dramas {
		if slices.Contains(drama.TagIDs, id) {
			result = append(result, drama)
		}
	}
	return result
}

// RankingTargets 在管理上下文分页搜索全部上线且属于指定官方标签的短剧，译名相同不会合并身份。
func (s *Service) RankingTargets(ctx context.Context, id string, input consumer.DiscoveryQuery) (consumer.DiscoveryPage, error) {
	if err := knownRankingTag(ctx, id); err != nil {
		return consumer.DiscoveryPage{}, err
	}
	input.TagID = id
	result, err := s.catalog.DiscoverDramas(ctx, input)
	if err != nil {
		slog.ErrorContext(ctx, "load category ranking candidates failed", "error", err)
	}
	if err != nil {
		return result, err
	}
	return s.candidateMetadata(ctx, result)
}

// SaveCategoryRanking 调整自动关联成员的顺序和展示状态；不允许手动添加关联。
func (s *Service) SaveCategoryRanking(ctx context.Context, id string, input Ranking) (RankingView, error) {
	if err := knownRankingTag(ctx, id); err != nil {
		return RankingView{}, err
	}
	configs, err := s.rankings(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "load category ranking before save failed", "error", err)
		return RankingView{}, err
	}
	previous := configs[id]
	if input.Revision != previous.Revision {
		slog.WarnContext(ctx, "save category ranking conflict", "error", ErrConflict)
		return RankingView{}, ErrConflict
	}
	ids := []string{}
	for _, item := range previous.Items {
		ids = append(ids, item.DramaID)
	}
	dramas, err := s.visible(ctx, ids)
	if err != nil {
		slog.ErrorContext(ctx, "validate category ranking candidates failed", "error", err)
		return RankingView{}, err
	}
	matching := rankedTagDramas(dramas, id)
	allowed := map[string]bool{}
	for _, item := range previous.Items {
		allowed[item.DramaID] = true
	}
	for _, item := range input.Items {
		if !allowed[item.DramaID] {
			slog.WarnContext(ctx, "save category ranking unavailable drama rejected", "error", consumer.ErrNotFound)
			return RankingView{}, consumer.ErrNotFound
		}
	}
	saved, err := s.rankingRepository.ReplaceRanking(ctx, id, input)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			slog.WarnContext(ctx, "save category ranking conflict", "error", err)
		} else {
			slog.ErrorContext(ctx, "save category ranking failed", "error", err)
		}
		return RankingView{}, err
	}
	if s.cache != nil {
		s.cache.Invalidate(ctx)
	}
	return RankingView{Ranking: saved, Dramas: matching}, nil
}

// RankingSections 批量查询全部引用，按官方目录与人工排名顺序返回非空榜单；每次复核发布、授权及标签归属。
func (s *Service) RankingSections(ctx context.Context) ([]RankingSection, error) {
	configs, err := s.rankings(ctx)
	if err != nil {
		return nil, err
	}
	result := []RankingSection{}
	if len(configs) == 0 {
		return result, nil
	}
	tags, err := s.rankingTags(ctx)
	if err != nil {
		return nil, err
	}
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
	dramas, err := s.visible(ctx, ids)
	if err != nil {
		slog.ErrorContext(ctx, "load public category ranking dramas failed", "error", err)
		return nil, err
	}
	byID := map[string]consumer.DramaSummary{}
	for _, drama := range dramas {
		byID[drama.ID] = drama
	}
	for _, tag := range tags {
		section := RankingSection{TagID: tag.ID, Name: tag.DisplayName(), Dramas: []consumer.DramaSummary{}}
		for _, item := range configs[tag.ID].Items {
			drama, ok := byID[item.DramaID]
			if ok && item.Visible && slices.Contains(drama.TagIDs, tag.ID) {
				section.Dramas = append(section.Dramas, drama)
			}
		}
		if len(section.Dramas) > 0 {
			result = append(result, section)
		}
	}
	return result, nil
}

func rankingCount(items []RankingItem) int {
	return len(items)
}
