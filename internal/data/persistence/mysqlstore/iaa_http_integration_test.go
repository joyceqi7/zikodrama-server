package mysqlstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	biz "drama-server/internal/biz"
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/sharding"
	"drama-server/internal/biz/visitor"
	"drama-server/internal/pkg/apimessages"
	"drama-server/internal/pkg/requesttime"
	consumerhttp "drama-server/internal/service/consumer"
	operationshttp "drama-server/internal/service/operations"
	configv1 "gitee.com/linpbin/styx/api/config/v1"
	styxapierror "gitee.com/linpbin/styx/apierror"
	styxi18n "gitee.com/linpbin/styx/i18n"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// iaaFixture 的所有业务数据来自独立 MySQL；只替换平台换码，不调用任何真实平台。
type iaaFixture struct {
	// operations 仅在发布专项验收中注入已配置的本地平台替身服务，其余场景使用默认组合。
	operations *operations.Service
	// direct 和 signer 只在直连专项测试中替换平台边界。
	direct    *directplay.Service
	signer    domain.DirectPlaybackProvider
	dsn       string
	store     *Store
	uidClient *redis.Client
}

// iaaIdentity 用固定测试码模拟平台用户，真实 Visitor 绑定、加密及认证仍走生产代码。
type iaaIdentity struct{}

// ExchangeCode 只接受明确的测试账号，禁止把任意输入当作成功登录。
func (iaaIdentity) ExchangeCode(_ context.Context, code string) (visitor.Identity, error) {
	switch code {
	case "user-a", "user-b", "user-c", "user-stale-offer":
		return visitor.Identity{OpenID: "fixture-" + code}, nil
	default:
		return visitor.Identity{}, visitor.ErrUnauthorized
	}
}

func newIAAFixture(t *testing.T) *iaaFixture {
	return newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), true)
}

// newIAAFixtureWithSchema 在一次性数据库中执行指定结构，可验证旧库升级；不复用任何开发业务库。
func newIAAFixtureWithSchema(t *testing.T, schemaText string, seed bool) *iaaFixture {
	t.Helper()
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置 TEST_MYSQL_DSN，跳过独立 MySQL 联调")
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = ""
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	server, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	// 名字只由本测试生成；清理仅删除本次创建的库，不复用用户现有数据库。
	name := fmt.Sprintf("drama_iaa_%d", time.Now().UnixNano())
	if _, err := server.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := server.Exec("DROP DATABASE `" + name + "`"); err != nil {
			t.Errorf("清理测试数据库: %v", err)
		}
	})
	cfg.DBName = name
	schema, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close()
	for _, stmt := range strings.Split(schemaText, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := schema.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	f := &iaaFixture{dsn: cfg.FormatDSN()}
	f.open(t)
	cleanupUIDIntegrationState(t, f.store, f.uidClient)
	if seed {
		f.seed(t)
	}
	return f
}

func (f *iaaFixture) open(t *testing.T) {
	t.Helper()
	data, err := OpenData(application.WithSubject(context.Background(), "1"), &configv1.MySQLConfig{Master: f.dsn}, newMySQLIntegrationRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	f.store = NewStore(data)
	if os.Getenv("TEST_REDIS_ADDR") != "" {
		f.uidClient = newUIDIntegrationClient(t)
		f.store = NewStore(data, newUIDIntegrationCache(t, f.uidClient))
	}
}

func (f *iaaFixture) seed(t *testing.T) {
	t.Helper()
	ctx := application.WithSubject(context.Background(), "1")
	for _, id := range []string{"101", "102"} {
		app := application.Application{SubjectID: "1", Name: "IAA Local " + id, Status: application.StatusActive,
			ID: id, TikTokAppID: id, ClientKey: "fixture-" + id, TimeZone: "Asia/Shanghai", RegionCode: "SG",
			ComplianceBaseURL: "https://legal.example", AdUnits: application.AdUnits{RewardedVideo: []string{"fixture-reward"}, Interstitial: []string{"fixture-interstitial"}}}
		if err := f.store.SaveApplication(ctx, app); err != nil {
			t.Fatal(err)
		}
		config := monetization.ApplicationMonetizationConfig{ApplicationID: id, Status: monetization.MonetizationEnabled,
			Strategy: monetization.MonetizationStrategyEpisodeUnlock, FreeEpisodeCount: 1, EpisodeRewardAdUnitCode: "fixture-reward",
			EpisodeReward: monetization.EpisodeRewardPolicy{UnlockEpisodeCount: 2, DailyRewardLimit: 2}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if err := f.store.SaveApplicationMonetization(application.WithContext(ctx, id), config); err != nil {
			t.Fatal(err)
		}
	}
	drama := domain.Drama{ID: "201", Title: "Local IAA Drama", Description: "Real API and MySQL integration fixture", Language: "en", Year: 2026,
		Status: domain.DramaStatusCompleted, PublishStatus: "published", OwnerApplicationID: "101", AuthorizedApplicationIDs: []string{"102"}, PlatformAlbumID: "fixture-album", PlatformVersion: 1}
	for n := 1; n <= 8; n++ {
		drama.Episodes = append(drama.Episodes, domain.Episode{ID: fmt.Sprintf("30%d", n), DramaID: drama.ID, Number: n, Title: fmt.Sprintf("Episode %d", n), MediaStatus: "ready", VID: fmt.Sprintf("fixture-vid-%d", n), PlatformEpisodeID: fmt.Sprintf("fixture-episode-%d", n), DurationSeconds: 60})
	}
	// 这是平台已经确认上线的测试快照，不验证真实平台送审或发布。
	published := drama
	drama.Published = &published
	if err := f.store.SaveDrama(ctx, drama); err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := saveSubjectState(tx, drama.ID, drama.PlatformAlbumID, &published); err != nil {
			return err
		}
		raw, err := json.Marshal(published)
		if err != nil {
			return err
		}
		return syncCategoryRankings(tx, drama.ID, nil, raw)
	}); err != nil {
		t.Fatal(err)
	}
	for _, ep := range drama.Episodes {
		if err := f.store.SaveEpisode(ctx, ep); err != nil {
			t.Fatal(err)
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("LocalIAA123!"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	user := adminrbac.User{Username: "iaa-admin", Nickname: "IAA Local", PasswordHash: string(hash), Status: adminrbac.StatusActive, IsSuper: true, AuthVersion: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := f.store.CreateAdminUser(ctx, &user); err != nil {
		t.Fatal(err)
	}
}

func (f *iaaFixture) handler(t *testing.T, homepageServices ...*homepage.Service) http.Handler {
	t.Helper()
	return f.handlerWithClock(t, time.Now, homepageServices...)
}

// handlerWithClock 在最外层注入同一个可控请求接收时钟，身份认证、管理配置和 MySQL 事务继续使用实际实现。
func (f *iaaFixture) handlerWithClock(t *testing.T, now func() time.Time, homepageServices ...*homepage.Service) http.Handler {
	home := homepage.NewService(f.store, f.store, f.store)
	if len(homepageServices) > 0 {
		home = homepageServices[0]
	}
	t.Helper()
	resolver, err := biz.NewApplicationResolver(f.store, func(application.Application) visitor.IdentityProvider { return iaaIdentity{} })
	if err != nil {
		t.Fatal(err)
	}
	visitors, err := visitor.NewService(resolver, f.store, visitor.Config{RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "local-iaa-integration-secret-only"})
	if err != nil {
		t.Fatal(err)
	}
	consumers := domain.NewService(f.store, now)
	if f.signer != nil {
		consumers.ConfigureDirectPlayback(f.signer)
	}
	globalConfigs := globalconfig.NewService(f.store.GlobalConfigs())
	ops := operations.NewService(operations.Repositories{
		ConsumerUsers: f.store,
		Application:   operations.ApplicationPorts{Subjects: f.store, Registry: f.store}, Monetization: operations.MonetizationPorts{Settings: f.store},
		Campaign: operations.CampaignPorts{Repository: f.store}, Content: operations.ContentPorts{Records: f.store, Tags: f.store, Catalog: f.store, Publications: f.store},
		Reporting: operations.ReportingPorts{Metrics: f.store, Audits: f.store, Ads: f.store, Postbacks: f.store},
		Media:     operations.MediaPorts{Jobs: f.store},
	}, nil)
	if f.operations != nil {
		ops = f.operations
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", consumerhttp.NewHandler(consumers, visitors, biz.NewConsumerBootstrap(visitors, consumers), consumerhttp.Config{Analytics: domain.NewAnalyticsService(f.store), Homepage: home, HealthCheck: f.store.data.pool.PingContext}))
	mux.Handle("/admin/", operationshttp.NewHandler(ops, adminrbac.NewService(f.store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")}), operationshttp.Config{Homepage: home, GlobalConfigs: globalConfigs, Direct: f.direct}))
	messages, err := apimessages.NewMessageBundle()
	if err != nil {
		t.Fatal(err)
	}
	next := styxi18n.HTTPFilter(messages)(styxapierror.HTTPFilter()(mux))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(requesttime.WithReceivedAt(r.Context(), now())))
	})
}

// iaaResponse 只在失败时输出状态码与稳定错误码，避免测试日志泄漏响应中的凭据。
type iaaResponse struct {
	// Data 仅供断言解码成功合同，不在失败日志中输出原文。
	Data json.RawMessage `json:"data"`
	// Reason 用于定位合同错误，不输出服务端响应正文。
	Reason string `json:"reason"`
}

func iaaRequest(t *testing.T, base, method, path, token, app string, input any, want int, alternatives ...int) json.RawMessage {
	t.Helper()
	if path == "/api/v1/bootstrap" {
		requireUIDIntegration(t)
	}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, base+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("x-subject-id", "1")
	}
	if app != "" {
		req.Header.Set("X-Application-ID", app)
	}
	client := http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body iaaResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("%s %s 响应解析失败: %v", method, path, err)
	}
	allowed := res.StatusCode == want || (want == 0 && (res.StatusCode == 200 || res.StatusCode == 201))
	for _, status := range alternatives {
		allowed = allowed || res.StatusCode == status
	}
	if !allowed {
		t.Fatalf("%s %s: HTTP %d，期望 %d，错误码 %s", method, path, res.StatusCode, want, body.Reason)
	}
	return body.Data
}

func iaaLogin(t *testing.T, base, app, user string) (string, string) {
	t.Helper()
	raw := iaaRequest(t, base, "POST", "/api/v1/bootstrap", "", "", visitor.BootstrapInput{TikTokAppID: app, LoginCode: user}, 200)
	var data struct {
		VisitorID string `json:"visitorId"`
		Token     string `json:"visitorToken"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if data.Token == "" || data.VisitorID == "" {
		t.Fatal("登录未返回完整身份")
	}
	return data.Token, data.VisitorID
}

func iaaPlay(t *testing.T, base, token string, episode int) domain.PlayResult {
	t.Helper()
	raw := iaaRequest(t, base, "POST", "/api/v1/play/resolve", token, "", map[string]string{"dramaId": "201", "episodeId": fmt.Sprintf("30%d", episode)}, 200)
	var result domain.PlayResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func iaaClaim(play domain.PlayResult, id string) monetization.ClaimRewardInput {
	return monetization.ClaimRewardInput{ClaimID: "iaa-claim-" + id, OfferID: play.Offer.ID, DramaID: "201", EpisodeID: play.Episode.ID, CompletedAdUnitID: play.Offer.AdUnitID, IsEnded: true}
}

func (f *iaaFixture) count(t *testing.T, table, where string, args ...any) int {
	t.Helper()
	if table == "entitlements" {
		table = "entitlement_grants"
	}
	sharded := table == "entitlement_grants" || table == "reward_grants" || table == "ad_shows" || table == "users" || table == "application_vip_progress" || table == "user_entitlement_state"
	if !sharded {
		var n int
		if err := f.store.data.pool.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+where, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	total := 0
	for shard := 0; shard < 16; shard++ {
		var n int
		physical := fmt.Sprintf("%s_%02d", table, shard)
		if err := f.store.data.pool.QueryRow("SELECT COUNT(*) FROM "+physical+" WHERE "+where, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		total += n
	}
	return total
}

// userShardFor 在集成数据库中定位测试用户所在物理分片。
func (f *iaaFixture) userShardFor(t *testing.T, userID string) int {
	t.Helper()
	for shard := 0; shard < 16; shard++ {
		var count int
		if err := f.store.data.pool.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM users_%02d WHERE id = ?", shard), userID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			return shard
		}
	}
	t.Fatalf("测试用户不存在: %s", userID)
	return 0
}

// userBucketFor 读取测试用户随身份保存的稳定逻辑桶。
func (f *iaaFixture) userBucketFor(t *testing.T, userID string) uint16 {
	t.Helper()
	shard := f.userShardFor(t, userID)
	var bucket uint16
	if err := f.store.data.pool.QueryRow(fmt.Sprintf("SELECT bucket_id FROM users_%02d WHERE id = ?", shard), userID).Scan(&bucket); err != nil {
		t.Fatal(err)
	}
	return bucket
}

// TestIAAHTTPIntegration 通过真实 Handler、身份、业务与 MySQL 验证 IAA 结算和配置联动。
func TestIAAHTTPIntegration(t *testing.T) {
	rootT := t
	f := newIAAFixture(t)
	server := httptest.NewServer(f.handler(t))
	defer func() { server.Close() }()
	token, user := iaaLogin(t, server.URL, "101", "user-a")
	other, _ := iaaLogin(t, server.URL, "101", "user-b")
	otherApp, _ := iaaLogin(t, server.URL, "102", "user-a")
	if got := iaaPlay(t, server.URL, token, 1); got.Status != domain.PlayStatusPlayable {
		t.Fatal("免费集不可播放")
	}
	locked := iaaPlay(t, server.URL, token, 2)
	if locked.Status != domain.PlayStatusAccessRequired || locked.Offer == nil {
		t.Fatal("付费范围未提供逐集广告 Offer")
	}
	claim := iaaClaim(locked, "concurrent-claim")
	t.Run("未完整观看不能结算", func(t *testing.T) {
		cancelled := claim
		cancelled.IsEnded = false
		iaaRequest(t, server.URL, "POST", "/api/v1/reward-grants", token, "", cancelled, 409)
		if f.count(t, "reward_grants", "visitor_id = ?", user) != 0 {
			t.Fatal("取消广告错误发奖")
		}
	})
	t.Run("八次并发重试只入账一次", func(t *testing.T) {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				iaaRequest(t, server.URL, "POST", "/api/v1/reward-grants", token, "", claim, 0)
			}()
		}
		close(start)
		wg.Wait()
		if f.count(t, "reward_grants", "visitor_id = ?", user) != 1 || f.count(t, "entitlements", "visitor_id = ?", user) != 1 || f.unlockedEpisodeCount(t, user) != 2 {
			t.Fatalf("并发记录数: 奖励=%d 权益=%d", f.count(t, "reward_grants", "visitor_id = ?", user), f.count(t, "entitlements", "visitor_id = ?", user))
		}
	})
	t.Run("奖励快照引用实际自增权益", func(t *testing.T) {
		ctx := sharding.WithBucketID(application.WithContext(application.WithSubject(context.Background(), "1"), "101"), f.userBucketFor(t, user))
		grant, found, err := f.store.FindRewardGrant(ctx, user, claim.ClaimID)
		if err != nil || !found || len(grant.Entitlements) == 0 {
			t.Fatalf("无法恢复奖励权益快照: %v", err)
		}
		for _, e := range grant.Entitlements {
			if e.ID == "" || f.count(t, "entitlements", "id = ? AND visitor_id = ?", e.ID, user) != 1 {
				t.Fatal("奖励快照未引用已入账权益")
			}
		}
	})
	t.Run("身份隔离和重新登录", func(t *testing.T) {
		again, id := iaaLogin(t, server.URL, "101", "user-a")
		if id != user || iaaPlay(t, server.URL, again, 3).Status != domain.PlayStatusPlayable {
			t.Fatal("重新登录丢失权益")
		}
		for _, credential := range []string{other, otherApp} {
			if iaaPlay(t, server.URL, credential, 2).Status != domain.PlayStatusAccessRequired {
				t.Fatal("跨账号或应用泄漏权益")
			}
		}
		iaaRequest(t, server.URL, "POST", "/api/v1/reward-grants", otherApp, "", claim, 409)
	})
	t.Run("管理设置和旧Offer按最新规则结算", func(t *testing.T) {
		raw := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
		var login adminrbac.LoginResult
		if err := json.Unmarshal(raw, &login); err != nil {
			t.Fatal(err)
		}
		staleUser, _ := iaaLogin(t, server.URL, "101", "user-stale-offer")
		stale := iaaClaim(iaaPlay(t, server.URL, staleUser, 4), "stale-offer")
		count, limit := 3, 1
		input := operations.ApplicationMonetizationInput{FreeEpisodeCount: 2, EpisodeUnlockAdUnitCode: "fixture-reward", UnlockEpisodeCount: &count, DailyRewardLimit: &limit}
		raw = iaaRequest(t, server.URL, "PUT", "/admin/v1/application-monetization", login.Token, "101", input, 200)
		var view operations.ApplicationMonetizationView
		if err := json.Unmarshal(raw, &view); err != nil {
			t.Fatal(err)
		}
		if view.UnlockEpisodeCount != 3 || view.DailyRewardLimit != 1 {
			t.Fatal("管理端没有保存奖励阈值")
		}
		if iaaPlay(t, server.URL, other, 2).Status != domain.PlayStatusPlayable {
			t.Fatal("免费范围更新未生效")
		}
		iaaRequest(t, server.URL, "POST", "/api/v1/reward-grants", staleUser, "", stale, 201)
		for _, ep := range []int{4, 5, 6} {
			if iaaPlay(t, server.URL, staleUser, ep).Status != domain.PlayStatusPlayable {
				t.Fatalf("旧入口应按最新规则授予第 %d 集", ep)
			}
		}
		// 已成功领取的旧 Offer 在配置变更后仍须能恢复原结算。
		iaaRequest(t, server.URL, "POST", "/api/v1/reward-grants", token, "", claim, 200)
		fresh := iaaClaim(iaaPlay(t, server.URL, other, 3), "configured-count")
		iaaRequest(t, server.URL, "POST", "/api/v1/reward-grants", other, "", fresh, 201)
		for _, ep := range []int{3, 4, 5} {
			if iaaPlay(t, server.URL, other, ep).Status != domain.PlayStatusPlayable {
				t.Fatalf("配置的解锁集数未覆盖第 %d 集", ep)
			}
		}
		limited := iaaPlay(t, server.URL, other, 6)
		if limited.Offer != nil || len(limited.Offers) != 0 {
			t.Fatal("达到日限额后仍返回广告入口")
		}
		// 已缓存的旧入口仍须由事务兜底拒绝，不能只依赖前端隐藏。
		next := fresh
		next.ClaimID, next.EpisodeID = "over-daily-limit", "306"
		raw = iaaRequest(t, server.URL, "POST", "/api/v1/reward-grants", other, "", next, 429)
		if iaaPlay(t, server.URL, other, 6).Status == domain.PlayStatusPlayable {
			t.Fatal("每日上限未阻止超额解锁")
		}

	})

	t.Run("不同领取键并发也不能突破每日上限", func(t *testing.T) {
		credential, subject := iaaLogin(t, server.URL, "101", "user-c")
		claims := []monetization.ClaimRewardInput{
			iaaClaim(iaaPlay(t, server.URL, credential, 3), "daily-concurrent-a"),
			iaaClaim(iaaPlay(t, server.URL, credential, 6), "daily-concurrent-b"),
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, claim := range claims {
			wg.Add(1)
			go func(claim monetization.ClaimRewardInput) {
				defer wg.Done()
				<-start
				iaaRequest(t, server.URL, "POST", "/api/v1/reward-grants", credential, "", claim, 201, 429)
			}(claim)
		}
		close(start)
		wg.Wait()
		if f.count(t, "reward_grants", "visitor_id = ?", subject) != 1 || f.count(t, "entitlements", "visitor_id = ?", subject) != 1 || f.unlockedEpisodeCount(t, subject) != 3 {
			t.Fatal("不同领取键并发突破了每日一次上限")
		}
	})
	t.Run("已入账但响应丢失后重试", func(t *testing.T) {
		fresh := iaaClaim(iaaPlay(t, server.URL, otherApp, 2), "lost-response")
		handler := f.handler(t)
		drop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recorded := httptest.NewRecorder()
			handler.ServeHTTP(recorded, r)
			if recorded.Code != 201 {
				t.Errorf("模拟丢响应前结算失败: %d", recorded.Code)
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		}))
		defer drop.Close()
		payload, _ := json.Marshal(fresh)
		req, _ := http.NewRequest("POST", drop.URL+"/api/v1/reward-grants", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+otherApp)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = res.Body.Close()
			t.Fatal("模拟链路没有丢失响应")
		}
		raw := iaaRequest(t, server.URL, "POST", "/api/v1/reward-grants", otherApp, "", fresh, 200)
		var result monetization.ClaimResult
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		if result.Status != monetization.ClaimStatusAlreadyGranted {
			t.Fatal("响应丢失后重试没有恢复既有结算")
		}
	})

	t.Run("权益写入失败整笔回滚且可复用领取键重试", func(t *testing.T) {
		credential, subject := iaaLogin(t, server.URL, "102", "user-c")
		claim := iaaClaim(iaaPlay(t, server.URL, credential, 7), "rollback-retry")
		// 在第二集权益写入时注入 SQL 故障，证明第一集权益和奖励流水也被回滚。
		shard := f.userShardFor(t, subject)
		trigger := fmt.Sprintf("iaa_fail_entitlement_%02d", shard)
		_, err := f.store.data.pool.Exec(fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON entitlement_grants_%02d FOR EACH ROW BEGIN IF JSON_CONTAINS(NEW.data, JSON_QUOTE('308'), '$.episodeIds') THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'fixture entitlement write failed'; END IF; END", trigger, shard))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _, _ = f.store.data.pool.Exec("DROP TRIGGER IF EXISTS " + trigger) }()
		var logs bytes.Buffer
		previous := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
		t.Cleanup(func() { slog.SetDefault(previous) })
		iaaRequest(t, server.URL, "POST", "/api/v1/reward-grants", credential, "", claim, 503)
		if !strings.Contains(logs.String(), "grant reward failed") || !strings.Contains(logs.String(), "fixture entitlement write failed") {
			t.Fatal("事务故障日志缺少操作或实际数据库错误")
		}
		slog.SetDefault(previous)

		if f.count(t, "reward_grants", "visitor_id = ?", subject) != 0 || f.count(t, "entitlements", "visitor_id = ?", subject) != 0 {
			t.Fatal("权益写入失败留下了半笔账")
		}
		if _, err := f.store.data.pool.Exec("DROP TRIGGER " + trigger); err != nil {
			t.Fatal(err)
		}
		iaaRequest(t, server.URL, "POST", "/api/v1/reward-grants", credential, "", claim, 201)
		if iaaPlay(t, server.URL, credential, 8).Status != domain.PlayStatusPlayable {
			t.Fatal("失败恢复后同领取键未成功解锁")
		}
	})
	t.Run("数据库故障恢复后保持原身份和权益", func(t *testing.T) {
		if err := f.store.data.pool.Close(); err != nil {
			t.Fatal(err)
		}
		iaaRequest(t, server.URL, "POST", "/api/v1/play/resolve", token, "", map[string]string{"dramaId": "201", "episodeId": "303"}, 503)
		server.Close()
		f.open(rootT)
		server = httptest.NewServer(f.handler(t))
		if iaaPlay(t, server.URL, token, 3).Status != domain.PlayStatusPlayable {
			t.Fatal("重建数据连接及 Handler 后丢失权益")
		}
		iaaRequest(t, server.URL, "POST", "/api/v1/reward-grants", token, "", claim, 200)
	})
	t.Run("线上快照撤回后旧权益不能绕过目录门禁", func(t *testing.T) {
		// 只模拟平台撤回已经落库的事实，不宣称验证真实平台下架接口。
		if _, err := f.store.data.pool.Exec("UPDATE tiktok_drama_states SET published_snapshot = NULL WHERE subject_id=1 AND drama_id = ?", "201"); err != nil {
			t.Fatal(err)
		}
		iaaRequest(t, server.URL, "POST", "/api/v1/play/resolve", token, "", map[string]string{"dramaId": "201", "episodeId": "303"}, 404)
		raw := iaaRequest(t, server.URL, "GET", "/api/v1/open/catalog?tiktokAppId=101", "", "", nil, 200)
		var catalog struct {
			Dramas []json.RawMessage `json:"dramas"`
		}
		if err := json.Unmarshal(raw, &catalog); err != nil {
			t.Fatal(err)
		}
		if len(catalog.Dramas) != 0 {
			t.Fatal("撤回后公开目录仍可见")
		}
	})
}

// unlockedEpisodeCount 检查单条来源 JSON 中实际授予的集数，防止把账本行数误当集数。
func (f *iaaFixture) unlockedEpisodeCount(t *testing.T, userID string) int {
	t.Helper()
	total := 0
	for shard := 0; shard < 16; shard++ {
		var count int
		if err := f.store.data.pool.QueryRow(fmt.Sprintf("SELECT COALESCE(SUM(JSON_LENGTH(data, '$.episodeIds')), 0) FROM entitlement_grants_%02d WHERE visitor_id = ? AND type = 'episode_access'", shard), userID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		total += count
	}
	return total
}
