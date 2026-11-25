package mysqlstore

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"strconv"
	"sync"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	content "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/sharding"
	"drama-server/internal/biz/visitor"
)

// TestAutoIncrementEntityFlowIntegration 验证实际 MySQL 分配主键、聚合回填、任务回调关联和来源幂等。
func TestAutoIncrementEntityFlowIntegration(t *testing.T) {
	requireUIDIntegration(t)
	f := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	ctx := application.WithSubject(context.Background(), "1")
	app := application.Application{SubjectID: "1", TikTokAppID: "0007685293427067586568", Name: "自增关联测试", Status: application.StatusActive, TimeZone: "UTC"}
	if err := f.store.CreateApplication(ctx, &app); err != nil {
		t.Fatal(err)
	}
	if app.ID == "" || app.ID == app.TikTokAppID {
		t.Fatalf("内部与官方应用身份未分离: %q", app.ID)
	}
	found, err := f.store.FindApplication(ctx, app.ID)
	if err != nil || found.TikTokAppID != app.TikTokAppID {
		t.Fatalf("按主键读取应用: %v", err)
	}
	official, err := f.store.FindApplicationByTikTokAppID(ctx, app.TikTokAppID)
	if err != nil || official.ID != app.ID {
		t.Fatalf("按官方身份读取应用: %v", err)
	}
	for _, table := range []string{"dramas", "episodes", "media_jobs", "tiktok_publications"} {
		if err := f.store.data.DB().Exec("ALTER TABLE `" + table + "` AUTO_INCREMENT=9007199254740993").Error; err != nil {
			t.Fatal(err)
		}
	}
	drama := content.Drama{Title: "自增短剧", Language: "en", Status: content.DramaStatusCompleted, PublishStatus: "draft", Episodes: []content.Episode{{Number: 1, Title: "第一集", MediaStatus: "pending"}, {Number: 2, Title: "第二集", MediaStatus: "pending"}}}
	if err := f.store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
		t.Fatal(err)
	}
	if drama.ID != "9007199254740993" {
		t.Fatalf("大整数主键丢失: %q", drama.ID)
	}
	for _, ep := range drama.Episodes {
		if ep.ID == "" || ep.DramaID != drama.ID {
			t.Fatalf("分集关联未回填: %q/%q", ep.ID, ep.DramaID)
		}
	}
	originalID := drama.Episodes[0].ID
	drama.Episodes = append(drama.Episodes, content.Episode{Number: 3, Title: "第三集", MediaStatus: "pending"})
	if err := f.store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
		t.Fatal(err)
	}
	if drama.Episodes[0].ID != originalID || drama.Episodes[2].ID == "" {
		t.Fatal("修改聚合改变已有 ID 或遗漏新增 ID")
	}
	now := time.Now().UTC()
	job := operations.MediaJob{Kind: operations.MediaJobUpload, Status: operations.MediaJobReserved, DramaID: drama.ID, EpisodeID: originalID, ResourceClass: operations.MediaResourceVideo, CreatedAt: now, UpdatedAt: now, NextAttemptAt: now}
	if err := f.store.CreateMediaJob(ctx, &job); err != nil {
		t.Fatal(err)
	}
	loadedJob, err := f.store.FindMediaJob(ctx, job.ID)
	if err != nil || job.ID != "9007199254740993" || loadedJob.EpisodeID != originalID {
		t.Fatalf("上传关联: %v", err)
	}
	cover := operations.MediaJob{Kind: operations.MediaJobCover, Status: operations.MediaJobReserved, CreatedAt: now, UpdatedAt: now, NextAttemptAt: now}
	if err := f.store.CreateMediaJob(ctx, &cover); err != nil {
		t.Fatal(err)
	}
	publication := operations.TikTokPublication{SubjectID: "1", DramaID: drama.ID, OwnerApplicationID: "official-owner", Status: operations.PublicationStatusQueued, Stage: operations.PublicationStageUploadCover, ContentSnapshot: &drama, CreatedAt: now, UpdatedAt: now, NextAttemptAt: now}
	// 发布事务使用已完成媒资的内容，ID 与上传关联沿用刚才创建的分集。
	for i := range drama.Episodes {
		drama.Episodes[i].VID = fmt.Sprintf("auto-increment-vid-%d", i+1)
		drama.Episodes[i].MediaStatus = "ready"
		if err := f.store.SaveEpisodeMedia(ctx, drama.Episodes[i], drama, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	seedReadyPublicationContent(t, f.store, &publication)
	// 插入任务后模拟检查点写入失败，事务不得留下任务或向调用方泄漏已回滚的 ID。
	failure := errors.New("fixture publication update failed")
	callback := f.store.data.DB().Callback().Create()
	if err := callback.After("gorm:create").Register("id_fixture:reject_drama_update", func(tx *gorm.DB) {
		if tx.Statement.Table == "tiktok_publications" {
			tx.AddError(failure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	failedErr := f.store.StartTikTokPublication(ctx, &publication)
	if err := callback.Remove("id_fixture:reject_drama_update"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(failedErr, failure) || publication.ID != "" || f.count(t, "tiktok_publications", "drama_id = ?", drama.ID) != 0 {
		t.Fatal("发布失败未完整回滚")
	}
	seedReadyPublicationContent(t, f.store, &publication)
	if err := f.store.StartTikTokPublication(ctx, &publication); err != nil {
		t.Fatal(err)
	}
	if publication.ID != "9007199254740994" {
		t.Fatalf("发布任务未回填 ID: %q", publication.ID)
	}
	// 审计读取同样保留超过 JavaScript 安全整数范围的主键。
	if err := f.store.data.DB().Exec("ALTER TABLE admin_audit_logs AUTO_INCREMENT=9007199254740993").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.store.SaveAuditRecord(ctx, operations.AuditRecord{RequestID: "id-fixture-audit", ActorID: "1", Action: "fixture", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	audits, err := f.store.ListAuditRecords(ctx, 1)
	if err != nil || len(audits) != 1 || audits[0].ID != "9007199254740993" {
		t.Fatalf("审计自增主键丢失: %v", err)
	}
	var ids [8]string
	var errs [8]error
	var workers sync.WaitGroup
	identityHash := visitor.TikTokOpenIDHash(app.TikTokAppID, "fixture-open-id")
	for i := range ids {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ids[i], errs[i] = f.store.ResolveTikTokIdentity(ctx, visitor.TikTokIdentityRecord{ApplicationID: app.ID, OpenIDHash: identityHash})
		}()
	}
	workers.Wait()
	for i, id := range ids {
		if errs[i] != nil || id == "" || id != ids[0] {
			t.Fatalf("并发身份创建不幂等: %q/%v", id, errs[i])
		}
		if _, err := strconv.ParseUint(id, 10, 64); err != nil {
			t.Fatal(err)
		}
	}
	grantContext := monetization.GrantContext{ApplicationID: app.ID, UserID: ids[0], SourceType: "order", SourceID: "order-event", DramaID: drama.ID, EpisodeIDs: []string{originalID}, Now: now}
	items, err := monetization.DefaultStrategies().BuildEntitlements([]monetization.GrantTemplate{{Type: "episode_access", Config: []byte(`{"selection":"next_locked_episodes","count":1,"validity":"unlimited"}`)}}, grantContext)
	if err != nil {
		t.Fatal(err)
	}
	scoped := application.WithContext(ctx, app.ID)
	scoped = sharding.WithBucketID(scoped, logicalBucket(app.ID, identityHash))
	if err := f.store.GrantEntitlements(scoped, ids[0], items); err != nil {
		t.Fatal(err)
	}
	// 第二项校验失败时第一项也必须回滚，且不得回填未提交的 ID。
	rollback := append([]monetization.Entitlement(nil), items...)
	rollback[0].ID, rollback[0].SourceID = "", "rollback-event"
	invalid := rollback[0]
	invalid.UserID = "0"
	rollback = append(rollback, invalid)
	if err := f.store.GrantEntitlements(scoped, ids[0], rollback); err == nil || rollback[0].ID != "" {
		t.Fatal("权益失败未保持输入和事务一致")
	}
	if f.count(t, "entitlements", "source_id = ?", "rollback-event") != 0 {
		t.Fatal("失败权益仍被提交")
	}
	id := items[0].ID
	items[0].ID = ""
	if err := f.store.GrantEntitlements(scoped, ids[0], items); err != nil {
		t.Fatal(err)
	}
	if id == "" || id != items[0].ID {
		t.Fatal("重复来源未恢复相同自增权益 ID")
	}
	allowed, err := f.store.HasEntitlement(scoped, ids[0], drama.ID, originalID, now)
	if err != nil || !allowed {
		t.Fatalf("权益关联不可读取: %v", err)
	}
	if err := f.store.RevokeEntitlementSource(scoped, ids[0], "order", "order-event", now.Add(time.Minute)); err != nil {
		t.Fatalf("撤销权益来源失败: %v", err)
	}
	allowed, err = f.store.HasEntitlement(scoped, ids[0], drama.ID, originalID, now.Add(2*time.Minute))
	if err != nil || allowed {
		t.Fatalf("撤销后播放投影仍允许访问: allowed=%t err=%v", allowed, err)
	}
	bucketID := logicalBucket(app.ID, identityHash)
	if f.count(t, userTable("entitlement_grants", bucketID), "visitor_id = ? AND source_id = ?", ids[0], "order-event") != 1 ||
		f.count(t, userTable("user_entitlement_state", bucketID), "visitor_id = ? AND grant_id = ?", ids[0], id) != 0 ||
		f.count(t, userTable("entitlement_revocations", bucketID), "visitor_id = ? AND source_id = ?", ids[0], "order-event") != 1 {
		t.Fatal("撤销没有保留不可变授予和撤销流水，或没有删除当前投影")
	}
	if err := f.store.GrantEntitlements(scoped, ids[0], items); err != nil {
		t.Fatal(err)
	}
	if allowed, err := f.store.HasEntitlement(scoped, ids[0], drama.ID, originalID, now.Add(2*time.Minute)); err != nil || allowed {
		t.Fatalf("撤销来源重放后权益被复活: allowed=%t err=%v", allowed, err)
	}
}
