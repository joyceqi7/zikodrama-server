// Package homepage 管理每主体、每渠道的首页轮播与人工热播推荐，不参与播放授权。
package homepage

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"drama-server/internal/biz/consumer"
)

// ErrConflict 表示推荐配置已被其他管理员修改，调用方应重新读取后编辑。
var ErrConflict = errors.New("recommendation revision conflict")

// ErrDramaRecommended 表示剧目仍在轮播或人工热播中，必须先移除引用才能下线。
var ErrDramaRecommended = errors.New("drama is still referenced by carousel or hot recommendations")

// CarouselItem 保存单个推荐位的运营配置。
type CarouselItem struct {
	// DramaID 是关联短剧的内部字符串标识。
	DramaID string `json:"dramaId"`
	// Title 为空时使用短剧线上名称。
	Title string `json:"title"`
	// Visible 决定是否在前台展示，仍须满足内容可见性。
	Visible bool `json:"visible"`
	// CreatedAt 由服务端生成；历史引用无时间时为空。
	CreatedAt string `json:"createdAt"`
}

// Carousel 保存主体与渠道内的有序剧目引用，不复制封面或官方发布状态。
type Carousel struct {
	// Revision 是整体替换时的并发版本，零表示尚未配置。
	Revision int64 `json:"revision"`
	// DramaIDs 按前台展示顺序保存内部短剧字符串标识。
	DramaIDs []string `json:"dramaIds"`
	// Items 按推荐顺序保存扩展配置，兼容历史纯 ID 配置。
	Items []CarouselItem `json:"items,omitempty"`
}

// ReferencesDrama 同时检查历史 ID 配置和扩展条目；隐藏不等于移除，仍保护下线门禁。
func (c Carousel) ReferencesDrama(id string) bool {
	for _, candidate := range c.DramaIDs {
		if candidate == id {
			return true
		}
	}
	for _, item := range c.Items {
		if item.DramaID == id {
			return true
		}
	}
	return false
}

// CarouselView 为运营提供当前配置及仍可展示的线上剧目；失效引用保留供移除。
type CarouselView struct {
	// Metadata 仅在直连运营范围提供版本与有效应用数。
	Metadata map[string]PublicationMetadata `json:"metadata,omitempty"`
	// Carousel 保留完整引用及并发版本。
	Carousel
	// Dramas 在管理端包含全部上线摘要，消费端按当前应用授权过滤，封面来自剧目当前线上版本。
	Dramas []consumer.DramaSummary `json:"dramas"`
}

// Sections 是与全部剧目分页独立的首页推荐区域。
type Sections struct {
	// Rankings 按官方标签返回自动关联、经运营排序的非空榜单。
	Rankings []RankingSection `json:"rankings"`
	// Carousel 按运营配置顺序返回仍然可见的短剧。
	Carousel []consumer.DramaSummary `json:"carousel"`
	// Hot 按当前主体的人工配置顺序返回可见短剧。
	Hot []consumer.DramaSummary `json:"hot"`
}

// Repository 持久化每主体的轮播和人工热播配置。
type Repository interface {
	// GetCarousel 读取当前主体配置；未配置返回版本零与空引用数组。
	GetCarousel(ctx context.Context) (Carousel, error)
	// ReplaceCarousel 按 input.Revision 原子整体替换，版本冲突返回 ErrConflict。
	ReplaceCarousel(ctx context.Context, input Carousel) (Carousel, error)
	// GetHot 读取当前主体人工热播；未配置返回版本零与空引用。
	GetHot(ctx context.Context) (Carousel, error)
	// ReplaceHot 按版本原子整体替换人工热播，冲突返回 ErrConflict。
	ReplaceHot(ctx context.Context, input Carousel) (Carousel, error)
}

// Catalog 只读取线上内容，避免推荐内容绕过发布与应用授权门禁。
type Catalog interface {
	// ListTikTokTags 返回当前官方身份与英文翻译，供分类榜识别和展示。
	ListTikTokTags(context.Context) ([]consumer.TikTokTag, error)
	// FindVisibleSummaries 批量返回指定剧目摘要；管理上下文查询当前主体全部上线剧，消费上下文按应用授权过滤，不存在或不可见项省略。
	FindVisibleSummaries(context.Context, []string) ([]consumer.DramaSummary, error)
	// FindVisibleRankingMembers 只读取可见线上剧目的标识和标签，分类目录与分页筛选不加载全部剧目摘要或分集。
	FindVisibleRankingMembers(context.Context, []string) ([]RankingMember, error)
	// DiscoverDramas 分页读取可见目录；管理端读取当前主体目录，消费端仍按应用授权过滤，支持搜索与既有筛选。
	DiscoverDramas(context.Context, consumer.DiscoveryQuery) (consumer.DiscoveryPage, error)
}

// ResultCache 缓存完整首页摘要，人工配置仍由 Repository 持久化。
type ResultCache interface {
	// Read 按当前应用和区域读取，未命中时调用 load；不缓存错误。
	Read(ctx context.Context, section string, load func() ([]consumer.DramaSummary, error)) ([]consumer.DramaSummary, error)
	// Invalidate 在配置提交后失效旧结果，失败记录日志且由 TTL 兜底。
	Invalidate(ctx context.Context)
}

// Service 在持久化引用与实时线上目录之间进行校验和排序。
type Service struct {
	repository        Repository
	rankingRepository RankingRepository
	catalog           Catalog
	cache             ResultCache
}

// NewService 分别注入推荐配置、线上摘要目录和主体分类仓储，不持有平台凭据。
func NewService(repository Repository, catalog Catalog, rankings RankingRepository, caches ...ResultCache) *Service {
	s := &Service{repository: repository, catalog: catalog, rankingRepository: rankings}
	if len(caches) > 0 {
		s.cache = caches[0]
	}
	return s
}

// Carousel 读取当前主体配置并附上可见摘要，保留失效引用供管理员识别。
func (s *Service) Carousel(ctx context.Context) (CarouselView, error) {
	return s.configuration(ctx, false)
}

// Hot 读取当前主体人工热播及线上摘要，保留失效引用供运营移除。
func (s *Service) Hot(ctx context.Context) (CarouselView, error) {
	return s.configuration(ctx, true)
}

func (s *Service) configuration(ctx context.Context, hot bool) (CarouselView, error) {
	read := s.repository.GetCarousel
	if hot {
		read = s.repository.GetHot
	}
	config, err := read(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "load homepage recommendation failed", "hot", hot, "error", err)
		return CarouselView{}, err
	}
	config.Items = carouselItems(config)
	dramas, err := s.visible(ctx, config.DramaIDs)
	if err != nil {
		return CarouselView{}, err
	}
	metadata, err := s.metadata(ctx, config.DramaIDs)
	if err != nil {
		return CarouselView{}, err
	}
	return CarouselView{Carousel: config, Dramas: dramas, Metadata: metadata}, nil
}

// SaveCarousel 整体替换有序引用，新加入项仅接受已上线剧目；HTTP 格式与数量校验由入口负责。
func (s *Service) SaveCarousel(ctx context.Context, input Carousel) (CarouselView, error) {
	return s.saveConfiguration(ctx, input, false)
}

// SaveHot 整体替换人工热播；新引用须可见，保留并发版本和既有引用清理语义。
func (s *Service) SaveHot(ctx context.Context, input Carousel) (CarouselView, error) {
	return s.saveConfiguration(ctx, input, true)
}

func (s *Service) saveConfiguration(ctx context.Context, input Carousel, hot bool) (CarouselView, error) {
	read, replace := s.repository.GetCarousel, s.repository.ReplaceCarousel
	if hot {
		read, replace = s.repository.GetHot, s.repository.ReplaceHot
	}
	dramas, err := s.visible(ctx, input.DramaIDs)
	if err != nil {
		return CarouselView{}, err
	}
	previous, err := read(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "load recommendation before replacement failed", "hot", hot, "error", err)
		return CarouselView{}, err
	}
	oldItems := map[string]CarouselItem{}
	for _, item := range carouselItems(previous) {
		oldItems[item.DramaID] = item
	}
	if input.Items == nil {
		for _, id := range input.DramaIDs {
			item, ok := oldItems[id]
			if !ok {
				item = CarouselItem{DramaID: id, Visible: true}
			}
			input.Items = append(input.Items, item)
		}
	}
	for i := range input.Items {
		old, ok := oldItems[input.Items[i].DramaID]
		if ok {
			input.Items[i].CreatedAt = old.CreatedAt
		} else {
			input.Items[i].CreatedAt = time.Now().UTC().Format(time.RFC3339)
		}
	}
	allowed := make(map[string]bool, len(previous.DramaIDs)+len(dramas))
	for _, id := range previous.DramaIDs {
		allowed[id] = true
	}
	for _, drama := range dramas {
		allowed[drama.ID] = true
	}
	// 已下架的既有引用可以保留或逐项移除，避免多条失效项让运营无法保存；新加入项必须可见。
	for _, id := range input.DramaIDs {
		if !allowed[id] {
			slog.WarnContext(ctx, "save carousel unavailable drama rejected", "error", consumer.ErrNotFound)
			return CarouselView{}, consumer.ErrNotFound
		}
	}
	config, err := replace(ctx, input)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			slog.WarnContext(ctx, "save homepage recommendation conflict", "hot", hot, "error", err)
		} else {
			slog.ErrorContext(ctx, "save homepage recommendation failed", "hot", hot, "error", err)
		}
		return CarouselView{}, err
	}
	if s.cache != nil {
		s.cache.Invalidate(ctx)
	}
	metadata, err := s.metadata(ctx, config.DramaIDs)
	if err != nil {
		return CarouselView{}, err
	}
	return CarouselView{Carousel: config, Dramas: dramas, Metadata: metadata}, nil
}

// Targets 在可信管理上下文分页提供当前主体全部已上线剧目，不能选择草稿。
func (s *Service) Targets(ctx context.Context, input consumer.DiscoveryQuery) (consumer.DiscoveryPage, error) {
	page, err := s.catalog.DiscoverDramas(ctx, input)
	if err != nil {
		slog.ErrorContext(ctx, "load carousel candidate dramas failed", "error", err)
	}
	if err != nil {
		return page, err
	}
	return s.candidateMetadata(ctx, page)
}

// HomeCarousel 返回当前应用可见的轮播摘要；配置仍以 MySQL 为准，结果可由 Redis 重建。
func (s *Service) HomeCarousel(ctx context.Context) ([]consumer.DramaSummary, error) {
	rows, err := s.homeRecommendationSection(ctx, "carousel", false)
	if err != nil {
		slog.ErrorContext(ctx, "load homepage carousel section failed", "error", err)
	}
	return rows, err
}

// HomeHot 返回当前应用可见的人工热播摘要，不等待轮播或分类榜读取。
func (s *Service) HomeHot(ctx context.Context) ([]consumer.DramaSummary, error) {
	// hot 使用独立缓存字段，首次读取不会展示旧版自动排行结果。
	rows, err := s.homeRecommendationSection(ctx, "hot", true)
	if err != nil {
		slog.ErrorContext(ctx, "load homepage hot section failed", "error", err)
	}
	return rows, err
}

// HomeRankings 返回当前应用可见的非空分类榜，不阻塞首屏轮播和热播接口。
func (s *Service) HomeRankings(ctx context.Context) ([]RankingSection, error) {
	rows, err := s.RankingSections(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "load homepage category rankings failed", "error", err)
	}

	return rows, err
}

func (s *Service) homeRecommendationSection(ctx context.Context, name string, hot bool) ([]consumer.DramaSummary, error) {
	return s.section(ctx, name, func() ([]consumer.DramaSummary, error) {
		view, err := s.configuration(ctx, hot)
		if err != nil {
			return nil, err
		}
		byID := map[string]consumer.DramaSummary{}
		for _, drama := range view.Dramas {
			byID[drama.ID] = drama
		}
		rows := []consumer.DramaSummary{}
		for _, item := range view.Items {
			drama, ok := byID[item.DramaID]
			if !ok || !item.Visible {
				continue
			}
			if item.Title != "" {
				drama.Title = item.Title
			}
			rows = append(rows, drama)
		}
		return rows, nil
	})
}

func (s *Service) section(ctx context.Context, name string, load func() ([]consumer.DramaSummary, error)) ([]consumer.DramaSummary, error) {
	if s.cache != nil {
		return s.cache.Read(ctx, name, load)
	}
	return load()
}

func (s *Service) visible(ctx context.Context, ids []string) ([]consumer.DramaSummary, error) {
	if len(ids) == 0 {
		return []consumer.DramaSummary{}, nil
	}
	rows, err := s.catalog.FindVisibleSummaries(ctx, ids)
	if err != nil {
		slog.ErrorContext(ctx, "load homepage visible drama summaries failed", "error", err)
		return nil, err
	}
	byID := make(map[string]consumer.DramaSummary, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	ordered := make([]consumer.DramaSummary, 0, len(rows))
	for _, id := range ids {
		if row, exists := byID[id]; exists {
			ordered = append(ordered, row)
		}
	}
	return ordered, nil
}

// carouselItems 将旧引用投影为默认展示项，不伪造历史创建时间。
func carouselItems(config Carousel) []CarouselItem {
	if config.Items != nil {
		return config.Items
	}
	items := make([]CarouselItem, 0, len(config.DramaIDs))
	for _, id := range config.DramaIDs {
		items = append(items, CarouselItem{DramaID: id, Visible: true})
	}
	return items
}
