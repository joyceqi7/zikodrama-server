package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"errors"
	"testing"
	"time"
)

// TestSupersededPublicationVersionIntegration 验证新版任务撤销旧修订，迟到的送审、上线和刷新不得恢复历史任务。
func TestSupersededPublicationVersionIntegration(t *testing.T) {
	for _, state := range []string{operations.PublicationStatusSynced, operations.PublicationStatusApproved} {
		t.Run(state, func(t *testing.T) {
			f := newIAAFixture(t)
			seedDirect(t, f)
			ctx := application.WithSubject(context.Background(), "1")
			d, err := f.store.PrepareDrama(ctx, "201", "review")
			if err != nil {
				t.Fatal(err)
			}
			stage := operations.PublicationStageAwaitSubmitReview
			if state == operations.PublicationStatusApproved {
				stage = operations.PublicationStageAwaitPublish
			}
			now := time.Now().UTC()
			old := operations.TikTokPublication{SubjectID: "1", DramaID: "201", ContentSnapshot: d, Status: state, Stage: stage, CreatedAt: now.Add(-time.Minute), UpdatedAt: now, ReviewStatus: 2}
			if err := f.store.StartTikTokPublication(ctx, &old); err != nil {
				t.Fatal(err)
			}
			stale, err := f.store.FindTikTokPublicationByDramaID(ctx, "201")
			if err != nil {
				t.Fatal(err)
			}
			d.Title = "新版内容"
			if err := f.store.SaveDrama(ctx, *d); err != nil {
				t.Fatal(err)
			}
			d, err = f.store.PrepareDrama(ctx, "201", "review")
			if err != nil {
				t.Fatal(err)
			}
			next := operations.TikTokPublication{SubjectID: "1", DramaID: "201", ContentSnapshot: d, Status: operations.PublicationStatusQueued, Stage: operations.PublicationStageUploadCover, NextAttemptAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now}
			if err := f.store.StartTikTokPublication(ctx, &next); err != nil {
				t.Fatal(err)
			}
			stale.Status = operations.PublicationStatusQueued
			stale.Stage = operations.PublicationStageSubmitReview
			if state == operations.PublicationStatusApproved {
				stale.Stage = operations.PublicationStageSetOnlineVersion
				stale.PublishRequested = true
			}
			stale.NextAttemptAt = now.Add(-time.Second)
			if err := f.store.UpdateTikTokPublication(ctx, stale, nil); !errors.Is(err, operations.ErrConflict) {
				t.Fatalf("迟到的旧版操作未被拒绝：%v", err)
			}
			var historical tiktokPublicationModel
			if err := f.store.data.DB().First(&historical, "id=?", old.ID).Error; err != nil {
				t.Fatal(err)
			}
			if historical.Revision != stale.Revision+1 || historical.Status != state {
				t.Fatal("创建新版没有撤销旧修订，或污染历史状态")
			}
			// 即使重新读到历史任务的最新修订，也不能绕过当前任务身份栅栏。
			stale.Revision = historical.Revision
			if err := f.store.UpdateTikTokPublication(ctx, stale, nil); !errors.Is(err, operations.ErrConflict) {
				t.Fatalf("历史任务读取新修订后仍可复活：%v", err)
			}
			claimed, err := f.store.ClaimTikTokPublication(ctx, "review-worker", now, now.Add(time.Minute))
			if err != nil || claimed != nil {
				t.Fatalf("Worker 领取到了被替换的任务：%v", err)
			}
			latest, err := f.store.FindTikTokPublicationByDramaID(ctx, "201")
			if err != nil || latest.ID != next.ID || latest.ContentSnapshot.Title != "新版内容" {
				t.Fatal("当前任务身份被旧任务覆盖", err)
			}
			current, err := f.store.FindDrama(ctx, "201")
			if err != nil || current.PublishStatus != "ready" {
				t.Fatal("任务竞争改变编辑版本", err)
			}

		})
	}
}
