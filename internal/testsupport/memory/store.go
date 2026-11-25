package memory

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/campaign"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/subject"
	"drama-server/internal/biz/visitor"
)

// TestApplicationID 是内存仓储测试夹具显式使用的应用，不参与生产代码或请求回退。
const TestApplicationID = "1"

// OperationsRepositories 仅为测试将内存 Store 装配成 operations 的窄端口集合。
// 生产代码必须在 data 组合根显式注入各端口，不接受一个“万能持久化”接口。
func OperationsRepositories(store *Store) operations.Repositories {
	return operations.Repositories{
		Application: operations.ApplicationPorts{
			Registry: store, Subjects: store,
		},
		Monetization: operations.MonetizationPorts{Settings: store},
		Campaign:     operations.CampaignPorts{Repository: store},
		Content: operations.ContentPorts{OwnerApplication: store.FindApplication, Tags: store, Catalog: store, Publications: store, OwnerApplicationID: func(ctx context.Context) (string, error) {
			apps, err := store.ListApplications(ctx)
			if err != nil {
				return "", err
			}
			for _, app := range apps {
				if app.BytePlusAccountID != "" && app.BytePlusSpaceName != "" {
					return app.TikTokAppID, nil
				}
			}
			return "", globalconfig.ErrNotFound
		}},
		Media:     operations.MediaPorts{Jobs: store},
		Reporting: operations.ReportingPorts{Metrics: store, Audits: store},
	}
}

// 执行广告标识对应的业务处理。
func containsAdUnitID(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// Seed 定义内存测试仓储的初始状态，仅供测试构造所需业务场景。
type Seed struct {
	// Subjects 显式补充测试主体，缺省夹具已有默认主体 1。
	Subjects []subject.Subject
	// PublishedOwnerID 显式为标记 published 的测试内容创建线上快照；为空时不自动发布。
	PublishedOwnerID string
	// PublishedTargets 显式指定测试快照已经授权的其他应用。
	PublishedTargets        []string
	Dramas                  []domain.Drama
	Applications            []application.Application
	ApplicationMonetization []monetization.ApplicationMonetizationConfig
}

// 定义权益。
type entitlement struct {
	// 保存应用标识。
	ApplicationID string
	// 保存访问者标识。
	VisitorID string
	// 保存短剧标识。
	DramaID string
	// 保存剧集标识。
	EpisodeID string
	// 保存过期时间。
	ExpiresAt time.Time
	// 保存权益成功授予的时间。
	GrantedAt time.Time
	// 保存目录权益的权威来源标识；逐集权益不使用该字段。
	SourceID string
}

// 定义会员记录。
type vipProgressRecord struct {
	// 保存应用标识。
	ApplicationID string
	// 保存访问者标识。
	VisitorID string
	// 保存键。
	PeriodKey string
	// 保存已经完成的计数。
	Completed int
	// 保存需要完成的目标计数。
	Required int
	// 保存记录最后更新时间。
	UpdatedAt time.Time
}

// 定义存储。
type Store struct {
	// reviewLists 按主体和发布渠道保存共享审核内容及修订，不按应用复制清单。
	reviewLists map[string]operations.ReviewWhitelist
	// subjects 保存测试主体，不作为生产缺省回退。
	subjects map[string]subject.Subject
	// tagEnglishNames 仅模拟运营维护的英文名。
	tagEnglishNames map[string]string
	// nextID 仅模拟测试仓储分配的递增主键，生产由数据库分配。
	nextID uint64

	// 保存并发锁。
	mu sync.RWMutex
	// 保存按短剧标识索引的短剧数据。
	dramas map[string]domain.Drama
	// dramaCreators 模拟数据库中创建后不覆盖的管理员关联。
	dramaCreators map[string]string
	// dramaOperators 模拟数据库中最新成功人工操作人关联。
	dramaOperators map[string]string

	// 保存每部短剧当前的 TikTok 官方发布任务。
	tiktokPublications map[string]operations.TikTokPublication
	tiktokReleases     map[string]operations.TikTokPublication
	releaseMu          sync.Mutex
	// 保存按应用标识索引的应用数据。
	applications map[string]application.Application
	// 保存按投放标识索引的投放数据。
	campaigns map[string]campaign.Campaign
	// rewardGrants 保存按 ClaimID 索引的不可变奖励结算事实。
	rewardGrants map[string]monetization.RewardGrant
	// adShows 保存按主体和事件幂等键隔离的通用成功展示事实。
	adShows map[string]monetization.AdShow
	// 保存运营端写操作产生的审计记录。
	audits []operations.AuditRecord
	// 保存媒体。
	mediaJobs map[string]operations.MediaJob
	// 保存媒体任务。
	mediaJobKeys map[string]string
	// 保存应用商业化。
	applicationMonetization map[string]monetization.ApplicationMonetizationConfig
	// 共享规则与应用资源映射分开存储。
	unlockStrategies map[string]monetization.UnlockStrategy
	strategyBindings map[string]monetization.StrategyBinding
	// applicationStrategyIDs 保留每个应用独立策略的标识，历史共享配置编辑时分离。
	applicationStrategyIDs map[string]string
	// benefitGrants 按来源项保存类型 JSON 权益。
	benefitGrants map[string]monetization.Entitlement
	// 保存会员。
	vipProgress map[string]vipProgressRecord
	// visitors 保存应用范围内已经创建的匿名或已绑定消费者主体。
	visitors map[string]time.Time
	// 保存平台。
	tiktokIdentities map[string]string
	// 保存管理端。
	adminUsers map[string]adminrbac.User
	// adminUserSequence 和 adminRoleSequence 模拟两张表独立的自增主键。
	adminUserSequence uint64
	adminRoleSequence uint64
	// adminPermissionSequence 为自定义权限分配独立自增主键。
	adminPermissionSequence uint64
	// 保存管理端登录会话摘要。
	// 保存管理端。
	adminRoles map[string]adminrbac.Role
	// 保存管理端。
	adminPermissions map[string]adminrbac.Permission
	// 保存管理端用户。
	adminUserRoles map[string]map[string]struct{}
	// 保存管理端角色。
	adminRolePermissions map[string]map[string]struct{}
}

// 创建并返回存储实例。
func NewStore(seed Seed) *Store {
	store := &Store{
		reviewLists:   map[string]operations.ReviewWhitelist{},
		benefitGrants: map[string]monetization.Entitlement{}, unlockStrategies: map[string]monetization.UnlockStrategy{}, strategyBindings: map[string]monetization.StrategyBinding{},
		dramas: make(map[string]domain.Drama, len(seed.Dramas)),

		tiktokPublications:      make(map[string]operations.TikTokPublication),
		applications:            make(map[string]application.Application, len(seed.Applications)+1),
		subjects:                map[string]subject.Subject{"1": {ID: "1", Name: "现有主体", OwnerID: "subject-1"}},
		campaigns:               make(map[string]campaign.Campaign),
		rewardGrants:            make(map[string]monetization.RewardGrant),
		adShows:                 make(map[string]monetization.AdShow),
		audits:                  make([]operations.AuditRecord, 0),
		dramaCreators:           make(map[string]string),
		dramaOperators:          make(map[string]string),
		mediaJobs:               make(map[string]operations.MediaJob),
		mediaJobKeys:            make(map[string]string),
		applicationMonetization: make(map[string]monetization.ApplicationMonetizationConfig, len(seed.ApplicationMonetization)),
		vipProgress:             make(map[string]vipProgressRecord),
		visitors:                make(map[string]time.Time),
		tiktokIdentities:        make(map[string]string),
		adminUsers:              make(map[string]adminrbac.User),
		adminRoles:              make(map[string]adminrbac.Role),
		adminPermissions:        systemPermissionFixture(),
		adminPermissionSequence: uint64(len(systemPermissionFixture())),
		adminUserRoles:          make(map[string]map[string]struct{}),
		adminRolePermissions:    make(map[string]map[string]struct{}),
	}
	for _, drama := range seed.Dramas {
		if seed.PublishedOwnerID != "" && drama.PublishStatus == "published" && drama.Published == nil {
			online := cloneDrama(drama)
			online.OwnerApplicationID, online.PlatformVersion = seed.PublishedOwnerID, 1
			online.AuthorizedApplicationIDs = append([]string(nil), seed.PublishedTargets...)
			drama.Published = &online
		}
		store.reserveID(drama.ID)
		for _, ep := range drama.Episodes {
			store.reserveID(ep.ID)
		}
		store.dramas[drama.ID] = cloneDrama(drama)
	}
	now := time.Now().UTC()
	store.reserveID(TestApplicationID)
	store.applications[TestApplicationID] = application.Application{SubjectID: "1", ID: TestApplicationID,
		TikTokAppID: TestApplicationID, Name: "Drama Minis", MonetizationMode: application.MonetizationModeIAA,
		AdUnits:   application.AdUnits{RewardedVideo: []string{"rewarded"}},
		CreatedAt: now, UpdatedAt: now,
	}
	for _, row := range seed.Subjects {
		store.subjects[row.ID] = row
		store.reserveID(row.ID)
	}
	for _, app := range seed.Applications {
		if app.SubjectID == "" {
			app.SubjectID = "1"
		}
		if app.ID == "" {
			app.ID = app.TikTokAppID
		}
		store.reserveID(app.ID)
		store.applications[app.ID] = app
		if len(app.MiniReview.DramaIDs) > 0 || app.MiniReview.Revision > 0 {
			store.reviewLists[memoryReviewScope(app)] = operations.ReviewWhitelist{Revision: app.MiniReview.Revision, DramaIDs: append([]string{}, app.MiniReview.DramaIDs...)}
		}
	}
	for _, config := range seed.ApplicationMonetization {
		store.applicationMonetization[config.ApplicationID] = cloneApplicationMonetization(config)
	}
	return store
}

// CountRewardGrants 统计测试存储中已经成功结算的广告奖励。
func (s *Store) CountRewardGrants(ctx context.Context, visitorID, rewardType string, since time.Time, progressKey ...string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	applicationID := recordApplicationID(application.IDFromContext(ctx))
	for _, grant := range s.rewardGrants {
		if len(progressKey) > 0 && grant.ProgressKey != progressKey[0] {
			continue
		}
		if recordApplicationID(grant.ApplicationID) == applicationID && grant.VisitorID == visitorID && grant.RewardType == rewardType &&
			!grant.GrantedAt.Before(since) {
			count++
		}
	}
	return count, nil
}

// 查询并返回符合条件的短剧列表。
func (s *Store) ListDramas(_ context.Context) ([]domain.Drama, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Drama, 0, len(s.dramas))
	for _, drama := range s.dramas {
		if drama.Published != nil {
			out = append(out, s.hydrateDramaLocked(drama))
		}
	}
	sort.Slice(out, func(left, right int) bool { return out[left].ID < out[right].ID })
	return out, nil
}

// 查询并返回符合条件的短剧列表。
func (s *Store) ListAllDramas(_ context.Context) ([]domain.Drama, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Drama, 0, len(s.dramas))
	for _, drama := range s.dramas {
		out = append(out, s.hydrateDramaLocked(drama))
	}
	sort.Slice(out, func(left, right int) bool { return out[left].ID < out[right].ID })
	return out, nil
}

// 查询并返回指定的短剧。
func (s *Store) FindDrama(_ context.Context, id string) (*domain.Drama, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	drama, ok := s.dramas[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	copy := s.hydrateDramaLocked(drama)
	return &copy, nil
}

// SaveDrama 保存草稿主记录并保留已有分集和线上快照，不生成或保存版本身份。
func (s *Store) SaveDrama(ctx context.Context, drama domain.Drama) error {
	drama.PublishStatus = "draft"
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.dramas[drama.ID]; ok {
		drama.Published = existing.Published
		drama.Episodes = append([]domain.Episode{}, existing.Episodes...)
	}
	s.recordDramaOperatorLocked(ctx, drama.ID, s.dramas[drama.ID].ID == "")
	s.dramas[drama.ID] = cloneDrama(drama)
	return nil
}

// SaveDramaOperations 保存协作字段且不改变内容或发布状态。
func (s *Store) SaveDramaOperations(ctx context.Context, id, assignee, note string, dueAt *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	drama, ok := s.dramas[id]
	if !ok {
		return domain.ErrNotFound
	}
	drama.Assignee, drama.OperationNote, drama.OperationDueAt = assignee, note, dueAt
	s.recordDramaOperatorLocked(ctx, id, false)
	s.dramas[id] = drama
	return nil
}

// SaveDramaAggregate 整体替换编辑内容并保留线上快照，不修改渠道发布快照。
func (s *Store) SaveDramaAggregate(ctx context.Context, drama *domain.Drama, cover *operations.MediaJob) error {
	drama.PublishStatus = "draft"
	s.mu.Lock()
	defer s.mu.Unlock()
	if drama.ID == "" {
		drama.ID = s.allocateIDLocked()
	}
	for i := range drama.Episodes {
		if drama.Episodes[i].ID == "" {
			drama.Episodes[i].ID = s.allocateIDLocked()
			drama.Episodes[i].DramaID = drama.ID
		}
	}
	previous, exists := s.dramas[drama.ID]
	for _, other := range s.dramas {
		if other.ID == drama.ID {
			continue
		}
		for _, ep := range other.Episodes {
			for _, candidate := range drama.Episodes {
				if ep.ID == candidate.ID {
					return operations.ErrInvalidInput
				}
			}
		}
	}
	if cover != nil {
		job, ok := s.mediaJobs[cover.ID]
		if !ok || job.Kind != operations.MediaJobCover || job.Status != operations.MediaJobUploaded || job.PublicURL != drama.CoverURL {
			return operations.ErrConflict
		}
		drama.CoverMID, drama.CoverSpaceName, drama.CoverFileName = job.RemoteVID, job.SpaceName, job.ProviderFileName
		job.DramaID = drama.ID
		job.Status = operations.MediaJobSucceeded
		s.mediaJobs[job.ID] = job
	} else if exists && previous.CoverURL == drama.CoverURL {
		drama.CoverMID, drama.CoverSpaceName, drama.CoverFileName = previous.CoverMID, previous.CoverSpaceName, previous.CoverFileName
	} else {
		drama.CoverMID, drama.CoverSpaceName, drama.CoverFileName = "", "", ""
	}
	for i := range drama.Episodes {
		for _, ep := range previous.Episodes {
			if ep.ID == drama.Episodes[i].ID {
				preserveEpisodeMedia(&drama.Episodes[i], ep)
			}
		}
	}
	drama.Published = previous.Published
	s.recordDramaOperatorLocked(ctx, drama.ID, !exists)
	stored := cloneDrama(*drama)
	s.dramas[drama.ID] = stored
	if previous.CoverMID != drama.CoverMID {
		s.queueCoverDeletionLocked(previous)
	}
	for _, ep := range previous.Episodes {
		found := false
		for _, next := range drama.Episodes {
			if ep.ID == next.ID {
				found = true
			}
		}
		if !found {
			s.queueEpisodeDeletionLocked(ep)
		}
	}
	return nil
}

// 删除指定的短剧。
func (s *Store) DeleteDrama(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	drama, ok := s.dramas[id]
	if !ok {
		return domain.ErrNotFound
	}
	if drama.Published != nil {
		return operations.ErrConflict
	}
	if task, exists := s.latestPublicationLocked(id); exists && task.Active() {
		return operations.ErrConflict
	}
	delete(s.dramas, id)
	s.queueCoverDeletionLocked(drama)
	for _, ep := range drama.Episodes {
		s.queueEpisodeDeletionLocked(ep)
	}
	for _, job := range s.mediaJobs {
		if job.DramaID == id && job.Kind != operations.MediaJobDelete {
			s.queueResourceDeletionLocked(job)
		}
	}
	return nil
}

// 查询并返回指定的剧集。
func (s *Store) FindEpisode(_ context.Context, id string) (*domain.Episode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, drama := range s.dramas {
		for _, episode := range drama.Episodes {
			if episode.ID == id {
				copy := episode
				return &copy, nil
			}
		}
	}
	return nil, domain.ErrNotFound
}

// 校验并保存剧集。
func (s *Store) SaveEpisode(_ context.Context, episode domain.Episode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveEpisodeLocked(episode)
}

// 校验并保存剧集媒体。
func (s *Store) SaveEpisodeMedia(_ context.Context, episode domain.Episode, drama domain.Drama, expectedVID, assetID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if episode.DramaID != drama.ID {
		return operations.ErrInvalidInput
	}
	stored, ok := s.dramas[drama.ID]
	if !ok {
		return domain.ErrNotFound
	}
	stored.PublishStatus = "draft"
	for index := range stored.Episodes {
		if stored.Episodes[index].ID != episode.ID {
			continue
		}
		if stored.Episodes[index].VID != expectedVID {
			return operations.ErrConflict
		}
		if assetID != "" {
			job, ok := s.mediaJobs[assetID]
			if !ok || job.Kind != operations.MediaJobUpload || job.EpisodeID != episode.ID || job.DramaID != drama.ID || job.RemoteVID != episode.VID || job.Status != operations.MediaJobRunning {
				return operations.ErrConflict
			}
		}
		if stored.Episodes[index].VID != episode.VID {
			s.queueEpisodeDeletionLocked(stored.Episodes[index])
		}
		preserveEpisodeContent(&episode, stored.Episodes[index])
		stored.Episodes[index] = episode
		s.dramas[drama.ID] = cloneDrama(stored)
		return nil
	}
	return domain.ErrNotFound
}

// UpdateEpisodeMediaStatus 只更新仍绑定预期 VID 的分集。
func (s *Store) UpdateEpisodeMediaStatus(_ context.Context, episodeID, expectedVID, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for dramaID, drama := range s.dramas {
		for index := range drama.Episodes {
			if drama.Episodes[index].ID != episodeID || drama.Episodes[index].VID != expectedVID {
				continue
			}
			drama.Episodes[index].MediaStatus = status
			s.dramas[dramaID] = cloneDrama(drama)
			return nil
		}
	}
	return domain.ErrNotFound
}

// 执行剧集媒体对应的业务处理。
func preserveEpisodeMedia(target *domain.Episode, current domain.Episode) {
	target.VID = current.VID
	target.MediaSpaceName = current.MediaSpaceName
	target.PosterURL = current.PosterURL
	target.DurationSeconds = current.DurationSeconds
	target.MediaStatus = current.MediaStatus
}

// 执行剧集对应的业务处理。
func preserveEpisodeContent(target *domain.Episode, current domain.Episode) {
	target.DramaID = current.DramaID
	target.Number = current.Number
	target.Title = current.Title
	target.Description = current.Description
}

// 校验并保存剧集。
func (s *Store) saveEpisodeLocked(episode domain.Episode) error {
	drama, ok := s.dramas[episode.DramaID]
	if !ok {
		return domain.ErrNotFound
	}
	replaced := false
	for index := range drama.Episodes {
		if drama.Episodes[index].ID == episode.ID {
			drama.Episodes[index] = episode
			replaced = true
			break
		}
		if drama.Episodes[index].Number == episode.Number {
			return operations.ErrInvalidInput
		}
	}
	if !replaced {
		drama.Episodes = append(drama.Episodes, episode)
	}
	sort.Slice(drama.Episodes, func(left, right int) bool { return drama.Episodes[left].Number < drama.Episodes[right].Number })
	s.dramas[drama.ID] = drama
	return nil
}

// 判断权益是否满足当前条件。
func (s *Store) HasEntitlement(ctx context.Context, visitorID, dramaID, episodeID string, now time.Time) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasBenefitLocked(recordApplicationID(application.IDFromContext(ctx)), visitorID, monetization.AccessTarget{DramaID: dramaID, EpisodeID: episodeID}, now), nil
}

// FindRewardGrant 恢复测试存储中的既有幂等结算。
func (s *Store) FindRewardGrant(ctx context.Context, visitorID, claimID string) (monetization.RewardGrant, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	grant, found := s.rewardGrants[claimID]
	if !found || recordApplicationID(grant.ApplicationID) != recordApplicationID(application.IDFromContext(ctx)) {
		return monetization.RewardGrant{}, false, nil
	}
	if grant.VisitorID != visitorID {
		return monetization.RewardGrant{}, false, monetization.ErrInvalidRewardClaim
	}
	return cloneRewardGrant(grant), true, nil
}

// GrantReward 模拟原子奖励结算；内存锁只用于测试，生产使用 MySQL 用户行锁与事务。
func (s *Store) GrantReward(ctx context.Context, grant monetization.RewardGrant, now time.Time) (monetization.ClaimResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	applicationID := recordApplicationID(application.IDFromContext(ctx))
	if existing, found := s.rewardGrants[grant.ClaimID]; found {
		if recordApplicationID(existing.ApplicationID) != applicationID || existing.VisitorID != grant.VisitorID {
			return monetization.ClaimResult{}, monetization.ErrInvalidRewardClaim
		}
		return memoryClaimResult(existing, monetization.ClaimStatusAlreadyGranted), nil
	}
	grant.ApplicationID = applicationID
	grant.GrantedAt = now.UTC()
	dailyLimitReached := false
	if grant.DailyRewardLimit > 0 {
		granted := 0
		for _, existing := range s.rewardGrants {
			if recordApplicationID(existing.ApplicationID) == applicationID && existing.VisitorID == grant.VisitorID &&
				existing.RewardType == grant.RewardType && existing.ProgressKey == grant.ProgressKey && !existing.GrantedAt.Before(grant.PeriodStartsAt) {
				granted++
			}
		}
		if granted >= grant.DailyRewardLimit {
			return monetization.ClaimResult{}, domain.ErrRateLimited
		}
		dailyLimitReached = granted+1 >= grant.DailyRewardLimit
	}

	if grant.RewardType == monetization.RewardTypeEpisodeUnlock {
		if s.hasBenefitLocked(applicationID, grant.VisitorID, monetization.AccessTarget{DramaID: grant.DramaID, EpisodeID: grant.EpisodeID}, now) {
			return monetization.ClaimResult{}, monetization.ErrInvalidRewardClaim
		}
	} else if grant.RewardType == monetization.RewardTypeVIPProgress {
		policy := policySnapshotFromGrant(grant)
		period := monetization.RewardPeriod{Key: grant.PeriodKey, ExpiresAt: grant.PeriodExpiresAt}
		state := s.vipProgressLocked(applicationID, grant.VisitorID, policy, period, now)
		if state.Active || state.AdCount >= state.RequiredCount || !grant.PeriodExpiresAt.After(now) {
			return monetization.ClaimResult{}, monetization.ErrInvalidRewardClaim
		}
	} else if grant.RewardType == monetization.RewardTypeEntryProgress {
		if !grant.PeriodExpiresAt.After(now) || grant.RequiredCount < 1 ||
			grant.DailyRewardLimit != 0 {
			return monetization.ClaimResult{}, monetization.ErrInvalidRewardClaim
		}
		// 与生产仓储一致：入剧 N 只限制继续拉广告，不丢弃已完成的事实。
	} else {
		return monetization.ClaimResult{}, monetization.ErrInvalidRewardClaim
	}

	ownedIDs := map[string]bool{}
	if grant.RewardType == monetization.RewardTypeEpisodeUnlock && grant.BuildContext != nil {
		for _, episodeID := range grant.OwnershipEpisodeIDs {
			if s.hasBenefitLocked(applicationID, grant.VisitorID, monetization.AccessTarget{DramaID: grant.DramaID, EpisodeID: episodeID}, now) {
				ownedIDs[episodeID] = true
			}
		}
		if err := monetization.RebuildRewardEntitlements(&grant, ownedIDs); err != nil {
			return monetization.ClaimResult{}, err
		}
	}
	items, err := monetization.RewardEntitlements(grant)
	if err != nil {
		return monetization.ClaimResult{}, err
	}
	s.rewardGrants[grant.ClaimID] = cloneRewardGrant(grant)
	result := memoryClaimResult(grant, monetization.ClaimStatusGranted)
	result.DailyLimitReached = dailyLimitReached
	result.OwnedEpisodeIDs = ownedIDs
	if grant.RewardType == monetization.RewardTypeVIPProgress {
		progressKey := scopedVIPProgressKey(applicationID, grant.VisitorID, grant.PeriodKey+grant.ProgressKey)
		progress := s.vipProgress[progressKey]
		progress.ApplicationID = applicationID
		progress.VisitorID = grant.VisitorID
		progress.PeriodKey = grant.PeriodKey
		progress.Required = grant.RequiredCount
		if progress.Required < 1 {
			progress.Required = 1
		}
		if progress.Completed < progress.Required {
			progress.Completed++
		}
		progress.UpdatedAt = now
		s.vipProgress[progressKey] = progress
		policy := policySnapshotFromGrant(grant)
		period := monetization.RewardPeriod{Key: grant.PeriodKey, ExpiresAt: grant.PeriodExpiresAt}
		state := s.vipProgressLocked(applicationID, grant.VisitorID, policy, period, now)
		result.VIPProgress = &state
		return result, nil
	}
	s.writeBenefitsLocked(items)
	return result, nil
}

func policySnapshotFromGrant(grant monetization.RewardGrant) monetization.MonetizationPolicy {
	required := grant.RequiredCount
	return monetization.MonetizationPolicy{
		ApplicationID:     grant.ApplicationID,
		Status:            monetization.MonetizationEnabled,
		ProgressNamespace: grant.ProgressKey,
		VIP:               monetization.ApplicationVIPPolicy{Enabled: true, RequiredAdCount: required},
	}
}

func memoryClaimResult(grant monetization.RewardGrant, status string) monetization.ClaimResult {
	return monetization.ClaimResult{
		SettledGrant: &grant,
		ClaimID:      grant.ClaimID, Status: status, Unlocked: grant.RewardType == monetization.RewardTypeEpisodeUnlock,
		DramaID: grant.DramaID, EpisodeID: grant.EpisodeID, RewardType: grant.RewardType,
	}
}

func cloneRewardGrant(grant monetization.RewardGrant) monetization.RewardGrant {
	grant.BuildContext = nil
	grant.BuildRule = nil
	grant.OwnershipEpisodeIDs = nil
	grant.UnlockEpisodeIDs = append([]string(nil), grant.UnlockEpisodeIDs...)
	original := grant.Entitlements
	grant.Entitlements = cloneStrategy(original)
	for i := range grant.Entitlements {
		grant.Entitlements[i].ApplicationID = original[i].ApplicationID
		grant.Entitlements[i].UserID = original[i].UserID
	}
	return grant
}

// 查询并返回符合条件的当前组件的数据列表。
func (s *Store) ListEntitlements(ctx context.Context, visitorID string, now time.Time) ([]monetization.EntitlementGrant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listEntitlementsLocked(recordApplicationID(application.IDFromContext(ctx)), visitorID, now)
}

func (s *Store) listEntitlementsLocked(applicationID, visitorID string, now time.Time) ([]monetization.EntitlementGrant, error) {
	items := []monetization.EntitlementGrant{}
	seen := map[string]bool{}
	registry := monetization.DefaultStrategies()
	for _, e := range s.benefitGrants {
		var data monetization.AccessData
		if json.Unmarshal(e.Data, &data) != nil {
			continue
		}
		for _, episodeID := range data.EpisodeIDs {
			key := data.DramaID + "\x00" + episodeID
			if seen[key] || !registry.Allows(e, applicationID, visitorID, monetization.AccessTarget{DramaID: data.DramaID, EpisodeID: episodeID}, now) {
				continue
			}
			seen[key] = true
			item := monetization.EntitlementGrant{ApplicationID: applicationID, VisitorID: visitorID, DramaID: data.DramaID, EpisodeID: episodeID, GrantedAt: e.CreatedAt}
			if e.ValidUntil != nil {
				item.ExpiresAt = *e.ValidUntil
			}
			items = append(items, item)
		}
	}
	sort.Slice(items, func(left, right int) bool { return items[left].GrantedAt.After(items[right].GrantedAt) })
	return items, nil
}

// 校验并保存审计记录。
func (s *Store) SaveAuditRecord(_ context.Context, record operations.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.audits {
		if record.RequestID != "" && s.audits[index].RequestID == record.RequestID {
			record.ID = s.audits[index].ID
			record.CreatedAt = s.audits[index].CreatedAt
			s.audits[index] = record
			return nil
		}
	}
	record.ID = strconv.Itoa(len(s.audits) + 1)
	s.audits = append(s.audits, record)
	return nil
}

// 查询并返回符合条件的审计记录列表。
func (s *Store) ListAuditRecords(_ context.Context, limit int) ([]operations.AuditRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit > len(s.audits) {
		limit = len(s.audits)
	}
	items := make([]operations.AuditRecord, 0, limit)
	for index := len(s.audits) - 1; index >= len(s.audits)-limit; index-- {
		items = append(items, s.audits[index])
	}
	return items, nil
}

// 执行权益对应的业务处理。
func entitlementKey(visitorID, dramaID, episodeID string) string {
	return visitorID + ":" + dramaID + ":" + episodeID
}

// 记录记录应用标识及其执行结果。
func recordApplicationID(applicationID string) string {
	if applicationID == "" {
		return TestApplicationID
	}
	return applicationID
}

// 生成应用范围内隔离的存储键。
func scopedKey(applicationID, key string) string {
	if recordApplicationID(applicationID) == TestApplicationID {
		return key
	}
	return applicationID + ":" + key
}

// 执行访问者对应的业务处理。
func scopedVisitorKey(applicationID, visitorID string) string {
	return scopedKey(applicationID, visitorID)
}

// 执行权益对应的业务处理。
func scopedEntitlementKey(applicationID, visitorID, dramaID, episodeID string) string {
	return scopedKey(applicationID, entitlementKey(visitorID, dramaID, episodeID))
}

// 执行短剧对应的业务处理。
func cloneDrama(drama domain.Drama) domain.Drama {
	copy := drama
	copy.AuthorizedApplicationIDs = append([]string(nil), drama.AuthorizedApplicationIDs...)
	if drama.Published != nil {
		online := *drama.Published
		online.Published = nil
		published := cloneDrama(online)
		copy.Published = &published
	}
	copy.Tags = append([]string{}, drama.Tags...)
	copy.TikTokTags = make([]domain.TikTokTag, len(drama.TikTokTags))
	for index, tag := range drama.TikTokTags {
		copy.TikTokTags[index] = cloneTikTokTag(tag)
	}
	copy.TagIDs = append([]string{}, drama.TagIDs...)
	copy.Episodes = make([]domain.Episode, len(drama.Episodes))
	for index, episode := range drama.Episodes {
		copy.Episodes[index] = episode
	}
	return copy
}

// 执行标签对应的业务处理。
func cloneTikTokTag(tag domain.TikTokTag) domain.TikTokTag {
	copy := tag
	return copy
}

// hydrateDramaLocked 合并标签及线上分集的派生海报；调用方须持有锁。
func (s *Store) hydrateDramaLocked(drama domain.Drama) domain.Drama {
	copy := cloneDrama(drama)
	if copy.Published != nil {
		mediaByReference := make(map[string]domain.Episode, len(copy.Episodes))
		for _, episode := range copy.Episodes {
			mediaByReference[episode.ID+"\x00"+episode.VID] = episode
		}
		for index := range copy.Published.Episodes {
			publishedEpisode := &copy.Published.Episodes[index]
			if media, ok := mediaByReference[publishedEpisode.ID+"\x00"+publishedEpisode.VID]; ok && media.PosterURL != "" {
				publishedEpisode.PosterURL = media.PosterURL
			}
		}
	}
	domain.AttachTikTokTagCatalog(&copy, s.tikTokTagsLocked())
	if copy.Published != nil {
		domain.AttachTikTokTagCatalog(copy.Published, s.tikTokTagsLocked())
	}
	return copy
}

// CreateVisitor 创建应用范围内的消费者主体。
func (s *Store) CreateVisitor(_ context.Context, applicationID string, output *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	visitorID := s.allocateIDLocked()
	key := scopedVisitorKey(applicationID, visitorID)
	if _, exists := s.visitors[key]; exists {
		return visitor.ErrUnauthorized
	}
	s.visitors[key] = time.Now().UTC()
	*output = visitorID
	return nil
}

// VisitorExists 验证消费者主体仍属于指定应用。
func (s *Store) VisitorExists(_ context.Context, applicationID, visitorID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.visitors[scopedVisitorKey(applicationID, visitorID)]
	return exists, nil
}

// ResolveTikTokIdentity 模拟身份唯一索引，不产生待合并的匿名中间用户。
func (s *Store) ResolveTikTokIdentity(_ context.Context, record visitor.TikTokIdentityRecord) (string, error) {
	if record.ApplicationID == "" || record.OpenIDHash == "" {
		return "", visitor.ErrUnauthorized
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := record.ApplicationID + "\x00" + record.OpenIDHash
	if existing, found := s.tiktokIdentities[key]; found {
		return existing, nil
	}
	visitorID := s.allocateIDLocked()
	if _, exists := s.visitors[scopedVisitorKey(record.ApplicationID, visitorID)]; exists {
		return "", visitor.ErrUnauthorized
	}
	s.visitors[scopedVisitorKey(record.ApplicationID, visitorID)] = time.Now().UTC()
	s.tiktokIdentities[key] = visitorID
	return visitorID, nil
}

// 生成应用、访问者和来源范围内隔离的目录权益键。
func scopedCatalogEntitlementKey(applicationID, visitorID, sourceID string) string {
	return scopedKey(applicationID, visitorID+"\x00"+sourceID)
}

// DiscoverDramas 在测试仓储中按应用可见快照生成分页摘要。
func (s *Store) DiscoverDramas(ctx context.Context, input domain.DiscoveryQuery) (domain.DiscoveryPage, error) {
	dramas, err := s.ListDramas(ctx)
	if err != nil {
		return domain.DiscoveryPage{}, err
	}
	visible := make([]domain.Drama, 0, len(dramas))
	for _, drama := range dramas {
		if published, ok := drama.PublishedForApplication(application.IDFromContext(ctx)); ok {
			if input.TagID != "" {
				matched := false
				for _, id := range published.TagIDs {
					if id == input.TagID {
						matched = true
					}
				}
				if !matched {
					continue
				}
			}
			visible = append(visible, published.ConsumerView())
		}
	}
	return domain.PaginateDramaSummaries(visible, input)
}

// ListDramaPage 仅在测试内存仓储中从聚合生成摘要。
func (s *Store) ListDramaPage(ctx context.Context, input operations.DramaListQuery) (operations.DramaListPage, error) {
	dramas, err := s.ListAllDramas(ctx)
	if err != nil {
		return operations.DramaListPage{}, err
	}
	items := []operations.DramaListItem{}
	for _, d := range dramas {
		// 测试仓储与真实目录一致：只有 ready 为待上线，历史渠道状态仍属于编辑草稿。
		if input.ContentStatus != "" && (d.PublishStatus == "ready") != (input.ContentStatus == "ready") {
			continue
		}
		version := 0
		if d.Published != nil {
			version = d.Published.PlatformVersion
		}
		s.mu.RLock()
		publication, found := s.latestPublicationLocked(d.ID)
		s.mu.RUnlock()
		ready := 0
		for _, ep := range d.Episodes {
			if ep.MediaStatus == "ready" && ep.VID != "" {
				ready++
			}
		}
		matchesWorkflow := true
		switch input.Publication {
		case "published":
			matchesWorkflow = version != 0
		case "draft":
			matchesWorkflow = version == 0
		case "needs_action":
			matchesWorkflow = found && slices.Contains([]string{"synced", "approved", "rejected", "failed"}, publication.Status)
		case "waiting_platform":
			matchesWorkflow = found && slices.Contains([]string{"queued", "processing", "under_review"}, publication.Status)
		case "content_incomplete":
			matchesWorkflow = d.CoverURL == "" || len(d.Episodes) == 0 || ready != len(d.Episodes)
		case "manual_attention":
			matchesWorkflow = found && slices.Contains([]string{"rejected", "failed"}, publication.Status)
		case "syncing":
			matchesWorkflow = found && slices.Contains([]string{"queued", "processing"}, publication.Status) && !slices.Contains([]string{"submit_review", "poll_review", "appeal_review", "poll_appeal", "withdraw_review", "set_online_version", "list_album", "delist_album"}, publication.Stage)
		case "await_review":
			matchesWorkflow = found && publication.Status == "synced"
		case "under_review":
			matchesWorkflow = found && publication.Status == "under_review"
		case "rejected":
			matchesWorkflow = found && slices.Contains([]string{"rejected", "failed"}, publication.Status)
		case "await_publish":
			matchesWorkflow = found && publication.Status == "approved"
		case "distribution_failed":
			matchesWorkflow = false
		}
		if !matchesWorkflow {
			continue
		}
		if input.Query != "" && !strings.Contains(strings.ToLower(d.Title+" "+strings.Join(d.Tags, " ")), strings.ToLower(input.Query)) {
			continue
		}
		s.mu.RLock()
		creator, operator := s.dramaCreators[d.ID], s.dramaOperators[d.ID]
		creatorUser, operatorUser := s.adminUsers[creator], s.adminUsers[operator]
		s.mu.RUnlock()
		creatorName, operatorName := creatorUser.Nickname, operatorUser.Nickname
		if creatorName == "" {
			creatorName = creatorUser.Username
		}
		if operatorName == "" {
			operatorName = operatorUser.Username
		}
		item := operations.DramaListItem{DeleteBlocked: (d.Published != nil && d.Published.PublishStatus == "published") || found && slices.Contains([]string{"queued", "processing", "under_review", "approved", "synced"}, publication.Status), CreatedByID: creator, UpdatedByID: operator, CreatedByName: creatorName, UpdatedByName: operatorName, ID: d.ID, Title: d.Title, Assignee: d.Assignee, OperationDueAt: d.OperationDueAt, CoverURL: d.CoverURL, Status: d.Status, Year: d.Year, Tags: d.Tags, PublishStatus: d.PublishStatus, OnlineVersion: version, EpisodeCount: len(d.Episodes), ReadyEpisodeCount: ready}
		item.AuthorizedApplicationCount = d.Published.ConfirmedAuthorizationCount()
		if d.Published != nil {
			item.OwnerApplicationID = d.Published.OwnerApplicationID
		}
		if found {
			item.Publication = &operations.DramaListPublication{CurrentContentChanged: publication.ContentSnapshot == nil || domain.OfficialContentDigest(d) != domain.OfficialContentDigest(*publication.ContentSnapshot), ID: publication.ID, Priority: publication.Priority, Status: publication.Status, Stage: publication.Stage, ReviewStatus: publication.ReviewStatus, ErrorCode: publication.ErrorCode}
		}
		items = append(items, item)
	}
	total := len(items)
	start := min((input.Page-1)*input.PageSize, total)
	end := min(start+input.PageSize, total)
	return operations.DramaListPage{Items: items[start:end], Total: int64(total)}, nil
}

// DramaOverview 统计测试内存中的剧与分集。
func (s *Store) DramaOverview(ctx context.Context) (*operations.Overview, error) {
	dramas, err := s.ListAllDramas(ctx)
	if err != nil {
		return nil, err
	}
	result := &operations.Overview{DramaCount: len(dramas)}
	for _, d := range dramas {
		if d.Published != nil {
			result.PublishedCount++
		}
		result.EpisodeCount += len(d.Episodes)
		for _, ep := range d.Episodes {
			if ep.MediaStatus != "ready" {
				result.PendingMediaCount++
			}
		}
	}
	return result, nil
}

// FindDramaDetail 为详情读取提供剧内容，缓存由组合根的读取装饰器负责。
func (s *Store) FindDramaDetail(ctx context.Context, id string) (*domain.Drama, error) {
	return s.FindDrama(ctx, id)
}

// allocateIDLocked 在调用方持有仓储锁时为新实体分配测试序号。
func (s *Store) allocateIDLocked() string { s.nextID++; return strconv.FormatUint(s.nextID, 10) }

// reserveID 使测试夹具中的已有主键不会被后续模拟自增覆盖；调用方负责初始化或持锁。
func (s *Store) reserveID(id string) {
	value, err := strconv.ParseUint(id, 10, 64)
	if err == nil && value > s.nextID {
		s.nextID = value
	}
}

// recordDramaOperatorLocked 在持有内存仓储锁时同步人工操作身份，自动任务不覆盖。
func (s *Store) recordDramaOperatorLocked(ctx context.Context, id string, creating bool) {
	if actor := operations.PublicationActor(ctx); actor != "" {
		if creating {
			s.dramaCreators[id] = actor
		}
		s.dramaOperators[id] = actor
	}
}
