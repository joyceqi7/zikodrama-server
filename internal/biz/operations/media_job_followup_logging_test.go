package operations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
)

// TestMediaFailureLogsUseActualCause 验证取消 Context 不遮盖无关故障，包装和合并错误仍按真实根因记录。
func TestMediaFailureLogsUseActualCause(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	failure := errors.New("SELECT id FROM media_jobs: connection refused")
	for _, cancelled := range []bool{false, true} {
		for _, tc := range []struct {
			name, level string
			cause       error
		}{
			{name: "依赖故障", cause: failure, level: "ERROR"},
			{name: "包装的依赖故障", cause: fmt.Errorf("provider failed: %w", failure), level: "ERROR"},
			{name: "主动取消", cause: fmt.Errorf("provider stopped: %w", context.Canceled), level: "INFO"},
			{name: "正常处理中", cause: ErrMediaProcessing, level: "DEBUG"},
			{name: "租约竞争", cause: errMediaJobLeaseLost, level: "WARN"},
			{name: "超时", cause: context.DeadlineExceeded, level: "ERROR"},
			{name: "合并取消和依赖故障", cause: fmt.Errorf("worker failed: %w", errors.Join(context.Canceled, failure)), level: "ERROR"},
			{name: "合并取消和冲突", cause: errors.Join(context.Canceled, ErrConflict), level: "WARN"},
		} {
			t.Run(fmt.Sprintf("%s/Context取消=%t", tc.name, cancelled), func(t *testing.T) {
				var logs bytes.Buffer
				slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if cancelled {
					cancel()
				}
				logMediaJobError(ctx, "renew media job lease failed", "1001", tc.cause)
				entries := mediaDiagnosticEntries(t, &logs)
				if len(entries) != 1 || entries[0]["level"] != tc.level || entries[0]["job_id"] != "1001" || entries[0]["error"] != tc.cause.Error() {
					t.Fatalf("真实故障被降级或诊断丢失: %v", entries)
				}
			})
		}
	}
}

// cleanupDiagnosticJobs 注入清理查找和入队故障，记录原任务与远端产物传递情况。
type cleanupDiagnosticJobs struct {
	*diagnosticMediaJobs
	findErr, queueErr     error
	findCalls, queueCalls int
	queued                MediaJob
}

// FindMediaJob 返回上传任务或指定数据库错误。
func (r *cleanupDiagnosticJobs) FindMediaJob(context.Context, string) (MediaJob, error) {
	r.findCalls++
	return MediaJob{ID: "1002", Kind: MediaJobUpload, PublicURL: "private-media-url", FileName: "private-file-name"}, r.findErr
}

// QueueMediaDeletion 记录待清理产物，返回指定入队错误。
func (r *cleanupDiagnosticJobs) QueueMediaDeletion(_ context.Context, job MediaJob) error {
	r.queueCalls++
	r.queued = job
	return r.queueErr
}

// TestUnattachedCleanupLogsActualSchedulingResult 验证只有入队提交成功才打印成功，失败记录实际原因且原错误链不变。
func TestUnattachedCleanupLogsActualSchedulingResult(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	original, findFailure, queueFailure := errors.New("original lease failure"), errors.New("find upload database unavailable"), errors.New("queue deletion database unavailable")
	for _, tc := range []struct {
		name, message, level     string
		cause, findErr, queueErr error
		queueCalls               int
	}{
		{name: "保留原故障且成功入队", cause: original, message: "unattached media cleanup queued", level: "INFO", queueCalls: 1},
		{name: "无原故障成功入队", message: "unattached media cleanup queued", level: "INFO", queueCalls: 1},
		{name: "查询失败", cause: original, findErr: findFailure, message: "find abandoned upload task failed", level: "ERROR"},
		{name: "入队失败", cause: original, queueErr: queueFailure, message: "queue abandoned video deletion failed", level: "ERROR", queueCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			repo := &cleanupDiagnosticJobs{diagnosticMediaJobs: &diagnosticMediaJobs{}, findErr: tc.findErr, queueErr: tc.queueErr}
			service := NewService(Repositories{Media: MediaPorts{Jobs: repo}}, nil)
			err := service.cleanupUnattachedMedia(context.Background(), "1002", "private-provider-id", tc.cause)
			if !errors.Is(err, tc.cause) || tc.findErr != nil && !errors.Is(err, tc.findErr) || tc.queueErr != nil && !errors.Is(err, tc.queueErr) || repo.findCalls != 1 || repo.queueCalls != tc.queueCalls {
				t.Fatalf("原返回链或入队次数改变: %v", err)
			}
			if tc.findErr == nil && tc.queueErr == nil && err != tc.cause {
				t.Fatal("成功入队没有原样返回原错误")
			}
			if repo.queueCalls > 0 && repo.queued.RemoteVID != "private-provider-id" {
				t.Fatal("待清理产物改变")
			}
			raw := logs.String()
			entries := mediaDiagnosticEntries(t, &logs)
			if len(entries) != 1 || entries[0]["msg"] != tc.message || entries[0]["level"] != tc.level || entries[0]["job_id"] != "1002" {
				t.Fatalf("误报清理结果或缺少任务标识: %s", raw)
			}
			actual := tc.findErr
			if actual == nil {
				actual = tc.queueErr
			}
			if actual != nil && entries[0]["error"] != actual.Error() {
				t.Fatal("清理失败没有具体原因")
			}
			for _, secret := range []string{"private-media-url", "private-file-name", "private-provider-id"} {
				if strings.Contains(raw, secret) {
					t.Fatal("日志泄漏完整任务或媒资")
				}
			}
		})
	}
}

// leaseFailureDiagnosticMedia 等待续租失败取消，不执行远端读写。
type leaseFailureDiagnosticMedia struct {
	MediaProvider
	DirectMediaProvider
}

// FinalizeUpload 返回调用取消以模拟在续租失败时停止远端处理。
func (leaseFailureDiagnosticMedia) FinalizeUpload(ctx context.Context, _ string, _ string) (MediaAsset, error) {
	<-ctx.Done()
	return MediaAsset{}, ctx.Err()
}

// TestLeaseFailureCleanupLogsPreserveErrorChain 验证整个续租失败链路不误报清理，仍返回原续租错误与实际清理错误。
func TestLeaseFailureCleanupLogsPreserveErrorChain(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	leaseFailure, findFailure, queueFailure := errors.New("lease database connection refused"), errors.New("cleanup lookup failed"), errors.New("cleanup insert failed")
	for _, tc := range []struct {
		name              string
		findErr, queueErr error
	}{
		{name: "成功入队"}, {name: "查询失败", findErr: findFailure}, {name: "入队失败", queueErr: queueFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			repo := &cleanupDiagnosticJobs{diagnosticMediaJobs: &diagnosticMediaJobs{renewErr: leaseFailure}, findErr: tc.findErr, queueErr: tc.queueErr}
			service := NewService(Repositories{Content: ContentPorts{Catalog: diagnosticCatalog{episode: &domain.Episode{ID: "301", DramaID: "201"}}}, Media: MediaPorts{Jobs: repo}}, leaseFailureDiagnosticMedia{})
			runner := &mediaJobRunner{service: service.DramaService, pollInterval: time.Second, retryBaseDelay: time.Second, maxAttempts: 3, leaseDuration: 3 * time.Second}
			job := MediaJob{ID: "1002", Kind: MediaJobUpload, Status: MediaJobRunning, EpisodeID: "301", RemoteVID: "new-vid", ProcessingDeadlineAt: time.Now().Add(time.Minute), LeaseOwner: "worker"}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := runner.processClaimedWithLease(ctx, &job)
			if !errors.Is(err, leaseFailure) || tc.findErr != nil && !errors.Is(err, tc.findErr) || tc.queueErr != nil && !errors.Is(err, tc.queueErr) || len(repo.updates) != 1 || repo.updates[0].Status != MediaJobQueued || repo.updates[0].Error != "" {
				t.Fatalf("续租失败返回或取消重排决策改变: %v %+v", err, repo.updates)
			}
			if strings.Contains(logs.String(), "cleanup unattached media failed") {
				t.Fatal("原续租错误被误报为清理失败")
			}
			entries := mediaDiagnosticEntries(t, &logs)
			queued, summary := 0, 0
			for _, entry := range entries {
				if entry["job_id"] != "1002" {
					t.Fatalf("续租失败缺少任务关联: %v", entry)
				}
				if entry["msg"] == "unattached media cleanup queued" {
					queued++
				}
				if entry["msg"] == "wait media job lease renewal failed" {
					summary++
					if entry["level"] != "ERROR" || !strings.Contains(entry["error"].(string), leaseFailure.Error()) {
						t.Fatal("汇总日志丢失原续租故障")
					}
					for _, actual := range []error{tc.findErr, tc.queueErr} {
						if actual != nil && !strings.Contains(entry["error"].(string), actual.Error()) {
							t.Fatal("汇总日志丢失真实清理故障")
						}
					}
				}
			}
			expected := 0
			if tc.findErr == nil && tc.queueErr == nil {
				expected = 1
			}
			if queued != expected || summary != 1 {
				t.Fatalf("清理或续租结果重复/误报: %v", entries)
			}
		})
	}
}

// terminalCleanupDiagnosticJobs 按任务分别注入回收错误，不改变扫描或删除顺序。
type terminalCleanupDiagnosticJobs struct {
	*diagnosticMediaJobs
	failures map[string]error
	deleted  []string
}

// DeleteMediaJob 记录待回收任务并返回对应错误。
func (r *terminalCleanupDiagnosticJobs) DeleteMediaJob(_ context.Context, id string) error {
	r.deleted = append(r.deleted, id)
	return r.failures[id]
}

// TestTerminalCleanupLogsIdentifyFailedJobs 验证批量回收失败可逐条定位，成功任务不记错误且删除顺序不变。
func TestTerminalCleanupLogsIdentifyFailedJobs(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	failure := errors.New("DELETE FROM media_jobs: database unavailable")
	for _, tc := range []struct {
		name     string
		failures map[string]error
	}{
		{name: "全部失败", failures: map[string]error{"1003": failure, "1004": failure}},
		{name: "部分失败", failures: map[string]error{"1004": failure}},
		{name: "全部成功"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			repo := &terminalCleanupDiagnosticJobs{diagnosticMediaJobs: &diagnosticMediaJobs{jobs: []MediaJob{{ID: "1003", PublicURL: "private-media-url"}, {ID: "1004", FileName: "private-file-name"}}}, failures: tc.failures}
			runner := &mediaJobRunner{service: NewService(Repositories{Media: MediaPorts{Jobs: repo}}, nil).DramaService, terminalRetention: time.Hour}
			runner.cleanupTerminalMediaJobs(context.Background())
			if strings.Join(repo.deleted, ",") != "1003,1004" {
				t.Fatalf("删除调用改变: %v", repo.deleted)
			}
			raw := logs.String()
			entries := mediaDiagnosticEntries(t, &logs)
			if len(entries) != len(tc.failures) {
				t.Fatalf("成功任务误报或故障漏记: %v", entries)
			}
			for _, entry := range entries {
				id, _ := entry["job_id"].(string)
				if tc.failures[id] == nil || entry["error"] != failure.Error() || entry["msg"] != "delete media job failed" || entry["level"] != "ERROR" {
					t.Fatalf("回收日志无法关联失败任务: %v", entry)
				}
			}
			if strings.Contains(raw, "private-media-url") || strings.Contains(raw, "private-file-name") {
				t.Fatal("回收日志泄漏任务载荷")
			}
		})
	}
}

// bindingDiagnosticCatalog 注入绑定阶段的四种失败，以检查清理后的日志不会把绑定失败误报为清理失败。
type bindingDiagnosticCatalog struct {
	CatalogRepository
	stage   string
	failure error
}

// FindEpisode 返回当前分集或指定查询故障。
func (r bindingDiagnosticCatalog) FindEpisode(context.Context, string) (*domain.Episode, error) {
	if r.stage == "episode" {
		return nil, r.failure
	}
	vid := "expected-vid"
	if r.stage == "conflict" {
		vid = "other-vid"
	}
	return &domain.Episode{ID: "301", DramaID: "201", VID: vid}, nil
}

// FindDrama 返回草稿或指定查询故障。
func (r bindingDiagnosticCatalog) FindDrama(context.Context, string) (*domain.Drama, error) {
	if r.stage == "drama" {
		return nil, r.failure
	}
	return &domain.Drama{ID: "201", PublishStatus: "draft"}, nil
}

// SaveEpisodeMedia 返回指定绑定提交故障，不修改分集。
func (r bindingDiagnosticCatalog) SaveEpisodeMedia(context.Context, domain.Episode, domain.Drama, string, string) error {
	return r.failure
}

// TestMediaBindingFailureDoesNotMisreportSuccessfulCleanup 验证各绑定失败分支保留原错误、只安排一次清理并准确记录两个操作结果。
func TestMediaBindingFailureDoesNotMisreportSuccessfulCleanup(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	failure := errors.New("binding database unavailable")
	for _, stage := range []string{"episode", "drama", "conflict", "save"} {
		t.Run(stage, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			repo := &cleanupDiagnosticJobs{diagnosticMediaJobs: &diagnosticMediaJobs{}}
			catalog := bindingDiagnosticCatalog{stage: stage, failure: failure}
			service := NewService(Repositories{Content: ContentPorts{Catalog: catalog}, Media: MediaPorts{Jobs: repo}}, nil)
			episode, err := service.attachEpisodeMedia(context.Background(), "301", "expected-vid", "1002", MediaAsset{VID: "private-provider-id", PosterURL: "private-media-url"})
			want := failure
			if stage == "conflict" {
				want = ErrConflict
			}
			if episode != nil || !errors.Is(err, want) || repo.queueCalls != 1 {
				t.Fatalf("绑定错误或清理调用改变: %v", err)
			}
			raw := logs.String()
			if strings.Contains(raw, "clean up unattached media failed") || strings.Contains(raw, "private-provider-id") || strings.Contains(raw, "private-media-url") {
				t.Fatal("清理误报或泄漏媒资")
			}
			entries := mediaDiagnosticEntries(t, &logs)
			queued, binding := 0, 0
			for _, entry := range entries {
				if entry["msg"] == "unattached media cleanup queued" {
					queued++
					if entry["level"] != "INFO" || entry["job_id"] != "1002" {
						t.Fatal("清理成功日志不正确")
					}
				}
				if entry["msg"] == "attach episode media failed after cleanup attempt" {
					binding++
					if entry["error"] != want.Error() || entry["job_id"] != "1002" {
						t.Fatal("绑定错误详情或关联丢失")
					}
				}
			}
			if queued != 1 || binding != 1 {
				t.Fatalf("绑定和清理结果漏记或重复: %v", entries)
			}
		})
	}
}
