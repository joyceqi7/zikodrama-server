package mysqlstore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// TestEntryPreviewUpgradeIntegration 验证旧任务升级后保留身份、远端请求和结果，片段秒数使用兼容缺省值。
func TestEntryPreviewUpgradeIntegration(t *testing.T) {
	migration := string(readDeploymentSQL(t, "migrations/20261109_001_entry_preview.sql"))
	previousSchema := strings.Replace(string(readDeploymentSchema(t)), migration, "", 1)
	f := newIAAFixtureWithSchema(t, previousSchema, false)
	ctx := application.WithSubject(t.Context(), "1")
	db := f.store.data.DB().WithContext(ctx)
	if err := db.Exec(`INSERT INTO media_jobs
		(subject_id, idempotency_key, kind, episode_id, public_url, file_name, file_size, remote_vid,
		 upload_request_id, status, created_at, updated_at)
		VALUES (1, 'legacy-media', 'upload', 101, '', 'episode.mp4', 200, 'legacy-vid',
		        'legacy-request', 'succeeded', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(migration).Error; err != nil {
		t.Fatal(err)
	}
	job, err := f.store.FindMediaJobByIdempotencyKey(ctx, "legacy-media")
	if err != nil || job.EpisodeID != "101" || job.Kind != operations.MediaJobUpload || job.Status != operations.MediaJobSucceeded ||
		job.RemoteVID != "legacy-vid" || job.UploadRequestID != "legacy-request" || job.PreviewSeconds != 0 {
		t.Fatalf("升级改写了历史媒资任务: %+v %v", job, err)
	}
}

// assertEntryPreviewMediaJobs 验证并发首播只创建一次、成品可复用、旧任务回收只删除短片。
func assertEntryPreviewMediaJobs(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	episode := domain.Episode{ID: "3015", DramaID: "3014", Number: 1, VID: "preview-test-source", MediaSpaceName: "space"}
	var callers sync.WaitGroup
	for range 4 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			if vid, err := store.FindOrQueueEntryPreview(ctx, episode, 5); vid != "" || err != nil {
				t.Errorf("排队中不得返回播放 VID: %s %v", vid, err)
			}
		}()
	}
	callers.Wait()
	db := store.data.DB().WithContext(ctx)
	var jobs []mediaJobModel
	if err := db.Where("kind = ? AND expected_vid = ?", operations.MediaJobEntryPreview, episode.VID).Find(&jobs).Error; err != nil ||
		len(jobs) != 1 || jobs[0].PreviewSeconds != 5 {
		t.Fatalf("首集片段任务未按来源与秒数去重: %d %v", len(jobs), err)
	}
	job := jobs[0].mediaJob()
	job.RemoteVID, job.Status = "preview-test-clip", operations.MediaJobSucceeded
	job.UpdatedAt = time.Now().UTC()
	if err := store.UpdateMediaJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if vid, err := store.FindOrQueueEntryPreview(ctx, episode, 5); vid != job.RemoteVID || err != nil {
		t.Fatalf("未找到已完成的独立片段: %s %v", vid, err)
	}
	if err := store.DeleteMediaJob(ctx, job.ID); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("清理扫描后仍在使用的片段未被保护: %v", err)
	}
	job.UpdatedAt = time.Now().Add(-8 * 24 * time.Hour)
	if err := store.UpdateMediaJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMediaJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	var deletion []mediaJobModel
	if err := db.Where("kind = ? AND drama_id = ?", operations.MediaJobDelete, episode.DramaID).Find(&deletion).Error; err != nil ||
		len(deletion) != 1 || deletion[0].RemoteVID != job.RemoteVID {
		t.Fatalf("回收时必须只删除片段: %+v %v", deletion, err)
	}
}

// assertGORMDramaMediaJobs 验证上传、回调和删除全部使用统一任务表及剧聚合事务。
func assertGORMDramaMediaJobs(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	drama := domain.Drama{ID: "1014", Title: "Media", PublishStatus: "draft", Episodes: []domain.Episode{{ID: "1015", DramaID: "1014", Number: 1, VID: "old-video", MediaSpaceName: "space", MediaStatus: "ready"}}}
	if err := store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	job := operations.MediaJob{ID: "1037", IdempotencyKey: "reserve-test", Kind: operations.MediaJobUpload, DramaID: drama.ID, EpisodeID: drama.Episodes[0].ID, ResourceClass: operations.MediaResourceVideo, SpaceName: "space", FileName: "one.mp4", Size: 1024, ExpectedVID: "old-video", Status: operations.MediaJobReserved, CreatedAt: now, UpdatedAt: now, NextAttemptAt: now}
	if err := store.CreateMediaJob(ctx, &job); err != nil {
		t.Fatal(err)
	}
	event := operations.VODEvent{UploadID: job.ID, ResourceClass: operations.MediaResourceVideo, SpaceName: "space", ProviderAssetID: "new-video", EventType: operations.VODEventTranscodeComplete, Code: "Success"}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.ApplyVODCallback(ctx, event); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	stored, err := store.FindMediaJob(ctx, job.ID)
	if err != nil || stored.RemoteVID != "new-video" || stored.Status != operations.MediaJobReserved {
		t.Fatalf("callback changed task incorrectly: %+v %v", stored, err)
	}
	// 大文件仍在传输时可能先收到平台回调；必须先留住 VID，再允许上传确认幂等推进。
	if err := store.data.DB().WithContext(ctx).Model(&mediaJobModel{}).Where("id = ?", job.ID).Updates(map[string]any{
		"status": operations.MediaJobUploading, "remote_vid": "",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if applied, err := store.ApplyVODCallback(ctx, event); err != nil || !applied {
		t.Fatalf("上传中回调未登记官方 VID: %v %v", applied, err)
	}
	stored, err = store.FindMediaJob(ctx, job.ID)
	if err != nil || stored.RemoteVID != "new-video" || stored.Status != operations.MediaJobUploading {
		t.Fatalf("上传中回调未保留任务状态和 VID: %+v %v", stored, err)
	}
	job.RemoteVID = "new-video"
	job.Status = operations.MediaJobQueued
	job.IdempotencyKey = "complete-test"
	job.ProcessingStartedAt = now
	job.ProcessingDeadlineAt = now.Add(time.Hour)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.ConfirmMediaUpload(ctx, job); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	stored, err = store.FindMediaJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimMediaJobLease(ctx, operations.MediaJobLeaseClaim{ID: job.ID, ExpectedStatus: stored.Status, ExpectedUpdatedAt: stored.UpdatedAt, LeaseOwner: "worker", ClaimedAt: now.Add(time.Second), LeaseExpiresAt: now.Add(time.Minute)})
	if err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	ep := drama.Episodes[0]
	ep.VID = "new-video"
	ep.DurationSeconds = 30
	// 故意使清理任务写入失败，分集切换必须整体回滚。
	db := store.data.DB().WithContext(ctx)
	if err := db.Exec("CREATE TRIGGER reject_media_cleanup BEFORE INSERT ON media_jobs FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'cleanup failure'").Error; err != nil {
		t.Fatal(err)
	}
	err = store.SaveEpisodeMedia(ctx, ep, drama, "old-video", job.ID)
	if err == nil {
		t.Fatal("cleanup failure did not rollback binding")
	}
	if err := db.Exec("DROP TRIGGER reject_media_cleanup").Error; err != nil {
		t.Fatal(err)
	}
	current, err := store.FindEpisode(ctx, ep.ID)
	if err != nil || current.VID != "old-video" {
		t.Fatalf("partial bind persisted: %+v %v", current, err)
	}
	if err := store.SaveEpisodeMedia(ctx, ep, drama, "old-video", job.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveEpisodeMedia(ctx, ep, drama, "old-video", job.ID); err != operations.ErrConflict {
		t.Fatalf("stale write accepted: %v", err)
	}
	var deleted int64
	if err := db.Model(&mediaJobModel{}).Where("kind = ? AND remote_vid = ?", operations.MediaJobDelete, "old-video").Count(&deleted).Error; err != nil || deleted != 1 {
		t.Fatalf("old video deletion missing: %d %v", deleted, err)
	}
	used, err := store.IsMediaResourceUsed(ctx, operations.MediaJob{ResourceClass: operations.MediaResourceVideo, SpaceName: "space", RemoteVID: "new-video"})
	if err != nil || !used {
		t.Fatal("new video is unprotected")
	}
	stored, err = store.FindMediaJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Status = operations.MediaJobSucceeded
	if err := store.UpdateMediaJob(ctx, stored); err != nil {
		t.Fatal(err)
	}
	event.Code = "Failed"
	event.EventType = operations.VODEventFileUploadComplete
	if applied, err := store.ApplyVODCallback(ctx, event); err != nil || applied {
		t.Fatalf("late callback changed success: %v %v", applied, err)
	}
	if err := store.DeleteDrama(ctx, drama.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&mediaJobModel{}).Where("kind = ? AND remote_vid = ?", operations.MediaJobDelete, "new-video").Count(&deleted).Error; err != nil || deleted != 1 {
		t.Fatalf("drama deletion lost remote task: %d %v", deleted, err)
	}
	used, err = store.IsMediaResourceUsed(ctx, operations.MediaJob{ResourceClass: operations.MediaResourceVideo, SpaceName: "space", RemoteVID: "new-video"})
	if err != nil || used {
		t.Fatalf("deleted drama still holds video: %v %v", used, err)
	}
	// 封面上传结果只在保存聚合时消费，MID、空间和 StoreUri 归入剧。
	cover := operations.MediaJob{ID: "1003", IdempotencyKey: "1003", Kind: operations.MediaJobCover, ResourceClass: operations.MediaResourceMaterial, SpaceName: "space", RemoteVID: "cover-mid", ProviderFileName: "cover.png", PublicURL: "https://cover.example.test/one.png", Status: operations.MediaJobUploaded, CreatedAt: now, UpdatedAt: now, NextAttemptAt: now}
	if err := store.CreateMediaJob(ctx, &cover); err != nil {
		t.Fatal(err)
	}
	drama.CoverURL = cover.PublicURL
	if err := store.SaveDramaAggregate(ctx, &drama, &cover); err != nil {
		t.Fatal(err)
	}
	saved, err := store.FindDrama(ctx, drama.ID)
	if err != nil || saved.CoverMID != cover.RemoteVID || saved.CoverSpaceName != "space" || saved.CoverFileName != "cover.png" {
		t.Fatalf("cover not in drama: %+v %v", saved, err)
	}
	if err := store.SaveDramaAggregate(ctx, &drama, &cover); err != operations.ErrConflict {
		t.Fatalf("cover task reused: %v", err)
	}
}
