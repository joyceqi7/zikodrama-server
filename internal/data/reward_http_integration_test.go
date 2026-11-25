package data

import (
	"bytes"
	"context"
	"database/sql"
	biz "drama-server/internal/biz"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/visitor"
	"drama-server/internal/data/idgen"
	"drama-server/internal/data/persistence/mysqlstore"
	"drama-server/internal/data/redisstore"
	consumerhttp "drama-server/internal/service/consumer"
	"encoding/json"
	"fmt"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// iaaIdentity 用固定测试码模拟平台用户，真实 Visitor 绑定、加密及认证仍走生产代码。
type iaaIdentity struct{}

// ExchangeCode 只接受明确的测试账号，禁止把任意输入当作成功登录。
func (iaaIdentity) ExchangeCode(_ context.Context, code string) (visitor.Identity, error) {
	switch code {
	case "user-a", "user-b", "user-c":
		return visitor.Identity{OpenID: "fixture-" + code}, nil
	default:
		return visitor.Identity{}, visitor.ErrUnauthorized
	}
}

// newProductionRewardStore 创建并回收独立数据库，执行当前部署 Schema。
func newProductionRewardStore(t *testing.T) (*mysqlstore.Store, *gorm.DB) {
	schemaBytes, err := os.ReadFile("../../sql/init.sql")
	if err != nil {
		t.Fatal(err)
	}
	schemaText := string(schemaBytes)
	// 与实际部署一致加载全部有序增量，避免官方回归测试使用过期结构。
	migrations, err := filepath.Glob("../../sql/migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range migrations {
		contents, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		schemaText += "\n" + string(contents)
	}
	t.Helper()
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" || os.Getenv("TEST_REDIS_ADDR") == "" {
		t.Skip("发号及仓储联调需要同时配置 TEST_MYSQL_DSN 和 TEST_REDIS_ADDR")
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
	db, err := gorm.Open(gormmysql.Open(cfg.FormatDSN()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	data, err := mysqlstore.NewData(db)
	if err != nil {
		t.Fatal(err)
	}
	uidClient := redis.NewClient(&redis.Options{Addr: os.Getenv("TEST_REDIS_ADDR"), DB: 15})
	t.Cleanup(func() { _ = uidClient.Close() })
	if err := uidClient.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	sharedUIDCache := redisstore.NewUIDCache(uidClient)
	t.Cleanup(func() { _ = sharedUIDCache.Close() })
	uidCache := &rewardUIDIntegrationCache{Cache: sharedUIDCache}
	t.Cleanup(func() {
		uidCache.keys.Range(func(key, _ any) bool {
			if err := uidClient.Del(context.Background(), key.(string)).Err(); err != nil {
				t.Errorf("清理本测试 UID 缓存失败：%v", err)
			}
			return true
		})
	})
	return mysqlstore.NewStore(data, uidCache), db
}

// rewardUIDIntegrationCache 只跟踪本测试实际使用的键，清理时不扫描或清空其他测试的数据。
type rewardUIDIntegrationCache struct {
	// Cache 继续执行真实 Redis 原子协议。
	idgen.Cache
	keys sync.Map
}

// Next 记录隔离库对应的缓存键，发号继续使用真实 Redis 原子协议。
func (c *rewardUIDIntegrationCache) Next(ctx context.Context, key, token string, lease time.Duration) (idgen.Allocation, error) {
	c.keys.Store(key, struct{}{})
	return c.Cache.Next(ctx, key, token, lease)
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

// TestProductionRewardHTTPIntegration 验证生产仓储包装保留非零桶，并完成登录、领取、幂等恢复与播放放行。
func TestProductionRewardHTTPIntegration(t *testing.T) {
	store, db := newProductionRewardStore(t)
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("未配置 TEST_REDIS_ADDR，跳过生产缓存装配联调")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	// 独立应用标识隔离共享测试 Redis 中的目录缓存，不清理其他测试的数据。
	appID := fmt.Sprint(time.Now().UnixNano())
	ctx := application.WithSubject(context.Background(), "1")
	for _, id := range []string{appID} {
		app := application.Application{SubjectID: "1", Name: "IAA Local " + id, Status: application.StatusActive,
			ID: id, TikTokAppID: id, ClientKey: "fixture-" + id, TimeZone: "Asia/Shanghai", RegionCode: "SG",
			ComplianceBaseURL: "https://legal.example", AdUnits: application.AdUnits{RewardedVideo: []string{"fixture-reward"}, Interstitial: []string{"fixture-interstitial"}}}
		if err := store.SaveApplication(ctx, app); err != nil {
			t.Fatal(err)
		}
		config := monetization.ApplicationMonetizationConfig{ApplicationID: id, Status: monetization.MonetizationEnabled,
			Strategy: monetization.MonetizationStrategyEpisodeUnlock, FreeEpisodeCount: 1, EpisodeRewardAdUnitCode: "fixture-reward",
			EpisodeReward: monetization.EpisodeRewardPolicy{UnlockEpisodeCount: 2, DailyRewardLimit: 2}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if err := store.SaveApplicationMonetization(application.WithContext(ctx, id), config); err != nil {
			t.Fatal(err)
		}
	}
	drama := domain.Drama{ID: "201", Title: "Local IAA Drama", Description: "Real API and MySQL integration fixture", Language: "en", Year: 2026,
		Status: domain.DramaStatusCompleted, PublishStatus: "published", OwnerApplicationID: appID, PlatformAlbumID: "fixture-album", PlatformVersion: 1}
	for n := 1; n <= 8; n++ {
		drama.Episodes = append(drama.Episodes, domain.Episode{ID: fmt.Sprintf("30%d", n), DramaID: drama.ID, Number: n, Title: fmt.Sprintf("Episode %d", n), MediaStatus: "ready", VID: fmt.Sprintf("fixture-vid-%d", n), PlatformEpisodeID: fmt.Sprintf("fixture-episode-%d", n), DurationSeconds: 60})
	}
	// 这是平台已经确认上线的测试快照，不验证真实平台送审或发布。
	published := drama
	drama.Published = &published
	if err := store.SaveDrama(ctx, drama); err != nil {
		t.Fatal(err)
	}
	rawProjection, err := json.Marshal(published)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Table("tiktok_drama_states").Create(map[string]any{"subject_id": "1", "drama_id": drama.ID, "platform_album_id": drama.PlatformAlbumID, "published_snapshot": rawProjection}).Error; err != nil {
		t.Fatal(err)
	}

	for _, ep := range drama.Episodes {
		if err := store.SaveEpisode(ctx, ep); err != nil {
			t.Fatal(err)
		}
	}

	resources := &Resources{store: store, redis: client, catalogCache: redisstore.NewCatalogCache(client)}
	resolver, err := biz.NewApplicationResolver(NewApplicationRepository(resources), func(application.Application) visitor.IdentityProvider { return iaaIdentity{} })
	if err != nil {
		t.Fatal(err)
	}
	visitors, err := visitor.NewService(resolver, NewVisitorRepository(resources), visitor.Config{RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "local-iaa-integration-secret-only"})
	if err != nil {
		t.Fatal(err)
	}
	consumers := domain.NewService(NewConsumerRepository(resources), time.Now)
	server := httptest.NewServer(consumerhttp.NewHandler(consumers, visitors, biz.NewConsumerBootstrap(visitors, consumers), consumerhttp.Config{}))
	defer server.Close()
	token, _ := iaaLogin(t, server.URL, appID, "user-a")
	identity, err := visitors.Resolve(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if identity.BucketID == 0 {
		token, _ = iaaLogin(t, server.URL, appID, "user-b")
		identity, err = visitors.Resolve(context.Background(), token)
		if err != nil {
			t.Fatal(err)
		}
	}
	if identity.BucketID == 0 {
		t.Fatal("生产包装未保留非零用户桶")
	}
	exists, err := store.VisitorExistsRef(context.Background(), appID, visitor.UserRef{ID: identity.VisitorID, BucketID: identity.BucketID})
	if err != nil || !exists {
		t.Fatalf("凭据中的桶与持久化用户不匹配: %v", err)
	}
	locked := iaaPlay(t, server.URL, token, 2)
	if locked.Status != domain.PlayStatusAccessRequired || locked.Offer == nil {
		t.Fatal("锁定集未返回广告奖励入口")
	}
	claim := iaaClaim(locked, "production-wrapper")
	iaaRequest(t, server.URL, "POST", "/api/v1/reward-grants", token, "", claim, 201)
	iaaRequest(t, server.URL, "POST", "/api/v1/reward-grants", token, "", claim, 200)
	if play := iaaPlay(t, server.URL, token, 2); play.Status != domain.PlayStatusPlayable {
		t.Fatal("领取成功后剧集仍未解锁")
	}
	t.Run("旧入口按最新策略结算且重试不重复发奖", func(t *testing.T) {
		assertLatestRewardPolicyHTTP(t, store, server.URL, token, appID)
	})
}
