package memory

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
)

// FindVisibleSummaries 让测试仓储沿用主体渠道和应用授权边界读取已发布审核候选。
func (s *Store) FindVisibleSummaries(ctx context.Context, ids []string) ([]domain.DramaSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := []domain.DramaSummary{}
	for _, id := range ids {
		drama, ok := s.dramas[id]
		if !ok || drama.Published == nil || drama.Published.PublishStatus != "published" {
			continue
		}
		if drama.SubjectID != "" && drama.SubjectID != application.SubjectIDFromContext(ctx) {
			continue
		}
		published := s.hydrateDramaLocked(drama).Published
		if (published.PlaybackProvider == "byteplus_web") != application.IsDirect(ctx) {
			continue
		}
		if !homepage.IsManagementContext(ctx) {
			if _, ok := drama.PublishedForApplication(application.IDFromContext(ctx)); !ok {
				continue
			}
		}
		result = append(result, published.ConsumerView().Summary())
	}
	return result, nil
}

// memoryReviewScope 与生产库保持同主体同渠道共享清单的边界。
func memoryReviewScope(app application.Application) string {
	return app.SubjectID + ":" + app.Normalized().PlaybackProvider
}

// miniReviewLocked 组合应用独立版本与当前渠道共享清单，调用方须持有仓库锁。
func (s *Store) miniReviewLocked(app application.Application) application.MiniReview {
	result := app.MiniReview
	shared := s.reviewLists[memoryReviewScope(app)]
	result.Revision = shared.Revision
	result.DramaIDs = append([]string{}, shared.DramaIDs...)
	result.Versions = result.ConfiguredVersions()
	return result
}

// GetReviewWhitelist 只读取当前主体渠道共享的审核列表。
func (s *Store) GetReviewWhitelist(ctx context.Context) (operations.ReviewWhitelist, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := application.SubjectIDFromContext(ctx) + ":" + application.PlaybackFromContext(ctx).Provider
	value := s.reviewLists[key]
	value.DramaIDs = append([]string{}, value.DramaIDs...)
	return value, nil
}

// SaveReviewWhitelist 串行保护共享列表与应用版本，其他主体或渠道不参与判断。
func (s *Store) SaveReviewWhitelist(ctx context.Context, input operations.ReviewWhitelist) (operations.ReviewWhitelist, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := application.SubjectIDFromContext(ctx) + ":" + application.PlaybackFromContext(ctx).Provider
	if application.SubjectIDFromContext(ctx) == "" {
		return input, operations.ErrInvalidInput
	}
	return s.saveReviewWhitelistLocked(key, input)
}

// saveReviewWhitelistLocked 与版本开关共用修订，任一启用版本都保护该渠道清单。
func (s *Store) saveReviewWhitelistLocked(key string, input operations.ReviewWhitelist) (operations.ReviewWhitelist, error) {
	if input.Revision != s.reviewLists[key].Revision {
		return input, operations.ErrConflict
	}
	if len(input.DramaIDs) == 0 {
		for _, app := range s.applications {
			if memoryReviewScope(app) == key && app.MiniReview.AnyEnabled() {
				return input, operations.ErrInvalidInput
			}
		}
	}
	input.Revision++
	input.DramaIDs = append([]string{}, input.DramaIDs...)
	s.reviewLists[key] = input
	return input, nil
}

// SaveMiniReviewSettings 只更新审核版本与开关，不覆盖清单、凭据或其他应用资料。
func (s *Store) SaveMiniReviewSettings(_ context.Context, id string, input application.MiniReview) (application.MiniReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	app, ok := s.applications[id]
	if !ok {
		return input, domain.ErrNotFound
	}
	app.MiniReview = s.miniReviewLocked(app)
	if input.Revision != app.MiniReview.Revision {
		return input, operations.ErrConflict
	}
	input = app.MiniReview.WithSettings(input)
	if input.AnyEnabled() && len(input.DramaIDs) == 0 {
		return input, operations.ErrInvalidInput
	}
	if input.AnyEnabled() && app.MonetizationMode == application.MonetizationModeIAA {
		binding := s.strategyBindings[id]
		if len(app.AdUnits.RewardedVideo) == 0 || !monetization.HasEnabledEpisodeRule(s.unlockStrategies[binding.StrategyID].Document) {
			return input, operations.ErrInvalidInput
		}
	}
	input.Revision++
	s.reviewLists[memoryReviewScope(app)] = operations.ReviewWhitelist{Revision: input.Revision, DramaIDs: append([]string{}, input.DramaIDs...)}
	app.MiniReview = input
	s.applications[id] = app
	return input, nil
}

// SaveMiniReview 仅替换审核清单，保留应用身份、凭据和最新开关。
func (s *Store) SaveMiniReview(_ context.Context, id string, input application.MiniReview) (application.MiniReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	app, ok := s.applications[id]
	if !ok {
		return input, domain.ErrNotFound
	}
	shared, err := s.saveReviewWhitelistLocked(memoryReviewScope(app), operations.ReviewWhitelist{Revision: input.Revision, DramaIDs: input.DramaIDs})
	result := s.miniReviewLocked(app)
	result.Revision, result.DramaIDs = shared.Revision, shared.DramaIDs
	return result, err
}

// 查询并返回符合条件的应用列表。
func (s *Store) ListApplications(_ context.Context) ([]application.Application, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]application.Application, 0, len(s.applications))
	for _, app := range s.applications {
		app.MiniReview = s.miniReviewLocked(app)
		app.ClientSecretConfigured = app.ClientSecret != ""
		app.AdUnits = cloneAdUnits(app.AdUnits)
		items = append(items, app)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].TikTokAppID < items[j].TikTokAppID })
	return items, nil
}

// 查询并返回指定的应用。
func (s *Store) FindApplication(_ context.Context, id string) (application.Application, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	app, ok := s.applications[id]
	if !ok {
		return application.Application{}, domain.ErrNotFound
	}
	app.ClientSecretConfigured = app.ClientSecret != ""
	app.AdUnits = cloneAdUnits(app.AdUnits)
	app.MiniReview = s.miniReviewLocked(app)
	return app, nil
}

// FindApplicationByTikTokAppID 精确匹配官方 AppID，重复配置返回错误，未找到时返回 ErrUnavailable。
func (s *Store) FindApplicationByTikTokAppID(_ context.Context, tiktokAppID string) (application.Application, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result application.Application
	found := false
	for _, app := range s.applications {
		if app.TikTokAppID != tiktokAppID {
			continue
		}
		if found {
			return application.Application{}, errors.New("multiple applications share tiktok app id")
		}
		result, found = app, true
	}
	if !found {
		return application.Application{}, application.ErrUnavailable
	}
	result.ClientSecretConfigured = result.ClientSecret != ""
	result.AdUnits = cloneAdUnits(result.AdUnits)
	result.MiniReview = s.miniReviewLocked(result)
	return result, nil
}

// SaveApplication 保存应用并在广告位变化时递增消费修订，保持已启用策略所需的完整配置。
func (s *Store) SaveApplication(_ context.Context, app application.Application) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if app.ID == "" {
		app.ID = app.TikTokAppID
	}
	if previous, exists := s.applications[app.ID]; exists {
		previous.MiniReview = s.miniReviewLocked(previous)
		playbackChanged := previous.Normalized().PlaybackProvider != app.Normalized().PlaybackProvider
		if playbackChanged && app.MiniReviewWrite {
			return operations.ErrConflict
		}
		if app.MiniReviewWrite {
			if app.MiniReview.Revision != previous.MiniReview.Revision {
				return operations.ErrConflict
			}
			app.MiniReview = previous.MiniReview.WithSettings(app.MiniReview)
			app.MiniReview.Revision++
		} else {
			app.MiniReview = previous.MiniReview
		}

		if previous.PlaybackRevision != app.PlaybackRevision {
			return operations.ErrConflict
		}
		if previous.Normalized().PlaybackProvider != app.Normalized().PlaybackProvider {
			app.PlaybackRevision++
			shared := s.reviewLists[memoryReviewScope(app)]
			app.MiniReview.Revision, app.MiniReview.DramaIDs = shared.Revision, append([]string{}, shared.DramaIDs...)
			if app.MiniReview.AnyEnabled() && len(app.MiniReview.DramaIDs) == 0 {
				return operations.ErrInvalidInput
			}
		}
		if app.ClientSecret == "" {
			app.ClientSecret = previous.ClientSecret
		}
		binding := s.strategyBindings[app.ID]
		if binding.StrategyID != "" {
			doc := s.unlockStrategies[binding.StrategyID].Document
			if err := monetization.ValidateApplicationMode(app.MonetizationMode, doc); err != nil {
				return err
			}
			if monetization.RequiresAdUnits(doc) && !app.AdUnits.Complete() {
				return operations.ErrApplicationAdUnitsIncomplete
			}
		}
		before, after := previous.AdUnits.Normalized(), app.AdUnits.Normalized()
		if previous.MonetizationMode != app.MonetizationMode || !slices.Equal(before.RewardedVideo, after.RewardedVideo) || !slices.Equal(before.Interstitial, after.Interstitial) {
			if config, legacy := s.applicationMonetization[app.ID]; legacy && binding.StrategyID == "" {
				if after.Complete() {
					config.EpisodeRewardAdUnitCode, config.VIPAdUnitCode = after.RewardedVideo[0], after.RewardedVideo[0]
				}
				s.applicationMonetization[app.ID] = config
			}
		}
		if app.MiniReviewWrite {
			s.reviewLists[memoryReviewScope(app)] = operations.ReviewWhitelist{Revision: app.MiniReview.Revision, DramaIDs: append([]string{}, app.MiniReview.DramaIDs...)}
		}
		if playbackChanged {
			for _, key := range []string{memoryReviewScope(previous), memoryReviewScope(app)} {
				shared := s.reviewLists[key]
				shared.Revision++
				s.reviewLists[key] = shared
			}
			app.MiniReview.Revision = s.reviewLists[memoryReviewScope(app)].Revision
		}
	}
	app.ClientSecretConfigured = app.ClientSecret != ""
	app.AdUnits = cloneAdUnits(app.AdUnits)
	s.applications[app.ID] = app
	return nil
}

// HasRewardedAdUnit 判断当前应用是否登记了指定激励视频广告位编码。
// 广告位不作为独立实体保存，因此校验始终读取 Application 聚合中的 AdUnits。
func (s *Store) HasRewardedAdUnit(ctx context.Context, code string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	app, exists := s.applications[recordApplicationID(application.IDFromContext(ctx))]
	if !exists {
		return false, domain.ErrNotFound
	}
	return app.AdUnits.HasRewardedVideo(strings.TrimSpace(code)), nil
}

func cloneAdUnits(units application.AdUnits) application.AdUnits {
	units.RewardedVideo = append([]string(nil), units.RewardedVideo...)
	units.Interstitial = append([]string(nil), units.Interstitial...)
	return units
}

// CreateApplication 使用官方 AppID 原子创建，重复身份不能覆盖已有配置。
func (s *Store) CreateApplication(_ context.Context, app *application.Application) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.applications {
		if existing.TikTokAppID == app.TikTokAppID {
			return operations.ErrConflict
		}
	}
	app.ID = s.allocateIDLocked()
	if app.PlaybackProvider == "byteplus_web" {
		app.PlaybackRevision = 1
	}
	app.ClientSecretConfigured = app.ClientSecret != ""
	app.AdUnits = cloneAdUnits(app.AdUnits)
	s.applications[app.ID] = *app
	return nil
}

// HasAdUnit 校验当前应用通用广告位，首页等场景无需奖励规则。
func (s *Store) HasAdUnit(ctx context.Context, code string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	app, exists := s.applications[recordApplicationID(application.IDFromContext(ctx))]
	if !exists {
		return false, domain.ErrNotFound
	}
	return app.AdUnits.HasCode(code), nil
}
