package operations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

// pendingDeletionProvider 模拟远端删除已接受但尚未确认不存在，不调用真实媒体服务。
type pendingDeletionProvider struct{ cleanupMediaProvider }

// ObserveMediaResource 始终返回仍存在，验证 Worker 会等待而不是提前宣告删除成功。
func (*pendingDeletionProvider) ObserveMediaResource(_ context.Context, asset operations.MediaResource) (operations.MediaResourceObservation, error) {
	return operations.MediaResourceObservation{ResourceClass: asset.ResourceClass, ProviderAssetID: asset.ProviderAssetID, Exists: true}, nil
}

// TestMediaDeletionWaitIsNotFailure 验证引用保护与远端确认具有不同间隔和原因，且不记技术失败日志。
func TestMediaDeletionWaitIsNotFailure(t *testing.T) {
	for _, used := range []bool{true, false} {
		t.Run(map[bool]string{true: "引用保护", false: "等待远端确认"}[used], func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			seed := memory.Seed{}
			if used {
				seed.Dramas = []domain.Drama{{ID: "5008", Episodes: []domain.Episode{{ID: "5014", DramaID: "5008", VID: "protected-vid"}}}}
			}
			store := memory.NewStore(seed)
			if err := store.QueueMediaDeletion(context.Background(), operations.MediaJob{DramaID: "5008", ResourceClass: operations.MediaResourceVideo, RemoteVID: "protected-vid"}); err != nil {
				t.Fatal(err)
			}
			jobs, err := store.ListMediaJobsByStatus(context.Background(), []string{operations.MediaJobQueued}, 10)
			if err != nil || len(jobs) != 1 {
				t.Fatalf("没有清理任务: %v %v", jobs, err)
			}
			provider := &pendingDeletionProvider{}
			service := operations.NewService(memory.OperationsRepositories(store), provider)
			if err := service.ConfigureMediaJobs(operations.MediaJobConfig{PollInterval: time.Second, ReconcileInterval: time.Hour}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := service.StartMediaWorker(ctx); err != nil {
				t.Fatal(err)
			}
			waitForJobStatus(t, service, jobs[0].ID, operations.MediaJobProcessing)
			cancel()
			if err := service.WaitMediaWorker(context.Background()); err != nil {
				t.Fatal(err)
			}
			job, err := service.FindMediaJob(context.Background(), jobs[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			delay, reason := time.Minute, "remote_delete_pending"
			if used {
				delay, reason = 30*time.Minute, "reference_in_use"
			}
			if job.Error != "" || job.NextAttemptAt.Sub(job.UpdatedAt) != delay {
				t.Fatalf("等待状态或检查间隔错误: %+v", job)
			}
			if used && provider.hasDeleted("protected-vid") {
				t.Fatal("受保护媒体被删除")
			}
			found := false
			for _, line := range bytes.Split(logs.Bytes(), []byte("\n")) {
				if len(line) == 0 {
					continue
				}
				var entry map[string]any
				if err := json.Unmarshal(line, &entry); err != nil {
					t.Fatal(err)
				}
				if entry["msg"] == "media deletion waiting" {
					found = true
					if entry["level"] != "INFO" || entry["reason"] != reason || entry["job_id"] != job.ID {
						t.Fatalf("等待原因或日志级别错误: %v", entry)
					}
				}
				if entry["msg"] == "delete drama resource failed" || entry["msg"] == "media processing pending" {
					t.Fatalf("清理等待被当成失败: %v", entry)
				}
			}
			if !found {
				t.Fatal("没有清理等待日志")
			}
		})
	}
}
