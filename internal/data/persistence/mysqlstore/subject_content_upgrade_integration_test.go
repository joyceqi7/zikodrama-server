package mysqlstore

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	biz "drama-server/internal/biz"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/subject"
	"drama-server/internal/biz/visitor"
)

// TestSubjectContentUpgradeIntegration 用真实旧表验证合并脚本保留原 ID、时间、修订、JSON、用户和旧 Mini 凭据。
func TestSubjectContentUpgradeIntegration(t *testing.T) {
	firstMigration := string(readDeploymentSQL(t, "migrations/20261105_001_subjects.sql"))
	oldSchema, _, found := strings.Cut(string(readDeploymentSchema(t)), firstMigration)
	if !found {
		t.Fatal("部署结构缺少主体迁移")
	}
	upgrade := string(readDeploymentSQL(t, "releases/upgrade-subjects-and-mini-review.sql"))
	f := newIAAFixtureWithSchema(t, oldSchema, false)
	ctx := application.WithSubject(t.Context(), "1")
	db := f.store.data.DB()
	app := application.Application{ID: "101", SubjectID: "1", TikTokAppID: "0009007199254740993", Name: "既有直连应用", ClientKey: "old-direct-key", ClientSecret: "old-direct-secret", PlaybackProvider: "byteplus_web", Status: "active", MonetizationMode: "IAP", TimeZone: "Asia/Shanghai"}
	// 按真实旧表写入既有应用，不能让新版仓储提前依赖尚未增加的主体列。
	if err := db.Exec("INSERT INTO applications(id,tiktok_app_id,name,client_key,client_secret,status,region_code,time_zone,compliance_base_url,ad_units_json,monetization_mode) VALUES(?,?,?,?,?,?,'GLOBAL',?,'',JSON_OBJECT(),?)", app.ID, app.TikTokAppID, app.Name, app.ClientKey, app.ClientSecret, app.Status, app.TimeZone, app.MonetizationMode).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO application_playback_profiles(application_id,provider,revision) VALUES(?,'byteplus_web',7)", app.ID).Error; err != nil {
		t.Fatal(err)
	}
	// 已有全局配置逐字段保留，默认主体只复制空间，不覆盖已有账号或密钥。
	mediaValue := json.RawMessage(`{"access_key_id":"fixture-ak","secret_access_key":"fixture-sk","vod_app_id":"001","byteplus_account_id":"000999","region":"ap-southeast-1","space_name":"shared-space","max_file_bytes":1073741824,"upload_token_ttl_seconds":600,"cover_domain":"https://fixture.test","processing_timeout_seconds":900}`)
	if err := f.store.GlobalConfigs().Put(ctx, globalconfig.Entry{Key: "media", Value: mediaValue}); err != nil {
		t.Fatal(err)
	}
	mediaBefore, err := f.store.GlobalConfigs().Get(ctx, "media")
	if err != nil {
		t.Fatal(err)
	}
	draft := consumer.Drama{ID: "201", Title: "迁移前已上线短剧", Description: "历史资料", CoverURL: "https://fixture.test/cover.jpg", Language: "en", Status: consumer.DramaStatusCompleted, TagIDs: []string{"21"}, Episodes: []consumer.Episode{{ID: "301", DramaID: "201", Number: 1, Title: "第一集", VID: "old-vid", MediaStatus: "ready", MediaSpaceName: "shared-space"}}}
	seedPreSubjectDrama(t, f, draft)
	snapshot := consumer.FreezeContent(draft)
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO direct_publication_versions(id,drama_id,version_number,source_digest,snapshot_json,created_by,created_at) VALUES(501,201,3,?,?, 'old-admin','2026-11-01 01:00:00')", consumer.ContentDigest(snapshot), raw).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO direct_publications(id,drama_id,status,current_version_id,revision,updated_by,created_at,updated_at) VALUES(601,201,'published',501,7,'old-admin','2026-11-01 01:00:00','2026-11-01 02:00:00')").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO direct_publication_operations(id,drama_id,request_key,input_digest,command_json,status,result_version_id,created_by,created_at,updated_at) VALUES(701,201,'old-request',REPEAT('a',64),JSON_OBJECT('kind','publish','requestId','old-request','expectedRevision',6),'succeeded',501,'old-admin','2026-11-01 01:00:00','2026-11-01 02:00:00')").Error; err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"homepage_recommendations", "direct_homepage_recommendations"} {
		for _, section := range []string{"carousel", "hot"} {
			if err := db.Exec("INSERT INTO "+table+"(section,revision,config_json,created_at,updated_at) VALUES(?,5,JSON_OBJECT('revision',5,'dramaIds',JSON_ARRAY('201'),'items',JSON_ARRAY(JSON_OBJECT('dramaId','201','title','历史推荐标题','visible',true,'createdAt','2026-11-01T01:00:00Z'))),'2026-11-01 01:00:00','2026-11-01 02:00:00')", section).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, table := range []string{"category_rankings", "direct_category_rankings"} {
		if err := db.Exec("INSERT INTO " + table + "(tag_id,revision,items_json,created_at,updated_at) VALUES('21',9,JSON_ARRAY(JSON_OBJECT('dramaId','201','visible',true)),'2026-11-01 01:00:00','2026-11-01 02:00:00')").Error; err != nil {
			t.Fatal(err)
		}
	}
	// 对旧字段逐行形成摘要；完整业务表另比对校验和，日志不输出任何密钥或连接信息。
	tables := []string{"direct_publications", "direct_publication_versions", "direct_publication_operations", "homepage_recommendations", "direct_homepage_recommendations", "category_rankings", "direct_category_rankings"}
	columns := map[string][]string{}
	before := map[string][]string{}
	for _, table := range tables {
		var names []string
		if err := db.Raw("SELECT COLUMN_NAME FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? ORDER BY ordinal_position", table).Scan(&names).Error; err != nil {
			t.Fatal(err)
		}
		columns[table] = names
		before[table] = subjectUpgradeRowDigests(t, f, table, columns[table])
	}
	visitors, err := visitor.NewService(preSubjectResolver{}, f.store, visitor.Config{RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "local-iaa-integration-secret-only"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := visitors.Bootstrap(ctx, visitor.BootstrapInput{TikTokAppID: app.TikTokAppID, LoginCode: "user-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(upgrade).Error; err != nil {
		t.Fatal(err)
	}
	mediaAfter, err := f.store.GlobalConfigs().Get(ctx, "media")
	if err != nil || string(mediaAfter.Value) != string(mediaBefore.Value) || !mediaAfter.UpdatedAt.Equal(*mediaBefore.UpdatedAt) {
		t.Fatal("主体升级改写了已存在的全局媒资配置", err)
	}
	for _, table := range tables {
		if strings.Join(before[table], "\n") != strings.Join(subjectUpgradeRowDigests(t, f, table, columns[table]), "\n") {
			t.Fatalf("迁移改变了原有字段：%s", table)
		}
		var invalid int64
		if err := db.Table(table).Where("subject_id<>1 OR subject_id IS NULL").Count(&invalid).Error; err != nil || invalid != 0 {
			t.Fatalf("历史数据未归默认主体：%s", table)
		}
	}
	resolver, err := biz.NewApplicationResolver(f.store, func(application.Application) visitor.IdentityProvider { return iaaIdentity{} })
	if err != nil {
		t.Fatal(err)
	}
	visitors, err = visitor.NewService(resolver, f.store, visitor.Config{RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "local-iaa-integration-secret-only"})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := visitors.Resolve(ctx, old.VisitorToken)
	if err != nil || restored.VisitorID != old.VisitorID || restored.BucketID != old.BucketID || restored.ApplicationID != old.ApplicationID {
		t.Fatal("旧凭据身份或分片改变", err)
	}
	actual, err := f.store.FindApplication(ctx, app.ID)
	if err != nil || actual.PlaybackRevision != 7 || actual.ClientSecret != app.ClientSecret || actual.TikTokAppID != app.TikTokAppID {
		t.Fatal("应用身份、密钥或通道修订改变", err)
	}
	direct := application.ContextForApplication(ctx, actual)
	workbench, err := NewDirectRepository(f.store).Workbench(direct, "201")
	if err != nil || workbench.Publication.CurrentVersionID != "501" || workbench.Publication.Revision != 7 || len(workbench.Versions) != 1 || workbench.Versions[0].Number != 3 {
		t.Fatal("历史发布版本或指针丢失", err)
	}
	home := homepage.NewService(f.store, f.store, f.store)
	for _, read := range []func(context.Context) ([]consumer.DramaSummary, error){home.HomeCarousel, home.HomeHot} {
		rows, err := read(direct)
		if err != nil || len(rows) != 1 || rows[0].Title != "历史推荐标题" {
			t.Fatal("原有首页改变", err)
		}
	}
	f.signer = &directHTTPPlayback{}
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	// 继续发送旧请求体、旧 AppID 和旧 VisitorToken，不增加主体字段或头。
	raw = iaaRequest(t, server.URL, "POST", "/api/v1/bootstrap", "", "", visitor.BootstrapInput{TikTokAppID: app.TikTokAppID, VisitorToken: old.VisitorToken, LoginCode: "user-a"}, 200)
	var startup struct {
		VisitorID string                     `json:"visitorId"`
		Playback  consumer.PlaybackSDKConfig `json:"playbackSdk"`
	}
	if err := json.Unmarshal(raw, &startup); err != nil || startup.VisitorID != old.VisitorID || startup.Playback.Provider != "byteplus_web" {
		t.Fatal("旧版启动合同改变", err)
	}
	iaaRequest(t, server.URL, "GET", "/api/v1/open/catalog?tiktokAppId="+app.TikTokAppID, "", "", nil, 200)
	iaaRequest(t, server.URL, "GET", "/api/v1/home", old.VisitorToken, "", nil, 200)
	raw = iaaRequest(t, server.URL, "GET", "/api/v1/dramas/201", old.VisitorToken, "", nil, 200)
	var detail struct {
		ID       string `json:"id"`
		Episodes []struct {
			ID string `json:"id"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil || detail.ID != "201" || len(detail.Episodes) != 1 || detail.Episodes[0].ID != "301" {
		t.Fatal("原链接的详情身份发生变化", err)
	}
	raw = iaaRequest(t, server.URL, "POST", "/api/v1/play/resolve", old.VisitorToken, "", map[string]string{"dramaId": "201", "episodeId": "301"}, 200)
	var playback consumer.PlayResult
	if err := json.Unmarshal(raw, &playback); err != nil || playback.Drama == nil || playback.Drama.ID != "201" || playback.Episode == nil || playback.Episode.ID != "301" {
		t.Fatal("原链接的播放身份发生变化", err)
	}
	other, err := subject.NewService(f.store).Save(ctx, "", subject.Input{Name: "新主体保持空目录"})
	if err != nil {
		t.Fatal(err)
	}
	empty := application.WithSubject(application.WithPlayback(ctx, "byteplus_web"), other.ID)
	page, err := f.store.DiscoverDramas(homepage.WithManagementContext(empty), consumer.DiscoveryQuery{Limit: 20})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("新主体继承默认目录", err)
	}
	config, err := f.store.GetCarousel(empty)
	if err != nil || config.Revision != 0 || len(config.DramaIDs) != 0 {
		t.Fatal("新主体继承默认推荐", err)
	}
}

// subjectUpgradeRowDigests 仅比较旧列，既验证 JSON 和时间不变，也避免在测试输出中泄漏原始业务内容。
func subjectUpgradeRowDigests(t *testing.T, f *iaaFixture, table string, columns []string) []string {
	t.Helper()
	quoted := make([]string, len(columns))
	for i, c := range columns {
		quoted[i] = "`" + c + "`"
	}
	var rows []string
	// 摘要本身提供稳定顺序，也支持 id_allocators 等没有 id 列的业务表。
	if err := f.store.data.DB().Raw("SELECT SHA2(CAST(JSON_ARRAY(" + strings.Join(quoted, ",") + ") AS CHAR),256) FROM `" + table + "` ORDER BY 1").Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}
