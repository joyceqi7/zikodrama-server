package operations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/subject"
	"drama-server/internal/pkg/requestmeta"
)

const (
	defaultPublicationPollInterval       = 2 * time.Second
	defaultPublicationMediaPollInterval  = 10 * time.Second
	defaultPublicationReviewPollInterval = 10 * time.Minute
	defaultPublicationLease              = 2 * time.Minute
	defaultPublicationRetryDelay         = 30 * time.Second
	defaultPublicationMaxAttempts        = 5
	defaultPublicationMediaStallTimeout  = 30 * time.Minute
)

// PublicationJobConfig 控制 TikTok 发布 Worker 的领取、轮询和重试策略。
// 零值使用适合生产的默认值，测试可以注入更短间隔。
type PublicationJobConfig struct {
	// PollInterval 是空队列轮询共享数据库的间隔。
	PollInterval time.Duration
	// MediaPollInterval 是视频登记及转码产物尚未就绪时再次查询的间隔，不用于审核。
	MediaPollInterval time.Duration
	// ReviewPollInterval 是审核中版本再次查询官方结果的基础间隔。
	ReviewPollInterval time.Duration
	// MediaStallTimeout 是转码没有新增成功分集时的最长观察窗口；超时暂停查询，不判定官方失败。
	MediaStallTimeout time.Duration
	// LeaseDuration 是一个 Worker 持有任务的最长时间。
	LeaseDuration time.Duration
	// RetryBaseDelay 是临时平台错误的指数退避起点。
	RetryBaseDelay time.Duration
	// MaxAttempts 是技术失败停止自动重试前的次数。
	MaxAttempts int
	// WorkerID 是跨实例数据库租约的所有者标识。
	WorkerID string
}

// PlatformCallError 表示官方 API 适配器已经安全分类的失败。
// 这里只携带稳定分类、重试与结果不确定性，不持有平台原始响应正文。
type PlatformCallError struct {
	// OutcomeUnknown 表示请求可能已经被平台执行，但没有取得可核实的响应。
	OutcomeUnknown bool
	// Code 是不含凭据和用户数据的稳定错误分类。
	Code string
	// Retryable 表示同一阶段稍后重试是否安全。
	Retryable bool
}

// Error 实现 error，同时避免把平台响应正文带入日志或管理端。
func (e *PlatformCallError) Error() string {
	if e == nil || strings.TrimSpace(e.Code) == "" {
		return "TikTok platform call failed"
	}
	return "TikTok platform call failed: " + strings.TrimSpace(e.Code)
}

// publicationJobRunner 执行共享数据库中的 TikTok 发布任务。
type publicationJobRunner struct {
	// service 提供应用、内容和发布仓储，以及官方适配器工厂。
	service *DramaService
	// pollInterval 控制空闲时查询共享队列的频率。
	pollInterval time.Duration
	// mediaPollInterval 只用于视频登记及转码产物查询。
	mediaPollInterval time.Duration
	// reviewPollInterval 是审核结果自动查询间隔。
	reviewPollInterval time.Duration
	// mediaStallTimeout 限制无进展的转码等待，人工恢复仍查询原任务。
	mediaStallTimeout time.Duration
	// leaseDuration 决定一次领取在数据库中占有任务的时间窗口。
	leaseDuration time.Duration
	// retryBaseDelay 是依赖失败自动重试的退避起点。
	retryBaseDelay time.Duration
	// maxAttempts 限制技术失败的自动重试次数。
	maxAttempts int
	// workerID 标记当前进程的数据库租约所有者。
	workerID string
	// wake 只通知本进程尽快扫描队列，不承载任务数据。
	wake chan struct{}
	// start 保证当前实例仅启动一次循环，不能代替数据库租约。
	start sync.Once
	// done 在本进程发布循环退出后关闭，供停止流程等待。
	done chan struct{}
}

// ConfigureTikTokPublications 装配官方发布适配器和可靠任务策略。
func (s *DramaService) ConfigureTikTokPublications(factory TikTokMediaProviderFactory, config PublicationJobConfig) error {
	if s == nil || s.repositories.Content.Catalog == nil || s.repositories.Content.Publications == nil ||
		s.repositories.Application.Registry == nil || factory == nil {
		slog.Warn("configure TikTok publications rejected", "error", ErrPublicationUnavailable)
		return ErrPublicationUnavailable
	}
	if config.PollInterval <= 0 {
		config.PollInterval = defaultPublicationPollInterval
	}
	if config.MediaPollInterval <= 0 {
		config.MediaPollInterval = defaultPublicationMediaPollInterval
	}
	if config.ReviewPollInterval <= 0 {
		config.ReviewPollInterval = defaultPublicationReviewPollInterval
	}
	if config.MediaStallTimeout <= 0 {
		config.MediaStallTimeout = defaultPublicationMediaStallTimeout
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = defaultPublicationLease
	}
	if config.RetryBaseDelay <= 0 {
		config.RetryBaseDelay = defaultPublicationRetryDelay
	}
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = defaultPublicationMaxAttempts
	}
	if config.MaxAttempts > 10 {
		slog.Warn("configure TikTok publications rejected", "error", ErrInvalidInput)
		return ErrInvalidInput
	}
	config.WorkerID = strings.TrimSpace(config.WorkerID)
	if config.WorkerID == "" {
		workerID, err := secureID("publication-worker")
		if err != nil {
			slog.Error("secure id failed", "error", err)
			return err
		}
		config.WorkerID = workerID
	}
	s.tiktokProviderFactory = factory
	s.publicationJobs = &publicationJobRunner{
		service: s, pollInterval: config.PollInterval, mediaPollInterval: config.MediaPollInterval,
		reviewPollInterval: config.ReviewPollInterval,
		mediaStallTimeout:  config.MediaStallTimeout,
		leaseDuration:      config.LeaseDuration, retryBaseDelay: config.RetryBaseDelay,
		maxAttempts: config.MaxAttempts, workerID: config.WorkerID,
		wake: make(chan struct{}, 1), done: make(chan struct{}),
	}
	return nil
}

// SubmitTikTokPublication 校验并冻结已保存内容并在请求内同步媒资；仅未完成的视频处理保留后台查询。
// 同步完成后任务会停在待送审阶段，审核只能由运营显式提交。
func (s *DramaService) SubmitTikTokPublication(ctx context.Context, dramaID string, input TikTokPublicationInput) (*TikTokPublication, error) {
	dramaID = strings.TrimSpace(dramaID)
	if s == nil || s.tiktokProviderFactory == nil || s.repositories.Content.Publications == nil {
		slog.WarnContext(ctx, "submit TikTok publication rejected", "error", ErrPublicationUnavailable)
		return nil, ErrPublicationUnavailable
	}
	drama, err := s.repositories.Content.Catalog.FindDrama(ctx, dramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find drama failed", "error", err)
		return nil, err
	}
	if !dramaPublicationReady(*drama) {
		slog.WarnContext(ctx, "sync TikTok content readiness rejected", "error", ErrPublicationNotReady)
		return nil, ErrPublicationNotReady
	}
	published := drama.Published
	snapshot := domain.FreezeContent(*drama)
	*drama = snapshot
	drama.Published = published
	if s.repositories.Content.OwnerApplicationID == nil {
		slog.WarnContext(ctx, "submit TikTok publication rejected", "error", ErrPublicationNotReady)
		return nil, ErrPublicationNotReady
	}
	ownerID, err := s.repositories.Content.OwnerApplicationID(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "load publication owner failed", "error", err)
		return nil, ErrPublicationNotReady
	}
	owner, err := s.resolvePublicationOwner(ctx, ownerID)
	if err != nil {
		slog.ErrorContext(ctx, "resolve publication applications failed", "error", err)
		return nil, err
	}
	region := ""
	if source, ok := s.media.(MediaPublicationScopeProvider); ok {
		var space string
		region, space, err = source.PublicationMediaScope(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "publication media scope failed", "error", err)
		}
		if err != nil || space != owner.BytePlusSpaceName {
			slog.WarnContext(ctx, "submit TikTok publication rejected", "error", ErrPublicationNotReady)
			return nil, ErrPublicationNotReady
		}
	}
	existing, findErr := s.repositories.Content.Publications.FindTikTokPublicationByDramaID(ctx, dramaID)
	if findErr != nil {
		if errors.Is(findErr, domain.ErrNotFound) {
			// 首次送审没有历史任务，属于正常创建路径，不应触发故障告警。
			slog.DebugContext(ctx, "no previous TikTok publication", "error", findErr)
		} else {
			slog.ErrorContext(ctx, "find TikTok publication by drama id failed", "error", findErr)
		}
	}
	if findErr == nil && ((existing.Active() && !replaceableTikTokVersion(existing)) || existing.ExternalWriteStarted || (existing.PlatformAlbumID != "" && existing.OwnerApplicationID != ownerID)) {
		slog.WarnContext(ctx, "submit TikTok publication rejected", "error", ErrConflict)
		return nil, ErrConflict
	} else if findErr != nil && !errors.Is(findErr, domain.ErrNotFound) {
		return nil, findErr
	}
	if findErr == nil && existing.RejectsContent(snapshot) {
		slog.WarnContext(ctx, "resubmit rejected content version denied", "error", ErrConflict)
		return nil, ErrConflict
	}
	now := time.Now().UTC()
	// 冻结本次送审内容，后续 Worker 不读取新的草稿字段；线上快照另行保留。
	snapshot = domain.FreezeContent(*drama)
	ownerScope := PublicationOwnerScope{Region: region, ClientKey: owner.ClientKey, AccountID: owner.BytePlusAccountID, SpaceName: owner.BytePlusSpaceName}
	reusable := findErr == nil && existing.OwnerApplicationID == ownerID && existing.OwnerScope == ownerScope
	// 官方剧目的分集必须来自同一空间；迁移后的新空间不能继续编辑旧空间的 Album。
	if !reusable {
		existing.PlatformAlbumID = ""
		existing.PlatformVersion = 0
	}
	skipTranscode := input.Transcode != nil && !*input.Transcode
	mediaPlan := planPublicationMedia(snapshot, existing, reusable, !skipTranscode)
	transcodeKeySeed, err := secureID("transcode")
	if err != nil {
		slog.ErrorContext(ctx, "generate transcode idempotency seed failed", "error", err)
		return nil, err
	}
	publication := TikTokPublication{
		SkipTranscode:  skipTranscode,
		ContentSummary: NewPublicationContentSummary(*drama),
		OwnerScope:     ownerScope, TranscodeKeySeed: transcodeKeySeed,
		ContentSnapshot: &snapshot, PlatformAlbumID: existing.PlatformAlbumID, PlatformVersion: existing.PlatformVersion,
		PlatformCoverID: mediaPlan.PlatformCoverID, Videos: mediaPlan.Videos, Transcodes: mediaPlan.Transcodes,
		SubjectID: application.SubjectIDFromContext(ctx), DramaID: dramaID, OwnerApplicationID: ownerID,
		Priority: normalizePublicationPriority(input.Priority), Status: PublicationStatusProcessing,
		Stage: mediaPlan.Stage, EpisodeIDsBySequence: reusablePublicationEpisodeIDs(snapshot, existing, reusable),
		NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}
	// 创建新任务时事务撤销旧修订，防止并发的旧版送审或上线恢复历史任务。
	if err := s.repositories.Content.Publications.StartTikTokPublication(ctx, &publication); err != nil {
		slog.Log(ctx, publicationErrorLevel(err), "start TikTok publication failed", "error", err)
		return nil, err
	}
	return s.executePublicationCall(ctx, &publication)
}

// SubmitTikTokReview 在请求内提交冻结版本审核，返回平台受理结果；审核结论另行查询。
func (s *DramaService) SubmitTikTokReview(ctx context.Context, dramaID string, input TikTokPublicationInput) (*TikTokPublication, error) {
	if s == nil || s.publicationJobs == nil || s.tiktokProviderFactory == nil {
		slog.WarnContext(ctx, "submit TikTok review unavailable", "error", ErrPublicationUnavailable)
		return nil, ErrPublicationUnavailable
	}
	p, err := s.FindTikTokPublication(ctx, dramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find publication for TikTok review failed", "error", err)
		return nil, err
	}
	if p.Status != PublicationStatusSynced || p.Stage != PublicationStageAwaitSubmitReview || p.ExternalWriteStarted ||
		p.PlatformAlbumID == "" || p.PlatformVersion < 1 || p.ContentSnapshot == nil || publicationLeaseActive(*p) {
		slog.WarnContext(ctx, "submit TikTok review rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	owner, err := s.loadPublicationOwner(ctx, p.OwnerApplicationID)
	if err != nil {
		slog.ErrorContext(ctx, "load TikTok review owner failed", "error", err)
		return nil, err
	}
	if !manualPublicationOwnerMatches(*p, owner) {
		slog.WarnContext(ctx, "submit TikTok review owner changed", "error", ErrPublicationNotReady)
		return nil, ErrPublicationNotReady
	}
	p.Priority = normalizePublicationPriority(input.Priority)
	p.Status, p.Stage = PublicationStatusQueued, PublicationStageSubmitReview
	p.ErrorCode, p.ErrorMessage, p.Attempts = "", "", 0
	p.NextAttemptAt, p.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	return s.executePublicationCall(ctx, p)
}

// restartPublicationMedia 使用失败任务自身的固定内容重建媒体计划，不要求当前草稿就绪，不读取直连发布版本。
func (s *DramaService) restartPublicationMedia(ctx context.Context, previous TikTokPublication) (*TikTokPublication, error) {
	if previous.ContentSnapshot == nil {
		slog.WarnContext(ctx, "restart publication media rejected", "error", ErrPublicationNotReady)
		return nil, ErrPublicationNotReady
	}
	owner, err := s.resolvePublicationOwner(ctx, previous.OwnerApplicationID)
	if err != nil {
		slog.Log(ctx, publicationErrorLevel(err), "resolve media restart owner failed", "error", err)
		return nil, err
	}
	if !manualPublicationOwnerMatches(previous, owner) {
		slog.WarnContext(ctx, "media restart owner changed", "error", ErrPublicationNotReady)
		return nil, ErrPublicationNotReady
	}
	seed, err := secureID("transcode")
	if err != nil {
		slog.ErrorContext(ctx, "generate media restart seed failed", "error", err)
		return nil, err
	}
	plan := planPublicationMedia(*previous.ContentSnapshot, previous, true, !previous.SkipTranscode)
	now := time.Now().UTC()
	next := TikTokPublication{SubjectID: previous.SubjectID, SkipTranscode: previous.SkipTranscode, DramaID: previous.DramaID, ContentSnapshot: previous.ContentSnapshot, ContentSummary: previous.ContentSummary, OwnerApplicationID: previous.OwnerApplicationID, OwnerScope: previous.OwnerScope, Priority: previous.Priority, PlatformAlbumID: previous.PlatformAlbumID, PlatformVersion: previous.PlatformVersion, PlatformCoverID: plan.PlatformCoverID, Videos: plan.Videos, Transcodes: plan.Transcodes, TranscodeKeySeed: seed, Status: PublicationStatusProcessing, Stage: plan.Stage, EpisodeIDsBySequence: reusablePublicationEpisodeIDs(*previous.ContentSnapshot, previous, true), CreatedAt: now, UpdatedAt: now, NextAttemptAt: now}
	if err := s.repositories.Content.Publications.RestartTikTokPublication(ctx, previous, &next); err != nil {
		slog.Log(ctx, publicationErrorLevel(err), "restart TikTok media task failed", "error", err)
		return nil, err
	}
	return s.executePublicationCall(ctx, &next)
}

// FindTikTokPublication 返回短剧当前官方发布状态。
func (s *DramaService) FindTikTokPublication(ctx context.Context, dramaID string) (*TikTokPublication, error) {
	if s == nil || s.repositories.Content.Publications == nil || strings.TrimSpace(dramaID) == "" {
		slog.WarnContext(ctx, "find TikTok publication rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	publication, err := s.repositories.Content.Publications.FindTikTokPublicationByDramaID(ctx, strings.TrimSpace(dramaID))
	if err != nil {
		slog.ErrorContext(ctx, "find TikTok publication by drama id failed", "error", err)
		return nil, err
	}
	if expected, _ := ctx.Value(expectedPublicationKey{}).(string); expected != "" && expected != publication.ID {
		slog.WarnContext(ctx, "historical publication action rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	drama, err := s.repositories.Content.Catalog.FindDrama(ctx, dramaID)
	if err != nil {
		slog.ErrorContext(ctx, "read current content for publication failed", "error", err)
		return nil, err
	}
	publication.CurrentContentChanged = (publication.ContentSnapshot == nil || domain.OfficialContentDigest(*drama) != domain.OfficialContentDigest(*publication.ContentSnapshot))
	publication = PublicationReadState(publication, time.Now().UTC())
	return &publication, nil
}

// RetryTikTokPublication 恢复失败任务自身的冻结内容；送审明确失败时可重新选择优先级。
// 未知 Album 更新先核对，确认原版本未推进时可受版本保护地恢复，首次同步仍只查询。
func (s *DramaService) RetryTikTokPublication(ctx context.Context, dramaID string, inputs ...TikTokPublicationInput) (*TikTokPublication, error) {
	publication, err := s.FindTikTokPublication(ctx, dramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find TikTok publication failed", "error", err)
		return nil, err
	}
	canResolveAlbum := publication.Stage == PublicationStageUpdateAlbum && publication.ErrorCode == "external_write_uncertain" &&
		publication.PlatformAlbumID != "" && publication.ContentSnapshot != nil
	recoverableCall := publication.Status == PublicationStatusProcessing && !publicationLeaseActive(*publication) || publication.Status == PublicationStatusQueued && publication.Stage != PublicationStageRegisterVideos && publication.Stage != PublicationStageTranscodeVideos
	if (publication.Status != PublicationStatusFailed && !recoverableCall) || publicationLeaseActive(*publication) ||
		((publication.ExternalWriteStarted || publication.ErrorCode == "external_write_uncertain") && !canResolveAlbum) {
		slog.WarnContext(ctx, "retry TikTok publication rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	// 优先级只能在送审明确失败且未被官方受理时修改，其他恢复阶段不接受此参数。
	if len(inputs) > 0 && inputs[0].Priority != "" {
		if publication.Stage != PublicationStageSubmitReview || publication.ReviewID != "" || publication.SubmittedAt != nil {
			slog.WarnContext(ctx, "change review priority rejected", "error", ErrConflict)
			return nil, ErrConflict
		}
		publication.Priority = inputs[0].Priority
	}
	// 历史任务已被官方明确拒绝时，空标签快照不能原样重试；补齐草稿后重新同步并复用已有 Album。
	if CanEditRejectedAlbumPublication(*publication) && (publication.ContentSnapshot == nil || len(publication.ContentSnapshot.TagIDs) == 0) {
		slog.WarnContext(ctx, "retry TikTok publication tags missing", "error", ErrPublicationNotReady)
		return nil, ErrPublicationNotReady
	}
	// 历史上下架失败也走新的串行命令，不能经旧阶段绕过推荐锁或改写后续审核版本。
	if publication.Stage == PublicationStageDelistAlbum || publication.Stage == PublicationStageSetOnlineVersion || publication.Stage == PublicationStageListAlbum {
		state, err := s.FindTikTokReleaseState(ctx, dramaID)
		if err != nil {
			slog.ErrorContext(ctx, "read selected version for legacy release recovery failed", "error", err)
			return nil, err
		}
		if publication.Stage == PublicationStageDelistAlbum {
			if state == nil {
				return nil, ErrConflict
			}
			return s.ChangeTikTokRelease(ctx, dramaID, TikTokReleaseInput{Action: "delist", SelectedPublicationID: state.SelectedPublicationID})
		}
		if state != nil && state.SelectedPublicationID == publication.ID {
			return s.ChangeTikTokRelease(ctx, dramaID, TikTokReleaseInput{Action: "list", SelectedPublicationID: state.SelectedPublicationID})
		}
		if !publication.PublishRequested {
			return nil, ErrConflict
		}
		publication.Status, publication.Stage = PublicationStatusApproved, PublicationStageAwaitPublish
		if err := ValidateSelectedRelease(*publication, domain.Drama{}, "publish"); err != nil {
			slog.WarnContext(ctx, "legacy release retry lacks approved frozen version", "error", err)
			return nil, err
		}
		publication.ErrorCode, publication.ErrorMessage, publication.DiagnosticCode = "", "", ""
		publication.LeaseOwner = ""
		publication.LeaseExpiresAt = time.Time{}
		publication.UpdatedAt = time.Now().UTC()
		if err := s.repositories.Content.Publications.UpdateTikTokPublication(ctx, *publication, nil); err != nil {
			slog.ErrorContext(ctx, "restore approved version for explicit release retry failed", "error", err)
			return nil, err
		}
		return s.PublishTikTokPublication(ctx, dramaID, publication.ID)
	}
	if canResolveAlbum {
		// 检查点在核对和受版本保护的写入期间始终保留，进程中断不会触发自动重放。
		publication.ExternalWriteStarted = true
		publication.ErrorMessage = "系统正在核对官方版本并恢复本次冻结内容。"
		if publication.PlatformVersion > 0 {
			ctx = context.WithValue(ctx, albumUpdateRetryKey{}, true)
		}
	}
	if publicationMediaRestartRequired(*publication) || publicationManualTranscodeRestartRequired(*publication) {
		return s.restartPublicationMedia(ctx, *publication)
	}
	now := time.Now().UTC()
	publication.Status = PublicationStatusQueued
	if publication.ErrorCode == "transcode_stalled" {
		// 只重新开启观察窗口，保留原任务、已完成分集和最后进展时间。
		for index := range publication.Transcodes {
			if !publication.Transcodes[index].Ready {
				publication.Transcodes[index].MonitoringStartedAt = now
			}
		}
	}
	publication.Attempts = 0
	if !publication.ExternalWriteStarted {
		publication.ErrorCode = ""
		publication.ErrorMessage = ""
		publication.DiagnosticCode = ""
	}
	publication.NextAttemptAt = now
	publication.UpdatedAt = now
	publication.LeaseOwner = ""
	publication.LeaseExpiresAt = time.Time{}
	return s.executePublicationCall(ctx, publication)
}

// QueueTikTokUnpublish 保留旧调用名称，实际在本次请求内下架已选版本。
func (s *DramaService) QueueTikTokUnpublish(ctx context.Context, dramaID string) (*TikTokPublication, error) {
	state, err := s.FindTikTokReleaseState(ctx, dramaID)
	if err != nil {
		return nil, err
	}
	if state == nil || state.SelectedPublicationID == "" {
		return nil, ErrConflict
	}
	if expected, _ := ctx.Value(expectedPublicationKey{}).(string); expected != "" && expected != state.SelectedPublicationID {
		return nil, ErrConflict
	}
	return s.ChangeTikTokRelease(ctx, dramaID, TikTokReleaseInput{Action: "delist", SelectedPublicationID: state.SelectedPublicationID})
}

// StartTikTokPublicationWorker 启动官方发布任务循环。
func (s *DramaService) StartTikTokPublicationWorker(ctx context.Context) error {
	if s == nil || s.publicationJobs == nil {
		slog.WarnContext(ctx, "start TikTok publication worker rejected", "error", ErrPublicationUnavailable)
		return ErrPublicationUnavailable
	}
	s.publicationJobs.start.Do(func() {
		go func() {
			defer close(s.publicationJobs.done)
			if s.repositories.Content.Distributions != nil {
				finished := make(chan struct{})
				go func() { defer close(finished); s.runDistributions(ctx) }()
				s.publicationJobs.run(ctx)
				<-finished
			} else {
				s.publicationJobs.run(ctx)
			}
		}()
	})
	return nil
}

// WaitTikTokPublicationWorker 等待发布任务循环完成本进程内收尾。
func (s *DramaService) WaitTikTokPublicationWorker(ctx context.Context) error {
	if s == nil || s.publicationJobs == nil {
		slog.WarnContext(ctx, "wait TikTok publication worker rejected", "error", ErrPublicationUnavailable)
		return ErrPublicationUnavailable
	}
	select {
	case <-ctx.Done():

		resultErr := ctx.Err()
		if resultErr != nil {
			slog.ErrorContext(ctx, "wait TikTok publication worker failed", "error", resultErr)
		}
		return resultErr
	case <-s.publicationJobs.done:
		return nil
	}
}

// run 持续领取到期发布任务；有任务立即继续，空闲时等定时器或本进程唤醒信号。
func (runner *publicationJobRunner) run(ctx context.Context) {
	ticker := time.NewTicker(runner.pollInterval)
	defer ticker.Stop()
	reviewTicker := time.NewTicker(runner.reviewPollInterval)
	defer reviewTicker.Stop()
	processReviews := func() {
		cutoff := time.Now().UTC()
		for runner.processNextReview(ctx, cutoff) {
		}
	}
	for {
		// 普通队列持续繁忙时也优先消费已经到点的审核 tick，防止审核扫描饥饿。
		select {
		case <-ctx.Done():
			return
		case <-reviewTicker.C:
			processReviews()
		default:
		}
		processed := runner.processNext(ctx)
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-runner.wake:
		case <-ticker.C:
		case <-reviewTicker.C:
			processReviews()
		}
	}
}

// processNext 通过仓储取得一个发布任务及租约，处理成功领取的任务后返回 true；空队列或领取失败返回 false。
func (runner *publicationJobRunner) processNext(ctx context.Context) bool {
	now := time.Now().UTC()
	publication, err := runner.service.repositories.Content.Publications.ClaimTikTokPublication(
		ctx, runner.workerID, now, now.Add(runner.leaseDuration),
	)
	if err != nil {
		slog.ErrorContext(ctx, "claim TikTok publication failed", "error", err)
	}
	if err != nil || publication == nil {
		return false
	}
	_, _ = runner.executeClaimed(ctx, *publication)
	return true
}

// processNextReview 领取并处理一个审核中任务；仅由独立十分钟扫描周期批量调用。
func (runner *publicationJobRunner) processNextReview(ctx context.Context, cutoff time.Time) bool {
	publication, err := runner.service.repositories.Content.Publications.ClaimTikTokReview(
		ctx, runner.workerID, cutoff, time.Now().UTC().Add(runner.leaseDuration),
	)
	if err != nil {
		slog.ErrorContext(ctx, "claim TikTok review failed", "error", err)
	}
	if err != nil || publication == nil {
		return false
	}
	_, _ = runner.executeClaimed(ctx, *publication)
	return true
}

// processClaimed 根据持久化阶段推进一次官方操作，并保存该步结果供下一次领取继续。
// 业务内容取提交快照，线上可见性取当前数据库；平台账号或媒资空间变化时暂停，避免跨账号写入。
func (runner *publicationJobRunner) processClaimed(ctx context.Context, publication TikTokPublication) {
	ctx = application.WithSubject(ctx, publication.SubjectID)
	// 上次进程可能已发出非幂等写请求；未确认结果前不能重放同一阶段。
	if publication.ExternalWriteStarted && publication.Stage != PublicationStageUpdateAlbum {
		runner.fail(ctx, publication, &PlatformCallError{Code: "external_write_uncertain", OutcomeUnknown: true})
		return
	}
	if publication.ExternalWriteStarted {
		// 查询期间即使归属或依赖校验失败，也不能丢失已发出更新的事实。
		publication.ErrorCode = "external_write_uncertain"
	}
	// 旧任务即便已经排到设置版本或上架，也必须先取得当前版本的人工上线指令。
	if (publication.Stage == PublicationStageSetOnlineVersion || publication.Stage == PublicationStageListAlbum) && !publication.PublishRequested {
		publication.Status, publication.Stage = PublicationStatusApproved, PublicationStageAwaitPublish
		if publication.ReviewStatus != 2 && publication.ReviewStatus != 6 {
			publication.Status, publication.Stage = PublicationStatusUnderReview, PublicationStagePollReview
		}
		runner.persist(ctx, publication, nil, "awaiting_manual_publication")
		return
	}
	publication.Status = PublicationStatusProcessing
	publication.UpdatedAt = time.Now().UTC()
	publicationLog(ctx, slog.LevelInfo, publication, "started", "")

	drama, err := runner.service.repositories.Content.Catalog.FindDrama(ctx, publication.DramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find drama failed", "error", err)
		runner.fail(ctx, publication, err)
		return
	}
	if publication.ContentSnapshot == nil {
		runner.fail(ctx, publication, &PlatformCallError{Code: "publication_snapshot_missing"})
		return
	}
	// Worker 只使用提交时快照；线上投影来自当前数据库，不能被快照中的旧值覆盖。
	frozen := *publication.ContentSnapshot
	frozen.Published = drama.Published
	frozen.PublishStatus = drama.PublishStatus
	drama = &frozen
	owner, err := runner.service.loadPublicationOwner(ctx, publication.OwnerApplicationID)
	if err != nil {
		slog.ErrorContext(ctx, "find application failed", "error", err)
		runner.fail(ctx, publication, &PlatformCallError{Code: "owner_application_unavailable"})
		return
	}
	provider := runner.service.tiktokProviderFactory(owner)
	if publication.OwnerScope != (PublicationOwnerScope{Region: publication.OwnerScope.Region, ClientKey: owner.ClientKey, AccountID: owner.BytePlusAccountID, SpaceName: owner.BytePlusSpaceName}) {
		runner.fail(ctx, publication, &PlatformCallError{Code: "owner_scope_changed"})
		return
	}
	if source, ok := runner.service.media.(MediaPublicationScopeProvider); ok {
		region, space, scopeErr := source.PublicationMediaScope(ctx)
		if scopeErr != nil {
			slog.ErrorContext(ctx, "publication media scope failed", "error", scopeErr)
		}
		if scopeErr != nil || region != publication.OwnerScope.Region || space != publication.OwnerScope.SpaceName {
			runner.fail(ctx, publication, &PlatformCallError{Code: "owner_scope_changed"})
			return
		}
	}
	if provider == nil {
		runner.fail(ctx, publication, &PlatformCallError{Code: "platform_not_configured"})
		return
	}

	switch publication.Stage {
	case PublicationStageUploadCover:
		coverID, callErr := provider.CreateImage(ctx, drama.CoverURL)
		if callErr != nil {
			slog.ErrorContext(ctx, "create image failed", "error", callErr)
			runner.fail(ctx, publication, callErr)
			return
		}
		publication.PlatformCoverID = strings.TrimSpace(coverID)
		if publication.PlatformCoverID == "" {
			runner.fail(ctx, publication, &PlatformCallError{Code: "invalid_platform_response"})
			return
		}
		// 封面与视频独立比较；只改封面时直接更新 Album，不重复登记和转码视频。
		runner.advance(ctx, publication, nextPublicationMediaStage(publication), nil)
	case PublicationStageCreateAlbum:
		if publication.PlatformAlbumID != "" {
			runner.advance(ctx, publication, PublicationStageUpdateAlbum, nil)
			return
		}
		if !runner.beginExternalWrite(ctx, &publication) {
			return
		}
		created, callErr := provider.CreateAlbum(ctx)
		if callErr != nil {
			slog.ErrorContext(ctx, "create album failed", "error", callErr)
			runner.fail(ctx, publication, callErr)
			return
		}
		publication.PlatformAlbumID = strings.TrimSpace(created.AlbumID)
		publication.PlatformVersion = created.Version
		if publication.PlatformAlbumID == "" {
			runner.fail(ctx, publication, &PlatformCallError{Code: "invalid_platform_response"})
			return
		}
		runner.advance(ctx, publication, PublicationStageUpdateAlbum, nil)
	case PublicationStageRegisterVideos:
		runner.registerVideos(ctx, publication, owner, *drama, provider)
	case PublicationStageTranscodeVideos:
		runner.transcodeVideos(ctx, publication, owner, provider)
	case PublicationStageUpdateAlbum:
		var updated UpdateAlbumResult
		var callErr error
		draft := publicationAlbumDraft(owner, publication, *drama)
		if publication.ExternalWriteStarted {
			// 只有显式人工恢复允许携带已确认原版本重试；Worker 和首次同步始终只查询。
			manualRetry, _ := ctx.Value(albumUpdateRetryKey{}).(bool)
			if retry, ok := provider.(TikTokAlbumRetryProvider); manualRetry && ok {
				updated, callErr = retry.RetryAlbumUpdate(ctx, draft)
			} else if recovery, ok := provider.(TikTokAlbumRecoveryProvider); ok {
				updated, callErr = recovery.ResolveAlbumUpdate(ctx, draft)
			} else {
				callErr = &PlatformCallError{Code: "album_update_unconfirmed", OutcomeUnknown: true}
			}
		} else {
			if !runner.beginExternalWrite(ctx, &publication) {
				return
			}
			updated, callErr = provider.UpdateAlbum(ctx, draft)
		}
		if callErr != nil {
			slog.Log(ctx, albumUpdateErrorLevel(callErr), "update album failed", "error", callErr)
			runner.fail(ctx, publication, callErr)
			return
		}
		if updated.Version <= publication.PlatformVersion || !completeEpisodeMapping(drama.Episodes, updated.EpisodeIDsBySequence) {
			runner.fail(ctx, publication, &PlatformCallError{Code: "invalid_episode_mapping"})
			return
		}
		publication.PlatformVersion = updated.Version
		publication.EpisodeIDsBySequence = updated.EpisodeIDsBySequence
		drama.PlatformAlbumID = publication.PlatformAlbumID
		for index := range drama.Episodes {
			drama.Episodes[index].PlatformEpisodeID = updated.EpisodeIDsBySequence[drama.Episodes[index].Number]
		}
		drama.PlatformVersion = updated.Version
		snapshot := *drama
		snapshot.Published = nil
		publication.ContentSnapshot = &snapshot
		publication.Stage = PublicationStageAwaitSubmitReview
		publication.Status = PublicationStatusSynced
		publication.ErrorCode, publication.ErrorMessage, publication.DiagnosticCode, publication.Attempts = "", "", "", 0
		publication.ExternalWriteStarted = false
		publication.NextAttemptAt = time.Time{}
		runner.persist(ctx, publication, drama, "content_synced")
	case PublicationStageSubmitReview:
		if !runner.beginExternalWrite(ctx, &publication) {
			return
		}
		reviewID, callErr := provider.SubmitReview(ctx, publication.PlatformAlbumID, publication.PlatformVersion, publicationPriorityScore(publication.Priority))
		if callErr != nil {
			slog.ErrorContext(ctx, "submit review failed", "error", callErr)
			runner.fail(ctx, publication, callErr)
			return
		}
		publication.ReviewID = strings.TrimSpace(reviewID)
		if publication.ReviewID == "" {
			runner.fail(ctx, publication, &PlatformCallError{Code: "invalid_platform_response"})
			return
		}
		now := time.Now().UTC()
		publication.SubmittedAt = &now
		publication.Stage = PublicationStagePollReview
		publication.Status = PublicationStatusUnderReview
		publication.NextAttemptAt = now.Add(runner.reviewPollDelay(publication))
		runner.persist(ctx, publication, nil, "review_submitted")
	case PublicationStagePollReview, PublicationStagePollAppeal:
		observation, callErr := provider.QueryAlbum(ctx, publication.PlatformAlbumID, publication.PlatformVersion)
		if callErr != nil {
			slog.ErrorContext(ctx, "query TikTok review automatically failed", "error", callErr)
			runner.fail(ctx, publication, callErr)
			return
		}
		if err := applyTikTokReviewObservation(&publication, drama, observation, time.Now().UTC()); err != nil {
			slog.ErrorContext(ctx, "apply automatic review observation failed", "error", err)
			runner.fail(ctx, publication, err)
			return
		}
		if publication.Status == PublicationStatusUnderReview {
			publication.NextAttemptAt = time.Now().UTC().Add(runner.reviewPollDelay(publication))
		}
		runner.persist(ctx, publication, drama, "review_refreshed_automatically")
	case PublicationStageAppealReview, PublicationStageWithdrawReview:
		reviewProvider, supported := provider.(TikTokReviewProvider)
		if !supported {
			runner.fail(ctx, publication, &PlatformCallError{Code: "platform_not_configured"})
			return
		}
		observation, callErr := provider.QueryAlbum(ctx, publication.PlatformAlbumID, publication.PlatformVersion)
		if callErr != nil {
			slog.ErrorContext(ctx, "query review action eligibility failed", "error", callErr)
			runner.fail(ctx, publication, callErr)
			return
		}
		publication.ReviewStatus, publication.ReviewItems = observation.ReviewStatus, observation.ReviewItems
		if publication.Stage == PublicationStageAppealReview {
			if publication.Appeal == nil {
				runner.fail(ctx, publication, &PlatformCallError{Code: "invalid_review_action"})
				return
			}
			state, known := observation.ReviewItems[publication.Appeal.ItemID]
			// 平台已进入申诉流程时复用现有结果，兼容排队期间状态变化及旧失败任务的人工恢复。
			alreadySubmitted := known && state >= 5 && state <= 7
			if !alreadySubmitted {
				if !known || state != 3 || observation.ReviewStatus != 3 {
					// 目标未被确认可申诉时同步整版终态，让旧失败任务可以重新选择被拒项目。
					// 不从整版被拒推断目标被拒，也不重放未获准的申诉请求。
					switch observation.ReviewStatus {
					case 2, 3, 4, 6, 7:
						publication.Stage = PublicationStagePollReview
						if err := applyTikTokReviewObservation(&publication, drama, observation, time.Now().UTC()); err != nil {
							slog.ErrorContext(ctx, "apply ineligible appeal observation failed", "error", err)
							runner.fail(ctx, publication, err)
							return
						}
						runner.persist(ctx, publication, drama, "appeal_skipped_target_unavailable")
						return
					}
					runner.fail(ctx, publication, &PlatformCallError{Code: "review_action_conflict"})
					return
				}
				if !runner.beginExternalWrite(ctx, &publication) {
					return
				}
				if err := reviewProvider.AppealReview(ctx, publication.PlatformAlbumID, publication.ReviewID, *publication.Appeal); err != nil {
					var platformErr *PlatformCallError
					if !errors.As(err, &platformErr) || platformErr.Code != "appeal_already_submitted" {
						slog.ErrorContext(ctx, "appeal review failed", "error", err)
						runner.fail(ctx, publication, err)
						return
					}
					slog.WarnContext(ctx, "appeal already submitted; resume result queries", "error", err, "publication_id", publication.ID)
					alreadySubmitted = true
				}
			}
			now := time.Now().UTC()
			publication.SubmittedAt = &now
			publication.Stage = PublicationStagePollAppeal
			// 已申诉仅证明平台受理过，整版是否通过仍以官方查询为准。
			if err := applyTikTokReviewObservation(&publication, drama, observation, now); err != nil {
				slog.ErrorContext(ctx, "apply resumed appeal observation failed", "error", err)
				runner.fail(ctx, publication, err)
				return
			}
			if publication.Status == PublicationStatusUnderReview {
				publication.NextAttemptAt = now.Add(runner.reviewPollDelay(publication))
			}
			outcome := "appeal_submitted"
			if alreadySubmitted {
				outcome = "appeal_result_resumed"
			}
			runner.persist(ctx, publication, drama, outcome)
		} else {
			if runner.reconcileWithdrawReview(ctx, &publication, drama, observation) {
				return
			}
			switch observation.ReviewStatus {
			case 0:
				// 已有 review_id 但查询仍显示未送审，等待平台状态可见后再继续撤回，不提前解除编辑锁。
				runner.fail(ctx, publication, &PlatformCallError{Code: "review_status_pending", Retryable: true})
				return
			case 1, 3, 5:
				// 审核或申诉仍待处理，继续执行运营已确认的撤回请求。
			default:
				runner.fail(ctx, publication, &PlatformCallError{Code: "unknown_review_status"})
				return
			}
			if !runner.beginExternalWrite(ctx, &publication) {
				return
			}
			if err := reviewProvider.WithdrawReview(ctx, publication.PlatformAlbumID, publication.PlatformVersion); err != nil {
				slog.ErrorContext(ctx, "withdraw review failed", "error", err)
				// 请求期间也可能完成审核；明确拒绝可同步终态，写入结果未知时只有已撤回能确认本次结果。
				latest, queryErr := provider.QueryAlbum(ctx, publication.PlatformAlbumID, publication.PlatformVersion)
				if queryErr != nil {
					slog.ErrorContext(ctx, "query withdraw review recovery failed", "error", queryErr)
				} else {
					publication.ReviewStatus, publication.ReviewItems = latest.ReviewStatus, latest.ReviewItems
					var platformErr *PlatformCallError
					knownOutcome := errors.As(err, &platformErr) && !platformErr.OutcomeUnknown
					if (latest.ReviewStatus == 4 || knownOutcome) && runner.reconcileWithdrawReview(ctx, &publication, drama, latest) {
						return
					}
				}
				runner.fail(ctx, publication, err)
				return
			}
			observation.ReviewStatus = 4
			runner.reconcileWithdrawReview(ctx, &publication, drama, observation)
		}
	case PublicationStageSetOnlineVersion:
		if callErr := provider.SetOnlineVersion(ctx, publication.PlatformAlbumID, publication.PlatformVersion); callErr != nil {
			confirmed, queryErr := runner.confirmOfficialOnlineState(ctx, provider, publication, false)
			if queryErr != nil {
				slog.ErrorContext(ctx, "query online version recovery failed", "error", queryErr)
			}
			if !confirmed {
				runner.fail(ctx, publication, callErr)
				return
			}
			slog.WarnContext(ctx, "set online version returned error but official state is confirmed", "error", callErr,
				"publication_id", publication.ID, "platform_version", publication.PlatformVersion)
		}
		runner.advance(ctx, publication, PublicationStageListAlbum, nil)
	case PublicationStageListAlbum:
		if callErr := provider.SetAlbumListed(ctx, publication.PlatformAlbumID, true); callErr != nil {
			confirmed, queryErr := runner.confirmOfficialOnlineState(ctx, provider, publication, true)
			if queryErr != nil {
				slog.ErrorContext(ctx, "query album listing recovery failed", "error", queryErr)
			}
			if !confirmed {
				runner.fail(ctx, publication, callErr)
				return
			}
			slog.WarnContext(ctx, "set album listed returned error but official state is confirmed", "error", callErr,
				"publication_id", publication.ID, "platform_version", publication.PlatformVersion)
		}
		now := time.Now().UTC()
		publication.PlatformPublishStatus = 1
		publication.OnlineAt = &now
		// 上架立即对拥有方生效，额外小程序授权不参与全局上线判定。
		online := *drama
		online.Published = nil
		online.PublishStatus = "published"
		online.OwnerApplicationID = publication.OwnerApplicationID
		online.PlatformVersion = publication.PlatformVersion
		online.PlatformAlbumID = publication.PlatformAlbumID
		// 重上架使用原版本映射，不能沿用新草稿的分集标识。
		for i := range online.Episodes {
			if id := publication.EpisodeIDsBySequence[online.Episodes[i].Number]; id != "" {
				online.Episodes[i].PlatformEpisodeID = id
			}
		}
		if drama.Published != nil {
			online.AuthorizedApplicationIDs = append([]string(nil), drama.Published.AuthorizedApplicationIDs...)
		}
		// 媒资拥有方历史键独立于应用身份，拥有方对应的官方应用仍应可直接播放。
		if owner.TikTokAppID != "" && owner.TikTokAppID != online.OwnerApplicationID && !slices.Contains(online.AuthorizedApplicationIDs, owner.TikTokAppID) {
			online.AuthorizedApplicationIDs = append(online.AuthorizedApplicationIDs, owner.TikTokAppID)
		}
		drama.Published = &online
		drama.PublishStatus = "published"
		publication.Status, publication.Stage = PublicationStatusOnline, PublicationStageDone
		runner.persist(ctx, publication, drama, "online")
	case PublicationStageDelistAlbum:
		// 旧排队任务与自动恢复也必须先通过推荐门禁，拒绝时不调用平台、不清除线上快照。
		if err := runner.service.repositories.Content.Publications.CheckTikTokUnpublish(ctx, publication.DramaID); err != nil {
			runner.fail(ctx, publication, err)
			return
		}
		// 只有官方下架调用成功后才清除本地线上快照，不能以“已排队”提前隐藏线上事实。
		if callErr := provider.SetAlbumListed(ctx, publication.PlatformAlbumID, false); callErr != nil {
			slog.ErrorContext(ctx, "set album listed failed", "error", callErr)
			runner.fail(ctx, publication, callErr)
			return
		}
		publication.Stage = PublicationStageDone
		publication.Status = PublicationStatusOffline
		publication.PlatformPublishStatus = 2
		drama.PublishStatus = "draft"
		drama.Published = nil
		runner.persist(ctx, publication, drama, "offline")
	default:
		runner.fail(ctx, publication, &PlatformCallError{Code: "invalid_publication_stage"})
	}
}

// confirmOfficialOnlineState 在上线写入返回失败时只读核对官方事实；目标版本必须已经在线，
// requireListed 为 true 时还要求 Album 处于上架状态，避免把真实拒绝误当作幂等成功。
func (runner *publicationJobRunner) confirmOfficialOnlineState(ctx context.Context, provider TikTokMediaProvider, publication TikTokPublication, requireListed bool) (bool, error) {
	observation, err := provider.QueryAlbum(ctx, publication.PlatformAlbumID, publication.PlatformVersion)
	if err != nil {
		return false, err
	}
	if observation.OnlineVersion != publication.PlatformVersion {
		return false, nil
	}
	return !requireListed || observation.PublishStatus == 1, nil
}

// mediaPollDelay 为媒资异步任务提供十秒轮询间隔；更短的配置间隔用于测试。
func (runner *publicationJobRunner) mediaPollDelay() time.Duration {
	return runner.mediaPollInterval
}

// advance 保存当前阶段的确认结果并在请求内继续；仅真实媒体等待交给结果查询。
func (runner *publicationJobRunner) advance(ctx context.Context, publication TikTokPublication, nextStage string, drama *domain.Drama) {
	publication.Stage = nextStage
	publication.Status = PublicationStatusQueued
	publication.ErrorCode = ""
	publication.ErrorMessage = ""
	publication.DiagnosticCode = ""
	// Attempts 只描述当前阶段的连续失败；已确认完成的阶段不能消耗下一阶段的重试额度。
	publication.Attempts = 0
	publication.NextAttemptAt = time.Now().UTC()
	runner.persist(ctx, publication, drama, "stage_completed")
	runner.service.notifyPublicationWorker()
}

// fail 分类本次失败并决定退避重试或终止；结果未知的非幂等外部写必须暂停。
// 仅官方已明确失败的媒资阶段解除内容编辑锁，其他情况保留恢复所需的检查点。
func (runner *publicationJobRunner) fail(ctx context.Context, publication TikTokPublication, cause error) {
	if cause != nil {
		var failure *PlatformCallError
		if errors.Is(cause, homepage.ErrDramaRecommended) {
			slog.WarnContext(ctx, "TikTok unpublish recommendation check rejected", "publication_id", publication.ID, "error", cause)
		} else if errors.As(cause, &failure) && failure.Code == "album_update_unconfirmed" {
			slog.WarnContext(ctx, "confirm TikTok publication pending", "publication_id", publication.ID, "error", cause)
		} else if errors.As(cause, &failure) && failure.Code == "transcode_stalled" {
			slog.WarnContext(ctx, "TikTok transcode progress stalled", "publication_id", publication.ID, "error", cause)
		} else {
			slog.ErrorContext(ctx, "process TikTok publication failed", "publication_id", publication.ID, "error", cause)
		}
	}
	publication.Attempts++
	code, retryable := publicationFailure(cause)
	originalCode := code
	if publication.ExternalWriteStarted {
		var platformErr *PlatformCallError
		if publication.ErrorCode == "external_write_uncertain" || !errors.As(cause, &platformErr) || platformErr.OutcomeUnknown || code == "invalid_episode_mapping" || code == "invalid_platform_response" || code == "album_create_rejected" || code == "review_submit_rejected" {
			// 仅明确标为可重试的版本查询可以自动恢复；下一次仍只 GET 原版本。
			canRetryQuery := publication.Stage == PublicationStageUpdateAlbum && errors.As(cause, &platformErr) &&
				platformErr.Code == "album_update_unconfirmed" && platformErr.Retryable
			code, retryable = "external_write_uncertain", canRetryQuery
		}
	}
	publication.ErrorCode = code
	publication.DiagnosticCode = ""
	if code == "external_write_uncertain" && originalCode != code {
		publication.DiagnosticCode = originalCode
	}
	publication.ErrorMessage = publicationFriendlyMessage(code, retryable)
	if code == "external_write_uncertain" && publication.Stage == PublicationStageUpdateAlbum {
		publication.ErrorMessage = "尚未确认官方版本。系统会保留已完成进度，核对内容一致后完成同步。"
	}
	// Album Update 已经发出后不自动重写；人工恢复必须先核对并由官方原版本条件保护。
	// 自动退避仅用于媒体或审核结果查询，未知 Album 更新保留检查点并等待人工命令。
	if retryable && (publication.Stage == PublicationStagePollReview || publication.Stage == PublicationStagePollAppeal || publication.Stage == PublicationStageRegisterVideos || publication.Stage == PublicationStageTranscodeVideos) && publication.Attempts < runner.maxAttempts {
		publication.Status = PublicationStatusQueued
		delay := runner.retryDelay(publication.Attempts)
		if publication.Stage == PublicationStagePollReview || publication.Stage == PublicationStagePollAppeal {
			// 审核查询失败仍由独立十分钟扫描器处理，不能落回高频发布队列。
			publication.Status = PublicationStatusUnderReview
			delay = runner.reviewPollDelay(publication)
		}
		publication.NextAttemptAt = time.Now().UTC().Add(delay)
	} else {
		publication.Status = PublicationStatusFailed
		publication.NextAttemptAt = time.Now().UTC()
	}
	var editable *domain.Drama
	if publicationMediaRestartRequired(publication) {
		// 仅明确的媒资失败解除编辑锁；超时、响应不完整和外部写未知仍沿用原恢复门禁。
		var err error
		editable, err = runner.service.repositories.Content.Catalog.FindDrama(ctx, publication.DramaID)
		if err != nil {
			slog.ErrorContext(ctx, "find drama failed", "error", err)
			return
		}
		editable.PublishStatus = "draft"
	}
	if state := publicationCall(ctx); state != nil {
		state.err = cause
	}
	runner.persist(ctx, publication, editable, "failed")
}

// publicationMediaRestartRequired 只承认官方已明确失败的媒体任务，不把未知结果当作可重建任务。
func publicationMediaRestartRequired(publication TikTokPublication) bool {
	return publication.Status == PublicationStatusFailed && !publication.ExternalWriteStarted &&
		((publication.Stage == PublicationStageTranscodeVideos && publication.ErrorCode == "transcode_failed") ||
			(publication.Stage == PublicationStageRegisterVideos && publication.ErrorCode == "video_registration_rejected"))
}

// publicationManualTranscodeRestartRequired 允许人工重试明确被拒绝且没有取得任务 ID 的转码批次。
// 旧键可能与平台上其他请求冲突，只有新发布任务才能使用新的幂等键；已有任务 ID 仍须继续查询。
func publicationManualTranscodeRestartRequired(publication TikTokPublication) bool {
	if publication.Status != PublicationStatusFailed || publication.ExternalWriteStarted ||
		publication.Stage != PublicationStageTranscodeVideos || len(publication.Transcodes) == 0 ||
		(publication.ErrorCode != "transcode_idempotency_conflict" &&
			!(publication.ErrorCode == "api_rejected" && publication.TranscodeKeySeed == "")) {
		return false
	}
	for _, batch := range publication.Transcodes {
		if !batch.Ready && batch.TaskID != "" {
			return false
		}
	}
	return true
}

// persist 保存发布进度及可选短剧投影；仅结果未知的外部写保留检查点标志，存储失败不输出成功事件。
func (runner *publicationJobRunner) persist(ctx context.Context, publication TikTokPublication, drama *domain.Drama, outcome string) {
	publication.UpdatedAt = time.Now().UTC()
	state := publicationCall(ctx)
	if state != nil && publication.Status == PublicationStatusQueued && !publication.NextAttemptAt.After(publication.UpdatedAt) && state.err == nil {
		publication.Status = PublicationStatusProcessing
	}
	if publication.Status == PublicationStatusProcessing {
		publication.LeaseExpiresAt = publication.UpdatedAt.Add(runner.leaseDuration)
	}
	if publication.ErrorCode != "external_write_uncertain" {
		publication.ExternalWriteStarted = false
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := runner.service.repositories.Content.Publications.UpdateTikTokPublication(saveCtx, publication, drama); err != nil {
		slog.ErrorContext(saveCtx, "update TikTok publication failed", "error", err)
		if state != nil {
			state.err = err
		}
		return
	}
	publication.Revision++
	if publication.Status != PublicationStatusProcessing {
		publication.LeaseOwner = ""
		publication.LeaseExpiresAt = time.Time{}
	}
	if state != nil {
		state.result = &publication
	}

	publicationLog(ctx, slog.LevelInfo, publication, outcome, publication.ErrorCode)
}

// beginExternalWrite 先确认持久化检查点成功，之后才允许发出没有官方幂等键的写请求。
func (runner *publicationJobRunner) beginExternalWrite(ctx context.Context, publication *TikTokPublication) bool {
	if err := runner.service.repositories.Content.Publications.BeginTikTokExternalWrite(ctx, publication); err != nil {
		slog.ErrorContext(ctx, "begin TikTok external write failed", "error", err)
		if state := publicationCall(ctx); state != nil {
			state.err = err
		}
		return false
	}
	return true
}

// retryDelay 按失败尝试数计算指数退避，避免平台故障时密集调用；延迟最多三十分钟。
func (runner *publicationJobRunner) retryDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return runner.retryBaseDelay
	}
	delay := runner.retryBaseDelay * time.Duration(1<<min(attempt-1, 6))
	if delay > 30*time.Minute {
		return 30 * time.Minute
	}
	return delay
}

// reviewPollDelay 返回固定审核查询间隔，避免不同任务阶段产生不可预期的平台请求频率。
func (runner *publicationJobRunner) reviewPollDelay(_ TikTokPublication) time.Duration {
	return runner.reviewPollInterval
}

// resolvePublicationOwner 只读取选定主体的发布方，分发目标由独立任务管理。
func (s *DramaService) resolvePublicationOwner(ctx context.Context, id string) (application.Application, error) {
	owner, err := s.loadPublicationOwner(ctx, id)
	if err != nil {
		return application.Application{}, err
	}
	if !publicationApplicationReady(owner, true) {
		slog.WarnContext(ctx, "subject publisher prerequisites rejected", "error", ErrPublicationNotReady)
		return application.Application{}, ErrPublicationNotReady
	}
	return owner, nil
}

// loadPublicationOwner 统一发布与分发的凭据来源，撤权不额外套用新发布状态门禁。
func (s *DramaService) loadPublicationOwner(ctx context.Context, id string) (application.Application, error) {
	lookup := s.repositories.Content.OwnerApplication
	if lookup == nil {
		slog.WarnContext(ctx, "subject publisher resolver unavailable", "error", ErrPublicationUnavailable)
		return application.Application{}, ErrPublicationUnavailable
	}
	owner, err := lookup(ctx, id)
	if err != nil {
		if errors.Is(err, subject.ErrNotConfigured) || errors.Is(err, subject.ErrNotFound) {
			slog.WarnContext(ctx, "read publication owner rejected", "error", err)
		} else {
			slog.ErrorContext(ctx, "read publication owner failed", "error", err)
		}
	}
	return owner, err
}

// publicationApplicationReady 要求应用启用且有 ClientKey；拥有方还需具备凭据来源和 BytePlus 账号、空间。
func publicationApplicationReady(app application.Application, requireSecret bool) bool {
	if app.Status != application.StatusActive || strings.TrimSpace(app.ClientKey) == "" {
		return false
	}
	if !requireSecret {
		return true
	}
	return (strings.TrimSpace(app.ClientSecret) != "" || strings.TrimSpace(app.SecretRef) != "") &&
		strings.TrimSpace(app.BytePlusAccountID) != "" && strings.TrimSpace(app.BytePlusSpaceName) != ""
}

// dramaPublicationReady 要求简介、封面、至少一个标签和全部分集媒资就绪，避免创建 Album 后才被官方拒绝；应用授权由发布前置检查负责。
func dramaPublicationReady(drama domain.Drama) bool {
	return strings.TrimSpace(drama.Description) != "" && strings.TrimSpace(drama.CoverURL) != "" && len(drama.TagIDs) > 0 && dramaMediaReady(drama)
}

// publicationAlbumDraft 将冻结内容和已取得的平台标识映射为官方 Album 更新参数，保持分集序号与官方映射对应。
func publicationAlbumDraft(owner application.Application, publication TikTokPublication, drama domain.Drama) AlbumDraft {
	episodes := make([]AlbumEpisodeDraft, 0, len(drama.Episodes))
	for _, episode := range drama.Episodes {
		// 渠道无关的冻结内容不保存官方身份；以任务内已确认的映射恢复更新及只读核对参数。
		platformID := publication.EpisodeIDsBySequence[episode.Number]
		episodes = append(episodes, AlbumEpisodeDraft{
			Sequence: episode.Number, Title: episode.Title, CoverID: publication.PlatformCoverID,
			BytePlusVID: episode.VID, PlatformEpisodeID: platformID,
		})
	}
	return AlbumDraft{
		AlbumStatus: map[string]int{domain.DramaStatusUnpublished: 1, domain.DramaStatusUpdating: 2, domain.DramaStatusCompleted: 3}[drama.Status],
		ClientKey:   owner.ClientKey, AlbumID: publication.PlatformAlbumID, Version: publication.PlatformVersion,
		Language: drama.Language, Title: drama.Title, Description: drama.Description,
		CoverID: publication.PlatformCoverID, Year: drama.Year, DramaType: drama.DramaType,
		TagIDs: append([]string(nil), drama.TagIDs...), Episodes: episodes,
	}
}

// completeEpisodeMapping 检查官方返回的映射覆盖所有本地集号且标识非空；不要求平台响应排除额外集号。
func completeEpisodeMapping(episodes []domain.Episode, mapping map[int]string) bool {
	if len(episodes) == 0 || len(mapping) < len(episodes) {
		return false
	}
	for _, episode := range episodes {
		if strings.TrimSpace(mapping[episode.Number]) == "" {
			return false
		}
	}
	return true
}

// normalizePublicationPriority 统一任务内部优先级表示，空值沿用普通优先级；合法枚举由请求入口保证。
func normalizePublicationPriority(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return PublicationPriorityNormal
	}
	return value
}

// publicationPriorityScore 把内部优先级映射为官方送审数值：紧急为 1，普通为 2。
func publicationPriorityScore(priority string) int {
	if normalizePublicationPriority(priority) == PublicationPriorityUrgent {
		return 1
	}
	return 2
}

// publicationFailure 保留适配器的稳定分类与可重试标志，未分类依赖错误默认允许有限重试。
func publicationFailure(err error) (string, bool) {
	if errors.Is(err, homepage.ErrDramaRecommended) {
		return "publication_in_recommendations", false
	}
	var platformErr *PlatformCallError
	if errors.As(err, &platformErr) {
		return strings.TrimSpace(platformErr.Code), platformErr.Retryable
	}
	return "dependency_error", true
}

// publicationFriendlyMessage 根据稳定错误分类生成运营提示，不把平台响应或底层异常直接暴露到页面。
func publicationFriendlyMessage(code string, retryable bool) string {
	switch code {
	case "publication_in_recommendations":
		return "请先从官方轮播或热播中移除该短剧，再重试下架。"
	case "urgent_review_quota_exceeded":
		return "组织今日加急审核额度已用完，请选择普通审核重新提交，或等待加急额度恢复后再试。"
	case "review_action_conflict":
		return "官方审核状态已变化或尚未返回该项目状态，请刷新核对后重试。"
	case "review_status_pending":
		return "官方尚未显示已提交的审核状态，系统会继续查询后再撤回；若任务已暂停，可重试继续查询。"
	case "unknown_review_status":
		return "官方返回了无法识别的审核状态，请联系管理员核对；当前内容仍保持冻结。"
	case "appeal_result_pending":
		return "申诉尚未取得最终结果，可从失败步骤继续查询；不会重复提交申诉。"
	case "transcode_failed", "video_registration_rejected":
		return "官方视频处理已失败，可先修改或替换素材，再重新同步。重新同步会创建新媒体任务，已上线版本不受影响。"
	case "transcode_stalled":
		return "官方转码长时间没有新增完成分集，已暂停自动查询。可检查原任务继续查询；若仍无进展，请核对 BytePlus 转码结果及回调投递记录。"
	case "transcode_idempotency_conflict":
		return "官方转码幂等键与已有请求冲突。请核对原任务后手动重试，系统将创建新的发布任务。"
	case "owner_scope_changed":
		return "应用的官方账号或媒资空间已改变，当前发布任务已暂停。请恢复提交时的应用配置后重试。"
	case "external_write_uncertain":
		return "TikTok 可能已受理本步骤，但系统尚未确认结果。任务已暂停以避免重复创建或送审，请联系管理员核对官方结果后恢复。"
	case "platform_not_configured", "owner_application_unavailable", "language_mapping_missing":
		return "TikTok 发布配置尚未完成，请联系管理员检查应用凭据和内容语言配置。"
	case "invalid_episode_mapping", "invalid_platform_response":
		return "TikTok 返回的剧集信息不完整，请稍后重试；若仍失败请联系管理员。"
	case "invalid_video_response":
		return "TikTok 返回的视频信息不完整，尚未确认处理结果。任务会查询原视频状态；若任务已暂停，可从失败步骤重试查询。"
	case "invalid_publication_stage":
		return "当前发布任务状态异常，请联系管理员协助恢复。"
	}
	if retryable {
		return "TikTok 暂时未完成本步骤，请稍后重试或刷新处理结果。"
	}
	return "TikTok 未能完成本次发布，请检查应用和内容配置后重试。"
}

// notifyPublicationWorker 非阻塞唤醒本进程发布循环；持久化队列仍负责跨实例发现和崩溃恢复。
func (s *DramaService) notifyPublicationWorker() {
	if s == nil || s.publicationJobs == nil {
		return
	}
	select {
	case s.publicationJobs.wake <- struct{}{}:
	default:
	}
}

// publicationLog 记录发布阶段的进度和稳定分类，不记录内容快照、平台凭据或素材地址；具体异常由失败处单独记录。
func publicationLog(ctx context.Context, level slog.Level, publication TikTokPublication, outcome, errorCode string) {
	fields := append(requestmeta.LogFields(ctx),
		"publication_id", publication.ID,
		"drama_id", publication.DramaID,
		"owner_application_id", publication.OwnerApplicationID,
		"stage", publication.Stage,
		"status", publication.Status,
		"review_status", publication.ReviewStatus,
		"platform_version", publication.PlatformVersion,
		"attempt", publication.Attempts,
		"outcome", outcome,
		"error_code", errorCode,
	)
	slog.Log(ctx, level, "tiktok publication", fields...)
}

// publicationInvariantError 仅供适配器和仓储在无法满足状态合同的情况下安全分类错误。
func publicationInvariantError(code string) error {
	return fmt.Errorf("%w: %s", ErrConflict, strings.TrimSpace(code))
}

// replaceableTikTokVersion 只允许替换尚未送审或已审核待发布且无执行中的旧版本；审核中继续处理固定版本。
func replaceableTikTokVersion(p TikTokPublication) bool {
	return !p.ExternalWriteStarted && !publicationLeaseActive(p) && ((p.Status == PublicationStatusSynced && p.Stage == PublicationStageAwaitSubmitReview) || (p.Status == PublicationStatusApproved && p.Stage == PublicationStageAwaitPublish))
}
