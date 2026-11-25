package biz

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"drama-server/internal/biz/operations"
)

// Worker 暴露媒体、官方发布与指标维护任务的业务生命周期，不依赖任何框架。
type Worker struct {
	// service 保存媒体、官方发布及指标维护用例。
	service *operations.Service

	// mu 仅保护当前进程内的启动状态和取消函数；媒体与官方发布任务由 MySQL 租约协调。
	mu sync.Mutex
	// stopMu 保证当前进程中的多个关闭调用按顺序等待同一业务循环。
	stopMu sync.Mutex
	// cancel 取消全部后台任务业务循环。
	cancel context.CancelFunc
	// started 表示后台任务已经成功启动且尚未完成关闭。
	started bool
	// finished 表示一次性业务 Worker 已完成关闭，禁止在同一实例上重启。
	finished bool
	// metricsDone 在指标维护循环退出时关闭，停止流程等待当前维护结束。
	metricsDone chan struct{}
}

// NewWorker 创建统一业务 Worker；各实例运行全部职责，媒体与官方发布任务通过 MySQL 租约协调领取。
func NewWorker(service *operations.Service) *Worker {
	return &Worker{service: service}
}

// Start 启动媒体、官方发布及指标维护循环；官方能力未装配时保留其余循环，重复启动或停止后重启返回冲突错误。
func (worker *Worker) Start(ctx context.Context) error {
	if worker == nil || worker.service == nil || ctx == nil {
		slog.WarnContext(ctx, "start rejected", "error", operations.ErrInvalidInput)
		return operations.ErrInvalidInput
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.started || worker.finished {
		slog.WarnContext(ctx, "start rejected", "error", operations.ErrConflict)
		return operations.ErrConflict
	}
	workerContext, cancel := context.WithCancel(ctx)
	// 每个实例运行同一组任务；媒体与官方发布的领取权限由持久层租约决定。
	if err := worker.service.StartMediaWorker(workerContext); err != nil {
		slog.ErrorContext(ctx, "start media worker failed", "error", err)
		cancel()
		return err
	}
	// 未装配官方发布能力时保留媒资循环；其他启动失败须取消并等待已启动任务。
	if err := worker.service.StartTikTokPublicationWorker(workerContext); err != nil {
		if errors.Is(err, operations.ErrPublicationUnavailable) {
			slog.WarnContext(ctx, "TikTok publication worker unavailable", "error", err)
		} else {
			slog.ErrorContext(ctx, "start TikTok publication worker failed", "error", err)
			cancel()
			if waitErr := worker.service.WaitMediaWorker(context.Background()); waitErr != nil {
				slog.ErrorContext(ctx, "wait media worker failed", "error", waitErr)
			}
			return err
		}
	}
	worker.metricsDone = make(chan struct{})
	go worker.runMetricMaintenance(workerContext)
	worker.cancel = cancel
	worker.started = true
	return nil
}

// Stop 取消全部后台任务并等待业务收尾完成。
// 如果调用方 Context 超时，状态会保留，以便框架使用新的 Context 再次等待。
func (worker *Worker) Stop(ctx context.Context) error {
	if worker == nil || worker.service == nil || ctx == nil {
		slog.WarnContext(ctx, "stop rejected", "error", operations.ErrInvalidInput)
		return operations.ErrInvalidInput
	}
	worker.stopMu.Lock()
	defer worker.stopMu.Unlock()
	worker.mu.Lock()
	cancel := worker.cancel
	started := worker.started
	worker.mu.Unlock()
	if !started || cancel == nil {
		return nil
	}
	cancel()
	if err := worker.service.WaitMediaWorker(ctx); err != nil {
		slog.ErrorContext(ctx, "wait media worker failed", "error", err)
		return err
	}
	if err := worker.service.WaitTikTokPublicationWorker(ctx); err != nil {
		if errors.Is(err, operations.ErrPublicationUnavailable) {
			slog.WarnContext(ctx, "TikTok publication worker unavailable", "error", err)
		} else {
			slog.ErrorContext(ctx, "wait TikTok publication worker failed", "error", err)
			return err
		}
	}
	select {
	case <-worker.metricsDone:
	case <-ctx.Done():
		slog.WarnContext(ctx, "wait metric maintenance interrupted", "error", ctx.Err())
		return ctx.Err()
	}
	worker.mu.Lock()
	worker.cancel = nil
	worker.started = false
	worker.finished = true
	worker.mu.Unlock()
	return nil
}

// configureMediaJobs 用业务默认任务参数完成媒体队列装配，实际任务所有权仍由持久层租约协调。
func configureMediaJobs(service *operations.Service) error {
	return service.ConfigureMediaJobs(operations.MediaJobConfig{})
}

// runMetricMaintenance 随业务 Worker 启停，每分钟刷新分表；失败由业务层记录，下一轮重试。
func (worker *Worker) runMetricMaintenance(ctx context.Context) {
	defer close(worker.metricsDone)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if worker.service.ReportingService != nil {
			if err := worker.service.ReportingService.MaintainMetrics(ctx, time.Now().UTC()); err != nil {
				slog.ErrorContext(ctx, "maintain worker metrics failed", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
