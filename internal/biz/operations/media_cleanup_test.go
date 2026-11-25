package operations_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

type cleanupMediaProvider struct {
	mu        sync.Mutex
	deleteErr error
	deleted   []string
}

type observingCleanupMediaProvider struct{ cleanupMediaProvider }

type blockingReconciliationProvider struct {
	started chan struct{}
}

func (provider *blockingReconciliationProvider) ObserveMedia(ctx context.Context, _ string) (operations.MediaObservation, error) {
	select {
	case provider.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return operations.MediaObservation{}, ctx.Err()
}

func (*blockingReconciliationProvider) ObserveMediaResource(_ context.Context, resource operations.MediaResource) (operations.MediaResourceObservation, error) {
	return operations.MediaResourceObservation{ResourceClass: resource.ResourceClass, ProviderAssetID: resource.ProviderAssetID, Exists: false}, nil
}

func (*blockingReconciliationProvider) Delete(context.Context, string, string, string, string) error {
	return nil
}

func TestMediaQueueStartsWhileReconciliationIsBlocked(t *testing.T) {
	store := memory.NewStore(memory.Seed{Dramas: []domain.Drama{{
		ID: "5008", PublishStatus: "draft", Episodes: []domain.Episode{{ID: "ep-1", DramaID: "5008", VID: "existing-vid"}},
	}}})
	job := operations.MediaJob{DramaID: "5008", EpisodeID: "ep-1", ResourceClass: operations.MediaResourceVideo, RemoteVID: "orphan-vid"}
	if err := store.QueueMediaDeletion(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListMediaJobsByStatus(context.Background(), []string{operations.MediaJobQueued}, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("清理任务未入队: jobs=%d err=%v", len(jobs), err)
	}
	provider := &blockingReconciliationProvider{started: make(chan struct{}, 1)}
	service := operations.NewService(memory.OperationsRepositories(store), provider)
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{PollInterval: time.Millisecond, ReconcileInterval: time.Hour}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.StartMediaWorker(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("巡检未开始")
	}
	waitForJobStatus(t, service, jobs[0].ID, operations.MediaJobSucceeded)
}

func (provider *observingCleanupMediaProvider) ObserveMediaResource(_ context.Context, asset operations.MediaResource) (operations.MediaResourceObservation, error) {
	return operations.MediaResourceObservation{
		ResourceClass: asset.ResourceClass, ProviderAssetID: asset.ProviderAssetID,
		Exists: !provider.hasDeleted(asset.ProviderAssetID),
	}, nil
}

func (provider *cleanupMediaProvider) Delete(_ context.Context, _, _, providerUploadID, _ string) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.deleted = append(provider.deleted, providerUploadID)
	return provider.deleteErr
}

func (provider *cleanupMediaProvider) hasDeleted(vid string) bool {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	for _, deleted := range provider.deleted {
		if deleted == vid {
			return true
		}
	}
	return false
}

func TestDeletingDraftDramaQueuesEveryEpisodeVIDForDeletion(t *testing.T) {
	store := memory.NewStore(memory.Seed{Dramas: []domain.Drama{{ID: "5008", PublishStatus: "draft", Episodes: []domain.Episode{{ID: "ep-1", DramaID: "5008", VID: "vid-1"}, {ID: "ep-2", DramaID: "5008", VID: "vid-2"}}}}})
	provider := &observingCleanupMediaProvider{}
	service := operations.NewService(memory.OperationsRepositories(store), provider)
	if err := service.DeleteDrama(context.Background(), "5008"); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListMediaJobsByStatus(context.Background(), []string{operations.MediaJobQueued}, 10)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("deletion tasks missing: %+v %v", jobs, err)
	}
	startCleanupWorker(t, service)
	for _, job := range jobs {
		if job.Kind != operations.MediaJobDelete {
			t.Fatal("not a deletion task")
		}
		waitForJobStatus(t, service, job.ID, operations.MediaJobProcessing)
		waiting, err := service.FindMediaJob(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		// 在测试库推进下次复查时间，验证远端确认；不为测试缩短生产清理等待策略。
		waiting.NextAttemptAt = time.Now().UTC().Add(-time.Second)
		if err := store.UpdateMediaJob(context.Background(), *waiting); err != nil {
			t.Fatal(err)
		}
		waitForJobStatus(t, service, job.ID, operations.MediaJobSucceeded)
	}
}

func TestPublishedSnapshotPreventsDeletionUntilReleased(t *testing.T) {
	published := domain.Drama{ID: "5008", Episodes: []domain.Episode{{ID: "ep-1", DramaID: "5008", VID: "old-vid"}}}
	draft := domain.Drama{ID: "5008", PublishStatus: "draft", Published: &published, Episodes: []domain.Episode{{ID: "ep-1", DramaID: "5008", VID: "new-vid"}}}
	store := memory.NewStore(memory.Seed{Dramas: []domain.Drama{draft}})
	job := operations.MediaJob{DramaID: draft.ID, EpisodeID: "ep-1", ResourceClass: operations.MediaResourceVideo, RemoteVID: "old-vid"}
	if err := store.QueueMediaDeletion(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	used, err := store.IsMediaResourceUsed(context.Background(), job)
	if err != nil || !used {
		t.Fatalf("published video not protected: %v %v", used, err)
	}
}

func TestInterruptedDirectMediaJobReconcilesAnAttachedRemoteVID(t *testing.T) {
	store := memory.NewStore(memory.Seed{Dramas: []domain.Drama{{
		ID: "5008", Title: "Direct recovery", PublishStatus: "draft",
		Episodes: []domain.Episode{{ID: "5014", DramaID: "5008", Number: 1, VID: "remote-vid", MediaStatus: "ready"}},
	}}})
	now := time.Now().UTC().Add(-2 * time.Minute)
	job := operations.MediaJob{
		ID: "media-direct-interrupted", IdempotencyKey: "direct-interrupted-media-key",
		EpisodeID: "5014", FileName: "interrupted.mp4", Size: 5,
		ExpectedVID: "", RemoteVID: "remote-vid", Status: operations.MediaJobRunning, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateMediaJob(context.Background(), &job); err != nil {
		t.Fatal(err)
	}
	service := operations.NewService(memory.OperationsRepositories(store), &directMediaProviderStub{finalizeErr: errors.New("must not refinalize attached media")})
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{
		PollInterval: time.Millisecond, StaleRunningAfter: time.Minute,
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := service.StartMediaWorker(ctx); err != nil {
		t.Fatal(err)
	}
	waitForJobStatus(t, service, job.ID, operations.MediaJobSucceeded)
}

func waitForJobStatus(t *testing.T, service *operations.Service, id, status string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		job, err := service.FindMediaJob(context.Background(), id)
		if err == nil && job.Status == status {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("media job %s did not reach %s", id, status)
}

func startCleanupWorker(t *testing.T, service *operations.Service) {
	t.Helper()
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{
		PollInterval: time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := service.StartMediaWorker(ctx); err != nil {
		t.Fatal(err)
	}
}

func waitForMediaDeletion(t *testing.T, provider *cleanupMediaProvider, vid string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if provider.hasDeleted(vid) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("media %q was not deleted", vid)
}
