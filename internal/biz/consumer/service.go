package consumer

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/pkg/requesttime"
)

// Service 编排应用范围内的可见目录、官方播放和奖励权益，输入格式由传输入口校验。
type Service struct {
	// store 提供当前应用目录、配置和奖励结算端口。
	store          Store
	directPlayback DirectPlaybackProvider
	// now 提供权益有效期与自然日计算使用的时钟，测试可替换。
	now func() time.Time
}

// NewService 组装消费者用例；未注入时钟时使用系统时间。
func NewService(store Store, now func() time.Time) *Service {
	if now == nil {
		// now 提供权益有效期与自然日计算使用的时钟，测试可替换。
		now = time.Now
	}
	return &Service{store: store, now: now}
}

// ListDramas 过滤出当前应用拥有或获授权的已发布快照，并按请求语言生成展示副本。
func (s *Service) ListDramas(ctx context.Context) ([]Drama, error) {
	dramas, err := s.store.ListDramas(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list dramas failed", "error", err)
		return nil, err
	}
	visible := make([]Drama, 0, len(dramas))
	for _, drama := range dramas {
		if published, ok := drama.PublishedForApplication(application.IDFromContext(ctx)); ok {
			visible = append(visible, published.ConsumerView())
		}
	}
	return visible, nil
}

// Discover 先加载当前应用可见目录，再筛选和分页；标签选项来自整个可见目录而非仅当前页。
func (s *Service) Discover(ctx context.Context, input DiscoveryQuery) (DiscoveryPage, error) {
	page, err := s.store.DiscoverDramas(ctx, input)
	if err != nil {
		slog.ErrorContext(ctx, "discover drama summaries failed", "error", err)
		return DiscoveryPage{}, err
	}

	return page, nil
}

// PaginateDramaSummaries 为内存测试仓储实现同样的筛选合同；生产仓储在数据库分页。
func PaginateDramaSummaries(dramas []Drama, input DiscoveryQuery) (DiscoveryPage, error) {
	offset := input.Offset

	tagLabels := make(map[string]string)
	filtered := make([]Drama, 0, len(dramas))
	for _, drama := range dramas {
		if input.DramaIDs != nil {
			allowed := false
			for _, id := range input.DramaIDs {
				if id == drama.ID {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}
		for _, tag := range drama.Tags {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				key := strings.ToLower(tag)
				if _, exists := tagLabels[key]; !exists {
					tagLabels[key] = tag
				}
			}
		}
		if input.Status != "" && !strings.EqualFold(strings.TrimSpace(drama.Status), input.Status) {
			continue
		}
		if input.Tag != "" && !dramaHasTag(drama, input.Tag) {
			continue
		}
		if input.Query != "" && !dramaMatchesQuery(drama, input.Query) {
			continue
		}
		filtered = append(filtered, drama)
	}
	if offset > len(filtered) {
		return DiscoveryPage{}, ErrInvalidDiscovery
	}
	end := offset + input.Limit
	if end > len(filtered) {
		end = len(filtered)
	}
	items := make([]DramaSummary, 0, end-offset)
	for _, drama := range filtered[offset:end] {
		items = append(items, drama.Summary())
	}
	tags := make([]string, 0, len(tagLabels))
	for _, label := range tagLabels {
		tags = append(tags, label)
	}
	sort.Slice(tags, func(left, right int) bool {
		return strings.ToLower(tags[left]) < strings.ToLower(tags[right])
	})
	page := DiscoveryPage{Items: items, Tags: tags}
	if end < len(filtered) {
		page.NextCursor = EncodeDiscoveryCursor(end)
	}
	return page, nil
}

// dramaHasTag 按消费者可见的官方标签名称匹配，忽略大小写和首尾空白。
func dramaHasTag(drama Drama, wanted string) bool {
	for _, tag := range drama.Tags {
		if strings.EqualFold(strings.TrimSpace(tag), strings.TrimSpace(wanted)) {
			return true
		}
	}
	return false
}

// dramaMatchesQuery 在标题、简介和标签中逐词匹配，所有搜索词均命中才保留短剧。
func dramaMatchesQuery(drama Drama, query string) bool {
	haystack := strings.ToLower(strings.Join(append([]string{drama.Title, drama.Description}, drama.Tags...), " "))
	for _, term := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

// EncodeDiscoveryCursor 编码版本号与下一页偏移；编码用于协议演进，不是授权凭据。
func EncodeDiscoveryCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("v1:" + strconv.Itoa(offset)))
}

// FindDrama 按内部标识读取并投影当前应用可见的线上版本；无线上版本或未授权均返回 ErrNotFound。
func (s *Service) FindDrama(ctx context.Context, dramaID string) (*Drama, error) {
	drama, err := s.store.FindDramaDetail(ctx, dramaID)
	if err != nil {
		level := slog.LevelError
		if errors.Is(err, ErrNotFound) {
			level = slog.LevelWarn
		}
		slog.Log(ctx, level, "find drama failed", "error", err)
		return nil, err
	}
	published, ok := drama.PublishedForApplication(application.IDFromContext(ctx))
	if !ok {
		slog.WarnContext(ctx, "find drama rejected", "error", ErrNotFound)
		return nil, ErrNotFound
	}
	localized := published.ConsumerView()
	return &localized, nil
}

// ResolveLaunchTarget 解析可见目录中的落地目标；未指定剧集时取首集，不可见或目标不匹配时返回 nil 供首页回退。
func (s *Service) ResolveLaunchTarget(ctx context.Context, dramaID, episodeID string) (*LaunchTarget, error) {
	dramaID = strings.TrimSpace(dramaID)
	episodeID = strings.TrimSpace(episodeID)
	if dramaID == "" {
		return nil, nil
	}
	drama, err := s.FindDrama(ctx, dramaID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		slog.ErrorContext(ctx, "find launch drama failed", "error", err)
		return nil, err
	}
	if episode := playbackEpisode(drama, episodeID); episode != nil {
		return &LaunchTarget{DramaID: drama.ID, EpisodeID: episode.ID}, nil
	}
	return nil, nil
}

// UnlockRecords 读取当前有效分集权益，补齐展示内容；已下架或失去应用授权的内容跳过。
func (s *Service) UnlockRecords(ctx context.Context, visitorID string) ([]UnlockRecord, error) {
	ctx = requesttime.Ensure(ctx, s.now)
	grants, err := s.store.ListEntitlements(ctx, strings.TrimSpace(visitorID), requesttime.At(ctx, s.now).UTC())
	if err != nil {
		slog.ErrorContext(ctx, "list entitlements failed", "error", err)
		return nil, err
	}
	items := make([]UnlockRecord, 0, len(grants))
	for _, grant := range grants {
		drama, episode, findErr := s.findEpisode(ctx, grant.DramaID, grant.EpisodeID)
		if findErr != nil {
			slog.ErrorContext(ctx, "find episode failed", "error", findErr)
		}
		if findErr == ErrNotFound {
			continue
		}
		if findErr != nil {
			return nil, findErr
		}
		items = append(items, UnlockRecord{
			Drama: drama.Summary(), Episode: episode.Summary(),
			GrantedAt: grant.GrantedAt, ExpiresAt: grant.ExpiresAt,
		})
	}
	return items, nil
}

// UnlockedEpisodeIDs 返回当前应用与访客仍有效的分集权益标识，不额外验证目录上架状态。
func (s *Service) UnlockedEpisodeIDs(ctx context.Context, visitorID string) ([]string, error) {
	ctx = requesttime.Ensure(ctx, s.now)
	grants, err := s.store.ListEntitlements(ctx, strings.TrimSpace(visitorID), requesttime.At(ctx, s.now).UTC())
	if err != nil {
		slog.ErrorContext(ctx, "list entitlements failed", "error", err)
		return nil, err
	}
	ids := make([]string, 0, len(grants))
	for _, grant := range grants {
		ids = append(ids, grant.EpisodeID)
	}
	return ids, nil
}
