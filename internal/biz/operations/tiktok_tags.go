package operations

import (
	"context"
	"log/slog"

	domain "drama-server/internal/biz/consumer"
)

// TikTokTagRepository 维护英文展示名并读取关联剧数；官方身份与名称由内置目录提供。
type TikTokTagRepository interface {
	// ListTikTokTags 返回完整官方目录及当前英文翻译，未维护的翻译为空。
	ListTikTokTags(context.Context) ([]domain.TikTokTag, error)
	// CountTikTokTagDramas 按官方标签 ID 返回当前主体编辑内容的关联剧数，包含未上线剧，每部剧每个标签只计一次。
	CountTikTokTagDramas(context.Context) (map[string]int64, error)
	// SaveTikTokTagEnglishName 按已确认存在的官方字符串 ID 幂等覆盖英文名；空串清除翻译。
	SaveTikTokTagEnglishName(context.Context, string, string) error
}

// TikTokTagInput 只允许修改英文展示名，不包含官方身份及名称。
type TikTokTagInput struct {
	// EnglishName 是去除首尾空白后最多 100 个字符的英文名，空串恢复原名展示。
	EnglishName string `json:"englishName"`
}

// ListTikTokTags 返回固定官方目录及运营维护的英文翻译。
func (s *DramaService) ListTikTokTags(ctx context.Context) ([]domain.TikTokTag, error) {
	tags, err := s.repositories.Content.Tags.ListTikTokTags(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list TikTok tag translations failed", "error", err)
	}
	return tags, err
}

// TikTokTagSummary 在运营标签目录中补充当前主体的关联数量，不改变消费者标签协议。
type TikTokTagSummary struct {
	// TikTokTag 保留官方身份及英文翻译。
	domain.TikTokTag
	// DramaCount 统计当前主体编辑标签关联的短剧，包括未上线剧，不因授权到多个应用重复计数。
	DramaCount int64 `json:"dramaCount"`
}

// ListTikTokTagSummaries 返回全局标签及当前主体的关联短剧数；统计失败时返回错误，不能将未知数量作为零展示。
func (s *DramaService) ListTikTokTagSummaries(ctx context.Context) ([]TikTokTagSummary, error) {
	tags, err := s.ListTikTokTags(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "load TikTok tag summaries failed", "error", err)
		return nil, err
	}
	counts, err := s.repositories.Content.Tags.CountTikTokTagDramas(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "count TikTok tag dramas failed", "error", err)
		return nil, err
	}
	result := make([]TikTokTagSummary, 0, len(tags))
	for _, tag := range tags {
		result = append(result, TikTokTagSummary{TikTokTag: tag, DramaCount: counts[tag.ID]})
	}
	return result, nil
}

// UpdateTikTokTag 仅修改已存在的官方标签英文名，未知 ID 返回未找到；不调用官方 API。
func (s *DramaService) UpdateTikTokTag(ctx context.Context, id string, input TikTokTagInput) (domain.TikTokTag, error) {
	tag, ok := domain.FindTikTokTag(id)
	if !ok {
		slog.WarnContext(ctx, "update TikTok tag rejected", "error", domain.ErrNotFound)
		return domain.TikTokTag{}, domain.ErrNotFound
	}
	if err := s.repositories.Content.Tags.SaveTikTokTagEnglishName(ctx, id, input.EnglishName); err != nil {
		slog.ErrorContext(ctx, "save TikTok tag translation failed", "error", err)
		return domain.TikTokTag{}, err
	}
	tag.EnglishName = input.EnglishName
	return tag, nil
}

// projectOperationsDrama 补充线上版本号，运营仍以官方原名识别标签。
func projectOperationsDrama(drama *domain.Drama) {
	if drama == nil {
		return
	}
	if drama.Published != nil {
		drama.OnlineVersion = drama.Published.PlatformVersion
		drama.OnlineEpisodeNumbers = make([]int, 0, len(drama.Published.Episodes))
		for _, episode := range drama.Published.Episodes {
			drama.OnlineEpisodeNumbers = append(drama.OnlineEpisodeNumbers, episode.Number)
		}
	}
	drama.Tags = make([]string, 0, len(drama.TikTokTags))
	for _, tag := range drama.TikTokTags {
		drama.Tags = append(drama.Tags, tag.OfficialName)
	}
}
