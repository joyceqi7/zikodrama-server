package operations_test

import (
	"context"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
	"testing"
	"time"
)

func TestVODCallbacksAreIdempotentWithoutInbox(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	now := time.Now().UTC()
	job := operations.MediaJob{ID: "upload-1", Kind: operations.MediaJobUpload, IdempotencyKey: "reserve-1", ResourceClass: operations.MediaResourceVideo, SpaceName: "test", Status: operations.MediaJobReserved, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateMediaJob(context.Background(), &job); err != nil {
		t.Fatal(err)
	}
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	event := operations.VODEvent{UploadID: job.ID, ResourceClass: operations.MediaResourceVideo, SpaceName: "test", ProviderAssetID: "vid-1", EventType: operations.VODEventTranscodeComplete, Code: "Success"}
	for range 3 {
		if _, err := service.ReceiveVODCallback(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := store.FindMediaJob(context.Background(), job.ID)
	if err != nil || stored.RemoteVID != "vid-1" || stored.Status != operations.MediaJobReserved {
		t.Fatalf("callback changed lifecycle: %+v %v", stored, err)
	}
	event.EventType = operations.VODEventFileUploadComplete
	event.Code = "Failed"
	if _, err := service.ReceiveVODCallback(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	event.ProviderAssetID = "other-vid"
	if applied, err := service.ReceiveVODCallback(context.Background(), event); err != nil || applied {
		t.Fatalf("foreign video accepted: %v %v", applied, err)
	}
	stored.Status = operations.MediaJobSucceeded
	if err := store.UpdateMediaJob(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	event.ProviderAssetID = "vid-1"
	if applied, err := service.ReceiveVODCallback(context.Background(), event); err != nil || applied {
		t.Fatalf("late event applied: %v %v", applied, err)
	}
}
func TestVODConsoleUploadDoesNotCreateDrama(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	applied, err := service.ReceiveVODCallback(context.Background(), operations.VODEvent{ResourceClass: operations.MediaResourceVideo, ProviderAssetID: "console-vid", EventType: operations.VODEventFileUploadComplete})
	if err != nil || applied {
		t.Fatalf("unknown resource accepted: %v %v", applied, err)
	}
	dramas, err := service.ListDramas(context.Background())
	if err != nil || len(dramas) != 0 {
		t.Fatal("callback created content")
	}
}
