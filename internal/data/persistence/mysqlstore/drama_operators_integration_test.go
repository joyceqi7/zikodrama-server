package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"errors"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/adminrbac"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// TestDramaOperatorsIntegration 验证长 ID、创建人不变、人工更新、事务回滚及自动任务不覆盖。
func TestDramaOperatorsIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithSubject(context.Background(), "1")
	creator := adminrbac.User{Username: "operator-creator", Nickname: "创建人", Status: "active", AuthVersion: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	editor := adminrbac.User{Username: "operator-editor", Nickname: "最新操作人", Status: "active", AuthVersion: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	for _, user := range []*adminrbac.User{&creator, &editor} {
		if err := f.store.CreateAdminUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	actor := func(id string) context.Context {
		return operations.WithPublicationActor(ctx, id)
	}
	drama := domain.Drama{ID: "9007199254740993", Title: "操作人联调", PublishStatus: "draft", Status: "updating", TagIDs: []string{}, Episodes: []domain.Episode{{ID: "9007199254740996", DramaID: "9007199254740993", Number: 1, Title: "发布分集", VID: "operator-vid", MediaStatus: "ready"}}}
	if err := f.store.SaveDramaAggregate(actor(creator.ID), &drama, nil); err != nil {
		t.Fatal(err)
	}
	assertActors := func(wantCreator, wantEditor string) {
		t.Helper()
		page, err := f.store.ListDramaPage(ctx, operations.DramaListQuery{Page: 1, PageSize: 20, Query: "操作人联调"})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("列表读取失败：%v", err)
		}
		item := page.Items[0]
		if item.ID != drama.ID || item.CreatedByID != wantCreator || item.UpdatedByID != wantEditor {
			t.Fatalf("操作人或长 ID 不符：%+v", item)
		}
		if item.CreatedByName != "创建人" {
			t.Fatal("创建人昵称未关联")
		}
	}
	assertActors(creator.ID, creator.ID)
	if err := f.store.SaveDramaOperations(actor(editor.ID), drama.ID, "负责人", "交接", nil); err != nil {
		t.Fatal(err)
	}
	assertActors(creator.ID, editor.ID)
	if err := f.store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
		t.Fatal(err)
	}
	assertActors(creator.ID, editor.ID)
	// 已更新操作人后任务不存在会导致事务回滚，不能把失败重试者写成最新操作人。
	job := operations.MediaJob{ID: "9007199254740994", DramaID: drama.ID, Kind: operations.MediaJobUpload, ResourceClass: operations.MediaResourceVideo, Status: operations.MediaJobQueued, CreatedAt: time.Now(), UpdatedAt: time.Now(), NextAttemptAt: time.Now()}
	err := f.store.UpdateMediaJob(actor(creator.ID), job)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("缺失任务应失败：%v", err)
	}
	assertActors(creator.ID, editor.ID)
	if err := f.store.SaveDramaAggregate(actor(editor.ID), &drama, nil); err != nil {
		t.Fatal(err)
	}
	assertActors(creator.ID, editor.ID)
	// 官方发布人工控制和后台结果投影共用持久化入口，只有可信人工上下文更新操作人。
	now := time.Now().UTC()
	publication := operations.TikTokPublication{SubjectID: "1", DramaID: drama.ID, Status: operations.PublicationStatusQueued, Stage: operations.PublicationStageUpdateAlbum, Priority: "normal", CreatedAt: now, UpdatedAt: now}
	seedReadyPublicationContent(t, f.store, &publication)
	if err := f.store.StartTikTokPublication(actor(creator.ID), &publication); err != nil {
		t.Fatal(err)
	}
	assertActors(creator.ID, creator.ID)
	publication.Status, publication.Stage = operations.PublicationStatusSynced, operations.PublicationStageAwaitSubmitReview
	if err := f.store.UpdateTikTokPublication(actor(editor.ID), publication, nil); err != nil {
		t.Fatal(err)
	}
	assertActors(creator.ID, editor.ID)
	publication.Revision++
	if err := f.store.UpdateTikTokPublication(ctx, publication, nil); err != nil {
		t.Fatal(err)
	}
	assertActors(creator.ID, editor.ID)
	// 上传预留、确认及重试在所属剧的事务中记账，官方后台投影不会覆盖。
	if err := f.store.data.DB().Model(&dramaModel{}).Where("id = ?", drama.ID).UpdateColumn("publish_status", "draft").Error; err != nil {
		t.Fatal(err)
	}
	drama.Episodes = []domain.Episode{{ID: "9007199254740995", DramaID: drama.ID, Number: 1, Title: "第一集", MediaStatus: "pending"}}
	if err := f.store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
		t.Fatal(err)
	}
	upload := operations.MediaJob{DramaID: drama.ID, EpisodeID: drama.Episodes[0].ID, Kind: operations.MediaJobUpload, ResourceClass: operations.MediaResourceVideo, FileName: "fixture.mp4", Size: 10, Status: operations.MediaJobReserved, CreatedAt: now, UpdatedAt: now, NextAttemptAt: now}
	if err := f.store.CreateMediaJob(actor(creator.ID), &upload); err != nil {
		t.Fatal(err)
	}
	assertActors(creator.ID, creator.ID)
	upload.RemoteVID, upload.Status = "fixture-vid", operations.MediaJobQueued
	if err := f.store.ConfirmMediaUpload(actor(editor.ID), upload); err != nil {
		t.Fatal(err)
	}
	assertActors(creator.ID, editor.ID)
	upload.Attempts++
	upload.UpdatedAt = time.Now().UTC()
	if err := f.store.UpdateMediaJob(actor(creator.ID), upload); err != nil {
		t.Fatal(err)
	}
	assertActors(creator.ID, creator.ID)
	upload.Attempts++
	upload.UpdatedAt = time.Now().UTC()
	if err := f.store.UpdateMediaJob(ctx, upload); err != nil {
		t.Fatal(err)
	}
	assertActors(creator.ID, creator.ID)
	app, err := f.store.FindApplication(ctx, "101")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.EnsureDistribution(ctx, operations.DistributionSource{SubjectID: "1", DramaID: drama.ID, OwnerID: "101"}, app); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetDistribution(actor(editor.ID), drama.ID, "101", true); err != nil {
		t.Fatal(err)
	}
	assertActors(creator.ID, editor.ID)
}

// TestDramaOperatorsUpgradeIntegration 验证已有库升级保留未知身份及原更新时间，审计表结构不变。
func TestDramaOperatorsUpgradeIntegration(t *testing.T) {
	schema := string(readDeploymentSQL(t, "init.sql"))
	f := newIAAFixtureWithSchema(t, schema, false)
	ctx := application.WithSubject(context.Background(), "1")
	oldTime := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"301", "302"} {
		_, err := f.store.data.pool.ExecContext(ctx, "INSERT INTO dramas (id,title,description,cover_url,status,tag_ids_json,publish_status,operation_note,updated_at) VALUES (?,?,'','','updating','[]','draft','',?)", id, "历史剧"+id, oldTime)
		if err != nil {
			t.Fatal(err)
		}
	}
	var auditBefore string
	if err := f.store.data.DB().Raw("SHOW CREATE TABLE admin_audit_logs").Row().Scan(new(string), &auditBefore); err != nil {
		t.Fatal(err)
	}
	script := readDeploymentSQL(t, "migrations/20260930_001_drama_operators.sql")
	for _, statement := range strings.Split(string(script), ";") {
		if strings.TrimSpace(statement) != "" {
			if _, err := f.store.data.pool.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, id := range []string{"301", "302"} {
		var drama dramaModel
		if err := f.store.data.DB().First(&drama, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if drama.CreatedBy != nil || drama.UpdatedBy != nil || !drama.UpdatedAt.Equal(oldTime) {
			t.Fatal("升级不能猜测历史身份或改变原更新时间")
		}
	}
	var auditAfter string
	if err := f.store.data.DB().Raw("SHOW CREATE TABLE admin_audit_logs").Row().Scan(new(string), &auditAfter); err != nil {
		t.Fatal(err)
	}
	if auditBefore != auditAfter {
		t.Fatal("短剧字段升级修改了审计表结构")
	}
}
