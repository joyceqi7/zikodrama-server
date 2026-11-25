package mysqlstore

import (
	"bytes"
	"context"
	"drama-server/internal/biz/application"
	"errors"
	"strings"
	"testing"

	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// TestLegacyDraftCatalogIntegration 验证历史状态与草稿筛选一致，读取和就绪确认均保留已有官方线上快照。
func TestLegacyDraftCatalogIntegration(t *testing.T) {
	f := newIAAFixture(t)
	seedDirect(t, f)
	ctx := application.WithSubject(context.Background(), "1")
	db := f.store.data.DB()
	var before, after struct{ PublishedSnapshot []byte }
	if err := db.Table("tiktok_drama_states").Select("published_snapshot").Where("subject_id=1 AND drama_id = 201").Scan(&before).Error; err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"draft", "published", "publishing", "unpublishing", "approved", "rejected", "ready"} {
		if err := db.Model(&dramaModel{}).Where("id = 201").Update("publish_status", status).Error; err != nil {
			t.Fatal(err)
		}
		for _, filter := range []string{"draft", "ready"} {
			page, err := f.store.ListDramaPage(ctx, operations.DramaListQuery{Page: 1, PageSize: 20, ContentStatus: filter})
			want := int64(0)
			if (status == "ready") == (filter == "ready") {
				want = 1
			}
			if err != nil || page.Total != want || int64(len(page.Items)) != want {
				t.Fatalf("历史状态筛选不一致 status=%s filter=%s total=%d err=%v", status, filter, page.Total, err)
			}
		}
		current, err := f.store.FindDrama(ctx, "201")
		if err != nil || current.PublishStatus != status {
			t.Fatal("列表读取改写历史内容状态", err)
		}
	}
	if err := db.Model(&dramaModel{}).Where("id = 201").Update("publish_status", "published").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PrepareDrama(ctx, "201", "operator"); err != nil {
		t.Fatal("历史内容无法确认待上线", err)
	}
	if err := db.Table("tiktok_drama_states").Select("published_snapshot").Where("subject_id=1 AND drama_id = 201").Scan(&after).Error; err != nil || !bytes.Equal(before.PublishedSnapshot, after.PublishedSnapshot) {
		t.Fatal("就绪确认改变旧官方线上快照", err)
	}
}

// TestBatchContentReadinessIntegration 验证真实事务整批校验回滚、幂等和内容筛选，确认就绪不创建发布记录。
func TestBatchContentReadinessIntegration(t *testing.T) {
	f := newIAAFixture(t)
	seedDirect(t, f)
	ctx := application.WithSubject(context.Background(), "1")
	first, err := f.store.FindDrama(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	second := consumer.FreezeContent(*first)
	second.ID = ""
	second.Title = "批量第二部"
	second.Description = ""
	for i := range second.Episodes {
		second.Episodes[i].ID = ""
		second.Episodes[i].DramaID = ""
	}
	if err := f.store.SaveDramaAggregate(ctx, &second, nil); err != nil {
		t.Fatal(err)
	}
	ids := []string{"201", second.ID}
	if err := f.store.PrepareDramas(ctx, ids, ""); !errors.Is(err, operations.ErrPublicationNotReady) {
		t.Fatal("未就绪批次未拒绝", err)
	}
	current, _ := f.store.FindDrama(ctx, "201")
	if current.PublishStatus != "draft" {
		t.Fatal("批量确认留下部分状态")
	}
	if err := f.store.PrepareDramas(ctx, []string{"201", "999999"}, ""); !errors.Is(err, consumer.ErrNotFound) {
		t.Fatal("缺失内容未拒绝", err)
	}
	second.Description = "完整简介"
	if err := f.store.SaveDramaAggregate(ctx, &second, nil); err != nil {
		t.Fatal(err)
	}
	// 聚合接口不接收源视频；按真实媒资绑定入口补齐新集的视频。
	for _, ep := range second.Episodes {
		expectedVID := ep.VID
		ep.VID = "batch-" + ep.ID
		ep.MediaStatus = "ready"
		if err := f.store.SaveEpisodeMedia(ctx, ep, second, expectedVID, ""); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := f.store.PrepareDramas(ctx, ids, ""); err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.store.ListDramaPage(ctx, operations.DramaListQuery{Page: 1, PageSize: 20, ContentStatus: "ready"})
	if err != nil || page.Total != 2 {
		t.Fatal("批量就绪筛选错误", page.Total, err)
	}
	for _, table := range []string{"direct_publication_versions", "direct_publication_operations", "tiktok_publications"} {
		var count int64
		if err := f.store.data.DB().Table(table).Count(&count).Error; err != nil || count != 0 {
			t.Fatal("确认就绪产生渠道记录", table, count, err)
		}
	}
}

// TestStandaloneVePlayerSchemaIntegration 验证测试库独立建表文件与完整增量得到同一最终结构，没有共享版本表或历史 TK 改写。
func TestStandaloneVePlayerSchemaIntegration(t *testing.T) {
	contentMigration := string(readDeploymentSQL(t, "migrations/20261105_001_subjects.sql"))
	schema := strings.Replace(strings.Replace(string(readDeploymentSchema(t)), string(readDeploymentSQL(t, "migrations/20261106_001_shared_vod_space.sql")), "", 1), contentMigration, "", 1)
	for _, file := range []string{"20261009_001_direct_playback.sql", "20261010_001_remove_application_playback_profile_key.sql", "20261010_002_content_versions.sql", "20261010_003_channel_publication_versions.sql"} {
		schema = strings.Replace(schema, string(readDeploymentSQL(t, "migrations/"+file)), "", 1)
	}
	schema += "\n" + string(readDeploymentSQL(t, "feat_veplayer_tables.sql")) + "\n" + contentMigration + "\n" + string(readDeploymentSQL(t, "migrations/20261106_001_shared_vod_space.sql"))
	f := newIAAFixtureWithSchema(t, schema, true)
	assertAllColumnsMatchComments(t, application.WithSubject(context.Background(), "1"), f.store.data.pool)
	repo, _ := seedDirect(t, f)
	published := submitDirect(t, repo, "publish", "")
	if published.ResultVersionID == "" {
		t.Fatal("独立建表文件无法发布直连内容")
	}
}
