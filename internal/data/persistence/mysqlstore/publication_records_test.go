package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"encoding/json"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
)

// TestPublicationRecordsLifecycle 验证真实MySQL中的审核冻结、上线幂等、历史替换与事务回滚。
func TestPublicationRecordsLifecycle(t *testing.T) {
	f := newIAAFixture(t)
	ctx := operations.WithPublicationActor(application.WithSubject(context.Background(), "1"), "901")
	db := f.store.data.DB().WithContext(ctx)
	now := time.Now().UTC()
	p := operations.TikTokPublication{SubjectID: "1", ID: "701", DramaID: "201", Status: operations.PublicationStatusApproved, Stage: operations.PublicationStageAwaitPublish, ReviewStatus: 2, SubmittedAt: &now, CreatedAt: now.Add(-time.Hour), UpdatedAt: now, ContentSnapshot: &domain.Drama{Title: "冻结标题"}}
	save := func() {
		t.Helper()
		if err := db.Transaction(func(tx *gorm.DB) error { return syncPublicationRecords(tx, p) }); err != nil {
			t.Fatal(err)
		}
	}
	save()
	p.PublishRequested = true
	p.Status = operations.PublicationStatusQueued
	p.Stage = operations.PublicationStageSetOnlineVersion
	p.UpdatedAt = now.Add(time.Minute)
	save()
	save()
	var review, release publicationRecordModel
	if err := db.Table("tiktok_review_records").Where("id = ?", p.ID).Take(&review).Error; err != nil {
		t.Fatal(err)
	}
	if review.Status != "approved" || review.ActorID != "901" {
		t.Fatalf("记录未保留审核事实: %s", review.Status)
	}
	if review.CreatedAt.Unix() != now.Unix() {
		t.Fatal("送审时间错误地使用了同步创建时间")
	}
	var count int64
	if err := db.Table("tiktok_release_records").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("平台确认前创建了上线事实: %d / %v", count, err)
	}
	p.Status = operations.PublicationStatusOnline
	p.Stage = operations.PublicationStageDone
	p.OnlineAt = &now
	save()
	save()
	if err := db.Table("tiktok_release_records").Where("id = ?", p.ID).Take(&release).Error; err != nil {
		t.Fatal(err)
	}
	if release.Status != "online" || release.Title != "冻结标题" {
		t.Fatalf("平台确认的上线事实错误: %s / %s", release.Status, release.Title)
	}
	if err := db.Table("tiktok_release_records").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("重复确认创建了重复上线记录: %d / %v", count, err)
	}
	p.ID = "702"
	p.UpdatedAt = now.Add(2 * time.Minute)
	save()
	db.Table("tiktok_release_records").Where("id = ?", "701").Take(&release)
	if release.Status != "superseded" {
		t.Fatalf("旧版本未替换: %s", release.Status)
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		p.Status = operations.PublicationStatusFailed
		if err := syncPublicationRecords(tx, p); err != nil {
			return err
		}
		return operations.ErrConflict
	})
	if err == nil {
		t.Fatal("预期事务失败")
	}
	var newest publicationRecordModel
	db.Table("tiktok_release_records").Where("id = ?", "702").Take(&newest)
	if newest.Status != "online" {
		t.Fatal("记录未随事务回滚")
	}
	// 后续审核失败后下架旧线上版本，不能把失败的审核创建成上线记录。
	p.ID = "703"
	p.PublishRequested = false
	p.OnlineAt = nil
	p.Stage = operations.PublicationStageDelistAlbum
	for _, state := range []string{operations.PublicationStatusQueued, operations.PublicationStatusFailed, operations.PublicationStatusQueued, operations.PublicationStatusOffline} {
		p.Status = state
		if state == operations.PublicationStatusOffline {
			p.Stage = operations.PublicationStageDone
		}
		save()
	}
	var offline publicationRecordModel
	if err := db.Table("tiktok_release_records").Where("id = ?", "702").Take(&offline).Error; err != nil {
		t.Fatal(err)
	}
	if offline.Status != "offline" {
		t.Fatalf("旧线上记录未下架: %s", offline.Status)
	}
	if err := db.Table("tiktok_release_records").Where("id = ?", "703").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("未通过审核的版本被记录为上线")
	}
	var exposed map[string]any
	if err := json.Unmarshal(review.RecordJSON, &exposed); err != nil {
		t.Fatal(err)
	}
	if _, exists := exposed["contentSnapshot"]; exists {
		t.Fatal("泄露内容快照")
	}
}

// TestPublicationRecordsExcludeSyncIntegration 验证同步阶段不创建审核记录，旧同步行也不混入审核列表。
func TestPublicationRecordsExcludeSyncIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithSubject(context.Background(), "1")
	db := f.store.data.DB().WithContext(ctx)
	p := operations.TikTokPublication{SubjectID: "1", ID: "801", DramaID: "201", Status: operations.PublicationStatusSynced, Stage: operations.PublicationStageAwaitSubmitReview, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := db.Transaction(func(tx *gorm.DB) error { return syncPublicationRecords(tx, p) }); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Table("tiktok_review_records").Where("id = ?", p.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("同步任务进入审核记录")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Table("tiktok_review_records").Create(&publicationRecordModel{SubjectID: "1", ID: p.ID, DramaID: p.DramaID, Status: p.Status, RecordJSON: raw, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}).Error; err != nil {
		t.Fatal(err)
	}
	page, err := f.store.ListPublicationRecords(ctx, operations.PublicationRecordQuery{Kind: "reviews", Page: 1, PageSize: 20, DramaID: p.DramaID})
	if err != nil || page.Total != 0 {
		t.Fatalf("旧同步记录未隔离: %v / %d", err, page.Total)
	}
}
