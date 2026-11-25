package operations_test

import (
	"context"
	domainapplication "drama-server/internal/biz/application"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

type coverMediaProviderStub struct {
	upload operations.Upload
	bytes  []byte
}

func (provider *coverMediaProviderStub) Delete(context.Context, string, string, string, string) error {
	return nil
}

func (provider *coverMediaProviderStub) UploadCover(_ context.Context, upload operations.Upload) (operations.CoverAsset, error) {
	provider.upload = upload
	provider.bytes, _ = io.ReadAll(upload.Reader)
	return operations.CoverAsset{MID: "cover-1", SpaceName: "short-drama", ProviderFileName: "covers/cover-1.png", URL: "https://cover.example.com/covers/cover-1.png"}, nil
}

type directMediaProviderStub struct {
	finalizedVID string
	finalizeErr  error
	deletedVID   string
}

var _ operations.ProxiedMediaUploadProvider = (*directMediaProviderStub)(nil)

func (provider *directMediaProviderStub) UploadEpisodeMedia(ctx context.Context, upload operations.Upload) (operations.MediaAsset, error) {
	if err := ctx.Err(); err != nil {
		return operations.MediaAsset{}, err
	}
	if _, err := io.ReadAll(upload.Reader); err != nil {
		return operations.MediaAsset{}, err
	}
	return operations.MediaAsset{VID: "proxy-vid", UploadRequestID: "proxy-request"}, nil
}

type concurrentDirectMediaProvider struct {
	started chan string
	release chan struct{}
}

type processingDirectMediaProvider struct {
	processingTimeout time.Duration
}

type reconcilingMediaProvider struct {
	*directMediaProviderStub
	published    atomic.Bool
	synced       atomic.Int32
	lastMetadata atomic.Value
}

func (provider *reconcilingMediaProvider) ObserveMedia(_ context.Context, vid string) (operations.MediaObservation, error) {
	if provider.published.Load() {
		return operations.MediaObservation{VID: vid, Exists: true, PublishStatus: operations.MediaProviderPublished}, nil
	}
	return operations.MediaObservation{VID: vid, Exists: false, PublishStatus: operations.MediaProviderMissing}, nil
}

func (provider *reconcilingMediaProvider) SyncVODEpisodeMetadata(
	_ context.Context,
	metadata operations.VODEpisodeMetadata,
	_ operations.MediaObservation,
) error {
	provider.synced.Add(1)
	provider.lastMetadata.Store(metadata)
	return nil
}

type denyHeadMediaClaimRepository struct {
	*memory.Store
	deniedID string
}

func (repository *denyHeadMediaClaimRepository) ClaimMediaJobLease(
	ctx context.Context,
	claim operations.MediaJobLeaseClaim,
) (bool, error) {
	if claim.ID == repository.deniedID {
		return false, nil
	}
	return repository.Store.ClaimMediaJobLease(ctx, claim)
}

func (provider *concurrentDirectMediaProvider) Delete(context.Context, string, string, string, string) error {
	return nil
}

func (provider *concurrentDirectMediaProvider) ProcessingTimeout(context.Context) (time.Duration, error) {
	return 15 * time.Minute, nil
}

func (provider *concurrentDirectMediaProvider) UploadAuthorization(context.Context) (operations.MediaUploadAuthorization, error) {
	return validDirectAuthorization(), nil
}

func (provider *concurrentDirectMediaProvider) SyncVODEpisodeMetadata(context.Context, operations.VODEpisodeMetadata, operations.MediaObservation) error {
	return nil
}

func (provider *concurrentDirectMediaProvider) FinalizeUpload(ctx context.Context, vid, requestID string) (operations.MediaAsset, error) {
	select {
	case provider.started <- vid:
	case <-ctx.Done():
		return operations.MediaAsset{}, ctx.Err()
	}
	select {
	case <-provider.release:
		return operations.MediaAsset{VID: vid, Status: "ready"}, nil
	case <-ctx.Done():
		return operations.MediaAsset{}, ctx.Err()
	}
}

func (processingDirectMediaProvider) Delete(context.Context, string, string, string, string) error {
	return nil
}

func (provider processingDirectMediaProvider) ProcessingTimeout(context.Context) (time.Duration, error) {
	if provider.processingTimeout > 0 {
		return provider.processingTimeout, nil
	}
	return 15 * time.Minute, nil
}

func (processingDirectMediaProvider) UploadAuthorization(context.Context) (operations.MediaUploadAuthorization, error) {
	return validDirectAuthorization(), nil
}

func (processingDirectMediaProvider) SyncVODEpisodeMetadata(context.Context, operations.VODEpisodeMetadata, operations.MediaObservation) error {
	return nil
}

func (processingDirectMediaProvider) FinalizeUpload(_ context.Context, vid, requestID string) (operations.MediaAsset, error) {
	if vid == "vid-processing" {
		return operations.MediaAsset{VID: vid}, operations.ErrMediaProcessing
	}
	return operations.MediaAsset{VID: vid, Status: "ready"}, nil
}

func (provider *directMediaProviderStub) Delete(_ context.Context, _, _, providerUploadID, _ string) error {
	provider.deletedVID = providerUploadID
	return nil
}

func (provider *directMediaProviderStub) ProcessingTimeout(context.Context) (time.Duration, error) {
	return 15 * time.Minute, nil
}

func (provider *directMediaProviderStub) UploadAuthorization(context.Context) (operations.MediaUploadAuthorization, error) {
	authorization := validDirectAuthorization()
	authorization.ProcessActions = []operations.MediaUploadAction{{Name: "GetMeta"}, {Name: "StartWorkflow", Input: map[string]string{"TemplateId": "workflow-1"}}}
	return authorization, nil
}

func (provider *directMediaProviderStub) SyncVODEpisodeMetadata(context.Context, operations.VODEpisodeMetadata, operations.MediaObservation) error {
	return nil
}

func validDirectAuthorization() operations.MediaUploadAuthorization {
	return operations.MediaUploadAuthorization{
		Mode: operations.MediaUploadModeDirect, AppID: "1038811", SpaceName: "short-drama",
		Region: "ap-southeast-1", MaxFileBytes: 20 << 30,
		Token: operations.MediaUploadToken{
			AccessKeyID: "temporary-ak", SecretAccessKey: "temporary-sk", SessionToken: "temporary-session",
			CurrentTime: "2026-07-14T00:00:00Z", ExpiredTime: "2026-07-14T01:00:00Z",
		},
	}
}

func (provider *directMediaProviderStub) FinalizeUpload(_ context.Context, vid, requestID string) (operations.MediaAsset, error) {
	provider.finalizedVID = vid
	return operations.MediaAsset{VID: vid, Status: "ready"}, provider.finalizeErr
}

func TestOperatorCreatesDramaAggregate(t *testing.T) {
	t.Parallel()

	repository := memory.NewStore(memory.Seed{})
	service := operations.NewService(memory.OperationsRepositories(repository), nil)
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "A Clean Start", Status: "updating", Year: 2026, Language: "en",
		DramaType: 4, Episodes: []operations.EpisodeInput{{Number: 1, Title: "The invitation", DurationSeconds: 65}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if drama.ID == "" || drama.PublishStatus != "draft" {
		t.Fatalf("unexpected drama defaults: %+v", drama)
	}

	episode := drama.Episodes[0]
	if episode.ID == "" || episode.DramaID != drama.ID || episode.MediaStatus != "pending" {
		t.Fatalf("unexpected episode: %+v", episode)
	}

	stored, err := service.FindDrama(context.Background(), drama.ID)
	if err != nil || len(stored.Episodes) != 1 || stored.Episodes[0].ID != episode.ID {
		t.Fatalf("episode was not persisted: drama=%+v err=%v", stored, err)
	}
}

func TestOperatorCreatesDramaWithEpisodesAsOneAggregate(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{})
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "Aggregate Drama",
		Episodes: []operations.EpisodeInput{
			{Number: 1, Title: "Episode 1"},
			{Number: 2, Title: "Episode 2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(drama.Episodes) != 2 || drama.Episodes[0].ID == "" || drama.Episodes[1].ID == "" {
		t.Fatalf("aggregate create did not persist child episodes: %+v", drama)
	}
	stored, err := store.FindDrama(context.Background(), drama.ID)
	if err != nil || len(stored.Episodes) != 2 || stored.Episodes[1].Number != 2 {
		t.Fatalf("stored aggregate is incomplete: drama=%+v err=%v", stored, err)
	}
}

func TestOperatorAggregateUpdatePreservesEpisodeMediaAndDeletesRemovedChildren(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{})
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "Aggregate Drama",
		Episodes: []operations.EpisodeInput{
			{Number: 1, Title: "Episode 1"},
			{Number: 2, Title: "Episode 2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstID := drama.Episodes[0].ID
	removedID := drama.Episodes[1].ID
	attached := drama.Episodes[0]
	attached.VID = "byteplus-vid-1"
	attached.MediaStatus = "ready"
	if err := store.SaveEpisodeMedia(context.Background(), attached, *drama, "", ""); err != nil {
		t.Fatal(err)
	}

	updated, err := service.UpdateDrama(context.Background(), drama.ID, operations.DramaInput{
		Title: "Aggregate Drama Revised",
		Episodes: []operations.EpisodeInput{
			{ID: firstID, Number: 1, Title: "Episode 1 Revised"},
			{Number: 2, Title: "Episode 2 Replacement"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Episodes) != 2 || updated.Episodes[0].VID != "byteplus-vid-1" || updated.Episodes[0].MediaStatus != "ready" {
		t.Fatalf("aggregate update lost existing media: %+v", updated.Episodes)
	}
	if updated.Episodes[1].ID == "" || updated.Episodes[1].ID == removedID {
		t.Fatalf("replacement child was not created correctly: %+v", updated.Episodes[1])
	}
	if _, err := store.FindEpisode(context.Background(), removedID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("removed child still exists: %v", err)
	}
}

func TestEpisodeMediaSaveRejectsAStaleExpectedVID(t *testing.T) {
	store := memory.NewStore(memory.Seed{Dramas: []domain.Drama{{
		ID: "5008", Title: "Concurrent uploads", PublishStatus: "draft",
		Episodes: []domain.Episode{{ID: "5014", DramaID: "5008", Number: 1, VID: "old-vid", MediaStatus: "ready"}},
	}}})
	drama, _ := store.FindDrama(context.Background(), "5008")
	first := drama.Episodes[0]
	first.VID = "first-vid"
	if err := store.SaveEpisodeMedia(context.Background(), first, *drama, "old-vid", ""); err != nil {
		t.Fatal(err)
	}
	second := drama.Episodes[0]
	second.VID = "second-vid"
	if err := store.SaveEpisodeMedia(context.Background(), second, *drama, "old-vid", ""); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("stale media replacement was not rejected: %v", err)
	}
}

func TestDirectMediaUploadAuthorizationAndCompletionAreServerAuthoritative(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	provider := &directMediaProviderStub{}
	service := operations.NewService(memory.OperationsRepositories(store), provider)
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{PollInterval: time.Millisecond, RetryBaseDelay: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.StartMediaWorker(ctx); err != nil {
		t.Fatal(err)
	}

	authorization, err := service.MediaUploadAuthorization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if authorization.Mode != operations.MediaUploadModeDirect || authorization.AppID != "1038811" ||
		authorization.SpaceName != "short-drama" || authorization.Region != "ap-southeast-1" ||
		authorization.Token.SessionToken == "" || len(authorization.ProcessActions) != 2 {
		t.Fatalf("unexpected direct upload authorization: %+v", authorization)
	}

	drama, err := service.CreateDrama(ctx, operations.DramaInput{
		Title: "Direct upload", Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode 1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := operations.DirectMediaUploadInput{
		UploadID: reserveEpisodeVideo(t, service, drama.Episodes[0].ID, "episode-1.mp4", 1024),
		VID:      "direct-vid-1", UploadRequestID: "upload-request-1", FileName: "episode-1.mp4", Size: 1024,
	}
	job, err := service.QueueDirectEpisodeMedia(ctx, drama.Episodes[0].ID, "direct-media-key-0001", input)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := service.QueueDirectEpisodeMedia(ctx, drama.Episodes[0].ID, "direct-media-key-0001", input)
	if err != nil || repeated.ID != job.ID {
		t.Fatalf("direct completion was not idempotent: first=%+v repeated=%+v err=%v", job, repeated, err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, findErr := service.FindMediaJob(ctx, job.ID)
		if findErr != nil {
			t.Fatal(findErr)
		}
		if current.Status == operations.MediaJobSucceeded {
			if current.Episode == nil ||
				current.Episode.VID != input.VID || current.Episode.MediaStatus != "ready" || provider.finalizedVID != input.VID {
				t.Fatalf("direct media job did not attach the finalized VID: %+v", current)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("direct media job did not complete")
}

func TestListDramaMediaJobsReturnsLatestJobForEachEpisode(t *testing.T) {
	repository := memory.NewStore(memory.Seed{})
	service := operations.NewService(memory.OperationsRepositories(repository), &directMediaProviderStub{})
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{}); err != nil {
		t.Fatal(err)
	}
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "Batch jobs",
		Episodes: []operations.EpisodeInput{
			{Number: 1, Title: "Episode 1"},
			{Number: 2, Title: "Episode 2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstEpisodeOld, err := service.QueueDirectEpisodeMedia(context.Background(), drama.Episodes[0].ID, "batch-media-key-old-0001", operations.DirectMediaUploadInput{
		UploadID: reserveEpisodeVideo(t, service, drama.Episodes[0].ID, "episode-1.mp4", 1024), VID: "vid-old", FileName: "episode-1.mp4", Size: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstEpisodeOld.Status = operations.MediaJobFailed
	firstEpisodeOld.Error = "superseded test upload"
	firstEpisodeOld.UpdatedAt = time.Now().UTC()
	if err := repository.UpdateMediaJob(context.Background(), *firstEpisodeOld); err != nil {
		t.Fatal(err)
	}
	firstEpisodeLatest, err := service.QueueDirectEpisodeMedia(context.Background(), drama.Episodes[0].ID, "batch-media-key-new-0001", operations.DirectMediaUploadInput{
		UploadID: reserveEpisodeVideo(t, service, drama.Episodes[0].ID, "episode-1.mp4", 1024), VID: "vid-new", FileName: "episode-1.mp4", Size: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondEpisode, err := service.QueueDirectEpisodeMedia(context.Background(), drama.Episodes[1].ID, "batch-media-key-episode-2", operations.DirectMediaUploadInput{
		UploadID: reserveEpisodeVideo(t, service, drama.Episodes[1].ID, "episode-2.mp4", 1024), VID: "vid-2", FileName: "episode-2.mp4", Size: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}

	jobs, err := service.ListDramaMediaJobs(context.Background(), drama.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].ID != firstEpisodeLatest.ID || jobs[1].ID != secondEpisode.ID {
		t.Fatalf("unexpected latest drama jobs: old=%s latest=%s jobs=%+v", firstEpisodeOld.ID, firstEpisodeLatest.ID, jobs)
	}
}

func TestDirectMediaCompletionRejectsCrossEpisodeAndFileTampering(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	service := operations.NewService(memory.OperationsRepositories(store), &directMediaProviderStub{})
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{}); err != nil {
		t.Fatal(err)
	}
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "Owned upload", Episodes: []operations.EpisodeInput{{Number: 1, Title: "One"}, {Number: 2, Title: "Two"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assetID := reserveEpisodeVideo(t, service, drama.Episodes[0].ID, "episode-1.mp4", 1024)
	base := operations.DirectMediaUploadInput{UploadID: assetID, VID: "owned-vid", FileName: "episode-1.mp4", Size: 1024}
	if _, err := service.QueueDirectEpisodeMedia(context.Background(), drama.Episodes[1].ID, "cross-episode-key-0001", base); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("cross-episode completion must conflict, got %v", err)
	}
	tampered := base
	tampered.FileName = "different.mp4"
	if _, err := service.QueueDirectEpisodeMedia(context.Background(), drama.Episodes[0].ID, "tampered-file-key-0001", tampered); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("tampered file metadata must conflict, got %v", err)
	}
	asset, err := store.FindMediaJob(context.Background(), assetID)
	if err != nil || asset.Status != operations.MediaJobReserved || asset.RemoteVID != "" {
		t.Fatalf("rejected completion changed reservation: asset=%+v err=%v", asset, err)
	}
}

func TestNewReservationReleasesAbandonedReservationForSameEpisode(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	service := operations.NewService(memory.OperationsRepositories(store), &directMediaProviderStub{})
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{PollInterval: time.Millisecond, RetryBaseDelay: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "Replace reservation", Episodes: []operations.EpisodeInput{{Number: 1, Title: "One"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstUploadID := reserveEpisodeVideo(t, service, drama.Episodes[0].ID, "episode-old.mp4", 1024)
	firstAsset, err := store.FindMediaJob(context.Background(), firstUploadID)
	if err != nil {
		t.Fatal(err)
	}
	firstAsset.RemoteVID = "vid-abandoned"
	firstAsset.Status = operations.MediaJobReserved
	firstAsset.UpdatedAt = time.Now().UTC()
	if err := store.UpdateMediaJob(context.Background(), firstAsset); err != nil {
		t.Fatal(err)
	}
	secondUploadID := reserveEpisodeVideo(t, service, drama.Episodes[0].ID, "episode-new.mp4", 2048)
	if secondUploadID == firstUploadID {
		t.Fatal("replacement reservation must use a new managed asset")
	}
	first, err := store.FindMediaJob(context.Background(), firstUploadID)
	if err != nil || first.Status != operations.MediaJobFailed {
		t.Fatalf("old reservation must expire: %+v %v", first, err)
	}
	second, err := store.FindMediaJob(context.Background(), secondUploadID)
	if err != nil || second.Status != operations.MediaJobReserved {
		t.Fatalf("new reservation missing: %+v %v", second, err)
	}
	count, err := store.CountMediaDeletionJobs(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("old remote upload must enter deletion queue: count=%d err=%v", count, err)
	}
}

func TestDirectMediaFinalizationFailureNeverMarksEpisodeReady(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	provider := &directMediaProviderStub{finalizeErr: errors.New("workflow failed")}
	service := operations.NewService(memory.OperationsRepositories(store), provider)
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{PollInterval: time.Millisecond, RetryBaseDelay: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.StartMediaWorker(ctx); err != nil {
		t.Fatal(err)
	}
	drama, err := service.CreateDrama(ctx, operations.DramaInput{
		Title: "Failed direct upload", Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode 1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.QueueDirectEpisodeMedia(ctx, drama.Episodes[0].ID, "direct-media-key-0002", operations.DirectMediaUploadInput{
		UploadID: reserveEpisodeVideo(t, service, drama.Episodes[0].ID, "episode-1.mp4", 1024), VID: "direct-vid-failed", FileName: "episode-1.mp4", Size: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, findErr := service.FindMediaJob(ctx, job.ID)
		if findErr != nil {
			t.Fatal(findErr)
		}
		if current.Status == operations.MediaJobFailed {
			stored, findDramaErr := service.FindDrama(ctx, drama.ID)
			if findDramaErr != nil {
				t.Fatal(findDramaErr)
			}
			if stored.Episodes[0].MediaStatus == "ready" || stored.Episodes[0].VID != "" {
				t.Fatalf("failed direct upload changed episode authority: %+v", stored.Episodes[0])
			}
			if provider.deletedVID != "" {
				t.Fatalf("retryable direct media was deleted before the job could be retried: deleted=%q", provider.deletedVID)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("failed direct media job did not reach a terminal state")
}

func TestDirectMediaJobsFinalizeWithBoundedConcurrency(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	provider := &concurrentDirectMediaProvider{started: make(chan string, 3), release: make(chan struct{})}
	service := operations.NewService(memory.OperationsRepositories(store), provider)
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{
		PollInterval: time.Millisecond, WorkerConcurrency: 3,
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.StartMediaWorker(ctx); err != nil {
		t.Fatal(err)
	}
	drama, err := service.CreateDrama(ctx, operations.DramaInput{
		Title: "Concurrent direct upload",
		Episodes: []operations.EpisodeInput{
			{Number: 1, Title: "Episode 1"}, {Number: 2, Title: "Episode 2"}, {Number: 3, Title: "Episode 3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, episode := range drama.Episodes {
		_, err := service.QueueDirectEpisodeMedia(ctx, episode.ID, "concurrent-direct-key-"+string(rune('a'+index)), operations.DirectMediaUploadInput{
			UploadID: reserveEpisodeVideo(t, service, episode.ID, "episode.mp4", 1024), VID: "concurrent-vid-" + string(rune('1'+index)), FileName: "episode.mp4", Size: 1024,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for count := 0; count < 3; count++ {
		select {
		case <-provider.started:
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("only %d of 3 direct jobs started concurrently", count)
		}
	}
	close(provider.release)
}

func TestProcessingDirectMediaDoesNotBlockTheNextJob(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	service := operations.NewService(memory.OperationsRepositories(store), processingDirectMediaProvider{})
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{
		PollInterval: time.Millisecond, WorkerConcurrency: 1,
	}); err != nil {
		t.Fatal(err)
	}
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "Processing queue",
		Episodes: []operations.EpisodeInput{
			{Number: 1, Title: "Episode 1"}, {Number: 2, Title: "Episode 2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	processingJob, err := service.QueueDirectEpisodeMedia(context.Background(), drama.Episodes[0].ID, "processing-direct-key-1", operations.DirectMediaUploadInput{
		UploadID: reserveEpisodeVideo(t, service, drama.Episodes[0].ID, "episode-1.mp4", 1024), VID: "vid-processing", FileName: "episode-1.mp4", Size: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	readyJob, err := service.QueueDirectEpisodeMedia(context.Background(), drama.Episodes[1].ID, "processing-direct-key-2", operations.DirectMediaUploadInput{
		UploadID: reserveEpisodeVideo(t, service, drama.Episodes[1].ID, "episode-2.mp4", 1024), VID: "vid-ready", FileName: "episode-2.mp4", Size: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.StartMediaWorker(ctx); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		processing, processingErr := service.FindMediaJob(ctx, processingJob.ID)
		ready, readyErr := service.FindMediaJob(ctx, readyJob.ID)
		if processingErr != nil || readyErr != nil {
			t.Fatalf("find media jobs: processing=%v ready=%v", processingErr, readyErr)
		}
		if processing.Status == operations.MediaJobProcessing && ready.Status == operations.MediaJobSucceeded {
			return
		}
		time.Sleep(time.Millisecond)
	}
	processing, _ := service.FindMediaJob(ctx, processingJob.ID)
	ready, _ := service.FindMediaJob(ctx, readyJob.ID)
	t.Fatalf("a processing VID blocked the next media job: processing=%+v ready=%+v", processing, ready)
}

func TestMediaWorkerClaimsTheNextCandidateWhenTheHeadJobWasAlreadyClaimed(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	repository := &denyHeadMediaClaimRepository{Store: store}
	repositories := memory.OperationsRepositories(store)
	repositories.Media.Jobs = repository
	service := operations.NewService(repositories, processingDirectMediaProvider{})
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{
		PollInterval: time.Millisecond, WorkerConcurrency: 2,
	}); err != nil {
		t.Fatal(err)
	}
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "Claim contention",
		Episodes: []operations.EpisodeInput{
			{Number: 1, Title: "Episode 1"}, {Number: 2, Title: "Episode 2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := service.QueueDirectEpisodeMedia(context.Background(), drama.Episodes[0].ID, "claim-contention-key-1", operations.DirectMediaUploadInput{
		UploadID: reserveEpisodeVideo(t, service, drama.Episodes[0].ID, "episode-1.mp4", 1024), VID: "vid-blocked", FileName: "episode-1.mp4", Size: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository.deniedID = blocked.ID
	next, err := service.QueueDirectEpisodeMedia(context.Background(), drama.Episodes[1].ID, "claim-contention-key-2", operations.DirectMediaUploadInput{
		UploadID: reserveEpisodeVideo(t, service, drama.Episodes[1].ID, "episode-2.mp4", 1024), VID: "vid-ready", FileName: "episode-2.mp4", Size: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.StartMediaWorker(ctx); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		current, findErr := service.FindMediaJob(ctx, next.ID)
		if findErr != nil {
			t.Fatal(findErr)
		}
		if current.Status == operations.MediaJobSucceeded {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("claim contention on the head job prevented the next media job from running")
}

func TestProcessingDirectMediaFailsAfterTheConfiguredDeadline(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	service := operations.NewService(memory.OperationsRepositories(store), processingDirectMediaProvider{processingTimeout: 5 * time.Millisecond})
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{
		PollInterval: time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "Never ready", Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode 1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.QueueDirectEpisodeMedia(context.Background(), drama.Episodes[0].ID, "processing-timeout-key-1", operations.DirectMediaUploadInput{
		UploadID: reserveEpisodeVideo(t, service, drama.Episodes[0].ID, "episode-1.mp4", 1024), VID: "vid-processing", FileName: "episode-1.mp4", Size: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.ProcessingStartedAt.IsZero() || job.ProcessingDeadlineAt.IsZero() || job.ProcessingDeadlineAt.Sub(job.ProcessingStartedAt) != 5*time.Millisecond {
		t.Fatalf("任务没有固化媒资配置中的处理超时：%+v", job)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.StartMediaWorker(ctx); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		current, findErr := service.FindMediaJob(ctx, job.ID)
		if findErr != nil {
			t.Fatal(findErr)
		}
		if current.Status == operations.MediaJobFailed {
			if !strings.Contains(strings.ToLower(current.Error), "timed out") {
				t.Fatalf("processing timeout did not produce a diagnostic error: %+v", current)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("never-ready direct media job did not fail after its processing deadline")
}

func TestExpiredRunningDirectMediaJobIsRecoveredAfterRestart(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	service := operations.NewService(memory.OperationsRepositories(store), processingDirectMediaProvider{processingTimeout: 5 * time.Millisecond})
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{
		PollInterval: time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "Interrupted processing", Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode 1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.QueueDirectEpisodeMedia(context.Background(), drama.Episodes[0].ID, "interrupted-direct-key-1", operations.DirectMediaUploadInput{
		UploadID: reserveEpisodeVideo(t, service, drama.Episodes[0].ID, "episode-1.mp4", 1024), VID: "vid-processing", FileName: "episode-1.mp4", Size: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Add(-time.Second)
	deadlineAt := startedAt.Add(5 * time.Millisecond)
	job.Status = operations.MediaJobRunning
	job.ProcessingStartedAt = startedAt
	job.ProcessingDeadlineAt = deadlineAt
	job.UpdatedAt = time.Now().UTC()
	if err := store.UpdateMediaJob(context.Background(), *job); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.StartMediaWorker(ctx); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		current, findErr := service.FindMediaJob(ctx, job.ID)
		if findErr != nil {
			t.Fatal(findErr)
		}
		if current.Status == operations.MediaJobFailed {
			if !strings.Contains(strings.ToLower(current.Error), "timed out") {
				t.Fatalf("interrupted processing timeout did not produce a diagnostic error: %+v", current)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("expired running direct media job was not recovered after restart")
}

func TestOnlyOneActiveMediaJobCanExistPerEpisode(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	service := operations.NewService(memory.OperationsRepositories(store), processingDirectMediaProvider{})
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{}); err != nil {
		t.Fatal(err)
	}
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "Single active upload", Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode 1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstUploadID := reserveEpisodeVideo(t, service, drama.Episodes[0].ID, "episode-1.mp4", 1024)
	first, err := service.QueueDirectEpisodeMedia(context.Background(), drama.Episodes[0].ID, "single-active-media-key-1", operations.DirectMediaUploadInput{
		UploadID: firstUploadID, VID: "vid-processing", FileName: "episode-1.mp4", Size: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	same, err := service.QueueDirectEpisodeMedia(context.Background(), drama.Episodes[0].ID, "single-active-media-key-same", operations.DirectMediaUploadInput{
		UploadID: firstUploadID, VID: "vid-processing", FileName: "episode-1.mp4", Size: 1024,
	})
	if err != nil || same.ID != first.ID {
		t.Fatalf("the same uploaded VID should reuse its active job: first=%+v same=%+v err=%v", first, same, err)
	}
	referenced, err := store.IsMediaResourceUsed(context.Background(), *first)
	if err != nil || !referenced {
		t.Fatalf("an active media job VID must be protected from cleanup: referenced=%t err=%v", referenced, err)
	}
	if _, err := service.QueueDirectEpisodeMedia(context.Background(), drama.Episodes[0].ID, "single-active-media-key-2", operations.DirectMediaUploadInput{
		UploadID: reserveEpisodeVideo(t, service, drama.Episodes[0].ID, "episode-1-replacement.mp4", 2048), VID: "vid-replacement", FileName: "episode-1-replacement.mp4", Size: 2048,
	}); !errors.Is(err, operations.ErrMediaUploadDiscarded) {
		t.Fatalf("second active job for one episode should conflict, got %v", err)
	}
	cleanupJobs, err := store.ListMediaJobsByStatus(context.Background(), []string{operations.MediaJobQueued}, 10)
	if err != nil {
		t.Fatal(err)
	}
	filtered := cleanupJobs[:0]
	for _, job := range cleanupJobs {
		if job.Kind == operations.MediaJobDelete {
			filtered = append(filtered, job)
		}
	}
	cleanupJobs = filtered
	if len(cleanupJobs) != 1 || cleanupJobs[0].RemoteVID != "vid-replacement" {
		t.Fatalf("conflicting uploaded VID must be queued for cleanup, got %+v", cleanupJobs)
	}
}

func TestBoundMediaReconciliationMarksMissingAndRestoresPublishedVideo(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	provider := &reconcilingMediaProvider{directMediaProviderStub: &directMediaProviderStub{}}
	service := operations.NewService(memory.OperationsRepositories(store), provider)
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{PollInterval: time.Millisecond, ReconcileInterval: time.Hour}); err != nil {
		t.Fatal(err)
	}
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "Reconciled video", Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode 1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assetID := reserveEpisodeVideo(t, service, drama.Episodes[0].ID, "episode.mp4", 1024)
	if _, err := service.QueueDirectEpisodeMedia(context.Background(), drama.Episodes[0].ID, "reconcile-media-key-0001", operations.DirectMediaUploadInput{
		UploadID: assetID, VID: "reconcile-vid", FileName: "episode.mp4", Size: 1024,
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.StartMediaWorker(ctx); err != nil {
		t.Fatal(err)
	}
	waitForEpisodeMediaStatus(t, service, drama.ID, "missing")
	episode, err := store.FindEpisode(context.Background(), drama.Episodes[0].ID)
	if err != nil || episode.MediaStatus != "missing" {
		t.Fatalf("missing VOD state was not synchronized: episode=%+v err=%v", episode, err)
	}
	provider.published.Store(true)
	if _, err := service.UpdateDrama(context.Background(), drama.ID, operations.DramaInput{
		Title: "Renamed authoritative video", Description: "Authoritative drama description", TagIDs: []string{"21"},
		Episodes: []operations.EpisodeInput{{
			ID: drama.Episodes[0].ID, Number: 1, Title: "Renamed episode", Description: "Authoritative episode description",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	waitForEpisodeMediaStatus(t, service, drama.ID, "ready")
	episode, err = store.FindEpisode(context.Background(), drama.Episodes[0].ID)
	if err != nil || episode.MediaStatus != "ready" {
		t.Fatalf("published VOD recovery was not synchronized: episode=%+v err=%v", episode, err)
	}
	if provider.synced.Load() == 0 {
		t.Fatal("ready media reconciliation did not enforce authoritative VOD metadata")
	}
	metadata, ok := provider.lastMetadata.Load().(operations.VODEpisodeMetadata)
	if !ok || metadata.Title != "Renamed authoritative video - Episode 001 - Renamed episode" ||
		!strings.Contains(metadata.Description, "Authoritative drama description") || metadata.PrimaryClassificationKey != "TT-TAG-21" {
		t.Fatalf("reconciliation did not use edited admin metadata: %+v", metadata)
	}
}

// TestProxyEpisodeMediaUploadDetachesRequestContext 确保浏览器断开后服务端仍能完成远端上传与任务入库。
func TestProxyEpisodeMediaUploadDetachesRequestContext(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	service := operations.NewService(memory.OperationsRepositories(store), &directMediaProviderStub{})
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{PollInterval: time.Millisecond, RetryBaseDelay: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	drama, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "代理上传上下文", Episodes: []operations.EpisodeInput{{Number: 1, Title: "第1集"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	uploadID := reserveEpisodeVideo(t, service, drama.Episodes[0].ID, "episode-1.mp4", 5)
	accepted, err := service.BeginProxiedEpisodeMediaUpload(context.Background(), drama.Episodes[0].ID, uploadID)
	if err != nil || accepted.Status != operations.MediaJobUploading {
		t.Fatalf("代理上传未进入异步状态: job=%+v err=%v", accepted, err)
	}
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	job, err := service.UploadEpisodeMedia(requestCtx, drama.Episodes[0].ID, uploadID, operations.Upload{
		FileName: "episode-1.mp4", Size: 5, Reader: strings.NewReader("video"),
	})
	if err != nil {
		t.Fatalf("代理上传不应因原请求取消而失败: %v", err)
	}
	if job == nil || job.Status != operations.MediaJobQueued {
		t.Fatalf("代理上传未入队: %+v", job)
	}
}

func waitForEpisodeMediaStatus(t *testing.T, service *operations.Service, dramaID, status string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		drama, err := service.FindDrama(context.Background(), dramaID)
		if err == nil && len(drama.Episodes) == 1 && drama.Episodes[0].MediaStatus == status {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("episode did not reach media status %q", status)
}

func reserveEpisodeVideo(t *testing.T, service *operations.Service, episodeID, fileName string, size int64) string {
	t.Helper()
	reservation, err := service.ReserveEpisodeMediaUpload(context.Background(), episodeID, operations.EpisodeMediaUploadReservationInput{
		FileName: fileName,
		Size:     size,
	})
	if err != nil {
		t.Fatalf("reserve episode media upload: %v", err)
	}
	if reservation.UploadID == "" || !strings.Contains(reservation.Authorization.CallbackArgs, reservation.UploadID) {
		t.Fatalf("reservation omitted managed media identity: %+v", reservation)
	}
	for _, action := range reservation.Authorization.ProcessActions {
		if action.Name == "AddOptionInfo" {
			t.Fatalf("reservation must not submit VOD metadata during direct upload: %+v", reservation.Authorization.ProcessActions)
		}
	}
	return reservation.UploadID
}

// TestApplicationPlatformIdentityIsImmutable 防止新 TikTok 应用继承原应用授权，且改名不能改变历史用户身份。
func TestApplicationPlatformIdentityIsImmutable(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore(memory.Seed{})
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	input := operations.ApplicationInput{SubjectID: "1", MonetizationMode: "IAA", AdUnits: domainapplication.AdUnits{RewardedVideo: []string{"reward"}, Interstitial: []string{"interstitial"}}, Name: "原应用", TikTokAppID: "7637420375425239060", ClientKey: "original-key", SecretRef: "ORIGINAL_SECRET", ComplianceBaseURL: "https://legal.example.test", Status: "active"}
	app, err := service.CreateApplication(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	app.IdentityHashNamespace = "legacy-identity"
	if err := store.SaveApplication(ctx, *app); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"clientKey", "tiktokAppId"} {
		changed := input
		if field == "clientKey" {
			changed.ClientKey = "new-platform-key"
		} else {
			changed.TikTokAppID = "7637420375425239061"
		}
		if _, err := service.UpdateApplication(ctx, app.ID, changed); !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("允许更换 %s：%v", field, err)
		}
	}
	input.Name = "同一应用的新名称"
	input.SecretRef = "ROTATED_SECRET"
	if _, err := service.UpdateApplication(ctx, app.ID, input); err != nil {
		t.Fatalf("同一应用无法改名或轮换密钥引用：%v", err)
	}
	saved, err := service.FindApplication(ctx, app.ID)
	if err != nil || saved.ClientKey != app.ClientKey || saved.TikTokAppID != app.TikTokAppID || saved.SecretRef != input.SecretRef || saved.IdentityHashNamespace != app.IdentityHashNamespace {
		t.Fatalf("平台身份或可变配置错误：%+v %v", saved, err)
	}
}
