package operations

import (
	"context"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
)

type recoverySubjectCatalog struct {
	CatalogRepository
	observed []string
}

// FindEpisode 只允许从分集对应主体恢复，拒绝沿用维护协程原上下文或前一任务主体。
func (r *recoverySubjectCatalog) FindEpisode(ctx context.Context, id string) (*domain.Episode, error) {
	sid := application.SubjectIDFromContext(ctx)
	r.observed = append(r.observed, sid)
	if sid != id {
		return nil, domain.ErrNotFound
	}
	return &domain.Episode{ID: id, VID: "vid-" + id}, nil
}

// TestInterruptedMediaRestoresEachSubject 验证跨主体队列恢复真实查询已绑定视频，不错误重排或误报上传失败。
func TestInterruptedMediaRestoresEachSubject(t *testing.T) {
	old := time.Now().UTC().Add(-time.Hour)
	repo := &diagnosticMediaJobs{}
	for _, id := range []string{"1", "2"} {
		repo.jobs = append(repo.jobs, MediaJob{SubjectID: id, ID: id, EpisodeID: id, Kind: MediaJobUpload, Status: MediaJobRunning, RemoteVID: "vid-" + id, UpdatedAt: old, ProcessingDeadlineAt: time.Now().Add(time.Hour)})
	}
	catalog := &recoverySubjectCatalog{}
	service := NewService(Repositories{Media: MediaPorts{Jobs: repo}, Content: ContentPorts{Catalog: catalog}}, nil)
	runner := &mediaJobRunner{service: service.DramaService, staleRunningAfter: time.Minute, leaseDuration: time.Minute, workerID: "scope-recovery"}
	runner.failInterrupted(application.WithSubject(t.Context(), "unrelated"))
	if len(repo.updates) != 2 || len(catalog.observed) != 2 {
		t.Fatal("恢复没有逐任务检查绑定状态")
	}
	for i, id := range []string{"1", "2"} {
		if catalog.observed[i] != id || repo.updates[i].Status != MediaJobSucceeded {
			t.Fatal("恢复使用错误主体或错误重排", catalog.observed)
		}
	}
}
