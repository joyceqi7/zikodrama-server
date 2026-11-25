package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/operations"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestPublicationChannelVersionsIntegration 验证直连发布版本和 TK 任务独立维护、草稿隔离及失败不留半版本。
func TestPublicationChannelVersionsIntegration(t *testing.T) {
	f := newIAAFixture(t)
	r, _ := seedDirect(t, f)
	ctx := application.WithSubject(context.Background(), "1")
	d, err := f.store.PrepareDrama(ctx, "201", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	command := directplay.Command{Kind: "publish", RequestID: "concurrent-publish"}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := r.Accept(ctx, "201", command, "fixture"); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	versions, err := f.store.listDirectPublicationVersions(ctx, "201")
	if err != nil || len(versions) != 1 || versions[0].Number != 1 {
		t.Fatal("并发上架重复发号", err)
	}
	original := versions[0]
	p := operations.TikTokPublication{SubjectID: "1", DramaID: "201", ContentSnapshot: d, Status: operations.PublicationStatusSynced, Stage: operations.PublicationStageAwaitSubmitReview, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := f.store.StartTikTokPublication(ctx, &p); err != nil || consumer.ContentDigest(*p.ContentSnapshot) != consumer.ContentDigest(original.Snapshot) {
		t.Fatal("TK 未冻结当前内容", err)
	}
	d, err = f.store.FindDrama(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	d.Title = "连载第二批"
	d.Episodes = append(d.Episodes, consumer.Episode{DramaID: "201", Number: 9, Title: "第九集", VID: "new-nine", MediaSpaceName: "fixture-space", MediaStatus: "ready"})
	if err := f.store.SaveDramaAggregate(ctx, d, nil); err != nil {
		t.Fatal(err)
	}
	// 新草稿可独立送到官方同步，直连仍保留 V1，直到显式发布更新。
	stale := operations.TikTokPublication{SubjectID: "1", DramaID: "201", ContentSnapshot: &original.Snapshot, Status: operations.PublicationStatusQueued, CreatedAt: time.Now().UTC()}
	if err := f.store.StartTikTokPublication(ctx, &stale); !errors.Is(err, operations.ErrConflict) {
		t.Fatal("过期内容计划没有回滚", err)
	}
	versions, err = f.store.listDirectPublicationVersions(ctx, "201")
	if err != nil || len(versions) != 1 {
		t.Fatal("失败同步留下孤立版本", err)
	}
	d, err = f.store.FindDrama(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	next := operations.TikTokPublication{SubjectID: "1", DramaID: "201", ContentSnapshot: d, Status: operations.PublicationStatusSynced, Stage: operations.PublicationStageAwaitSubmitReview, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := f.store.StartTikTokPublication(ctx, &next); err != nil || len(next.ContentSnapshot.Episodes) != 9 {
		t.Fatal("TK 同步未冻结新批次内容", err)
	}
	w, err := r.Workbench(ctx, "201")
	if err != nil || w.Publication.CurrentVersionID != original.ID || !w.Summary.PendingChanges {
		t.Fatal("TK 更新影响直连当前版本或丢失待办", err)
	}
	op, err := r.Accept(ctx, "201", directplay.Command{Kind: "publish", RequestID: "direct-second-batch", ExpectedRevision: 1}, "fixture")
	if err != nil || op.ResultVersionID == original.ID {
		t.Fatal("直连未生成自己的新版快照", err)
	}
	versions, err = f.store.listDirectPublicationVersions(ctx, "201")
	if err != nil || len(versions) != 2 {
		t.Fatal("直连历史数量不符", err)
	}
	w, err = r.Workbench(ctx, "201")
	if err != nil || w.Summary.PendingChanges {
		t.Fatal("相同内容仍被视为待更新", err)
	}
}

// TestRetainedTaskMediaRetryIntegration 验证没有共享版本的失败任务可重建同内容媒体计划，并保留官方历史与源资源。
func TestRetainedTaskMediaRetryIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithSubject(context.Background(), "1")
	original, err := f.store.FindDrama(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	frozen := consumer.FreezeContent(*original)
	frozen.CoverMID, frozen.CoverSpaceName = "task-only-cover", "task-space"
	now := time.Now().UTC()
	previous := operations.TikTokPublication{SubjectID: "1", DramaID: "201", OwnerApplicationID: "101", OwnerScope: operations.PublicationOwnerScope{ClientKey: "fixture-101"}, PlatformAlbumID: "retained-album", PlatformVersion: 7, PlatformCoverID: "official-cover", Status: operations.PublicationStatusFailed, Stage: operations.PublicationStageTranscodeVideos, ErrorCode: "transcode_failed", ContentSnapshot: &frozen, CreatedAt: now, UpdatedAt: now}
	for _, ep := range frozen.Episodes {
		previous.Videos = append(previous.Videos, operations.PublicationVideo{EpisodeID: ep.ID, VID: ep.VID, Status: "ready"})
	}
	model, err := newTikTokPublicationModel(previous)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	previous.ID = model.ID
	original.Title = "不能混入重试的新草稿"
	if err := f.store.SaveDramaAggregate(ctx, original, nil); err != nil {
		t.Fatal(err)
	}
	svc := operations.NewService(operations.Repositories{Application: operations.ApplicationPorts{Subjects: f.store, Registry: f.store}, Content: operations.ContentPorts{Catalog: f.store, Publications: f.store, OwnerApplication: func(context.Context, string) (application.Application, error) {
		return application.Application{SubjectID: "1", ClientKey: "fixture-101", ClientSecret: "test-only", BytePlusAccountID: "test-account", BytePlusSpaceName: "task-space", Status: application.StatusActive}, nil
	}}}, nil)
	if err := svc.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return &retainedPublicationProvider{} }, operations.PublicationJobConfig{WorkerID: "media-retry"}); err != nil {
		t.Fatal(err)
	}
	// 所有重试都核对固定官方账号，测试任务同样携带准确的冻结空间。
	previous.OwnerScope.AccountID, previous.OwnerScope.SpaceName = "test-account", "task-space"
	if err := f.store.UpdateTikTokPublication(ctx, previous, nil); err != nil {
		t.Fatal(err)
	}
	next, err := svc.RetryTikTokPublication(ctx, "201")
	if err != nil || next.ID == previous.ID || next.ContentSnapshot.Title != frozen.Title || next.PlatformAlbumID != previous.PlatformAlbumID || next.TranscodeKeySeed == "" || next.Stage != operations.PublicationStageTranscodeVideos {
		t.Fatal("旧任务重试依赖版本或混入草稿", err)
	}
	versions, err := f.store.listDirectPublicationVersions(ctx, "201")
	if err != nil || len(versions) != 0 {
		t.Fatal("重试回填历史版本", err)
	}
	used, err := f.store.IsMediaResourceUsed(ctx, operations.MediaJob{ResourceClass: operations.MediaResourceMaterial, RemoteVID: "task-only-cover", SpaceName: "task-space"})
	if err != nil || !used {
		t.Fatal("历史任务封面不受保护", err)
	}
	if err := f.store.RestartTikTokPublication(ctx, previous, next); !errors.Is(err, operations.ErrConflict) {
		t.Fatal("迟到的重复重建覆盖当前任务", err)
	}
	latest, err := f.store.FindTikTokPublicationByDramaID(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	latest.Status, latest.Stage = operations.PublicationStatusFailed, operations.PublicationStageTranscodeVideos
	if err := f.store.UpdateTikTokPublication(ctx, latest, nil); err != nil {
		t.Fatal(err)
	}
	// 仅在隔离夹具中准备已经没有线上内容的可删除短剧。
	if err := f.store.data.DB().Exec("UPDATE tiktok_drama_states SET published_snapshot=NULL WHERE subject_id=1 AND drama_id=201").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteDrama(ctx, "201"); err != nil {
		t.Fatal(err)
	}
	used, err = f.store.IsMediaResourceUsed(ctx, operations.MediaJob{ResourceClass: operations.MediaResourceMaterial, RemoteVID: "task-only-cover", SpaceName: "task-space"})
	if err != nil || used {
		t.Fatal("显式删剧没有释放任务资源", err)
	}
	var queued int64
	if err := f.store.data.DB().Table("media_jobs").Where("kind=? AND remote_vid=?", operations.MediaJobDelete, "task-only-cover").Count(&queued).Error; err != nil || queued != 1 {
		t.Fatal("历史任务独有媒体没有进入回收队列", err)
	}
}
