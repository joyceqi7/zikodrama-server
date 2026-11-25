package memory

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"drama-server/internal/biz/operations"
)

func TestMediaJobLeasePreventsTakeoverUntilItExpires(t *testing.T) {
	store := NewStore(Seed{})
	createdAt := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	job := operations.MediaJob{
		ID: "media-lease", IdempotencyKey: "media-lease-key-1", EpisodeID: "5014",
		Status: operations.MediaJobQueued, NextAttemptAt: createdAt, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	if err := store.CreateMediaJob(context.Background(), &job); err != nil {
		t.Fatal(err)
	}

	firstClaim := operations.MediaJobLeaseClaim{
		ID: job.ID, ExpectedStatus: operations.MediaJobQueued, ExpectedUpdatedAt: job.UpdatedAt,
		LeaseOwner: "worker-a", ClaimedAt: createdAt.Add(time.Second), LeaseExpiresAt: createdAt.Add(time.Minute),
	}
	claimed, err := store.ClaimMediaJobLease(context.Background(), firstClaim)
	if err != nil || !claimed {
		t.Fatalf("first lease claim failed: claimed=%v err=%v", claimed, err)
	}

	claimed, err = store.ClaimMediaJobLease(context.Background(), operations.MediaJobLeaseClaim{
		ID: job.ID, ExpectedStatus: operations.MediaJobRunning, ExpectedUpdatedAt: firstClaim.ClaimedAt,
		LeaseOwner: "worker-b", ClaimedAt: createdAt.Add(30 * time.Second), LeaseExpiresAt: createdAt.Add(2 * time.Minute),
	})
	if err != nil || claimed {
		t.Fatalf("unexpired lease was taken over: claimed=%v err=%v", claimed, err)
	}

	claimed, err = store.ClaimMediaJobLease(context.Background(), operations.MediaJobLeaseClaim{
		ID: job.ID, ExpectedStatus: operations.MediaJobRunning, ExpectedUpdatedAt: firstClaim.ClaimedAt,
		LeaseOwner: "worker-b", ClaimedAt: createdAt.Add(time.Minute), LeaseExpiresAt: createdAt.Add(2 * time.Minute),
	})
	if err != nil || !claimed {
		t.Fatalf("expired lease was not recovered: claimed=%v err=%v", claimed, err)
	}

	stale := job
	stale.Status = operations.MediaJobSucceeded
	stale.LeaseOwner = "worker-a"
	stale.UpdatedAt = createdAt.Add(2 * time.Minute)
	if err := store.UpdateMediaJob(context.Background(), stale); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("stale worker update must be rejected, got %v", err)
	}
}

func TestOnlyOneWorkerCanClaimAMediaCleanupJob(t *testing.T) {
	store := NewStore(Seed{})
	if err := store.QueueMediaDeletion(context.Background(), operations.MediaJob{DramaID: "5008", ResourceClass: operations.MediaResourceVideo, RemoteVID: "vid-claim"}); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListMediaJobsByStatus(context.Background(), []string{operations.MediaJobQueued}, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%v err=%v", jobs, err)
	}
	job := jobs[0]
	runningAt := time.Now().UTC().Add(time.Second)

	var claimed atomic.Int32
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			ok, claimErr := store.ClaimMediaJobLease(context.Background(), operations.MediaJobLeaseClaim{ID: job.ID, ExpectedStatus: job.Status, ExpectedUpdatedAt: job.UpdatedAt, ClaimedAt: runningAt, LeaseExpiresAt: runningAt.Add(5 * time.Minute), LeaseOwner: "worker"})
			if claimErr != nil {
				t.Errorf("claim media cleanup job: %v", claimErr)
				return
			}
			if ok {
				claimed.Add(1)
			}
		}()
	}
	wait.Wait()
	if claimed.Load() != 1 {
		t.Fatalf("expected one successful cleanup claim, got %d", claimed.Load())
	}
}
