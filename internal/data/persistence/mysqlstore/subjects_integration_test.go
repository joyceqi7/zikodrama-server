package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	biz "drama-server/internal/biz"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/subject"
	"drama-server/internal/biz/visitor"
)

// preSubjectResolver 模拟已部署版本签发身份时的应用视图，不读取尚未增加的 subject_id。
type preSubjectResolver struct{}

// Resolve 返回迁移前既有直连应用及测试换码器，原身份不包含主体字段。
func (preSubjectResolver) Resolve(context.Context, string) (visitor.Application, visitor.IdentityProvider, error) {
	return visitor.Application{ID: "101", TikTokAppID: "0009007199254740993", PlaybackProvider: "byteplus_web", TimeZone: "Asia/Shanghai"}, iaaIdentity{}, nil
}

// TestSubjectUpgradeIntegration 验证旧直连应用只补归属，原身份、播放通道和全局 VOD 保持不变。
func TestSubjectUpgradeIntegration(t *testing.T) {
	migration := string(readDeploymentSQL(t, "migrations/20261105_001_subjects.sql"))
	parts := strings.SplitN(string(readDeploymentSchema(t)), migration, 2)
	oldSchema := parts[0]
	// 旧库按顺序执行主体及其后的所有增量，新版运行模型才能读取新增应用字段。
	upgrade := migration + parts[1]
	f := newIAAFixtureWithSchema(t, oldSchema, false)
	db := f.store.data.DB()
	if err := db.Exec(`INSERT INTO applications(id,name,tiktok_app_id,client_key,client_secret,region_code,time_zone,compliance_base_url,ad_units_json,status) VALUES(101,'线上直连','0009007199254740993','existing-client','existing-secret','GLOBAL','Asia/Shanghai','',JSON_OBJECT('rewardedVideo',JSON_ARRAY('rewarded'),'interstitial',JSON_ARRAY('interstitial')),'active')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO application_playback_profiles(application_id,provider,revision) VALUES(101,'byteplus_web',7)`).Error; err != nil {
		t.Fatal(err)
	}
	// 使用旧版应用视图在真实用户分片中先签发凭据，并建立已有直连版本指针。
	visitorConfig := visitor.Config{RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "subject-upgrade-test-identity-secret"}
	beforeVisitors, err := visitor.NewService(preSubjectResolver{}, f.store, visitorConfig)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := beforeVisitors.Bootstrap(t.Context(), visitor.BootstrapInput{TikTokAppID: "0009007199254740993", LoginCode: "user-a"})
	if err != nil {
		t.Fatal(err)
	}
	draft := domain.Drama{ID: "201", Title: "已有直连短剧", Description: "描述", CoverURL: "https://fixture.test/cover.jpg", Language: "en", Status: domain.DramaStatusCompleted, Episodes: []domain.Episode{{ID: "301", DramaID: "201", Number: 1, VID: "existing-vid", MediaStatus: "ready", MediaSpaceName: "shared-space"}}}
	seedPreSubjectDrama(t, f, draft)
	// 旧直连版本没有主体列，显式按旧结构写入，不经过新版仓储。
	rawSnapshot, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO direct_publication_versions(id,drama_id,version_number,source_digest,snapshot_json,created_by) VALUES(501,201,1,?,?, 'fixture')", directplay.Digest(draft), rawSnapshot).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO direct_publications(drama_id,status,current_version_id,revision) VALUES(201,'published',501,1)").Error; err != nil {
		t.Fatal(err)
	}
	directRepo := NewDirectRepository(f.store)

	media := `{"access_key_id":"fixture-ak","secret_access_key":"fixture-sk","vod_app_id":"001","region":"ap-southeast-1","space_name":"shared-space","max_file_bytes":1073741824,"upload_token_ttl_seconds":600,"cover_domain":"https://fixture.test","processing_timeout_seconds":900}`
	if err := f.store.GlobalConfigs().Put(context.Background(), globalconfig.Entry{Key: "media", Value: json.RawMessage(media)}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`UPDATE global_configs SET config_value=JSON_OBJECT('owner_application_id','historical-owner','tiktok_app_id','000123','client_key','publisher-key','client_secret','publisher-secret','byteplus_account_id','000456','byteplus_space_name','shared-space') WHERE config_key='distribution'`).Error; err != nil {
		t.Fatal(err)
	}
	distributionBefore, err := f.store.GlobalConfigs().Get(t.Context(), "distribution")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(upgrade).Error; err != nil {
		t.Fatal(err)
	}
	app, err := f.store.FindApplication(context.Background(), "101")
	if err != nil || app.SubjectID != "1" || app.TikTokAppID != "0009007199254740993" || app.ClientKey != "existing-client" || app.ClientSecret != "existing-secret" || app.PlaybackProvider != "byteplus_web" || app.PlaybackRevision != 7 || app.TimeZone != "Asia/Shanghai" {
		t.Fatal("迁移改变线上直连应用配置", err)
	}
	row, err := f.store.FindSubject(context.Background(), app.SubjectID)
	if err != nil || row.OwnerID != "historical-owner" || row.ClientSecret != "publisher-secret" {
		t.Fatal("迁移丢失历史发布归属", err)
	}
	stored, err := f.store.GlobalConfigs().Get(context.Background(), "media")
	if err != nil || !strings.Contains(string(stored.Value), "000456") || !strings.Contains(string(stored.Value), "fixture-ak") {
		t.Fatal("迁移丢失共享 VOD", err)
	}
	var count int64
	// 单主体旧库仅执行 SQL 即可恢复媒资配置，不依赖历史拆分命令或新标识。
	if err := db.Table("subjects").Count(&count).Error; err != nil || count != 1 {
		t.Fatal("迁移未保留唯一初始化主体", err)
	}
	mediaConfig, err := globalconfig.NewService(f.store.GlobalConfigs()).Media(t.Context())
	if err != nil || mediaConfig == nil || mediaConfig.SpaceName != "shared-space" || mediaConfig.VODAppID != "001" {
		t.Fatal("SQL 升级后的媒资配置不能通过启动校验", err)
	}
	for _, table := range []string{"dramas", "episodes"} {
		if err := db.Table(table).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("单主体升级改变了原有剧集数量：%s", table)
		}
	}
	distributionAfter, err := f.store.GlobalConfigs().Get(t.Context(), "distribution")
	if err != nil || string(distributionAfter.Value) != string(distributionBefore.Value) {
		t.Fatal("主体升级删除或改写了原全局发布方配置", err)
	}
	resolver, err := biz.NewApplicationResolver(f.store, func(application.Application) visitor.IdentityProvider { return iaaIdentity{} })
	if err != nil {
		t.Fatal(err)
	}
	afterVisitors, err := visitor.NewService(resolver, f.store, visitorConfig)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := afterVisitors.Resolve(t.Context(), identity.VisitorToken)
	if err != nil || restored.VisitorID != identity.VisitorID || restored.BucketID != identity.BucketID || restored.VisitorToken != identity.VisitorToken || restored.SubjectID != app.SubjectID {
		t.Fatal("迁移使已有凭据或用户身份失效", err)
	}
	workbench, err := directRepo.Workbench(application.WithSubject(t.Context(), app.SubjectID), "201")
	if err != nil || workbench.Publication.CurrentVersionID != "501" {
		t.Fatal("迁移改变直连线上指针", err)
	}
	// 旧代码省略主体字段时直接写入默认主体，无需后续回填任务。
	for _, statement := range []string{
		"INSERT INTO dramas(id,title,description,cover_url,status,tag_ids_json,publish_status,operation_note) VALUES(202,'旧代码新建剧','','','updating',JSON_ARRAY(),'draft','')",
		"INSERT INTO media_jobs(id,kind,drama_id,public_url,file_name,file_size,status,created_at,updated_at,next_attempt_at) VALUES(502,'cover',202,'','old-write.png',100,'succeeded',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal("升级后旧代码省略主体的写入失败", err)
		}
	}
	for table, id := range map[string]string{"dramas": "202", "media_jobs": "502"} {
		if err := db.Table(table).Where("id=? AND subject_id=1", id).Count(&count).Error; err != nil || count != 1 {
			t.Fatal("旧代码新建记录未直接归属默认主体", table, err)
		}
	}
	// 无发布方配置的新主体不继承默认主体的直连目录，仍可独立配置直连播放。
	b, err := subject.NewService(f.store).Save(t.Context(), "", subject.Input{Name: "仅直连主体"})
	if err != nil {
		t.Fatal(err)
	}
	other := application.Application{SubjectID: b.ID, TikTokAppID: "000102", ClientKey: "direct-other", Name: "另一个直连应用", Status: "active", MonetizationMode: "IAP", PlaybackProvider: "byteplus_web", TimeZone: "UTC"}
	if err := f.store.CreateApplication(t.Context(), &other); err != nil {
		t.Fatal(err)
	}
	for _, current := range []application.Application{app, other} {
		page, err := f.store.DiscoverDramas(application.ContextForApplication(t.Context(), current), domain.DiscoveryQuery{Limit: 20})
		expected := 1
		if current.SubjectID != app.SubjectID {
			expected = 0
		}
		if err != nil || len(page.Items) != expected {
			t.Fatal("旧目录未保留或新主体继承了目录", err)
		}
	}
}

// TestSubjectPublicationIsolationIntegration 覆盖同剧双主体的版本、上线投影及消费者目录，平台调用使用既有存储端口替代。
func TestSubjectPublicationIsolationIntegration(t *testing.T) {
	f := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	ctx := context.Background()
	service := subject.NewService(f.store)
	rows, err := service.List(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatal("默认主体缺失", err)
	}
	a, err := service.Save(ctx, rows[0].ID, subject.Input{Name: "主体 A", TikTokAppID: "00011", ClientKey: "publisher-A", ClientSecret: "secret-A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.Save(ctx, "", subject.Input{Name: "主体 B", TikTokAppID: "00022", ClientKey: "publisher-B", ClientSecret: "secret-B"})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, subjectID, key string }{{"101", a.ID, "publisher-A"}, {"102", b.ID, "publisher-B"}} {
		app := application.Application{SubjectID: row.subjectID, ID: row.id, TikTokAppID: row.id, ClientKey: row.key, Name: row.id, Status: "active", MonetizationMode: application.MonetizationModeIAP, TimeZone: "UTC"}
		if err := f.store.CreateApplication(ctx, &app); err != nil {
			t.Fatal(err)
		}
	}
	draft := domain.Drama{ID: "201", Title: "共享草稿", CoverURL: "https://fixture.test/cover.jpg", Description: "描述", Language: "en", Status: domain.DramaStatusCompleted, Year: 2026, Episodes: []domain.Episode{{ID: "301", DramaID: "201", Number: 1, Title: "第1集", VID: "shared-vid", MediaSpaceName: "shared-space", MediaStatus: "ready", DurationSeconds: 60}}}
	if err := f.store.SaveDrama(application.WithSubject(ctx, a.ID), draft); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SaveEpisode(application.WithSubject(ctx, a.ID), draft.Episodes[0]); err != nil {
		t.Fatal(err)
	}
	bDraft := draft
	bDraft.ID = "202"
	bDraft.Title = "主体 B 草稿"
	bDraft.Episodes = append([]domain.Episode(nil), draft.Episodes...)
	bDraft.Episodes[0].ID = "302"
	bDraft.Episodes[0].DramaID = "202"
	bDraft.Episodes[0].VID = "subject-b-vid"
	bDraft.Episodes[0].MediaSpaceName = "subject-b-space"
	if err := f.store.SaveDrama(application.WithSubject(ctx, b.ID), bDraft); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SaveEpisode(application.WithSubject(ctx, b.ID), bDraft.Episodes[0]); err != nil {
		t.Fatal(err)
	}
	dramaFor := func(scope context.Context) string {
		if application.SubjectIDFromContext(scope) == b.ID {
			return "202"
		}
		return "201"
	}
	start := func(subjectID, album string) operations.TikTokPublication {
		t.Helper()
		scope := application.WithSubject(ctx, subjectID)
		frozen, err := f.store.FindDrama(scope, dramaFor(scope))
		if err != nil {
			t.Fatal(err)
		}
		snapshot := domain.FreezeContent(*frozen)
		owner, _ := f.store.FindSubject(ctx, subjectID)
		now := time.Now().UTC()
		p := operations.TikTokPublication{SubjectID: subjectID, DramaID: dramaFor(scope), OwnerApplicationID: owner.OwnerID, OwnerScope: operations.PublicationOwnerScope{ClientKey: owner.ClientKey, AccountID: "shared-account", SpaceName: "shared-space"}, ContentSnapshot: &snapshot, PlatformAlbumID: album, PlatformVersion: 1, Status: operations.PublicationStatusSynced, Stage: operations.PublicationStageAwaitSubmitReview, CreatedAt: now, UpdatedAt: now}
		if err := f.store.StartTikTokPublication(scope, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	pa, pb := start(a.ID, "album-A"), start(b.ID, "album-B")
	ca := application.WithSubject(application.WithContext(ctx, "101"), a.ID)
	cb := application.WithSubject(application.WithContext(ctx, "102"), b.ID)
	if latest, err := f.store.FindTikTokPublicationByDramaID(ca, "201"); err != nil || latest.ID != pa.ID {
		t.Fatal("另一主体覆盖最新任务", err)
	}
	list := func(scope context.Context, p operations.TikTokPublication, episodeID string) {
		t.Helper()
		online := *p.ContentSnapshot
		online.PublishStatus = "published"
		if episodeID == "episode-A" {
			online.TagIDs = []string{"21"}
		} else {
			online.TagIDs = []string{"22"}
		}
		online.PlatformAlbumID = p.PlatformAlbumID
		online.PlatformVersion = 1
		online.Episodes = append([]domain.Episode(nil), online.Episodes...)
		online.Episodes[0].PlatformEpisodeID = episodeID
		container := *p.ContentSnapshot
		container.PlatformAlbumID = p.PlatformAlbumID
		container.Published = &online
		now := time.Now().UTC()
		p.Status = operations.PublicationStatusOnline
		p.Stage = operations.PublicationStageDone
		p.PublishRequested = true
		p.OnlineAt = &now
		p.UpdatedAt = now
		if err := f.store.UpdateTikTokPublication(scope, p, &container); err != nil {
			t.Fatal(err)
		}
	}
	list(ca, pa, "episode-A")
	if drama, err := f.store.FindDrama(cb, "202"); err != nil || drama.Published != nil {
		t.Fatal("未上线主体读取其他主体结果", err)
	}
	list(cb, pb, "episode-B")
	for _, expected := range []struct {
		scope          context.Context
		album, episode string
	}{{ca, "album-A", "episode-A"}, {cb, "album-B", "episode-B"}} {
		drama, err := f.store.FindDrama(expected.scope, dramaFor(expected.scope))
		if err != nil || drama.Published == nil || drama.Published.PlatformAlbumID != expected.album || drama.Published.Episodes[0].PlatformEpisodeID != expected.episode {
			t.Fatal("主体线上投影串用", err)
		}
		page, err := f.store.DiscoverDramas(expected.scope, domain.DiscoveryQuery{Limit: 20})
		if err != nil || len(page.Items) != 1 {
			t.Fatal("现有消费者目录未取得正确主体上线内容", err)
		}
	}
	if _, err := service.Save(ctx, a.ID, subject.Input{Name: "主体 A", TikTokAppID: "00099", ClientKey: "another-publisher", ClientSecret: "new-secret"}); err == nil {
		t.Fatal("已有资产仍可更换发布身份")
	}
	for _, scope := range []context.Context{ca, cb} {
		page, err := f.store.ListPublicationRecords(scope, operations.PublicationRecordQuery{Kind: "releases", Page: 1, PageSize: 20})
		if err != nil || page.Total != 1 {
			t.Fatal("上线记录没有按主体分页", err)
		}
		if page.Items[0].Publication.SubjectID != application.SubjectIDFromContext(scope) {
			t.Fatal("记录丢失固定主体")
		}
	}
	called := false
	if err := f.store.ApplyDistribution(ca, "201", "102", true, false, func(context.Context, operations.DramaDistribution) error { called = true; return nil }); !errors.Is(err, operations.ErrConflict) || called {
		t.Fatal("跨主体授权仍调用平台", err)
	}
	// 官方推荐也按主体分开；B 的推荐不能阻止 A 下架，也不能继承 A 的修订。
	home := homepage.NewService(f.store, f.store, f.store)
	for _, entry := range []struct {
		scope context.Context
		title string
	}{{ca, "主体 A 官方推荐"}, {cb, "主体 B 官方推荐"}} {
		for _, save := range []func(context.Context, homepage.Carousel) (homepage.CarouselView, error){home.SaveCarousel, home.SaveHot} {
			if _, err := save(homepage.WithManagementContext(entry.scope), homepage.Carousel{DramaIDs: []string{dramaFor(entry.scope)}, Items: []homepage.CarouselItem{{DramaID: dramaFor(entry.scope), Title: entry.title, Visible: true}}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, read := range []func(context.Context) (homepage.Carousel, error){f.store.GetCarousel, f.store.GetHot} {
		ac, err := read(ca)
		if err != nil || ac.Revision != 1 || ac.Items[0].Title != "主体 A 官方推荐" {
			t.Fatal("官方推荐串主体", err)
		}
		bc, err := read(cb)
		if err != nil || bc.Revision != 1 || bc.Items[0].Title != "主体 B 官方推荐" {
			t.Fatal("官方推荐串主体", err)
		}
	}
	ac, _ := f.store.GetCarousel(ca)
	if _, err := f.store.ReplaceCarousel(ca, homepage.Carousel{Revision: ac.Revision, DramaIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	ah, _ := f.store.GetHot(ca)
	if _, err := f.store.ReplaceHot(ca, homepage.Carousel{Revision: ah.Revision, DramaIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CheckTikTokUnpublish(ca, "201"); err != nil {
		t.Fatal("其他主体的官方推荐阻止下架", err)
	}
	current, err := f.store.FindTikTokPublicationByDramaID(ca, "201")
	if err != nil {
		t.Fatal(err)
	}
	current.Status, current.Stage = operations.PublicationStatusOffline, operations.PublicationStageDone
	container := *current.ContentSnapshot
	container.PlatformAlbumID = current.PlatformAlbumID
	if err := f.store.UpdateTikTokPublication(ca, current, &container); err != nil {
		t.Fatal(err)
	}
	rankings, err := f.store.GetRankings(ca)
	if err != nil || len(rankings["21"].Items) != 0 || len(rankings["22"].Items) != 0 {
		t.Fatal("当前主体分类混入其他主体", err)
	}
	otherRankings, err := f.store.GetRankings(cb)
	if err != nil || len(otherRankings["22"].Items) != 1 {
		t.Fatal("下架一主体误删另一主体分类成员", err)
	}

	if err := f.store.DeleteDrama(ca, "201"); err != nil {
		t.Fatal("另一主体线上剧阻断了本主体删除", err)
	}
	page, err := f.store.ListDramaPage(ca, operations.DramaListQuery{Page: 1, PageSize: 20})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("删除没有从本主体列表生效", err)
	}
}

// TestSubjectPublisherIdentityBoundariesIntegration 验证发布身份唯一、并发旧表单不能借用其他身份密钥，以及只有 Album 仍冻结主体身份。
func TestSubjectPublisherIdentityBoundariesIntegration(t *testing.T) {
	f := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	ctx := t.Context()
	service := subject.NewService(f.store)
	a, err := service.Save(ctx, "", subject.Input{Name: "发布主体 A", TikTokAppID: "000123", ClientKey: "publisher-A", ClientSecret: "secret-A"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Save(ctx, "", subject.Input{Name: "发布主体 B", TikTokAppID: "000123", ClientKey: "publisher-B", ClientSecret: "secret-B"}); !errors.Is(err, subject.ErrConflict) {
		t.Fatal("不同主体共用同一发布方 AppID", err)
	}
	stale := subject.Subject{ID: a.ID, Name: a.Name, TikTokAppID: "000124", ClientKey: "publisher-B"}
	if err := f.store.UpdateSubject(ctx, stale); !errors.Is(err, subject.ErrNotConfigured) {
		t.Fatal("旧表单更换身份时沿用已有密钥", err)
	}
	if err := f.store.data.DB().Create(&tiktokDramaStateModel{SubjectID: a.ID, DramaID: "201", PlatformAlbumID: "existing-album"}).Error; err != nil {
		t.Fatal(err)
	}
	stale.ClientSecret = "secret-B"
	if err := f.store.UpdateSubject(ctx, stale); !errors.Is(err, subject.ErrConflict) {
		t.Fatal("存在官方 Album 但没有任务时仍可换身份", err)
	}
	if _, err := service.Save(ctx, a.ID, subject.Input{Name: a.Name, TikTokAppID: a.TikTokAppID, ClientKey: a.ClientKey, ClientSecret: "rotated-secret"}); err != nil {
		t.Fatal("同身份密钥轮换被拒绝", err)
	}
}

// TestSubjectHistoricalTaskDeleteIntegration 验证删除只受各主体当前任务约束，历史已替换的 synced 不能永久冻结共享草稿。
func TestSubjectHistoricalTaskDeleteIntegration(t *testing.T) {
	f := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	ctx := application.WithSubject(t.Context(), "1")
	b, err := subject.NewService(f.store).Save(ctx, "", subject.Input{Name: "另一个主体"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id          string
		otherActive bool
	}{{"201", false}, {"202", true}} {
		draft := domain.Drama{ID: test.id, Title: "删除门禁 " + test.id, TagIDs: []string{}, PublishStatus: "draft"}
		if err := f.store.SaveDrama(ctx, draft); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		tasks := []operations.TikTokPublication{
			{SubjectID: "1", DramaID: test.id, Status: operations.PublicationStatusSynced, Stage: operations.PublicationStageAwaitSubmitReview, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)},
			{SubjectID: "1", DramaID: test.id, Status: operations.PublicationStatusOffline, Stage: operations.PublicationStageDone, CreatedAt: now, UpdatedAt: now},
		}
		if test.otherActive {
			tasks = append(tasks, operations.TikTokPublication{SubjectID: b.ID, DramaID: test.id, Status: operations.PublicationStatusUnderReview, Stage: operations.PublicationStagePollReview, CreatedAt: now, UpdatedAt: now})
		}
		for _, task := range tasks {
			model, err := newTikTokPublicationModel(task)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.data.DB().Create(&model).Error; err != nil {
				t.Fatal(err)
			}
		}
		page, err := f.store.ListDramaPage(ctx, operations.DramaListQuery{Page: 1, PageSize: 20, Query: draft.Title})
		if err != nil || len(page.Items) != 1 || page.Items[0].DeleteBlocked != test.otherActive {
			t.Fatal("列表没有按各主体当前任务计算删除门禁", err)
		}
		err = f.store.DeleteDrama(ctx, test.id)
		if test.otherActive {
			if !errors.Is(err, operations.ErrConflict) {
				t.Fatal("其他主体在审核仍可删除", err)
			}
		} else if err != nil {
			t.Fatal("历史已替换任务永久阻止删除", err)
		}
	}
}
