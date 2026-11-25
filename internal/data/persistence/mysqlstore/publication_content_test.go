package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"encoding/json"
	"gorm.io/gorm"
	"slices"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// TestPublicationContentSummaryRoundTrip 验证范围摘要随任务检查点持久化，旧任务不猜测历史增量。
func TestPublicationContentSummaryRoundTrip(t *testing.T) {
	drama := domain.Drama{Episodes: []domain.Episode{{Number: 1}, {Number: 2}}, Published: &domain.Drama{Episodes: []domain.Episode{{Number: 1}}}}
	p := operations.TikTokPublication{SubjectID: "1", ContentSnapshot: &drama, ContentSummary: operations.NewPublicationContentSummary(drama)}
	model, err := newTikTokPublicationModel(p)
	if err != nil {
		t.Fatal(err)
	}
	read, err := model.domain()
	if err != nil {
		t.Fatal(err)
	}
	if read.ContentSummary == nil || !slices.Equal(read.ContentSummary.EpisodeNumbers, []int{1, 2}) || !slices.Equal(read.ContentSummary.PreviousEpisodeNumbers, []int{1}) {
		t.Fatal("重载后丢失发布范围")
	}
	p.ContentSummary = nil
	model, err = newTikTokPublicationModel(p)
	if err != nil {
		t.Fatal(err)
	}
	read, err = model.domain()
	if err != nil || read.ContentSummary == nil || read.ContentSummary.PreviousEpisodeNumbers != nil {
		t.Fatal("旧任务虚构了同步前的线上范围")
	}
}

// TestNextBatchStorageIsolation 在真实 MySQL 中验证冻结边界、上传事务与本批上线后的下一批草稿。
func TestNextBatchStorageIsolation(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithSubject(context.Background(), "1")
	drama, err := f.store.FindDrama(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	drama.Status = domain.DramaStatusUpdating
	if err := f.store.SaveDrama(ctx, *drama); err != nil {
		t.Fatal(err)
	}
	frozen := *drama
	frozen.Published = nil
	now := time.Now().UTC()
	p := operations.TikTokPublication{SubjectID: "1", DramaID: drama.ID, Status: operations.PublicationStatusUnderReview, Stage: operations.PublicationStagePollReview, ContentSnapshot: &frozen, ContentSummary: operations.NewPublicationContentSummary(*drama), SubmittedAt: &now, CreatedAt: now, UpdatedAt: now}
	seedReadyPublicationContent(t, f.store, &p)
	if err := f.store.StartTikTokPublication(ctx, &p); err != nil {
		t.Fatal(err)
	}
	drama, err = f.store.FindDrama(ctx, drama.ID)
	if err != nil {
		t.Fatal(err)
	}
	originalTitle := drama.Title
	drama.Title = "绕过服务修改冻结资料"
	if err := f.store.SaveDramaAggregate(ctx, drama, nil); err != nil {
		t.Fatalf("事务未保护冻结资料: %v", err)
	}
	drama.Title = originalTitle
	drama.Episodes = append(drama.Episodes, domain.Episode{DramaID: drama.ID, Number: 9, Title: "下一批", MediaStatus: "pending"})
	if err := f.store.SaveDramaAggregate(ctx, drama, nil); err != nil {
		t.Fatal(err)
	}
	blocked := drama.Episodes[0]
	blocked.VID = "replacement"
	if err := f.store.SaveEpisodeMedia(ctx, blocked, *drama, drama.Episodes[0].VID, ""); err != nil {
		t.Fatalf("事务未保护冻结媒资: %v", err)
	}
	ep := drama.Episodes[8]
	ep.VID, ep.MediaStatus = "next-batch-vid", "ready"
	if err := f.store.SaveEpisodeMedia(ctx, ep, *drama, "", ""); err != nil {
		t.Fatal(err)
	}
	during, err := f.store.FindDrama(ctx, drama.ID)
	if err != nil || during.PublishStatus != "draft" {
		t.Fatalf("上传重置审核状态: %v", err)
	}
	p.Status, p.Stage, p.OnlineAt = operations.PublicationStatusOnline, operations.PublicationStageDone, &now
	online := frozen
	online.PlatformVersion = 2
	online.PublishStatus = "published"
	finished := frozen
	finished.PublishStatus, finished.Published = "published", &online
	if err := f.store.UpdateTikTokPublication(ctx, p, &finished); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.FindDrama(ctx, drama.ID)
	if err != nil || after.PublishStatus != "draft" || len(after.Episodes) != 9 || after.Published == nil || len(after.Published.Episodes) != 8 || after.Episodes[8].VID != "next-batch-vid" {
		t.Fatalf("本批上线污染下一批: %v", err)
	}
}

// TestRejectedAlbumEditIntegration 验证拒绝任务保留原版本，修改草稿后通过新版本创建任务并复用官方 Album。
func TestRejectedAlbumEditIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithSubject(context.Background(), "1")
	drama, err := f.store.FindDrama(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	frozen := *drama
	frozen.Published = nil
	now := time.Now().UTC()
	p := operations.TikTokPublication{SubjectID: "1",
		DramaID: drama.ID, Status: operations.PublicationStatusQueued, Stage: operations.PublicationStageUpdateAlbum,
		PlatformAlbumID: "7685293427067586568", ContentSnapshot: &frozen, CreatedAt: now, UpdatedAt: now,
	}
	seedReadyPublicationContent(t, f.store, &p)
	if err := f.store.StartTikTokPublication(ctx, &p); err != nil {
		t.Fatal(err)
	}
	drama, err = f.store.FindDrama(ctx, drama.ID)
	if err != nil {
		t.Fatal(err)
	}
	drama.Description = "修正后的短剧简介"
	drama.PublishStatus = "draft"
	if err := f.store.SaveDramaAggregate(ctx, drama, nil); err != nil {
		t.Fatalf("处理中的任务不应允许编辑: %v", err)
	}
	p.Status, p.ErrorCode = operations.PublicationStatusFailed, "api_rejected"
	if err := f.store.UpdateTikTokPublication(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SaveDramaAggregate(ctx, drama, nil); err != nil {
		t.Fatal(err)
	}
	updatedDrama, err := f.store.FindDrama(ctx, drama.ID)
	if err != nil || updatedDrama.PublishStatus != "draft" || updatedDrama.Description != "修正后的短剧简介" {
		t.Fatalf("未恢复可编辑草稿: drama=%+v err=%v", updatedDrama, err)
	}
	old, err := f.store.FindTikTokPublicationByDramaID(ctx, drama.ID)
	if err != nil || old.ErrorCode != "api_rejected" || old.ContentSnapshot == nil || old.ContentSnapshot.Description == updatedDrama.Description {
		t.Fatalf("旧任务快照未作废或被覆盖: error_code=%q err=%v", old.ErrorCode, err)
	}
	next := operations.TikTokPublication{SubjectID: "1", DramaID: drama.ID, Status: operations.PublicationStatusQueued, Stage: operations.PublicationStageUpdateAlbum,
		PlatformAlbumID: old.PlatformAlbumID, ContentSnapshot: updatedDrama, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	seedReadyPublicationContent(t, f.store, &next)
	if err := f.store.StartTikTokPublication(ctx, &next); err != nil || next.ID == old.ID {
		t.Fatalf("修正后无法创建新同步任务: id=%q err=%v", next.ID, err)
	}
}

// seedOfficialProjection 只供存储测试模拟平台已确认的主体投影，使用与发布相同的分类事务。
func seedOfficialProjection(db *gorm.DB, id string, raw []byte) error {
	return db.WithContext(application.WithSubject(db.Statement.Context, "1")).Transaction(func(tx *gorm.DB) error {
		var old tiktokDramaStateModel
		if err := tx.Where("subject_id=1 AND drama_id=?", id).Find(&old).Error; err != nil {
			return err
		}
		var snapshot *domain.Drama
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				return err
			}
		}
		album := old.PlatformAlbumID
		if snapshot != nil && snapshot.PlatformAlbumID != "" {
			album = snapshot.PlatformAlbumID
		}
		if err := saveSubjectState(tx, id, album, snapshot); err != nil {
			return err
		}
		return syncCategoryRankings(tx, id, old.PublishedSnapshot, raw)
	})
}
