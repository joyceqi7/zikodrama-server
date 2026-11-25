package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/operations"
)

// TestApplicationCategoryLifecycleIntegration 从真实发布和分发存储入口验证事务联动，不调用平台服务。
func TestApplicationCategoryLifecycleIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithSubject(context.Background(), "1")
	drama := consumer.Drama{ID: "901", Title: "Automatic ranking", PublishStatus: "draft", Status: "completed", TagIDs: []string{"21"}, Episodes: []consumer.Episode{{ID: "902", DramaID: "901", Number: 1, Title: "第一集", VID: "category-vid", MediaStatus: "ready"}}}
	if err := f.store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
		t.Fatal(err)
	}
	read := func(tag string) homepage.Ranking {
		t.Helper()
		rows, err := f.store.GetRankings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return rows[tag]
	}
	if len(read("21").Items) != 0 {
		t.Fatal("草稿错误入榜")
	}
	publication := operations.TikTokPublication{SubjectID: "1", DramaID: drama.ID, OwnerApplicationID: "fixture-owner", OwnerScope: operations.PublicationOwnerScope{ClientKey: "fixture-101"}, Status: operations.PublicationStatusQueued, Stage: operations.PublicationStageUploadCover, Priority: "normal", NextAttemptAt: time.Now().UTC()}
	seedReadyPublicationContent(t, f.store, &publication)
	if err := f.store.StartTikTokPublication(ctx, &publication); err != nil {
		t.Fatal(err)
	}
	publish := func(tags []string) error {
		current, err := f.store.FindTikTokPublicationByDramaID(ctx, drama.ID)
		if err != nil {
			return err
		}
		online := drama
		online.Published = nil
		online.PublishStatus = "published"
		online.TagIDs = tags
		online.PlatformVersion++
		drama.Published = &online
		drama.PublishStatus = "published"
		current.Status = operations.PublicationStatusOnline
		current.Stage = operations.PublicationStageDone
		return f.store.UpdateTikTokPublication(ctx, current, &drama)
	}
	if err := publish([]string{"21"}); err != nil {
		t.Fatal(err)
	}
	first := read("21")
	if len(first.Items) != 1 || first.Items[0].DramaID != drama.ID || !first.Items[0].Visible {
		t.Fatal("上线没有自动关联")
	}
	first.Items[0].Visible = false
	saved, err := f.store.ReplaceRanking(ctx, "21", first)
	if err != nil {
		t.Fatal(err)
	}
	// 普通草稿改标签不应改变线上分类或覆盖运营配置。
	drama.TagIDs = []string{"22"}
	drama.PublishStatus = "draft"
	if err := f.store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read("21"), saved) || len(read("22").Items) != 0 {
		t.Fatal("草稿编辑覆盖线上关联或运营配置")
	}
	// 官方授权仅改变消费端可见性，不改变共享榜单的人工设置。
	app, err := f.store.FindApplication(ctx, "102")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.EnsureDistribution(ctx, operations.DistributionSource{SubjectID: "1", DramaID: drama.ID, OwnerID: "fixture-owner", AlbumID: "fixture-album"}, app); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetDistribution(ctx, drama.ID, app.ID, true); err != nil {
		t.Fatal(err)
	}
	job, err := f.store.ClaimDistribution(ctx, "category-test", time.Now().Add(time.Second))
	if err != nil || job == nil {
		t.Fatal("未领取分发", err)
	}
	if err := f.store.FinishDistribution(ctx, *job, nil); err != nil {
		t.Fatal(err)
	}
	recipient := read("21")
	if !reflect.DeepEqual(recipient, saved) {
		t.Fatal("授权改变了共享配置")
	}
	if err := f.store.SetDistribution(ctx, drama.ID, "102", false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read("21"), saved) {
		t.Fatal("撤权分类联动错误")
	}
	if err := publish([]string{"22"}); err != nil {
		t.Fatal(err)
	}
	if len(read("21").Items) != 0 || len(read("22").Items) != 1 {
		t.Fatal("正式标签变更未移动分类")
	}
	if _, err := f.store.ReplaceRanking(ctx, "21", saved); !errors.Is(err, homepage.ErrConflict) {
		t.Fatal("标签联动后旧版本仍能回写", err)
	}
	// 注入真实存储失败，证明分类同步失败会同时回滚线上快照和发布任务修订。
	before, err := f.store.FindTikTokPublicationByDramaID(ctx, drama.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().Exec("ALTER TABLE category_rankings RENAME TO category_failure_fixture").Error; err != nil {
		t.Fatal(err)
	}
	publishErr := publish([]string{"23"})
	if err := f.store.data.DB().Exec("ALTER TABLE category_failure_fixture RENAME TO category_rankings").Error; err != nil {
		t.Fatal(err)
	}
	if publishErr == nil {
		t.Fatal("分类故障未传播")
	}
	after, err := f.store.FindTikTokPublicationByDramaID(ctx, drama.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := f.store.FindDrama(ctx, drama.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Revision != after.Revision || !reflect.DeepEqual(stored.Published.TagIDs, []string{"22"}) {
		t.Fatal("分类失败留下半提交的发布结果")
	}
	stored.Published = nil
	after.Status = operations.PublicationStatusOnline
	if err := f.store.UpdateTikTokPublication(ctx, after, stored); err != nil {
		t.Fatal(err)
	}
	if len(read("22").Items) != 0 {
		t.Fatal("下架未清理关联")
	}
}

// TestApplicationCategoryConcurrentIntegration 验证并发排序只有一方成功，新成员不会被旧版本整表保存覆盖。
func TestApplicationCategoryConcurrentIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithSubject(context.Background(), "1")
	seedCategoryRankingDramas(t, f)
	rows, err := f.store.GetRankings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	initial := rows["21"]
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := f.store.ReplaceRanking(ctx, "21", initial); results <- err }()
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, homepage.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal("并发更新覆盖了其他管理员的修改")
	}
	rows, err = f.store.GetRankings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := rows["21"]
	published := consumer.Drama{ID: "902", Title: "New arrival", PublishStatus: "published", Status: "completed", OwnerApplicationID: "101", TagIDs: []string{"21"}}
	drama := published
	drama.Published = &published
	if err := f.store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
		t.Fatal(err)
	}
	snapshotRaw, _ := json.Marshal(published)
	if err := seedOfficialProjection(f.store.data.DB().WithContext(ctx), drama.ID, snapshotRaw); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ReplaceRanking(ctx, "21", before); !errors.Is(err, homepage.ErrConflict) {
		t.Fatal("上线新成员被旧排序覆盖", err)
	}
	rows, err = f.store.GetRankings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows["21"].Items) != 4 || rows["21"].Items[3].DramaID != "902" {
		t.Fatal("新上线剧未追加")
	}
	// 即使可见性查询返回新剧，存储层也不能手动关联别的官方标签。
	input := rows["21"]
	input.Items = append(input.Items, homepage.RankingItem{DramaID: "204", Visible: true})
	if _, err := f.store.ReplaceRanking(ctx, "21", input); !errors.Is(err, consumer.ErrNotFound) {
		t.Fatal("可手动插入未关联成员", err)
	}
	// 确认落库 JSON 保留字符串 ID，避免 JavaScript 大整数精度丢失。
	var row categoryRankingModel
	if err := f.store.data.DB().Where("tag_id=?", "21").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	var raw []map[string]any
	if err := json.Unmarshal(row.ItemsJSON, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw[0]["dramaId"].(string); !ok {
		t.Fatal("短剧标识被写为数字")
	}
}
