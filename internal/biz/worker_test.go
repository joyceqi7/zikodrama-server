package biz_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"drama-server/internal/biz"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

// workerRepositoryProbe 观察实例是否实际进入三种后台职责，业务读写仍委托共享内存仓储。
type workerRepositoryProbe struct {
	// MediaJobRepository 提供共享媒体队列。
	operations.MediaJobRepository
	// TikTokPublicationRepository 提供共享官方发布队列。
	operations.TikTokPublicationRepository
	// MetricsRepository 保留统计查询，并补充测试维护观察。
	operations.MetricsRepository
	mediaSeen, publicationSeen, metricsSeen chan struct{}
	mediaOnce, publicationOnce, metricsOnce sync.Once
	metricsErr                              error
}

// ListMediaJobsByStatus 标记媒体队列已扫描，保留仓储原有结果。
func (p *workerRepositoryProbe) ListMediaJobsByStatus(ctx context.Context, statuses []string, limit int) ([]operations.MediaJob, error) {
	p.mediaOnce.Do(func() { close(p.mediaSeen) })
	return p.MediaJobRepository.ListMediaJobsByStatus(ctx, statuses, limit)
}

// ClaimTikTokPublication 标记官方队列已尝试领取，保留仓储原有租约语义。
func (p *workerRepositoryProbe) ClaimTikTokPublication(ctx context.Context, owner string, now, expiry time.Time) (*operations.TikTokPublication, error) {
	p.publicationOnce.Do(func() { close(p.publicationSeen) })
	return p.TikTokPublicationRepository.ClaimTikTokPublication(ctx, owner, now, expiry)
}

// MaintainMetrics 标记指标维护已启动，并按用例注入故障。
func (p *workerRepositoryProbe) MaintainMetrics(context.Context, time.Time) error {
	p.metricsOnce.Do(func() { close(p.metricsSeen) })
	return p.metricsErr
}

// workerTestMedia 阻止生命周期测试调用真实平台。
type workerTestMedia struct{}

// Delete 拒绝空队列场景不应出现的媒资删除。
func (workerTestMedia) Delete(context.Context, string, string, string, string) error {
	return errors.New("fixture unexpected media deletion")
}

func newWorkerFixture(t *testing.T, store *memory.Store, official bool, metricsErr error) (*biz.Worker, *workerRepositoryProbe) {
	t.Helper()
	repositories := memory.OperationsRepositories(store)
	probe := &workerRepositoryProbe{
		MediaJobRepository: repositories.Media.Jobs, TikTokPublicationRepository: repositories.Content.Publications,
		MetricsRepository: repositories.Reporting.Metrics, metricsErr: metricsErr,
		mediaSeen: make(chan struct{}), publicationSeen: make(chan struct{}), metricsSeen: make(chan struct{}),
	}
	repositories.Media.Jobs = probe
	repositories.Content.Publications = probe
	repositories.Reporting.Metrics = probe
	service := operations.NewService(repositories, workerTestMedia{})
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{}); err != nil {
		t.Fatal(err)
	}
	if official {
		// 空队列只验证领取循环，不创建外部客户端。
		if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return nil }, operations.PublicationJobConfig{}); err != nil {
			t.Fatal(err)
		}
	}
	worker := biz.NewWorker(service)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := worker.Stop(ctx); err != nil {
			t.Error("清理后台任务失败", err)
		}
	})
	return worker, probe
}

func waitWorkerResponsibility(t *testing.T, seen <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("后台职责未启动", name)
	}
}

func captureWorkerLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	logs := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

// TestWorkerRunsAllResponsibilities 验证共享仓储的两个实例均启动全部职责，停止等待收尾，成功路径不误报错误。
func TestWorkerRunsAllResponsibilities(t *testing.T) {
	logs := captureWorkerLogs(t)
	store := memory.NewStore(memory.Seed{})
	for range 2 {
		worker, probe := newWorkerFixture(t, store, true, nil)
		if err := worker.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		waitWorkerResponsibility(t, probe.mediaSeen, "media")
		waitWorkerResponsibility(t, probe.publicationSeen, "official-publication")
		waitWorkerResponsibility(t, probe.metricsSeen, "metrics")
		if err := worker.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := worker.Stop(t.Context()); err != nil {
			t.Fatal("重复停止应成功", err)
		}
	}
	if logs.Len() != 0 {
		t.Fatal("正常后台任务生命周期误报日志", logs.String())
	}
}

// TestWorkerUnavailableOfficialAndMetricFailure 验证官方能力缺失保留其他职责，维护故障记录实际原因且可以正常关闭。
func TestWorkerUnavailableOfficialAndMetricFailure(t *testing.T) {
	logs := captureWorkerLogs(t)
	metricsErr := errors.New("fixture metric table unavailable")
	worker, probe := newWorkerFixture(t, memory.NewStore(memory.Seed{}), false, metricsErr)
	if err := worker.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	waitWorkerResponsibility(t, probe.mediaSeen, "media")
	waitWorkerResponsibility(t, probe.metricsSeen, "metrics")
	if err := worker.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"TikTok publication worker unavailable", operations.ErrPublicationUnavailable.Error(), "maintain worker metrics failed", metricsErr.Error()} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatal("日志缺少具体操作或故障原因", expected)
		}
	}
	if strings.Contains(logs.String(), `"level":"ERROR","msg":"TikTok publication worker unavailable"`) {
		t.Fatal("可预期的未装配官方能力不应误报 ERROR")
	}
}

// TestWorkerLifecycleRejections 验证启动失败及重复生命周期请求的错误传播与诊断。
func TestWorkerLifecycleRejections(t *testing.T) {
	logs := captureWorkerLogs(t)
	unconfigured := biz.NewWorker(operations.NewService(memory.OperationsRepositories(memory.NewStore(memory.Seed{})), nil))
	if err := unconfigured.Start(t.Context()); !errors.Is(err, operations.ErrMediaNotConfigured) {
		t.Fatal("缺少媒体任务装配应拒绝启动", err)
	}
	if !strings.Contains(logs.String(), "start media worker failed") || !strings.Contains(logs.String(), operations.ErrMediaNotConfigured.Error()) {
		t.Fatal("启动失败缺少实际原因")
	}
	logs.Reset()
	worker, probe := newWorkerFixture(t, memory.NewStore(memory.Seed{}), true, nil)
	if err := worker.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	waitWorkerResponsibility(t, probe.metricsSeen, "metrics")
	if err := worker.Start(t.Context()); !errors.Is(err, operations.ErrConflict) {
		t.Fatal("重复启动应返回冲突", err)
	}
	if err := worker.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(t.Context()); !errors.Is(err, operations.ErrConflict) {
		t.Fatal("停止后重启应返回冲突", err)
	}
	if !strings.Contains(logs.String(), "start rejected") || !strings.Contains(logs.String(), operations.ErrConflict.Error()) || strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Fatal("生命周期冲突应保留原因且只记录 WARN")
	}
}
