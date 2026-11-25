package operations_test

import (
	"context"
	"testing"
	"time"

	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

func TestMediaWorkerWaitsForBackgroundPersistenceBeforeReturning(t *testing.T) {
	service := operations.NewService(memory.OperationsRepositories(memory.NewStore(memory.Seed{})), workerLifecycleMediaProvider{})
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{PollInterval: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	workerContext, cancelWorker := context.WithCancel(context.Background())
	if err := service.StartMediaWorker(workerContext); err != nil {
		t.Fatal(err)
	}
	cancelWorker()

	waitContext, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	if err := service.WaitMediaWorker(waitContext); err != nil {
		t.Fatalf("媒体 worker 取消后未完成：%v", err)
	}
}

type workerLifecycleMediaProvider struct{}

func (workerLifecycleMediaProvider) Delete(context.Context, string, string, string, string) error {
	return nil
}
