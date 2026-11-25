package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
)

// diagnosticMediaJobs 注入任务提交和续租故障，保留待提交状态以核对业务结果。
type diagnosticMediaJobs struct {
	MediaJobRepository
	updates             []MediaJob
	updateErr, renewErr error
	renewed             bool
	stopped             chan struct{}
	jobs                []MediaJob
}

// UpdateMediaJob 记录原样提交的任务，不模拟成功写入之外的状态变化。
func (r *diagnosticMediaJobs) UpdateMediaJob(_ context.Context, job MediaJob) error {
	r.updates = append(r.updates, job)
	return r.updateErr
}

// RenewMediaJobLease 返回指定续租结果，成功时通知测试结束。
func (r *diagnosticMediaJobs) RenewMediaJobLease(context.Context, string, string, time.Time, time.Time) (bool, error) {
	if r.renewed {
		close(r.stopped)
	}
	return r.renewed, r.renewErr
}

// ListMediaJobsUpdatedBefore 返回单页中断任务。
func (r *diagnosticMediaJobs) ListMediaJobsUpdatedBefore(context.Context, []string, time.Time, int) ([]MediaJob, error) {
	return r.jobs, nil
}

// ClaimMediaJobLease 模拟成功取得恢复租约。
func (r *diagnosticMediaJobs) ClaimMediaJobLease(context.Context, MediaJobLeaseClaim) (bool, error) {
	return true, nil
}

// diagnosticCatalog 只提供任务恢复所需的当前分集。
type diagnosticCatalog struct {
	CatalogRepository
	episode *domain.Episode
}

// FindEpisode 返回指定分集以检查已绑定媒资的恢复日志。
func (r diagnosticCatalog) FindEpisode(context.Context, string) (*domain.Episode, error) {
	return r.episode, nil
}

// mediaDiagnosticEntries 解码每条结构化日志，拒绝不完整的记录。
func mediaDiagnosticEntries(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	decoder := json.NewDecoder(logs)
	for decoder.More() {
		var entry map[string]any
		if err := decoder.Decode(&entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	return entries
}

// TestMediaJobResultLogsFollowPersistence 验证每种任务去向、重试时间和错误传播保持原合同，提交失败不伪报结果。
func TestMediaJobResultLogsFollowPersistence(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	failure, persistFailure := errors.New("VOD connection refused"), errors.New("UPDATE media_jobs: database unavailable")
	for _, tc := range []struct {
		name, kind, status, message, level string
		attempt                            int
		cause, saveErr, returned           error
		cancelled                          bool
		delay                              time.Duration
	}{
		{name: "成功", status: MediaJobSucceeded, message: "media job succeeded", level: "INFO"},
		{name: "依赖失败安排重试", cause: failure, returned: failure, attempt: 1, status: MediaJobQueued, message: "media job retry scheduled", level: "WARN", delay: 3 * time.Second},
		{name: "耗尽重试", cause: failure, returned: failure, attempt: 3, status: MediaJobFailed, message: "media job failed", level: "ERROR"},
		{name: "业务冲突不重试", cause: ErrConflict, returned: ErrConflict, status: MediaJobFailed, message: "media job failed", level: "WARN"},
		{name: "平台正常处理", cause: ErrMediaProcessing, status: MediaJobProcessing, message: "media processing pending", level: "DEBUG", delay: 2 * time.Second},
		{name: "删除继续等待", kind: MediaJobDelete, cause: ErrMediaProcessing, status: MediaJobProcessing, message: "media processing pending", level: "DEBUG", delay: time.Minute},
		{name: "请求取消重新排队", cause: context.Canceled, returned: context.Canceled, cancelled: true, status: MediaJobQueued, message: "media job requeued after context ended", level: "INFO"},
		{name: "成功结果保存失败", saveErr: persistFailure, returned: persistFailure, status: MediaJobSucceeded, message: "persist media job success result failed", level: "ERROR"},
		{name: "重试保存失败仍返回原处理错误", cause: failure, saveErr: persistFailure, returned: failure, status: MediaJobQueued, message: "persist media job failure result failed", level: "ERROR", delay: 3 * time.Second},
		{name: "正常轮询保存失败仍保持原返回", cause: ErrMediaProcessing, saveErr: persistFailure, status: MediaJobProcessing, message: "persist media processing wait failed", level: "ERROR", delay: 2 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
			repo := &diagnosticMediaJobs{updateErr: tc.saveErr}
			runner := &mediaJobRunner{service: NewService(Repositories{Media: MediaPorts{Jobs: repo}}, nil).DramaService, pollInterval: 2 * time.Second, retryBaseDelay: 3 * time.Second, maxAttempts: 3}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelled {
				cancel()
			}
			job := MediaJob{ID: "901", Kind: tc.kind, Status: MediaJobRunning, Attempts: tc.attempt, PublicURL: "private-media-url", FileName: "private-original-filename", LeaseOwner: "worker-1"}
			if err := runner.persistProcessedMediaJob(ctx, &job, tc.cause); !errors.Is(err, tc.returned) {
				t.Fatalf("返回值改变: %v", err)
			}
			if len(repo.updates) != 1 || repo.updates[0].Status != tc.status || job.NextAttemptAt.Sub(job.UpdatedAt) != tc.delay && tc.status != MediaJobFailed || job.LeaseExpiresAt == nil || !job.LeaseExpiresAt.Equal(job.UpdatedAt) {
				t.Fatalf("状态、租约或等待时间改变: %+v", job)
			}
			if tc.cancelled || errors.Is(tc.cause, ErrMediaProcessing) {
				if job.Error != "" {
					t.Fatal("正常等待或取消被记为任务错误")
				}
			}
			raw := logs.String()
			if strings.Contains(raw, "private-media-url") || strings.Contains(raw, "private-original-filename") {
				t.Fatal("日志泄漏完整任务字段")
			}
			entries := mediaDiagnosticEntries(t, &logs)
			found := false
			for _, entry := range entries {
				if entry["job_id"] != "901" {
					t.Fatalf("缺少任务标识: %v", entry)
				}
				if entry["msg"] == tc.message {
					found = true
					if entry["level"] != tc.level {
						t.Fatalf("级别错误: %v", entry)
					}
					if tc.saveErr != nil {
						if entry["error"] != persistFailure.Error() {
							t.Fatal("缺少实际持久化错误")
						}
					} else {
						if entry["status"] != tc.status || entry["attempt"] != float64(tc.attempt) {
							t.Fatalf("日志与已提交状态不一致: %v", entry)
						}
						if tc.cause != nil && entry["error"] != tc.cause.Error() {
							t.Fatal("丢失处理原因")
						}
						if (tc.status == MediaJobQueued || tc.status == MediaJobProcessing) && entry["next_attempt_at"] == nil {
							t.Fatal("缺少下次执行时间")
						}
					}
				}
				if tc.saveErr != nil && (entry["msg"] == "media job succeeded" || entry["msg"] == "media job retry scheduled" || entry["msg"] == "media processing pending") {
					t.Fatal("保存失败却误报提交成功")
				}
			}
			if !found {
				t.Fatalf("缺少任务结果日志: %s", raw)
			}
			if tc.saveErr != nil && tc.cause != nil && !errors.Is(tc.cause, ErrMediaProcessing) && !strings.Contains(raw, tc.cause.Error()) {
				t.Fatal("提交失败吞掉原处理错误")
			}
		})
	}
}

// TestMediaJobLeaseFailureLoggedOnce 验证续租错误只有一条本地诊断，所有权丢失和成功停止均保持原结果。
func TestMediaJobLeaseFailureLoggedOnce(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	failure := errors.New("UPDATE media_jobs lease: connection refused")
	for _, tc := range []struct {
		name, level   string
		failure, want error
		renewed       bool
	}{
		{name: "数据库失败", failure: failure, want: failure, level: "ERROR"},
		{name: "所有权已丢失", want: errMediaJobLeaseLost, level: "WARN"},
		{name: "正常续租后停止", renewed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			stopped := make(chan struct{})
			repo := &diagnosticMediaJobs{renewErr: tc.failure, renewed: tc.renewed, stopped: stopped}
			runner := &mediaJobRunner{service: NewService(Repositories{Media: MediaPorts{Jobs: repo}}, nil).DramaService, leaseDuration: 3 * time.Second}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := runner.renewMediaJobLease(ctx, "902", "worker", stopped)
			if !errors.Is(err, tc.want) {
				t.Fatalf("续租返回改变: %v", err)
			}
			if tc.renewed {
				if logs.Len() != 0 {
					t.Fatal("成功续租产生错误日志")
				}
				return
			}
			entries := mediaDiagnosticEntries(t, &logs)
			if len(entries) != 1 || entries[0]["job_id"] != "902" || entries[0]["level"] != tc.level || entries[0]["error"] != tc.want.Error() {
				t.Fatalf("重复日志或诊断缺失: %v", entries)
			}
		})
	}
}

// TestMediaJobRecoveryLogsCommittedState 验证中断恢复日志与实际保存结果一致，保存失败不伪报恢复成功。
func TestMediaJobRecoveryLogsCommittedState(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, tc := range []struct {
		name, status, message     string
		bound, expired, saveFails bool
	}{
		{name: "服务端上传中断", status: MediaJobUploading, message: "proxy media upload interrupted"},
		{name: "已绑定恢复成功", status: MediaJobRunning, bound: true, message: "media job recovered as succeeded"},
		{name: "租约中断重排", status: MediaJobRunning, message: "interrupted media job requeued"},
		{name: "处理总超时", status: MediaJobRunning, expired: true, message: "media job processing timed out"},
		{name: "恢复提交失败", status: MediaJobRunning, bound: true, saveFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			old := time.Now().UTC().Add(-time.Hour)
			job := MediaJob{ID: "903", Kind: MediaJobUpload, Status: tc.status, RemoteVID: "vid", UpdatedAt: old, ProcessingDeadlineAt: time.Now().Add(time.Hour)}
			if tc.expired {
				job.ProcessingDeadlineAt = old
			}
			repo := &diagnosticMediaJobs{jobs: []MediaJob{job}}
			if tc.saveFails {
				repo.updateErr = errors.New("recovery commit refused")
			}
			episode := &domain.Episode{}
			if tc.bound {
				episode.VID = job.RemoteVID
			}
			runner := &mediaJobRunner{service: NewService(Repositories{Media: MediaPorts{Jobs: repo}, Content: ContentPorts{Catalog: diagnosticCatalog{episode: episode}}}, nil).DramaService, staleRunningAfter: time.Minute, leaseDuration: time.Minute, workerID: "worker"}
			runner.failInterrupted(context.Background())
			if len(repo.updates) != 1 {
				t.Fatalf("恢复保存次数改变: %d", len(repo.updates))
			}
			entries := mediaDiagnosticEntries(t, &logs)
			if len(entries) != 1 || entries[0]["job_id"] != "903" {
				t.Fatalf("恢复日志缺少标识: %v", entries)
			}
			if tc.saveFails {
				if entries[0]["error"] != repo.updateErr.Error() || entries[0]["msg"] == "media job recovered as succeeded" {
					t.Fatal("恢复失败却报告成功")
				}
			} else if entries[0]["msg"] != tc.message || entries[0]["status"] != repo.updates[0].Status {
				t.Fatalf("日志和保存结果不同: %v", entries)
			}
		})
	}
}

// FindDrama 返回可编辑草稿，避免日志测试触发发布或远端写操作。
func (r diagnosticCatalog) FindDrama(context.Context, string) (*domain.Drama, error) {
	return &domain.Drama{ID: "201", PublishStatus: "draft"}, nil
}

// diagnosticDirectMedia 只注入完成确认结果，不执行上传、绑定或远端删除。
type diagnosticDirectMedia struct {
	MediaProvider
	DirectMediaProvider
	err error
}

// FinalizeUpload 返回正常处理、取消或依赖失败，验证整个 Worker 调用链的日志级别。
func (p diagnosticDirectMedia) FinalizeUpload(context.Context, string, string) (MediaAsset, error) {
	return MediaAsset{}, p.err
}

// TestMediaJobProcessingChainPreservesExpectedLogLevels 验证正常轮询和取消经过业务处理链时仍不产生 ERROR，超时和依赖故障保持诊断。
func TestMediaJobProcessingChainPreservesExpectedLogLevels(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	failure := errors.New("VOD finalize connection refused")
	for _, tc := range []struct {
		name, level, status string
		cause, want         error
		cancelled, expired  bool
	}{
		{name: "平台轮询", cause: ErrMediaProcessing, status: MediaJobProcessing, level: "DEBUG"},
		{name: "主动取消", cause: context.Canceled, want: context.Canceled, status: MediaJobQueued, level: "INFO", cancelled: true},
		{name: "请求超时", cause: context.DeadlineExceeded, want: context.DeadlineExceeded, status: MediaJobQueued, level: "ERROR", expired: true},
		{name: "依赖故障", cause: failure, want: failure, status: MediaJobQueued, level: "ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelled {
				cancel()
			}
			if tc.expired {
				var expiredCancel context.CancelFunc
				ctx, expiredCancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer expiredCancel()
			}
			repo := &diagnosticMediaJobs{}
			service := NewService(Repositories{Content: ContentPorts{Catalog: diagnosticCatalog{episode: &domain.Episode{ID: "301", DramaID: "201"}}}, Media: MediaPorts{Jobs: repo}}, diagnosticDirectMedia{err: tc.cause})
			runner := &mediaJobRunner{service: service.DramaService, pollInterval: 2 * time.Second, retryBaseDelay: 3 * time.Second, maxAttempts: 3}
			job := MediaJob{ID: "904", EpisodeID: "301", RemoteVID: "new-vid", Kind: MediaJobUpload, Status: MediaJobRunning, ProcessingDeadlineAt: time.Now().Add(time.Minute)}
			err := runner.processClaimed(ctx, &job)
			if !errors.Is(err, tc.want) || job.Status != tc.status || len(repo.updates) != 1 {
				t.Fatalf("处理结果改变: %v %+v", err, job)
			}
			entries := mediaDiagnosticEntries(t, &logs)
			if len(entries) == 0 {
				t.Fatal("处理原因没有日志")
			}
			for _, entry := range entries {
				if entry["job_id"] != "904" || entry["error"] == nil {
					t.Fatalf("处理链日志缺少任务标识或原因: %v", entry)
				}
				if tc.level == "DEBUG" || tc.level == "INFO" {
					if entry["level"] != tc.level {
						t.Fatalf("正常处理产生错误级别: %v", entry)
					}
				}
			}
		})
	}
}
