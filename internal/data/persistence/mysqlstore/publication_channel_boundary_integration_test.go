package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/operations"
)

// TestDramaManagementPublicationBoundaryIntegration 验证真实管理接口返回编辑信息及渠道摘要，渠道发布各自冻结快照，连载更新互不污染。
func TestDramaManagementPublicationBoundaryIntegration(t *testing.T) {
	f := newIAAFixture(t)
	repo, _ := seedDirect(t, f)
	ctx := application.WithSubject(context.Background(), "1")
	f.direct = directplay.NewService(repo, nil)
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	raw := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var admin adminrbac.LoginResult
	if err := json.Unmarshal(raw, &admin); err != nil {
		t.Fatal(err)
	}
	assertNoVersions := func() {
		t.Helper()
		for _, path := range []string{"/admin/v1/dramas?page=1&pageSize=20", "/admin/v1/dramas/201"} {
			raw := iaaRequest(t, server.URL, "GET", path, admin.Token, "", nil, 200)
			var object map[string]json.RawMessage
			if err := json.Unmarshal(raw, &object); err != nil {
				t.Fatal(err)
			}
			rows := []map[string]json.RawMessage{object}
			if items, ok := object["items"]; ok {
				if err := json.Unmarshal(items, &rows); err != nil {
					t.Fatal(err)
				}
			}
			for _, row := range rows {
				for _, key := range []string{"contentVersionId", "contentVersion", "versions"} {
					if _, ok := row[key]; ok {
						t.Fatalf("短剧管理泄漏发布版本字段 %s", key)
					}
				}
			}
		}
	}
	assertNoVersions()
	// 未上线共享版本表彻底移除，短剧主表也没有版本列。
	var columns, tables int64
	db := f.store.data.DB()
	if err := db.Table("information_schema.COLUMNS").Where("TABLE_SCHEMA=DATABASE() AND TABLE_NAME='dramas' AND COLUMN_NAME IN ?", []string{"content_version_id", "content_version"}).Count(&columns).Error; err != nil || columns != 0 {
		t.Fatal("短剧主表仍保存发布版本", err)
	}
	if err := db.Table("information_schema.TABLES").Where("TABLE_SCHEMA=DATABASE() AND TABLE_NAME='drama_content_versions'").Count(&tables).Error; err != nil || tables != 0 {
		t.Fatal("共享版本表未移除", err)
	}
	first := submitDirect(t, repo, "publish", "")
	ready, err := f.store.FindDrama(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tk := operations.TikTokPublication{SubjectID: "1", DramaID: "201", PlatformVersion: 7, ContentSnapshot: ready, Status: operations.PublicationStatusSynced, Stage: operations.PublicationStageAwaitSubmitReview, CreatedAt: now, UpdatedAt: now}
	if err := f.store.StartTikTokPublication(ctx, &tk); err != nil {
		t.Fatal(err)
	}
	ready.Title = "连载新增一集"
	ready.Episodes = append(ready.Episodes, consumer.Episode{DramaID: "201", Number: 9, Title: "第九集", VID: "channel-nine", MediaSpaceName: "fixture-space", MediaStatus: "ready"})
	if err := f.store.SaveDramaAggregate(ctx, ready, nil); err != nil {
		t.Fatal(err)
	}
	if ready.PublishStatus != "draft" {
		t.Fatal("编辑没有回到草稿")
	}
	ready, err = f.store.PrepareDrama(ctx, "201", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	assertNoVersions()
	next := operations.TikTokPublication{SubjectID: "1", DramaID: "201", PlatformVersion: 8, ContentSnapshot: ready, Status: operations.PublicationStatusSynced, Stage: operations.PublicationStageAwaitSubmitReview, CreatedAt: now.Add(time.Second), UpdatedAt: now}
	if err := f.store.StartTikTokPublication(ctx, &next); err != nil {
		t.Fatal(err)
	}
	versions, err := f.store.listDirectPublicationVersions(ctx, "201")
	if err != nil || len(versions) != 1 || versions[0].Number != 1 {
		t.Fatal("TK 同步生成或改变了直连版本", err)
	}
	work, err := repo.Workbench(ctx, "201")
	if err != nil || work.Publication.CurrentVersionID != first.ResultVersionID || !work.Summary.PendingChanges {
		t.Fatal("TK 新内容改变直连线上指针", err)
	}
	second := submitDirect(t, repo, "publish", "")
	versions, err = f.store.listDirectPublicationVersions(ctx, "201")
	if err != nil || len(versions) != 2 || versions[0].Number != 2 || len(versions[0].Snapshot.Episodes) != 9 || len(versions[1].Snapshot.Episodes) != 8 {
		t.Fatal("直连新旧版本快照错误", err)
	}
	current, err := f.store.FindTikTokPublicationByDramaID(ctx, "201")
	if err != nil || current.ID != next.ID || current.PlatformVersion != 8 || len(current.ContentSnapshot.Episodes) != 9 {
		t.Fatal("直连上架修改了 TK 任务或版本", err)
	}
	if second.ResultVersionID == first.ResultVersionID {
		t.Fatal("直连新内容没有生成新版本")
	}
	assertNoVersions()
	raw = iaaRequest(t, server.URL, "GET", "/admin/v1/dramas?page=1&pageSize=20", admin.Token, "", nil, 200)
	var page operations.DramaListPage
	if err := json.Unmarshal(raw, &page); err != nil || !page.Items[0].DeleteBlocked {
		t.Fatal("移除版本字段后丢失发布引用删除门禁", err)
	}
	// 旧短剧版本入口已删除，发布历史仅在各渠道工作台提供。
	request, err := http.NewRequest("GET", server.URL+"/admin/v1/dramas/201/versions", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+admin.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatal("短剧管理仍提供版本接口", response.StatusCode)
	}
}

// TestChannelPublicationConcurrencyIntegration 验证两渠道并发冻结同一内容时，直连独立生成 V1，TK 保持自身平台版本和任务快照。
func TestChannelPublicationConcurrencyIntegration(t *testing.T) {
	f := newIAAFixture(t)
	repo, _ := seedDirect(t, f)
	ctx := application.WithSubject(context.Background(), "1")
	ready, err := f.store.PrepareDrama(ctx, "201", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tk := operations.TikTokPublication{SubjectID: "1", DramaID: "201", PlatformVersion: 7, ContentSnapshot: ready, Status: operations.PublicationStatusSynced, Stage: operations.PublicationStageAwaitSubmitReview, CreatedAt: now, UpdatedAt: now}
	var op directplay.Operation
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		var err error
		op, err = repo.Accept(ctx, "201", directplay.Command{Kind: "publish", RequestID: "concurrent-channel"}, "fixture")
		results <- err
	}()
	go func() { defer wg.Done(); <-start; results <- f.store.StartTikTokPublication(ctx, &tk) }()
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	versions, err := f.store.listDirectPublicationVersions(ctx, "201")
	if err != nil || len(versions) != 1 || versions[0].Number != 1 || op.ResultVersionID != versions[0].ID {
		t.Fatal("直连并发版本错误", err)
	}
	if tk.PlatformVersion != 7 || tk.ContentSnapshot.Title != versions[0].Snapshot.Title {
		t.Fatal("TK 平台版本或冻结内容被直连改变")
	}
}
