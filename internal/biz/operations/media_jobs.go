package operations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
)

// MediaJobConfig 配置业务 Worker 的轮询、租约、重试及并发策略；测试可缩短执行间隔。
// 部署配置不暴露这些字段；零值会使用本文件中的稳定默认策略。
type MediaJobConfig struct {
	// PollInterval 控制空闲 Worker 扫描共享 MySQL 队列的频率。
	PollInterval time.Duration
	// MaintenanceInterval 控制终态任务和孤立媒资清理的扫描频率。
	MaintenanceInterval time.Duration
	// TerminalRetention 控制成功或失败任务在清理前保留的时间。
	TerminalRetention time.Duration
	// StaleRunningAfter 是缺失有效租约和截止时间时识别异常运行任务的最终保护线。
	StaleRunningAfter time.Duration
	// LeaseDuration 控制单次数据库任务所有权；运行期间会按三分之一周期续租。
	LeaseDuration time.Duration
	// RetryBaseDelay 是可重试依赖错误的指数退避起点。
	RetryBaseDelay time.Duration
	// MaxAttempts 是自动处理上限；运营人员仍可对终态失败任务手动重试。
	MaxAttempts int
	// WorkerID 唯一标识当前进程，用作共享数据库租约所有者。
	WorkerID string
	// WorkerConcurrency 是当前实例内并行消费共享队列的 Goroutine 数。
	WorkerConcurrency int
	// ReconcileInterval 控制已绑定媒资的远端状态校准频率。
	ReconcileInterval time.Duration
}

var errMediaJobLeaseLost = errors.New("media job lease lost")

const (
	// 以下策略只约束媒资任务的内部执行方式，不属于部署或产品配置。
	defaultMediaJobPollInterval   = 2 * time.Second
	defaultMediaJobMaintenance    = time.Hour
	defaultMediaJobRetention      = 7 * 24 * time.Hour
	defaultMediaJobStaleRunning   = 6 * time.Hour
	defaultMediaJobLeaseDuration  = 2 * time.Minute
	defaultMediaJobRetryBaseDelay = 30 * time.Second
	defaultMediaJobMaxAttempts    = 3
	defaultMediaWorkerConcurrency = 1
	defaultMediaReconcileInterval = 10 * time.Minute
)

// mediaJobRunner 执行数据库持久化队列；业务事实和跨实例所有权都保存在 MySQL。
type mediaJobRunner struct {
	// service 提供媒资用例与窄仓储端口。
	service *DramaService
	// pollInterval 控制队列轮询频率。
	pollInterval time.Duration
	// maintenanceInterval 控制终态数据清理频率。
	maintenanceInterval time.Duration
	// terminalRetention 控制终态任务保留时间。
	terminalRetention time.Duration
	// staleRunningAfter 是异常任务恢复的最终兜底阈值。
	staleRunningAfter time.Duration
	// leaseDuration 控制每次任务租约及续租周期。
	leaseDuration time.Duration
	// retryBaseDelay 控制自动重试的指数退避起点。
	retryBaseDelay time.Duration
	// maxAttempts 控制自动尝试上限。
	maxAttempts int
	// workerID 是当前进程的数据库租约所有者标识。
	workerID string
	// workerConcurrency 控制当前实例的队列消费 Goroutine 数。
	workerConcurrency int
	// reconcileInterval 控制已绑定媒资下一次 VOD 观察时间。
	reconcileInterval time.Duration
	// wake 只缩短当前进程中新任务的等待时间，不承载任务事实。
	// 其他实例写入的任务仍由定时轮询共享 MySQL 发现。
	wake chan struct{}
	// reconcileWake 让当前实例立即处理已经在数据库中标记为到期的 VOD 校准任务。
	reconcileWake chan struct{}
	// start 只保护当前 Go 对象的生命周期，不能替代数据库任务租约。
	start sync.Once
	// state 只保护 started/done 两个进程生命周期字段。
	state sync.RWMutex
	// 表示组件是否已经启动。
	started bool
	// done 只用于等待当前进程内 goroutine 完成。
	done chan struct{}
}

// ConfigureMediaJobs 校验 Worker 依赖并补齐内部执行策略，构造队列与维护循环；调用后仍需显式启动 Worker。
// 这是进程装配入口，因此在此校验并发数、重试上限和租约配置，不依赖 HTTP 参数校验。
func (s *DramaService) ConfigureMediaJobs(config MediaJobConfig) error {
	if s == nil || s.repositories.Content.Catalog == nil || s.repositories.Media.Jobs == nil || s.media == nil {
		slog.Warn("configure media jobs rejected", "error", ErrMediaNotConfigured)
		return ErrMediaNotConfigured
	}
	if config.PollInterval <= 0 {
		config.PollInterval = defaultMediaJobPollInterval
	}
	if config.MaintenanceInterval <= 0 {
		config.MaintenanceInterval = defaultMediaJobMaintenance
	}
	if config.TerminalRetention <= 0 {
		config.TerminalRetention = defaultMediaJobRetention
	}
	if config.StaleRunningAfter <= 0 {
		config.StaleRunningAfter = defaultMediaJobStaleRunning
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = defaultMediaJobLeaseDuration
	}
	if config.LeaseDuration < time.Minute {
		config.LeaseDuration = time.Minute
	}
	if config.RetryBaseDelay <= 0 {
		config.RetryBaseDelay = defaultMediaJobRetryBaseDelay
	}
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = defaultMediaJobMaxAttempts
	}
	if config.MaxAttempts > 10 {
		slog.Warn("configure media jobs rejected", "error", ErrInvalidInput)
		return ErrInvalidInput
	}
	config.WorkerID = strings.TrimSpace(config.WorkerID)
	if config.WorkerID == "" {
		workerID, err := secureID("media-worker")
		if err != nil {
			slog.Error("secure id failed", "error", err)
			return err
		}
		config.WorkerID = workerID
	}
	if config.WorkerConcurrency <= 0 {
		config.WorkerConcurrency = defaultMediaWorkerConcurrency
	}
	if config.WorkerConcurrency > 16 {
		slog.Warn("configure media jobs rejected", "error", ErrInvalidInput)
		return ErrInvalidInput
	}
	if config.ReconcileInterval <= 0 {
		config.ReconcileInterval = defaultMediaReconcileInterval
	}
	s.mediaJobs = &mediaJobRunner{
		service: s, pollInterval: config.PollInterval,
		maintenanceInterval: config.MaintenanceInterval, terminalRetention: config.TerminalRetention,
		staleRunningAfter: config.StaleRunningAfter,
		leaseDuration:     config.LeaseDuration, retryBaseDelay: config.RetryBaseDelay, maxAttempts: config.MaxAttempts,
		workerID:          config.WorkerID,
		workerConcurrency: config.WorkerConcurrency,
		reconcileInterval: config.ReconcileInterval,
		wake:              make(chan struct{}, config.WorkerConcurrency),
		reconcileWake:     make(chan struct{}, 1),
		done:              make(chan struct{}),
	}
	return nil
}

// StartMediaWorker 在当前 Service 实例中仅启动一次 Worker，生命周期随 ctx 取消；跨实例任务互斥依赖数据库租约。
func (s *DramaService) StartMediaWorker(ctx context.Context) error {
	if s == nil || s.mediaJobs == nil {
		slog.WarnContext(ctx, "start media worker rejected", "error", ErrMediaNotConfigured)
		return ErrMediaNotConfigured
	}
	s.mediaJobs.start.Do(func() {
		s.mediaJobs.state.Lock()
		s.mediaJobs.started = true
		s.mediaJobs.state.Unlock()
		go func() {
			defer close(s.mediaJobs.done)
			s.mediaJobs.run(ctx)
		}()
	})
	return nil
}

// WaitMediaWorker 等待已启动的媒体任务及其取消后持久化收尾完成。
func (s *DramaService) WaitMediaWorker(ctx context.Context) error {
	if s == nil || s.mediaJobs == nil {
		slog.WarnContext(ctx, "wait media worker rejected", "error", ErrMediaNotConfigured)
		return ErrMediaNotConfigured
	}

	resultErr := s.mediaJobs.wait(ctx)
	if resultErr != nil {
		slog.ErrorContext(ctx, "wait failed", "error", resultErr)
	}
	return resultErr
}

// wait 等待本进程全部 Worker 退出；尚未启动时直接返回，等待方 ctx 超时只结束等待。
func (runner *mediaJobRunner) wait(ctx context.Context) error {
	runner.state.RLock()
	started := runner.started
	done := runner.done
	runner.state.RUnlock()
	if !started {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():

		resultErr := ctx.Err()
		if resultErr != nil {
			slog.ErrorContext(ctx, "wait failed", "error", resultErr)
		}
		return resultErr
	}
}

// QueueDirectEpisodeMedia 接收入口已校验的直传结果，按幂等键复用任务或建立视频处理任务。
// 先核对资产预留归属，再固化原 VID 和处理截止时间；确认上传与任务入队通过同一仓储操作保存。
func (s *DramaService) QueueDirectEpisodeMedia(
	ctx context.Context,
	episodeID, idempotencyKey string,
	input DirectMediaUploadInput,
) (*MediaJob, error) {
	if s == nil || s.mediaJobs == nil {
		slog.WarnContext(ctx, "queue direct episode media rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	if _, ok := s.media.(DirectMediaProvider); !ok {
		slog.WarnContext(ctx, "queue direct episode media rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	policy, ok := s.media.(MediaProcessingPolicyProvider)
	if !ok {
		slog.WarnContext(ctx, "queue direct episode media rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}

	// 优先恢复重复确认的结果；成功返回后，即使分集后来改变，也不创建第二个任务。
	{
		existing, err := s.repositories.Media.Jobs.FindMediaJobByIdempotencyKey(ctx, idempotencyKey)
		if err != nil {
			slog.ErrorContext(ctx, "find media job by idempotency key failed", "error", err)
		}
		if err == nil {
			if existing.EpisodeID != episodeID || existing.ID != input.UploadID ||
				existing.RemoteVID != input.VID || existing.FileName != input.FileName || existing.Size != input.Size {
				slog.WarnContext(ctx, "queue direct episode media rejected", "error", ErrConflict)
				return nil, ErrConflict
			}
			return s.enrichMediaJob(ctx, existing), nil
		} else if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
	}
	episode, err := s.repositories.Content.Catalog.FindEpisode(ctx, episodeID)
	if err != nil {
		slog.ErrorContext(ctx, "find episode failed", "error", err)
		return nil, err
	}
	drama, err := s.repositories.Content.Catalog.FindDrama(ctx, episode.DramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find drama failed", "error", err)
		return nil, err
	}
	mutable, err := s.episodeContentMutable(ctx, *drama, *episode)
	if err != nil {
		slog.ErrorContext(ctx, "check direct upload confirmation boundary failed", "error", err)
		return nil, err
	}
	if !mutable {
		slog.WarnContext(ctx, "queue direct episode media rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	reserved, err := s.repositories.Media.Jobs.FindMediaJob(ctx, input.UploadID)
	if err != nil {
		slog.ErrorContext(ctx, "find reserved upload task failed", "error", err)
		return nil, err
	}
	if reserved.Kind != MediaJobUpload || reserved.DramaID != drama.ID || reserved.EpisodeID != episodeID ||
		reserved.FileName != input.FileName || reserved.Size != input.Size ||
		(reserved.RemoteVID != "" && reserved.RemoteVID != input.VID) {
		return nil, ErrConflict
	}
	if reserved.Status != MediaJobReserved && reserved.Status != MediaJobUploading {
		if reserved.RemoteVID == input.VID && reserved.Status != MediaJobFailed {
			return s.enrichMediaJob(ctx, reserved), nil
		}
		return nil, ErrConflict
	}
	processingTimeout, err := policy.ProcessingTimeout(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "processing timeout failed", "error", err)
	}
	if err != nil || processingTimeout <= 0 {
		slog.WarnContext(ctx, "queue direct episode media rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	now := time.Now().UTC()
	job := reserved
	job.IdempotencyKey = idempotencyKey
	job.RemoteVID, job.UploadRequestID = input.VID, input.UploadRequestID
	job.Status = MediaJobQueued
	job.ProcessingStartedAt, job.ProcessingDeadlineAt = now, now.Add(processingTimeout)
	job.NextAttemptAt, job.UpdatedAt = now, now
	if err := s.repositories.Media.Jobs.ConfirmMediaUpload(ctx, job); err != nil {
		slog.ErrorContext(ctx, "confirm episode upload task failed", "error", err)
		return nil, err
	}
	s.mediaJobs.notify()
	return &job, nil
}

// FindMediaJob 按内部任务 ID 读取状态；成功任务尽力附带当前分集，分集读取失败不隐藏已保存的任务结果。
func (s *DramaService) FindMediaJob(ctx context.Context, id string) (*MediaJob, error) {
	if s == nil || s.repositories.Media.Jobs == nil || s.repositories.Content.Catalog == nil {
		slog.WarnContext(ctx, "find media job rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	job, err := s.repositories.Media.Jobs.FindMediaJob(ctx, strings.TrimSpace(id))
	if err != nil {
		slog.ErrorContext(ctx, "find media job failed", "error", err)
		return nil, err
	}
	return s.enrichMediaJob(ctx, job), nil
}

// ListDramaMediaJobs 先确认短剧存在，再按集号返回每集的最后一条任务，未有任务的分集不占位。
func (s *DramaService) ListDramaMediaJobs(ctx context.Context, dramaID string) ([]MediaJob, error) {
	if s == nil || s.repositories.Media.Jobs == nil || s.repositories.Content.Catalog == nil {
		slog.WarnContext(ctx, "list drama media jobs rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	drama, err := s.repositories.Content.Catalog.FindDrama(ctx, strings.TrimSpace(dramaID))
	if err != nil {
		slog.ErrorContext(ctx, "find drama failed", "error", err)
		return nil, err
	}
	episodes := append([]domain.Episode(nil), drama.Episodes...)
	sort.SliceStable(episodes, func(left, right int) bool { return episodes[left].Number < episodes[right].Number })
	episodeIDs := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		episodeIDs = append(episodeIDs, episode.ID)
	}
	jobs, err := s.repositories.Media.Jobs.ListMediaJobsByEpisodeIDs(ctx, episodeIDs)
	if err != nil {
		slog.ErrorContext(ctx, "list media jobs by episode ids failed", "error", err)
		return nil, err
	}
	// 仓储按创建时间、ID 升序返回；同集后写覆盖前写，得到稳定的最新任务。
	latest := make(map[string]MediaJob, len(episodeIDs))
	for _, job := range jobs {
		if job.Kind != MediaJobUpload || job.Status == MediaJobReserved {
			continue
		}
		latest[job.EpisodeID] = job
	}
	result := make([]MediaJob, 0, len(latest))
	for _, episode := range episodes {
		if job, ok := latest[episode.ID]; ok {
			result = append(result, *s.enrichMediaJob(ctx, job))
		}
	}
	return result, nil
}

// RetryMediaJob 只重新排队已失败且仍有远端 VID 的任务，不重新上传文件。
// 沿用原任务固化的处理时长重建截止时间，清零尝试次数并尽力恢复资产状态。
func (s *DramaService) RetryMediaJob(ctx context.Context, id string) (*MediaJob, error) {
	if s == nil || s.mediaJobs == nil {
		slog.WarnContext(ctx, "retry media job rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	job, err := s.repositories.Media.Jobs.FindMediaJob(ctx, strings.TrimSpace(id))
	if err != nil {
		slog.ErrorContext(ctx, "find media job failed", "error", err)
		return nil, err
	}
	if job.Status != MediaJobFailed {
		slog.WarnContext(ctx, "retry media job rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	if strings.TrimSpace(job.RemoteVID) == "" {
		slog.WarnContext(ctx, "retry media job rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	processingTimeout, ok := mediaJobProcessingTimeout(job)
	if !ok {
		slog.WarnContext(ctx, "retry media job rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	job.Status = MediaJobQueued
	job.Error = ""
	job.Attempts = 0
	job.NextAttemptAt = time.Now().UTC()
	job.LeaseOwner = ""
	job.LeaseExpiresAt = nil
	processingStartedAt := time.Now().UTC()
	processingDeadlineAt := processingStartedAt.Add(processingTimeout)
	job.ProcessingStartedAt = processingStartedAt
	job.ProcessingDeadlineAt = processingDeadlineAt
	job.UpdatedAt = time.Now().UTC()
	if err := s.repositories.Media.Jobs.UpdateMediaJob(ctx, job); err != nil {
		slog.ErrorContext(ctx, "update media job failed", "error", err)
		return nil, err
	}
	s.mediaJobs.notify()
	return &job, nil
}

// RecordDirectUploadDiagnostic 记录浏览器直传失败的安全摘要。
// 仅接受仍归属指定分集的上传任务，避免前端把无关失败写入服务端日志。
func (s *DramaService) RecordDirectUploadDiagnostic(ctx context.Context, episodeID, uploadID string, input DirectUploadDiagnosticInput) error {
	if s == nil || s.repositories.Media.Jobs == nil {
		slog.WarnContext(ctx, "record direct upload diagnostic rejected", "error", ErrMediaNotConfigured)
		return ErrMediaNotConfigured
	}
	job, err := s.repositories.Media.Jobs.FindMediaJob(ctx, strings.TrimSpace(uploadID))
	if err != nil {
		slog.ErrorContext(ctx, "find media job for direct upload diagnostic failed", "error", err)
		return err
	}
	if job.Kind != MediaJobUpload || job.EpisodeID != strings.TrimSpace(episodeID) {
		slog.WarnContext(ctx, "record direct upload diagnostic rejected", "error", ErrConflict)
		return ErrConflict
	}
	slog.ErrorContext(ctx, "byteplus direct upload failed", "upload_id", job.ID, "stage", input.Stage, "code", input.Code, "status", input.Status, "reason", input.Reason)
	return nil
}

// enrichMediaJob 为成功任务附加当前分集视图；补充查询失败仅记日志，任务本身仍然可读。
func (s *DramaService) enrichMediaJob(ctx context.Context, job MediaJob) *MediaJob {
	if job.Kind == MediaJobUpload && job.Status == MediaJobSucceeded {
		{
			episode, err := s.repositories.Content.Catalog.FindEpisode(ctx, job.EpisodeID)
			if err != nil {
				slog.ErrorContext(ctx, "find episode failed", "error", err)
			}
			if err == nil {
				job.Episode = episode
			}
		}
	}
	return &job
}

// notify 非阻塞通知本实例的队列消费者；通知可合并，跨实例任务仍靠定时扫描发现。
func (runner *mediaJobRunner) notify() {
	for index := 0; index < runner.workerConcurrency; index++ {
		select {
		case runner.wake <- struct{}{}:
		default:
			return
		}
	}
}

// notifyReconciliation 合并本实例的媒资校准唤醒请求，避免维护任务未读通知时阻塞 HTTP 请求。
func (runner *mediaJobRunner) notifyReconciliation() {
	select {
	case runner.reconcileWake <- struct{}{}:
	default:
	}
}

// run 先启动队列消费者，再独立运行恢复和巡检；大量媒资观察不能阻塞新任务领取。
// 取消后等待消费者与维护循环完成收尾，供 WaitMediaWorker 判断本进程已停止。
func (runner *mediaJobRunner) run(ctx context.Context) {
	var workers sync.WaitGroup
	for index := 0; index < runner.workerConcurrency; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runner.runQueueWorker(ctx)
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		runner.runMaintenance(ctx)
	}()
	<-ctx.Done()
	workers.Wait()
}

// runMaintenance 把恢复、清理和全量媒资校准移出队列主循环，避免启动阶段饥饿。
func (runner *mediaJobRunner) runMaintenance(ctx context.Context) {
	runner.failInterrupted(ctx)
	runner.cleanupTerminalMediaJobs(ctx)
	runner.reconcileMediaAssets(ctx)
	recoveryTicker := time.NewTicker(runner.recoveryInterval())
	defer recoveryTicker.Stop()
	maintenanceTicker := time.NewTicker(runner.maintenanceInterval)
	defer maintenanceTicker.Stop()
	reconcileTicker := time.NewTicker(runner.reconcileInterval)
	defer reconcileTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-recoveryTicker.C:
			runner.failInterrupted(ctx)
		case <-maintenanceTicker.C:
			runner.cleanupTerminalMediaJobs(ctx)
		case <-reconcileTicker.C:
			runner.reconcileMediaAssets(ctx)
		case <-runner.reconcileWake:
			runner.reconcileMediaAssets(ctx)
		}
	}
}

// runQueueWorker 每轮尝试领取持久化任务；空闲时等待通知或定时器。
func (runner *mediaJobRunner) runQueueWorker(ctx context.Context) {
	ticker := time.NewTicker(runner.pollInterval)
	defer ticker.Stop()
	for {
		runner.processQueued(ctx)
		select {
		case <-ctx.Done():
			return
		case <-runner.wake:
		case <-ticker.C:
		}
	}
}

// failInterrupted 扫描租约过期、处理超时或长期停滞的 running 任务，并重新争取租约后恢复。
// 已绑定相同 VID 的任务补记成功；未完成的任务在截止时间内重新排队，超时则记为失败。
func (runner *mediaJobRunner) failInterrupted(ctx context.Context) {
	for ctx.Err() == nil {
		now := time.Now().UTC()
		jobs, err := runner.service.repositories.Media.Jobs.ListMediaJobsUpdatedBefore(
			ctx, []string{MediaJobRunning, MediaJobUploading}, now, 100,
		)
		if err != nil {
			slog.ErrorContext(ctx, "list media jobs updated before failed", "error", err)
		}
		if err != nil || len(jobs) == 0 {
			return
		}
		updated := 0
		for _, job := range jobs {
			ctx := ctx
			if job.SubjectID != "" {
				ctx = application.WithSubject(ctx, job.SubjectID)
			}
			if job.Status == MediaJobUploading {
				if job.UpdatedAt.After(now.Add(-runner.staleRunningAfter)) {
					continue
				}
				job.Status = MediaJobFailed
				job.Error = "服务端上传任务已中断，请重新上传"
				job.UpdatedAt = now
				if updateErr := runner.service.repositories.Media.Jobs.UpdateMediaJob(ctx, job); updateErr != nil {
					slog.ErrorContext(ctx, "mark stale proxy upload failed", "job_id", job.ID, "error", updateErr)
				} else {
					updated++
					logMediaJobState(ctx, slog.LevelError, "proxy media upload interrupted", job, errors.New(job.Error))
				}
				continue
			}
			directExpired := runner.directProcessingExpired(job, now)
			leaseExpired := job.LeaseExpiresAt != nil && !job.LeaseExpiresAt.After(now)
			staleJob := job.UpdatedAt.Before(now.Add(-runner.staleRunningAfter))
			if !directExpired && !leaseExpired && !staleJob {
				continue
			}
			leaseExpiresAt := now.Add(runner.leaseDuration)
			claimed, claimErr := runner.service.repositories.Media.Jobs.ClaimMediaJobLease(ctx, MediaJobLeaseClaim{
				ID: job.ID, ExpectedStatus: job.Status, ExpectedUpdatedAt: job.UpdatedAt,
				LeaseOwner: runner.workerID, ClaimedAt: now, LeaseExpiresAt: leaseExpiresAt,
			})
			if claimErr != nil {
				logMediaJobError(ctx, "claim media job recovery lease failed", job.ID, claimErr)
			}
			if claimErr != nil || !claimed {
				continue
			}
			job.LeaseOwner = runner.workerID
			job.LeaseExpiresAt = &leaseExpiresAt
			job.UpdatedAt = now
			if job.Kind != MediaJobDelete && strings.TrimSpace(job.RemoteVID) != "" {
				episode, findErr := runner.service.repositories.Content.Catalog.FindEpisode(ctx, job.EpisodeID)
				if findErr != nil {
					logMediaJobError(ctx, "find interrupted media job episode failed", job.ID, findErr)
				}
				// 绑定已成功但任务终态未落库时，以当前分集的 VID 为依据补记成功，避免重复绑定。
				if findErr == nil && episode.VID == job.RemoteVID {
					job.Status = MediaJobSucceeded
					job.Error = ""
					job.UpdatedAt = now
					runner.releaseMediaJobLease(&job, now)
					persistCtx, cancel := mediaJobPersistenceContext(ctx)
					err := runner.service.repositories.Media.Jobs.UpdateMediaJob(persistCtx, job)
					if err != nil {
						slog.ErrorContext(ctx, "persist recovered media job success failed", "job_id", job.ID, "error", err)
					}
					cancel()
					if err == nil {
						updated++
						logMediaJobState(ctx, slog.LevelInfo, "media job recovered as succeeded", job, nil)
					}
					continue
				}
			}
			if directExpired {
				job.Status = MediaJobFailed
				job.Error = truncateMediaError(ErrMediaProcessingTimeout.Error())
			} else {
				job.Status = MediaJobQueued
				job.Error = ""
				job.NextAttemptAt = now
			}
			job.UpdatedAt = now
			runner.releaseMediaJobLease(&job, now)
			persistCtx, cancel := mediaJobPersistenceContext(ctx)
			err := runner.service.repositories.Media.Jobs.UpdateMediaJob(persistCtx, job)
			if err != nil {
				slog.ErrorContext(ctx, "persist interrupted media job recovery failed", "job_id", job.ID, "error", err)
			}
			cancel()
			if err == nil {
				updated++
				if directExpired {
					logMediaJobState(ctx, slog.LevelError, "media job processing timed out", job, ErrMediaProcessingTimeout)
				} else {
					logMediaJobState(ctx, slog.LevelInfo, "interrupted media job requeued", job, nil)
				}
			}
		}
		if len(jobs) < 100 || updated == 0 {
			return
		}
	}
}

// processQueued 从共享队列读取候选，尝试处理首个成功取得租约的任务；取消时停止领取。
func (runner *mediaJobRunner) processQueued(ctx context.Context) {
	if cancelErr := ctx.Err(); cancelErr != nil {
		slog.WarnContext(ctx, "media task cancelled", "error", cancelErr)
		return
	}
	jobs, err := runner.service.repositories.Media.Jobs.ListMediaJobsByStatus(ctx, []string{MediaJobQueued, MediaJobProcessing}, runner.workerConcurrency)
	if err != nil {
		slog.ErrorContext(ctx, "list media jobs by status failed", "error", err)
	}
	if err != nil || len(jobs) == 0 {
		return
	}
	for _, job := range jobs {
		if runner.process(ctx, job) {
			return
		}
	}
}

// process 按读取时的状态与更新时间争取租约；领取成功后处理一次并返回 true，冲突返回 false。
// queued 的新尝试才增加次数，processing 的常规轮询不消耗重试预算。
func (runner *mediaJobRunner) process(ctx context.Context, job MediaJob) bool {
	// 队列由服务端跨主体领取，处理时恢复任务固化的主体，不能继承某个后台页面的选择。
	if job.SubjectID != "" {
		ctx = application.WithSubject(ctx, job.SubjectID)
	}
	expectedStatus := job.Status
	expectedUpdatedAt := job.UpdatedAt
	claimedAt := time.Now().UTC()
	leaseExpiresAt := claimedAt.Add(runner.leaseDuration)
	claimed, err := runner.service.repositories.Media.Jobs.ClaimMediaJobLease(ctx, MediaJobLeaseClaim{
		ID: job.ID, ExpectedStatus: expectedStatus, ExpectedUpdatedAt: expectedUpdatedAt,
		LeaseOwner: runner.workerID, ClaimedAt: claimedAt, LeaseExpiresAt: leaseExpiresAt,
	})
	if err != nil {
		logMediaJobError(ctx, "claim media job lease failed", job.ID, err)
	}
	if err != nil || !claimed {
		return false
	}
	if expectedStatus == MediaJobQueued {
		job.Attempts++
	}
	job.Status = MediaJobRunning
	job.Error = ""
	job.LeaseOwner = runner.workerID
	job.LeaseExpiresAt = &leaseExpiresAt
	job.UpdatedAt = claimedAt
	if ignoredErr := runner.processClaimedWithLease(ctx, &job); ignoredErr != nil {
		logMediaJobError(ctx, "process media job attempt failed", job.ID, ignoredErr)
	}
	return true
}

// processClaimedWithLease 并行续租并执行官方处理确认；续租失败取消依赖调用，避免失去所有权后继续工作。
// 等续租协程退出后合并处理与续租错误，必要时安排未绑定产物清理。
func (runner *mediaJobRunner) processClaimedWithLease(ctx context.Context, job *MediaJob) error {
	if job.SubjectID != "" {
		ctx = application.WithSubject(ctx, job.SubjectID)
	}
	ctx = WithMediaSpace(ctx, job.SpaceName)
	processingContext, cancelProcessing := context.WithCancelCause(ctx)
	defer cancelProcessing(nil)
	stopped := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		err := runner.renewMediaJobLease(processingContext, job.ID, job.LeaseOwner, stopped)
		if err != nil {
			// Provider 必须观察该 Context；租约无法续期后，当前实例不再拥有继续上传
			// 或提交远端媒资结果的权利。
			cancelProcessing(err)
		}
		done <- err
	}()
	err := runner.processClaimed(processingContext, job)
	if err != nil {
		logMediaJobError(ctx, "process claimed media job failed", job.ID, err)
	}
	close(stopped)
	renewalErr := <-done
	if renewalErr != nil {
		if job.Kind != MediaJobDelete && job.Kind != MediaJobEntryPreview && strings.TrimSpace(job.RemoteVID) != "" {
			renewalErr = runner.service.cleanupUnattachedMedia(ctx, job.ID, job.RemoteVID, renewalErr)
		}
		// 清理函数始终保留原续租错误；汇总错误用于诊断，清理是否入队由函数内部单独记录。
		logMediaJobError(ctx, "wait media job lease renewal failed", job.ID, renewalErr)
		if err == nil || errors.Is(err, context.Canceled) {
			return renewalErr
		}

		resultErr := errors.Join(err, renewalErr)
		return resultErr
	}
	return err
}

// renewMediaJobLease 每个租约周期的三分之一续租一次；数据库拒绝续租视为所有权丢失。
// 上下文取消或处理完成通知只停止心跳，不单独制造任务失败。
func (runner *mediaJobRunner) renewMediaJobLease(ctx context.Context, jobID, leaseOwner string, stopped <-chan struct{}) error {
	interval := runner.leaseDuration / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-stopped:
			return nil
		case <-ticker.C:
			now := time.Now().UTC()
			renewed, err := runner.service.repositories.Media.Jobs.RenewMediaJobLease(ctx, jobID, leaseOwner, now, now.Add(runner.leaseDuration))
			if err != nil {
				resultErr := fmt.Errorf("renew media job lease: %w", err)
				logMediaJobError(ctx, "renew media job lease failed", jobID, err)
				return resultErr
			}
			if !renewed {
				logMediaJobError(ctx, "renew media job lease rejected", jobID, errMediaJobLeaseLost)
				return errMediaJobLeaseLost
			}
		}
	}
}

// processClaimed 使用任务创建时固化的截止时间确认远端处理结果，再统一落库。
// 截止时间缺失属于任务数据异常；任务自身超时与进程取消区分处理。
func (runner *mediaJobRunner) processClaimed(ctx context.Context, job *MediaJob) error {
	if job.Kind == MediaJobDelete {
		waitReason, err := runner.deleteDramaResource(ctx, *job)
		if err != nil {
			logMediaJobError(ctx, "delete drama resource failed", job.ID, err)
		}
		if err == nil && waitReason != "" {
			return runner.persistMediaDeletionWait(ctx, job, waitReason)
		}
		return runner.persistProcessedMediaJob(ctx, job, err)
	}

	deadline, validDeadline := mediaJobProcessingDeadline(*job)
	var err error
	if !validDeadline {
		err = ErrInvalidInput
	} else if !time.Now().UTC().Before(deadline) {
		err = ErrMediaProcessingTimeout
	} else {
		finalizeCtx, cancel := context.WithDeadline(ctx, deadline)
		if job.Kind == MediaJobEntryPreview {
			err = runner.processEntryPreview(finalizeCtx, job)
			if err != nil {
				logMediaJobError(finalizeCtx, "process first episode clip failed", job.ID, err)
			}
		} else {
			_, err = runner.service.finalizeDirectEpisodeMedia(finalizeCtx, *job)
			if err != nil {
				logMediaJobError(finalizeCtx, "finalize direct episode media failed", job.ID, err)
			}
		}
		cancel()
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			err = ErrMediaProcessingTimeout
		}
	}

	resultErr := runner.persistProcessedMediaJob(ctx, job, err)
	if resultErr != nil {
		logMediaJobError(ctx, "persist processed media job failed", job.ID, resultErr)
	}
	return resultErr
}

// persistMediaDeletionWait 把引用保护与远端删除确认记录为正常等待，复用任务状态和下次执行时间。
func (runner *mediaJobRunner) persistMediaDeletionWait(ctx context.Context, job *MediaJob, reason string) error {
	now := time.Now().UTC()
	delay := time.Minute
	if reason == "reference_in_use" {
		delay = 30 * time.Minute
	}
	job.Status, job.Error, job.UpdatedAt = MediaJobProcessing, "", now
	job.NextAttemptAt = now.Add(delay)
	runner.releaseMediaJobLease(job, now)
	persistCtx, cancel := mediaJobPersistenceContext(ctx)
	defer cancel()
	if err := runner.service.repositories.Media.Jobs.UpdateMediaJob(persistCtx, *job); err != nil {
		slog.ErrorContext(ctx, "save media deletion wait failed", "job_id", job.ID, "error", err)
		return err
	}
	slog.InfoContext(ctx, "media deletion waiting", "job_id", job.ID, "drama_id", job.DramaID,
		"resource_class", job.ResourceClass, "reason", reason, "next_attempt_at", job.NextAttemptAt)
	return nil
}

// persistProcessedMediaJob 保持原有成功、等待、重试和终态失败决策，仅在提交成功后记录任务去向。
// 正常轮询与取消不记为技术故障；持久化失败仍记录实际错误，不改变原返回语义。
func (runner *mediaJobRunner) persistProcessedMediaJob(ctx context.Context, job *MediaJob, err error) error {
	job.UpdatedAt = time.Now().UTC()
	if err != nil {
		if errors.Is(err, ErrMediaProcessing) {
			job.Status = MediaJobProcessing
			job.Error = ""
			job.NextAttemptAt = job.UpdatedAt.Add(runner.pollInterval)
			if job.Kind == MediaJobDelete && runner.pollInterval >= time.Second {
				job.NextAttemptAt = job.UpdatedAt.Add(time.Minute)
			}
			runner.releaseMediaJobLease(job, job.UpdatedAt)
			persistCtx, cancel := mediaJobPersistenceContext(ctx)
			if persistErr := runner.service.repositories.Media.Jobs.UpdateMediaJob(persistCtx, *job); persistErr != nil {
				slog.ErrorContext(ctx, "persist media processing wait failed", "job_id", job.ID, "error", persistErr)
			} else {
				logMediaJobState(ctx, slog.LevelDebug, "media processing pending", *job, err)
			}
			cancel()
			return nil
		}
		if cancelErr := ctx.Err(); cancelErr != nil {
			job.Status = MediaJobQueued
			job.Error = ""
			job.NextAttemptAt = job.UpdatedAt
		} else if job.Kind == MediaJobDelete || (retryableMediaJobError(err) && job.Attempts < runner.maxAttempts) {
			job.Status = MediaJobQueued
			job.Error = truncateMediaError(err.Error())
			job.NextAttemptAt = job.UpdatedAt.Add(runner.retryDelay(job.Attempts))
		} else {
			job.Status = MediaJobFailed
			job.Error = truncateMediaError(err.Error())
		}
		runner.releaseMediaJobLease(job, job.UpdatedAt)
		persistCtx, cancel := mediaJobPersistenceContext(ctx)
		if persistErr := runner.service.repositories.Media.Jobs.UpdateMediaJob(persistCtx, *job); persistErr != nil {
			logMediaJobError(ctx, "process media job failed", job.ID, err)
			slog.ErrorContext(ctx, "persist media job failure result failed", "job_id", job.ID, "error", persistErr)
		} else if job.Status == MediaJobQueued {
			if ctx.Err() != nil {
				logMediaJobState(ctx, mediaJobErrorLevel(err), "media job requeued after context ended", *job, err)
			} else {
				logMediaJobState(ctx, slog.LevelWarn, "media job retry scheduled", *job, err)
			}
		} else {
			logMediaJobState(ctx, mediaJobErrorLevel(err), "media job failed", *job, err)
		}
		cancel()
		if cancelErr := ctx.Err(); cancelErr != nil {
			return ctx.Err()
		}
		return err
	}
	job.Status = MediaJobSucceeded
	job.NextAttemptAt = job.UpdatedAt
	runner.releaseMediaJobLease(job, job.UpdatedAt)
	persistCtx, cancel := mediaJobPersistenceContext(ctx)
	persistErr := runner.service.repositories.Media.Jobs.UpdateMediaJob(persistCtx, *job)
	if persistErr != nil {
		slog.ErrorContext(ctx, "persist media job success result failed", "job_id", job.ID, "error", persistErr)
	} else {
		logMediaJobState(ctx, slog.LevelInfo, "media job succeeded", *job, nil)
	}
	cancel()
	return persistErr
}

// releaseMediaJobLease 将租约截止时间设为收尾时刻，保留所有者供仓储核对本次结果的提交身份。
func (runner *mediaJobRunner) releaseMediaJobLease(job *MediaJob, releasedAt time.Time) {
	expiresAt := releasedAt.UTC()
	job.LeaseExpiresAt = &expiresAt
}

// 根据尝试次数计算带上限的重试间隔。
func (runner *mediaJobRunner) retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := runner.retryBaseDelay
	for index := 1; index < attempt && delay < time.Hour/2; index++ {
		delay *= 2
	}
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

// retryableMediaJobError 排除字段错误、业务冲突和处理总超时，其余依赖故障允许在次数上限内重试。
func retryableMediaJobError(err error) bool {
	return err != nil && !errors.Is(err, ErrInvalidInput) && !errors.Is(err, ErrConflict) &&
		!errors.Is(err, ErrMediaProcessingTimeout)
}

// cleanupTerminalMediaJobs 分批清理超过保留期的终态任务；失败上传先成功安排资产删除，再移除任务记录。
func (runner *mediaJobRunner) cleanupTerminalMediaJobs(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-runner.terminalRetention)
	for ctx.Err() == nil {
		jobs, err := runner.service.repositories.Media.Jobs.ListMediaJobsUpdatedBefore(
			ctx, []string{MediaJobSucceeded, MediaJobFailed, MediaJobReserved, MediaJobUploaded}, cutoff, 100,
		)
		if err != nil {
			slog.ErrorContext(ctx, "list media jobs updated before failed", "error", err)
		}
		if err != nil || len(jobs) == 0 {
			return
		}
		deleted := 0
		for _, job := range jobs {
			persistCtx, cancel := mediaJobPersistenceContext(ctx)
			deleteErr := runner.service.repositories.Media.Jobs.DeleteMediaJob(persistCtx, job.ID)
			if deleteErr != nil {
				slog.ErrorContext(ctx, "delete media job failed", "job_id", job.ID, "error", deleteErr)
			}
			cancel()
			if deleteErr == nil {
				deleted++
			}
		}
		if len(jobs) < 100 || deleted == 0 {
			return
		}
	}
}

// activeMediaJob 判断任务是否仍可能继续确认或绑定视频，用于冲突处理时保留正在使用的产物。
func activeMediaJob(job MediaJob) bool {
	return job.Status == MediaJobQueued || job.Status == MediaJobRunning || job.Status == MediaJobProcessing
}

// 返回任务创建时固化的处理截止时间。
func mediaJobProcessingDeadline(job MediaJob) (time.Time, bool) {
	if job.ProcessingDeadlineAt.IsZero() {
		return time.Time{}, false
	}
	return job.ProcessingDeadlineAt.UTC(), true
}

// 返回任务创建时固化的处理时长，供管理员手动重试时重建截止时间。
func mediaJobProcessingTimeout(job MediaJob) (time.Duration, bool) {
	if job.ProcessingStartedAt.IsZero() {
		return 0, false
	}
	deadline, ok := mediaJobProcessingDeadline(job)
	if !ok {
		return 0, false
	}
	timeout := deadline.Sub(job.ProcessingStartedAt.UTC())
	return timeout, timeout > 0
}

// 判断直传任务的处理阶段是否已经超时。
func (runner *mediaJobRunner) directProcessingExpired(job MediaJob, now time.Time) bool {
	if job.Kind == MediaJobDelete {
		return false
	}
	deadline, ok := mediaJobProcessingDeadline(job)
	return !ok || !now.Before(deadline)
}

// 计算后台任务扫描异常状态的时间间隔。
func (runner *mediaJobRunner) recoveryInterval() time.Duration {
	interval := time.Minute
	if interval < runner.pollInterval {
		interval = runner.pollInterval
	}
	if interval > runner.maintenanceInterval {
		interval = runner.maintenanceInterval
	}
	return interval
}

// mediaJobPersistenceContext 为退出和故障收尾提供独立的五秒写入窗口，同时保留原上下文中的业务关联值。
func mediaJobPersistenceContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
}

// mediaDeletionRetryDelay 计算远端删除重试退避。
func mediaDeletionRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Minute << min(attempt-1, 6)
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

// truncateMediaError 去除首尾空白并把持久化错误摘要限制为 1000 字节；完整错误仍由失败处理处记录。
func truncateMediaError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 1000 {
		return message[:1000]
	}
	return message
}

// reconcileMediaAssets 从 MySQL 领取单剧检查点；测试仓储仍可使用完整聚合接口。
func (runner *mediaJobRunner) reconcileMediaAssets(ctx context.Context) {
	provider, ok := runner.service.media.(MediaObservationProvider)
	if !ok {
		return
	}
	if pager, ok := runner.service.repositories.Content.Catalog.(MediaReconciliationRepository); ok {
		for ctx.Err() == nil {
			dramaID, claimed, err := pager.ClaimMediaReconciliation(ctx, runner.workerID)
			if err != nil {
				slog.ErrorContext(ctx, "claim media reconciliation failed", "error", err)
				return
			}
			if !claimed || dramaID == "" {
				return
			}
			var drama *domain.Drama
			if reader, ok := runner.service.repositories.Content.Catalog.(interface {
				FindReconciliationDrama(context.Context, string) (*domain.Drama, error)
			}); ok {
				drama, err = reader.FindReconciliationDrama(ctx, dramaID)
			} else {
				drama, err = runner.service.repositories.Content.Catalog.FindDrama(ctx, dramaID)
			}
			if err != nil {
				slog.ErrorContext(ctx, "find drama for media reconciliation failed", "error", err)
			}
			completed := err == nil
			if completed {
				completed = runner.reconcileDramaMedia(ctx, provider, *drama, pager)
			}
			finishCtx, cancel := mediaJobPersistenceContext(ctx)
			finishErr := pager.FinishMediaReconciliation(finishCtx, runner.workerID, dramaID, completed)
			cancel()
			if finishErr != nil {
				slog.ErrorContext(ctx, "finish media reconciliation failed", "error", finishErr)
				return
			}
			if !completed {
				return
			}
		}
		return
	}
	dramas, err := runner.service.repositories.Content.Catalog.ListAllDramas(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list dramas for video reconciliation failed", "error", err)
		return
	}
	for _, drama := range dramas {
		runner.reconcileDramaMedia(ctx, provider, drama, nil)
	}
}

// reconcileDramaMedia 对单剧逐集观察并按当前 VID 条件更新，长任务在每集前续租。
func (runner *mediaJobRunner) reconcileDramaMedia(ctx context.Context, provider MediaObservationProvider, drama domain.Drama, pager MediaReconciliationRepository) bool {
	if drama.SubjectID != "" {
		ctx = application.WithSubject(ctx, drama.SubjectID)
	}
	for _, episode := range drama.Episodes {
		if ctx.Err() != nil {
			return false
		}
		if pager != nil {
			if err := pager.RenewMediaReconciliation(ctx, runner.workerID, drama.ID); err != nil {
				slog.ErrorContext(ctx, "renew media reconciliation lease failed", "error", err)
				return false
			}
		}
		if episode.VID == "" {
			continue
		}
		observeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		observation, err := provider.ObserveMedia(observeCtx, episode.VID)
		cancel()
		if err != nil {
			slog.ErrorContext(ctx, "observe episode video failed", "episode_id", episode.ID, "error", err)
			continue
		}
		if observation.VID != "" && observation.VID != episode.VID {
			continue
		}
		status := "unavailable"
		if !observation.Exists {
			status = "missing"
		} else if observation.PublishStatus == MediaProviderPublished {
			status = "ready"
		}
		if observation.Exists {
			if catalog, ok := runner.service.media.(VODCatalogProvider); ok {
				syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				err := catalog.SyncVODEpisodeMetadata(syncCtx, newVODEpisodeMetadata(drama, episode), observation)
				cancel()
				if err != nil {
					slog.ErrorContext(ctx, "sync episode metadata failed", "error", err)
				}
			}
		}
		if err := runner.service.repositories.Content.Catalog.UpdateEpisodeMediaStatus(ctx, episode.ID, episode.VID, status); err != nil {
			slog.ErrorContext(ctx, "save episode video observation failed", "error", err)
		}
	}
	return ctx.Err() == nil
}

// deleteDramaResource 在删除前重新核对剧草稿及发布快照，远端权威确认不存在后才完成任务。
func (runner *mediaJobRunner) deleteDramaResource(ctx context.Context, job MediaJob) (string, error) {
	used, err := runner.service.repositories.Media.Jobs.IsMediaResourceUsed(ctx, job)
	if err != nil {
		slog.ErrorContext(ctx, "check drama resource before deletion failed", "error", err)
		return "", err
	}
	if used {
		return "reference_in_use", nil
	}
	provider, ok := runner.service.media.(MediaResourceObservationProvider)
	if !ok {
		return "", ErrMediaNotConfigured
	}
	resource := MediaResource{ResourceClass: job.ResourceClass, SpaceName: job.SpaceName,
		ProviderAssetID: job.RemoteVID, ProviderFileName: job.ProviderFileName}
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	observation, err := provider.ObserveMediaResource(callCtx, resource)
	if errors.Is(err, ErrMediaProcessing) {
		return "remote_delete_pending", nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "observe resource before deletion failed", "error", err)
		return "", err
	}
	if observation.ResourceClass != job.ResourceClass || observation.ProviderAssetID != job.RemoteVID {
		return "", ErrConflict
	}
	if !observation.Exists {
		return "", nil
	}
	if err := runner.service.media.Delete(callCtx, job.ResourceClass, job.SpaceName, job.RemoteVID, job.ProviderFileName); err != nil {
		if errors.Is(err, ErrMediaProcessing) {
			return "remote_delete_pending", nil
		}
		slog.ErrorContext(ctx, "request remote resource deletion failed", "error", err)
		return "", err
	}
	return "remote_delete_pending", nil
}
