package operations

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// loggingAuditRepository 验证 Service 直接返回 DAO 结果时仍记录错误。
type loggingAuditRepository struct{ err error }

// SaveAuditRecord 返回注入的写入错误。
func (r loggingAuditRepository) SaveAuditRecord(context.Context, AuditRecord) error { return r.err }

// ListAuditRecords 返回注入的查询错误。
func (r loggingAuditRepository) ListAuditRecords(context.Context, int) ([]AuditRecord, error) {
	return nil, r.err
}

// loggingJobRepository 模拟 Worker 轮询失败；错误在任务循环中被处理，不会传到 HTTP 层。
type loggingJobRepository struct {
	MediaJobRepository
	err error
}

// ListMediaJobsByStatus 返回指定的任务查询错误。
func (r loggingJobRepository) ListMediaJobsByStatus(context.Context, []string, int) ([]MediaJob, error) {
	return nil, r.err
}

func TestOperationsLogsDirectDAOAndWorkerFailures(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	failure := errors.New("SELECT id FROM media_jobs: connection refused")
	for _, test := range []struct {
		name    string
		run     func(*Service) error
		message string
	}{
		{"直接返回审计列表", func(s *Service) error { _, err := s.AuditRecords(context.Background(), 10); return err }, "list audit records failed"},
		{"直接返回审计保存", func(s *Service) error {
			return s.RecordAudit(context.Background(), AuditRecord{ActorID: "1", RequestID: "test-request", Action: "test", ActorUsername: "private-actor"})
		}, "save audit record failed"},
		{"任务轮询吞掉错误", func(s *Service) error {
			runner := &mediaJobRunner{service: s.DramaService, workerConcurrency: 1}
			runner.processQueued(context.Background())
			return nil
		}, "list media jobs by status failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			service := NewService(Repositories{Reporting: ReportingPorts{Audits: loggingAuditRepository{err: failure}}, Media: MediaPorts{Jobs: loggingJobRepository{err: failure}}}, nil)
			err := test.run(service)
			if test.name != "任务轮询吞掉错误" && err != failure {
				t.Fatalf("错误返回被改变：%v", err)
			}
			raw := output.String()
			if !strings.Contains(raw, test.message) || !strings.Contains(raw, failure.Error()) || !strings.Contains(raw, `"level":"ERROR"`) {
				t.Fatalf("缺少实际故障日志：%s", raw)
			}
			if strings.Contains(raw, "private-actor") {
				t.Fatal("日志泄漏完整审计对象")
			}
		})
	}
	t.Run("空任务队列不打印错误", func(t *testing.T) {
		var output bytes.Buffer
		slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
		runner := &mediaJobRunner{service: NewService(Repositories{Media: MediaPorts{Jobs: loggingJobRepository{}}}, nil).DramaService, workerConcurrency: 1}
		runner.processQueued(context.Background())
		if output.Len() != 0 {
			t.Fatalf("正常空队列产生错误日志：%s", output.String())
		}
	})
}

type loggingCallbackRepository struct {
	MediaJobRepository
	err error
}

func (r loggingCallbackRepository) ApplyVODCallback(context.Context, VODEvent) (bool, error) {
	return false, r.err
}
func TestVODEventLogsDAOErrorAlongsideBooleanResult(t *testing.T) {
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	var out bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, nil)))
	failure := errors.New("UPDATE media_jobs: connection refused")
	s := NewService(Repositories{Media: MediaPorts{Jobs: loggingCallbackRepository{err: failure}}}, nil)
	_, err := s.ReceiveVODCallback(context.Background(), VODEvent{EventType: VODEventFileUploadComplete})
	if err != failure || !strings.Contains(out.String(), failure.Error()) {
		t.Fatalf("missing callback error: %v %s", err, out.String())
	}
}

func TestCancelledMediaPollingLogsReasonWithoutCallingDAO(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// 不提供仓储，确保取消入口不会继续发起任务查询。
	runner := &mediaJobRunner{}
	runner.processQueued(ctx)
	if !strings.Contains(logs.String(), context.Canceled.Error()) || !strings.Contains(logs.String(), `"level":"WARN"`) || strings.Count(logs.String(), `"error":`) != 1 {
		t.Fatalf("取消原因应只记录一次：%s", logs.String())
	}
}
