package mysqlstore

import (
	"context"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// assertGORMPublicationIsolation 在调用者创建的隔离库中验证真实事务，不访问平台或业务数据库。
func assertGORMPublicationIsolation(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	owner := "2041"
	if err := store.SaveApplication(ctx, application.Application{SubjectID: "1", ID: owner, ClientKey: "owner-client-key", TikTokAppID: "official-owner", Name: "Publication owner", Status: application.StatusActive, TimeZone: "UTC", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	drama := domain.Drama{ID: "1025", Title: "Approved title", Status: domain.DramaStatusCompleted, Language: "en", PublishStatus: "draft",
		Episodes: []domain.Episode{{ID: "1026", DramaID: "1025", Number: 1, Title: "Approved episode", VID: "publication-vid", MediaStatus: "ready"}}}
	if err := store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
		t.Fatal(err)
	}
	publication := operations.TikTokPublication{SubjectID: "1", ID: "1027", DramaID: drama.ID, OwnerApplicationID: "official-owner", OwnerScope: operations.PublicationOwnerScope{ClientKey: "owner-client-key"}, ContentSnapshot: &drama,
		Transcodes: []operations.PublicationTranscode{{TaskID: "transcode-original", VIDs: []string{"a", "b"}, CompletedVIDs: []string{"a"}, StartedAt: now, LastProgressAt: now, LastCheckedAt: now.Add(time.Minute), MonitoringStartedAt: now}},
		Appeal:     &operations.TikTokReviewAppealInput{ItemType: 1, ItemID: "7637420375425239060", Reason: "test appeal", AttachmentURLs: []string{"https://assets.example/evidence"}}, ReviewItems: map[string]int{"7637420375425239060": 3},
		Status: operations.PublicationStatusQueued, Stage: operations.PublicationStageRegisterVideos, Priority: "normal", NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
	seedReadyPublicationContent(t, store, &publication)
	if err := store.StartTikTokPublication(ctx, &publication); err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimTikTokPublication(ctx, "worker-first", now, now.Add(time.Second))
	if err != nil || first == nil || first.ContentSnapshot == nil || first.ContentSnapshot.Title != drama.Title {
		t.Fatalf("快照未持久化：%+v %v", first, err)
	}
	if len(first.Transcodes) != 1 || len(first.Transcodes[0].CompletedVIDs) != 1 || first.Transcodes[0].CompletedVIDs[0] != "a" || !first.Transcodes[0].LastProgressAt.Equal(now) || !first.Transcodes[0].LastCheckedAt.Equal(now.Add(time.Minute)) {
		t.Fatal("转码进度与时间在数据库往返后丢失")
	}
	if first.Appeal == nil || first.Appeal.Reason != "test appeal" || len(first.Appeal.AttachmentURLs) != 1 || first.ReviewItems["7637420375425239060"] != 3 {
		t.Fatal("申诉输入或单项状态持久化丢失")
	}
	if err := store.BeginTikTokExternalWrite(ctx, first); err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimTikTokPublication(ctx, "worker-second", now.Add(2*time.Second), now.Add(time.Minute))
	if err != nil || second == nil || second.Revision <= first.Revision {
		t.Fatalf("租约未被新修订接管：%+v %v", second, err)
	}
	if !second.ExternalWriteStarted {
		t.Fatal("外部请求检查点在接管后丢失")
	}
	if err := store.BeginTikTokExternalWrite(ctx, first); err == nil {
		t.Fatal("过期进程仍可取得外部写许可")
	}
	first.Stage = operations.PublicationStageDone
	if err := store.UpdateTikTokPublication(ctx, *first, nil); err == nil {
		t.Fatal("过期 Worker 覆盖了新租约")
	}
	// 审核中按计划自动领取查询，待上线保持暂停；旧 JSON 没有人工指令字段时按未确认处理。
	if second.PublishRequested {
		t.Fatal("旧工作流被误认为已人工确认")
	}
	second.ExternalWriteStarted = false
	for _, status := range []string{operations.PublicationStatusUnderReview, operations.PublicationStatusApproved} {
		second.Status = status
		second.Stage = operations.PublicationStagePollReview
		if err := store.UpdateTikTokPublication(ctx, *second, nil); err != nil {
			t.Fatal(err)
		}
		nextTime := now.Add(365 * 24 * time.Hour)
		claimed, err := store.ClaimTikTokPublication(ctx, "regular-publication-worker", nextTime, nextTime.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if claimed != nil {
			t.Fatalf("普通发布扫描领取了审核任务：%v", claimed)
		}
		reviewClaimed, err := store.ClaimTikTokReview(ctx, "review-worker", nextTime, nextTime.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if status == operations.PublicationStatusUnderReview && reviewClaimed == nil {
			t.Fatal("审核扫描没有领取审核中任务")
		}
		if status == operations.PublicationStatusApproved && reviewClaimed != nil {
			t.Fatalf("审核扫描领取了待上线任务：%v", reviewClaimed)
		}
		if reviewClaimed != nil {
			reviewClaimed.Status = operations.PublicationStatusApproved
			reviewClaimed.Stage = operations.PublicationStageAwaitPublish
			if err := store.UpdateTikTokPublication(ctx, *reviewClaimed, nil); err != nil {
				t.Fatal(err)
			}
		}
		saved, err := store.FindTikTokPublicationByDramaID(ctx, drama.ID)
		if err != nil {
			t.Fatal(err)
		}
		second = &saved
	}
	second.PublishRequested = true
	if err := store.UpdateTikTokPublication(ctx, *second, nil); err != nil {
		t.Fatal(err)
	}
	saved, err := store.FindTikTokPublicationByDramaID(ctx, drama.ID)
	if err != nil || !saved.PublishRequested {
		t.Fatalf("人工指令未持久化：%v", err)
	}
	second = &saved
	// 模拟已取得平台审核、线上版本和上架结果后原子安装发布快照。
	online := drama
	online.OwnerApplicationID, online.PlatformVersion, online.PlatformAlbumID, online.PublishStatus = owner, 1, "7637420375425239060", "published"
	online.Episodes = append([]domain.Episode(nil), drama.Episodes...)
	online.Episodes[0].PlatformEpisodeID = "7637437361307027476"
	drama.Published = &online
	drama.PlatformAlbumID = online.PlatformAlbumID
	drama.PublishStatus = "published"
	second.ContentSnapshot = &online
	second.PlatformAlbumID, second.PlatformVersion = online.PlatformAlbumID, 1
	second.Status, second.Stage = operations.PublicationStatusOnline, operations.PublicationStageDone
	second.ExternalWriteStarted = false
	if err := store.UpdateTikTokPublication(ctx, *second, &drama); err != nil {
		t.Fatal(err)
	}
	read, err := store.FindDrama(ctx, drama.ID)
	if err != nil {
		t.Fatal(err)
	}
	if published, ok := read.PublishedForApplication(owner); !ok || published.Title != "Approved title" {
		t.Fatal("拥有方缺少线上快照")
	}
	if _, ok := read.PublishedForApplication("unrelated-app"); ok {
		t.Fatal("授权泄露到其他小程序")
	}
	// 删除编辑区分集不得删除线上分集的主键，也不得回收线上视频。
	read.Title, read.PublishStatus = "Unreviewed title", "draft"
	read.Episodes = []domain.Episode{{ID: "1029", DramaID: drama.ID, Number: 1, Title: "New episode", VID: "new-vid", MediaStatus: "ready"}}
	if err := store.SaveDramaAggregate(ctx, read, nil); err != nil {
		t.Fatal(err)
	}
	read, err = store.FindDrama(ctx, drama.ID)
	if err != nil || read.Title != "Unreviewed title" || len(read.Episodes) != 1 || read.Episodes[0].ID != "1029" || read.Published.Title != "Approved title" {
		t.Fatalf("编辑污染线上：%+v %v", read, err)
	}
	var retained int64
	if err := store.data.DB().WithContext(ctx).Model(&episodeModel{}).Where("id = ? AND is_draft = ?", "1026", false).Count(&retained).Error; err != nil || retained != 1 {
		t.Fatalf("线上分集主键被删除：%d %v", retained, err)
	}
	referenced, err := store.IsMediaResourceUsed(ctx, operations.MediaJob{ResourceClass: operations.MediaResourceVideo, RemoteVID: "publication-vid"})
	if err != nil || !referenced {
		t.Fatalf("published resource unprotected: %v %v", referenced, err)
	}
	// 新提交保留原任务历史，且两个快照独立。
	next := operations.TikTokPublication{SubjectID: "1", ID: "1028", DramaID: drama.ID, OwnerApplicationID: "official-owner", OwnerScope: operations.PublicationOwnerScope{ClientKey: "owner-client-key"}, ContentSnapshot: read, PlatformAlbumID: online.PlatformAlbumID, PlatformVersion: 1, Priority: "normal", Status: operations.PublicationStatusProcessing, Stage: operations.PublicationStageUploadCover, NextAttemptAt: now.Add(time.Second), CreatedAt: now.Add(time.Second), UpdatedAt: now}
	seedReadyPublicationContent(t, store, &next)
	if err := store.StartTikTokPublication(ctx, &next); err != nil {
		t.Fatal(err)
	}
	var history int64
	if err := store.data.DB().WithContext(ctx).Model(&tiktokPublicationModel{}).Where("drama_id = ?", drama.ID).Count(&history).Error; err != nil || history != 2 {
		t.Fatalf("发布历史被覆盖：%d %v", history, err)
	}
	latest, err := store.FindTikTokPublicationByDramaID(ctx, drama.ID)
	if err != nil || latest.ID != next.ID || latest.ContentSnapshot.Title != "Unreviewed title" {
		t.Fatalf("新版本任务错误：%+v %v", latest, err)
	}
	page, err := store.ListDramaPage(ctx, operations.DramaListQuery{Page: 1, PageSize: 20, Query: "Unreviewed title"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Publication == nil || page.Items[0].Publication.Status != operations.PublicationStatusProcessing || page.Items[0].Publication.Stage != operations.PublicationStageUploadCover || page.Items[0].Publication.Priority != "normal" {
		t.Fatalf("列表最新发布任务摘要错误：%+v %v", page, err)
	}
}
