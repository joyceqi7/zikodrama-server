package mysqlstore

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/officialmedia"
)

// TestContentReadinessConcurrencyIntegration 验证真实行锁保证并发就绪确认幂等，失败不改变状态或生成渠道版本。
func TestContentReadinessConcurrencyIntegration(t *testing.T) {
	f := newIAAFixture(t)
	seedDirect(t, f)
	ctx := application.WithSubject(context.Background(), "1")
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := f.store.PrepareDrama(ctx, "201", "fixture"); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	versions, err := f.store.listDirectPublicationVersions(ctx, "201")
	if err != nil || len(versions) != 0 {
		t.Fatal("并发确认重复发号", err)
	}
	d, err := f.store.FindDrama(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	d.Description = ""
	if err := f.store.SaveDramaAggregate(ctx, d, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PrepareDrama(ctx, "201", "fixture"); err != operations.ErrPublicationNotReady {
		t.Fatal("未就绪内容被确认", err)
	}
	versions, err = f.store.listDirectPublicationVersions(ctx, "201")
	if err != nil || len(versions) != 0 {
		t.Fatal("失败确认留下版本", err)
	}
	current, err := f.store.FindDrama(ctx, "201")
	if err != nil || current.PublishStatus != "draft" {
		t.Fatal("失败确认污染状态", err)
	}
}

// TestChannelPublicationsPreserveTikTokUpgradeIntegration 验证升级不回填 TK 版本或重置编辑状态，旧快照和审核任务独立保护内容。
func TestChannelPublicationsPreserveTikTokUpgradeIntegration(t *testing.T) {
	migration := string(readDeploymentSQL(t, "migrations/20261010_003_channel_publication_versions.sql"))
	schema := strings.Replace(strings.Replace(string(readDeploymentSchema(t)), string(readDeploymentSQL(t, "migrations/20261106_001_shared_vod_space.sql")), "", 1), migration, "", 1)
	schema = strings.Replace(schema, string(readDeploymentSQL(t, "migrations/20261105_001_subjects.sql")), "", 1)
	f := newIAAFixtureWithSchema(t, schema, false)
	db := f.store.data.DB()
	ctx := application.WithSubject(context.Background(), "1")
	if err := db.Exec("INSERT INTO applications(id,tiktok_app_id,client_key,name,status,time_zone,region_code,compliance_base_url,ad_units_json) VALUES(101,'fixture-owner','fixture-owner-key','已有官方应用','active','UTC','GLOBAL','',JSON_OBJECT())").Error; err != nil {
		t.Fatal(err)
	}
	source := consumer.Drama{ID: "201", Title: "历史线上", Description: "历史简介", CoverURL: "https://example.test/online.png", CoverMID: "online-cover", CoverSpaceName: "online-space", Status: "updating", Language: "en", PublishStatus: "published", PlatformAlbumID: "official-album", PlatformVersion: 7, OwnerApplicationID: "101", AuthorizedApplicationIDs: []string{"102"}, TagIDs: []string{}, Episodes: []consumer.Episode{{ID: "301", DramaID: "201", Number: 1, Title: "线上第一集", VID: "online-vid", MediaSpaceName: "online-space", PlatformEpisodeID: "official-episode-1", DurationSeconds: 60, MediaStatus: "ready"}}}
	rawSource, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO dramas(id,title,description,cover_url,status,language,tag_ids_json,publish_status,operation_note,published_snapshot) VALUES(201,?,?,?,?,?,?,'publishing','',?)", source.Title, source.Description, source.CoverURL, source.Status, source.Language, "[]", rawSource).Error; err != nil {
		t.Fatal(err)
	}
	// 编辑区与两个官方快照共用分集身份，删集后历史行必须继续存在。
	if err := db.Exec("INSERT INTO episodes(id,drama_id,episode_number,title,description,vid,media_space_name,poster_url,duration_seconds,media_status,is_draft) VALUES(301,201,1,'线上第一集','','online-vid','online-space','',60,'ready',1),(302,201,2,'审核第一集','','review-vid','review-space','',60,'ready',1)").Error; err != nil {
		t.Fatal(err)
	}
	for i, status := range []string{"draft", "ready", "published", "approved", "rejected"} {
		if err := db.Exec("INSERT INTO dramas(id,title,description,cover_url,status,tag_ids_json,publish_status,operation_note) VALUES(?, '已有编辑状态', '', '', 'completed', '[]', ?, '')", 401+i, status).Error; err != nil {
			t.Fatal(err)
		}
	}
	frozen := source
	frozen.Title, frozen.CoverURL, frozen.CoverMID, frozen.CoverSpaceName = "历史审核", "https://example.test/review.png", "review-cover", "review-space"
	frozen.PlatformVersion = 8
	frozen.Episodes = []consumer.Episode{{ID: "302", DramaID: "201", Number: 1, Title: "审核第一集", VID: "review-vid", MediaSpaceName: "review-space", PlatformEpisodeID: "official-episode-2", DurationSeconds: 60, MediaStatus: "ready"}}
	now := time.Now().UTC()
	p := operations.TikTokPublication{SubjectID: "1", DramaID: "201", OwnerApplicationID: "101", OwnerScope: operations.PublicationOwnerScope{ClientKey: "fixture-owner-key"}, PlatformAlbumID: source.PlatformAlbumID, PlatformVersion: 8, ReviewID: "official-review", ReviewStatus: 1, Status: operations.PublicationStatusUnderReview, Stage: operations.PublicationStagePollReview, ContentSnapshot: &frozen, CreatedAt: now, UpdatedAt: now}
	model, err := newTikTokPublicationModel(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Omit("subject_id").Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	type unchanged struct {
		PublishedSnapshot, WorkflowJSON []byte
		PublishStatus                   string
	}
	var before, after unchanged
	read := func(target *unchanged) {
		t.Helper()
		if err := db.Table("dramas d").Select("d.published_snapshot, d.publish_status, p.workflow_json").Joins("JOIN tiktok_publications p ON p.drama_id = d.id").Where("d.id = 201").Take(target).Error; err != nil {
			t.Fatal(err)
		}
	}
	read(&before)
	for _, stmt := range strings.Split(migration, ";") {
		if strings.TrimSpace(stmt) != "" {
			if err := db.Exec(stmt).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	read(&after)
	if !bytes.Equal(before.PublishedSnapshot, after.PublishedSnapshot) || !bytes.Equal(before.WorkflowJSON, after.WorkflowJSON) || before.PublishStatus != after.PublishStatus {
		t.Fatal("升级改写了历史快照、审核任务或编辑状态")
	}
	for i, status := range []string{"draft", "ready", "published", "approved", "rejected"} {
		var stored dramaModel
		if err := db.First(&stored, "id = ?", 401+i).Error; err != nil || stored.PublishStatus != status {
			t.Fatal("升级重置已有编辑状态或自动发号", err)
		}
	}
	if err := db.Exec(string(readDeploymentSQL(t, "migrations/20261105_001_subjects.sql"))).Error; err != nil {
		t.Fatal(err)
	}
	drama, err := f.store.FindDrama(ctx, "201")
	if err != nil || drama.PublishStatus != "publishing" || drama.Published == nil {
		t.Fatal("历史线上内容被转换", err)
	}
	for _, app := range []string{"101", "102"} {
		online, ok := drama.PublishedForApplication(app)
		if !ok || online.Title != source.Title || online.PlatformAlbumID != source.PlatformAlbumID || online.PlatformVersion != 7 || online.Episodes[0].PlatformEpisodeID != "official-episode-1" {
			t.Fatal("已有应用无法继续消费官方内容")
		}
	}
	if _, ok := drama.PublishedForApplication("103"); ok {
		t.Fatal("升级扩大了官方授权")
	}
	task, err := f.store.FindTikTokPublicationByDramaID(ctx, "201")
	if err != nil || task.ID != model.ID || task.ContentSnapshot.Title != "历史审核" || task.ReviewID != p.ReviewID || task.Status != p.Status {
		t.Fatal("审核任务身份或内容发生变化", err)
	}
	versions, err := f.store.listDirectPublicationVersions(ctx, "201")
	if err != nil || len(versions) != 0 {
		t.Fatal("升级回填了历史共享版本", err)
	}
	if err := f.store.DeleteDrama(ctx, "201"); err != operations.ErrConflict {
		t.Fatal("已有上线内容或审核任务允许删除", err)
	}
	// 后续编辑只改变草稿，即使移除官方任务分集也不能回收其媒资。
	drama.Title, drama.CoverURL = "下一批草稿", "https://example.test/new.png"
	drama.Episodes = []consumer.Episode{{ID: "303", DramaID: "201", Number: 1, Title: "新第一集", VID: "new-vid", MediaSpaceName: "new-space", DurationSeconds: 60, MediaStatus: "ready"}}
	if err := f.store.SaveDramaAggregate(ctx, drama, nil); err != nil {
		t.Fatal(err)
	}
	var retained int64
	if err := db.Model(&episodeModel{}).Where("id IN ? AND is_draft = false", []string{"301", "302"}).Count(&retained).Error; err != nil || retained != 2 {
		t.Fatal("历史官方分集身份被草稿删除", err)
	}
	for _, item := range []struct {
		class, id, space string
		used             bool
	}{
		{operations.MediaResourceVideo, "online-vid", "online-space", true},
		{operations.MediaResourceMaterial, "online-cover", "online-space", true},
		{operations.MediaResourceVideo, "review-vid", "review-space", true},
		{operations.MediaResourceMaterial, "review-cover", "review-space", true},
		{operations.MediaResourceVideo, "review-vid", "other-space", false},
		{operations.MediaResourceMaterial, "review-cover", "other-space", false},
		{operations.MediaResourceVideo, "unused-vid", "review-space", false},
	} {
		used, err := f.store.IsMediaResourceUsed(ctx, operations.MediaJob{ResourceClass: item.class, RemoteVID: item.id, SpaceName: item.space})
		if err != nil || used != item.used {
			t.Fatal("历史内容媒资保护或空间隔离错误", item.class, item.id, err)
		}
	}
	ready, err := f.store.PrepareDrama(ctx, "201", "fixture")
	if err != nil {
		t.Fatal("首次就绪提前生成版本", err)
	}
	again, err := f.store.PrepareDrama(ctx, "201", "fixture")
	if err != nil || again.PublishStatus != ready.PublishStatus {
		t.Fatal("重复确认重新发号", err)
	}
	if err := db.Table("tiktok_drama_states ss").Select("ss.published_snapshot,d.publish_status,p.workflow_json").Joins("JOIN dramas d ON d.id=ss.drama_id").Joins("JOIN tiktok_publications p ON p.drama_id=d.id AND p.subject_id=ss.subject_id").Where("ss.subject_id=1 AND ss.drama_id=201").Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.PublishedSnapshot, after.PublishedSnapshot) || !bytes.Equal(before.WorkflowJSON, after.WorkflowJSON) {
		t.Fatal("显式确认新版本改写了旧官方内容")
	}
	// 使用真实业务服务和 Worker 推进原任务，不需要给旧任务补建版本。
	svc := operations.NewService(operations.Repositories{Application: operations.ApplicationPorts{Subjects: f.store, Registry: f.store}, Content: operations.ContentPorts{OwnerApplication: f.store.FindApplication, Catalog: f.store, Publications: f.store}}, nil)
	provider := &retainedPublicationProvider{}
	if err := svc.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, operations.PublicationJobConfig{PollInterval: time.Millisecond, LeaseDuration: time.Second, WorkerID: "retained-task-test"}); err != nil {
		t.Fatal(err)
	}
	storedTask, err := f.store.FindTikTokPublicationByDramaID(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	storedTask.EpisodeIDsBySequence = map[int]string{}
	for _, ep := range storedTask.ContentSnapshot.Episodes {
		storedTask.EpisodeIDsBySequence[ep.Number] = ep.PlatformEpisodeID
	}
	if err := f.store.UpdateTikTokPublication(ctx, storedTask, nil); err != nil {
		t.Fatal(err)
	}
	approved, err := svc.RefreshTikTokReview(ctx, "201")
	if err != nil || approved.Status != operations.PublicationStatusApproved || approved.ID != model.ID {
		t.Fatal("已有审核任务依赖共享版本", err)
	}
	if _, err := svc.PublishTikTokPublication(ctx, "201", model.ID); err != nil {
		t.Fatal(err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	if err := svc.StartTikTokPublicationWorker(workerCtx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		stopCtx, stop := context.WithTimeout(ctx, time.Second)
		defer stop()
		if err := svc.WaitTikTokPublicationWorker(stopCtx); err != nil {
			t.Error(err)
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		task, err = f.store.FindTikTokPublicationByDramaID(ctx, "201")
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == operations.PublicationStatusOnline {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("原任务未完成上线", task.Status, task.Stage, task.ErrorCode)
		}
		time.Sleep(10 * time.Millisecond)
	}
	drama, err = f.store.FindDrama(ctx, "201")
	if err != nil || drama.Title != "下一批草稿" || drama.PublishStatus != "ready" || drama.Published == nil || drama.Published.Title != "历史审核" || drama.Published.PlatformVersion != 8 || task.ReviewID != p.ReviewID {
		t.Fatal("旧任务上线污染了新版本或改变官方身份", err)
	}
	operation, err := NewDirectRepository(f.store).Accept(ctx, "201", directplay.Command{Kind: "publish", RequestID: "first-current-direct"}, "fixture")
	if err != nil || operation.ResultVersionID == "" {
		t.Fatal("首次直连上架未创建版本", err)
	}
	versions, err = f.store.listDirectPublicationVersions(ctx, "201")
	if err != nil || len(versions) != 1 || versions[0].Number != 1 || versions[0].Snapshot.Title != "下一批草稿" {
		t.Fatal("发布回填了 TK 历史或没有生成 V1", err)
	}

}

// retainedPublicationProvider 只实现旧任务继续审核和上线需要的官方边界，全部调用留在测试进程。
type retainedPublicationProvider struct {
	operations.TikTokMediaProvider
	media officialmedia.Capabilities
}

// QueryAlbum 固定返回旧任务审核通过，不调用真实平台。
func (*retainedPublicationProvider) QueryAlbum(context.Context, string, int) (operations.AlbumObservation, error) {
	return operations.AlbumObservation{ReviewStatus: 2}, nil
}

// SetOnlineVersion 模拟官方接受已审核的平台版本。
func (*retainedPublicationProvider) SetOnlineVersion(context.Context, string, int) error { return nil }

// SetAlbumListed 模拟原任务上架，禁止测试触发真实外部写入。
func (*retainedPublicationProvider) SetAlbumListed(context.Context, string, bool) error { return nil }

// StartTranscode 模拟平台已受理，实际完成由后续异步查询确认。
func (p *retainedPublicationProvider) StartTranscode(ctx context.Context, input operations.TikTokTranscodeInput) (string, error) {
	return p.media.StartTranscode(ctx, input)
}

// QueryTranscode 仅在测试需要完成处理时返回之前受理的本地任务。
func (p *retainedPublicationProvider) QueryTranscode(ctx context.Context, id, account string) (operations.TikTokTranscodeObservation, error) {
	return p.media.QueryTranscode(ctx, id, account)
}
