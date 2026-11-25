package mysqlstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	biz "drama-server/internal/biz"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/visitor"
)

// entryVIPHTTPFixture 使用真实 HTTP 与独立数据库；应用和策略由管理接口创建，平台登录仅使用测试替身。
type entryVIPHTTPFixture struct {
	*iaaFixture
	server     *httptest.Server
	adminToken string
	app        application.Application
	clock      atomic.Int64
}

// newEntryVIPHTTPFixture 建立没有策略的新应用，内容只预置已发布快照，不调用真实平台。
func newEntryVIPHTTPFixture(t *testing.T) *entryVIPHTTPFixture {
	t.Helper()
	requireUIDIntegration(t)
	f := &entryVIPHTTPFixture{iaaFixture: newIAAFixture(t)}
	f.clock.Store(time.Now().UTC().UnixNano())
	f.server = httptest.NewServer(f.handlerWithClock(t, f.now))
	t.Cleanup(func() { f.server.Close() })
	login := entryVIPDecode[struct {
		Token string `json:"token"`
	}](t, f.request(t, "POST", "/admin/v1/session", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200))
	f.adminToken = login.Token
	f.app = entryVIPDecode[application.Application](t, f.request(t, "POST", "/admin/v1/applications", f.adminToken, map[string]any{
		"name": "入剧与 VIP 接口联调", "tiktokAppId": "9007199254740993123", "clientKey": "fixture-entry-vip-key",
		"clientSecret": "fixture-entry-vip-secret", "monetizationMode": "IAA", "status": "active", "timeZone": "Asia/Shanghai",
		"adUnits": application.AdUnits{RewardedVideo: []string{"fixture-reward"}, Interstitial: []string{"fixture-interstitial"}}, "subjectId": "1",
	}, 201))
	if f.app.ID == "" || f.app.TikTokAppID != "9007199254740993123" {
		t.Fatal("管理接口未保留字符串应用标识")
	}
	initial := entryVIPDecode[monetization.ApplicationStrategy](t, f.request(t, "GET", "/admin/v1/application-strategy", f.adminToken, nil, 200))
	if initial.Strategy != nil {
		t.Fatal("新应用应未配置策略")
	}
	for index, dramaID := range []string{"202", "203"} {
		drama := domain.Drama{ID: dramaID, Title: "接口联调剧 " + dramaID, Language: "en", Year: 2026,
			Status: domain.DramaStatusCompleted, PublishStatus: "published", OwnerApplicationID: f.app.ID, PlatformAlbumID: "fixture-album-" + dramaID, PlatformVersion: 1}
		for number := 1; number <= 2; number++ {
			drama.Episodes = append(drama.Episodes, domain.Episode{ID: fmt.Sprint(400 + index*10 + number), DramaID: dramaID, Number: number,
				Title: "测试集", MediaStatus: "ready", VID: "fixture-vid", PlatformEpisodeID: fmt.Sprint(number), DurationSeconds: 60})
		}
		published := drama
		drama.Published = &published
		if err := f.store.SaveDrama(application.WithSubject(context.Background(), "1"), drama); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(published)
		if err := seedOfficialProjection(f.store.data.DB(), drama.ID, raw); err != nil {
			t.Fatal(err)
		}
		for _, episode := range drama.Episodes {
			if err := f.store.SaveEpisode(application.WithSubject(context.Background(), "1"), episode); err != nil {
				t.Fatal(err)
			}
		}
	}
	return f
}

func (f *entryVIPHTTPFixture) now() time.Time { return time.Unix(0, f.clock.Load()).UTC() }

// exchange 不在工作协程中调用 Fatal，也不把含身份凭据的响应原文写入日志。
func (f *entryVIPHTTPFixture) exchange(method, path, token string, input any) (int, iaaResponse, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return 0, iaaResponse{}, err
	}
	req, err := http.NewRequest(method, f.server.URL+path, bytes.NewReader(payload))
	if err != nil {
		return 0, iaaResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if strings.HasPrefix(path, "/admin/") && f.app.ID != "" {
		req.Header.Set("X-Application-ID", f.app.ID)
	}
	client := http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return 0, iaaResponse{}, err
	}
	defer response.Body.Close()
	var envelope iaaResponse
	err = json.NewDecoder(response.Body).Decode(&envelope)
	return response.StatusCode, envelope, err
}

func (f *entryVIPHTTPFixture) request(t *testing.T, method, path, token string, input any, want int) json.RawMessage {
	t.Helper()
	status, response, err := f.exchange(method, path, token, input)
	if err != nil {
		t.Fatalf("%s %s 请求失败: %v", method, path, err)
	}
	if status != want {
		t.Fatalf("%s %s: HTTP %d，期望 %d，错误码 %s", method, path, status, want, response.Reason)
	}
	return response.Data
}

func entryVIPDecode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("解析接口合同失败: %v", err)
	}
	return result
}

func entryHTTPRule(scope, countBy string, count int) monetization.UnlockRule {
	raw, _ := json.Marshal(monetization.EntryRewardedAdConfig{Scope: scope, CountBy: countBy, RequiredAdCount: count})
	return monetization.UnlockRule{Code: "entry_ad", Type: "entry_rewarded_ad", Enabled: true, Config: raw}
}

// vipHTTPRule 可以显式传 false 或完全省略新开关，分别验证新旧 JSON 合同。
func vipHTTPRule(require *bool) monetization.UnlockRule {
	config := map[string]any{"requiredAdCount": 2, "forceWatchCount": 2, "delaySeconds": 5, "title": "测试 VIP", "content": "看完后领取", "failureMessage": "稍后重试"}
	if require != nil {
		config["requireBeforePlayback"] = *require
	}
	raw, _ := json.Marshal(config)
	return monetization.UnlockRule{Code: "vip_ad", Type: "rewarded_ad", Enabled: true, Config: raw,
		Grants: []monetization.GrantTemplate{{Type: "vip", Config: json.RawMessage(`{"scope":"application","adFree":true,"validity":"day"}`)}}}
}

func (f *entryVIPHTTPFixture) saveRules(t *testing.T, rules ...monetization.UnlockRule) {
	t.Helper()
	input := monetization.ApplicationStrategyInput{Document: monetization.StrategyDocument{SchemaVersion: 1, Enabled: true, FreeEpisodeCount: 1, Rules: rules}, Rules: map[string]monetization.RuleBinding{}}
	saved := entryVIPDecode[monetization.ApplicationStrategy](t, f.request(t, "PUT", "/admin/v1/application-strategy", f.adminToken, input, 200))
	loaded := entryVIPDecode[monetization.ApplicationStrategy](t, f.request(t, "GET", "/admin/v1/application-strategy", f.adminToken, nil, 200))
	if saved.Strategy == nil || loaded.Strategy == nil || saved.Strategy.ID != loaded.Strategy.ID || len(loaded.Strategy.Document.Rules) != len(rules) {
		t.Fatal("管理接口保存与读回的策略不一致")
	}
}

func (f *entryVIPHTTPFixture) bootstrap(t *testing.T, user string) biz.BootstrapResult {
	t.Helper()
	result := entryVIPDecode[biz.BootstrapResult](t, f.request(t, "POST", "/api/v1/bootstrap", "", visitor.BootstrapInput{
		TikTokAppID: f.app.TikTokAppID, LoginCode: user, LaunchQuery: map[string]string{"dramaId": "202", "episodeId": "401"},
	}, 200))
	if result.VisitorToken == "" || result.VisitorID == "" || result.ApplicationID != f.app.ID || result.LaunchTarget == nil || result.LaunchTarget.DramaID != "202" {
		t.Fatal("登录或指定剧集落地合同不完整")
	}
	return result
}

func (f *entryVIPHTTPFixture) entry(t *testing.T, token, drama string) domain.EntryAdStatus {
	t.Helper()
	path := "/api/v1/me/entry-ad"
	if drama != "" {
		path += "?dramaId=" + drama
	}
	return entryVIPDecode[domain.EntryAdStatus](t, f.request(t, "GET", path, token, nil, 200))
}

func (f *entryVIPHTTPFixture) show(t *testing.T, token, event, scene, drama string) monetization.AdShowInput {
	t.Helper()
	// 满足 HTTP 幂等键长度约束，让后续断言实际覆盖业务分支。
	input := monetization.AdShowInput{EventID: "http-fixture-" + event, Scene: scene, AdUnitID: "fixture-reward", DramaID: drama, OccurredAt: f.now()}
	f.request(t, "POST", "/api/v1/me/ad-shows", token, input, 200)
	return input
}

func entryVIPClaim(offer *monetization.AccessOffer, id, drama string) monetization.ClaimRewardInput {
	return monetization.ClaimRewardInput{ClaimID: "http-fixture-" + id, OfferID: offer.ID, CompletedAdUnitID: offer.AdUnitID, DramaID: drama, IsEnded: true}
}

func (f *entryVIPHTTPFixture) claim(t *testing.T, token string, input monetization.ClaimRewardInput, want int) domain.RewardClaimResult {
	t.Helper()
	return entryVIPDecode[domain.RewardClaimResult](t, f.request(t, "POST", "/api/v1/reward-grants", token, input, want))
}

func (f *entryVIPHTTPFixture) play(t *testing.T, token, episode string) domain.PlayResult {
	t.Helper()
	return entryVIPDecode[domain.PlayResult](t, f.request(t, "POST", "/api/v1/play/resolve", token, map[string]string{"dramaId": "202", "episodeId": episode}, 200))
}

func (f *entryVIPHTTPFixture) replays(t *testing.T, path, token string, input any, allowed ...int) {
	t.Helper()
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, response, err := f.exchange("POST", path, token, input)
			accepted := false
			for _, want := range allowed {
				accepted = accepted || status == want
			}
			if err == nil && !accepted {
				err = fmt.Errorf("并发请求 HTTP %d，错误码 %s", status, response.Reason)
			}
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestEntryFourModesHTTPIntegration 经管理 API 配置四种模式，验证两个剧目、并发重放、主体隔离与午夜边界。
func TestEntryFourModesHTTPIntegration(t *testing.T) {
	for _, scope := range []string{"user", "user_drama"} {
		for _, countBy := range []string{"shown", "completed"} {
			t.Run(scope+"/"+countBy, func(t *testing.T) {
				f := newEntryVIPHTTPFixture(t)
				before := f.bootstrap(t, "user-a")
				if before.Monetization.Status != "disabled" || before.VIPOffer != nil || f.entry(t, before.VisitorToken, "").Enabled {
					t.Fatal("未配置策略仍提供广告入口")
				}
				f.saveRules(t, entryHTTPRule(scope, countBy, 2))
				boot := f.bootstrap(t, "user-a")
				other := f.bootstrap(t, "user-b")
				status := f.entry(t, boot.VisitorToken, "202")
				if status.RequiredCount != 2 || status.Scope != scope || status.CountBy != countBy || status.Offer == nil || status.ShownCount != 0 || status.CompletedCount != 0 {
					t.Fatal("配置未生效或初始进度不为零")
				}
				if scope == "user" {
					f.entry(t, boot.VisitorToken, "")
				} else {
					f.request(t, "GET", "/api/v1/me/entry-ad", boot.VisitorToken, nil, 400)
				}
				shown := f.show(t, boot.VisitorToken, "mode-shown-0001", "entry_reward", "202")
				f.replays(t, "/api/v1/me/ad-shows", boot.VisitorToken, shown, 200)
				conflict := shown
				conflict.DramaID = "203"
				f.request(t, "POST", "/api/v1/me/ad-shows", boot.VisitorToken, conflict, 400)
				cancelled := entryVIPClaim(status.Offer, "mode-cancelled-0001", "202")
				cancelled.IsEnded = false
				f.request(t, "POST", "/api/v1/reward-grants", boot.VisitorToken, cancelled, 409)
				progress := f.entry(t, boot.VisitorToken, "202")
				wantShown := 0
				if countBy == "shown" {
					wantShown = 1
				}
				if progress.ShownCount != wantShown || progress.CompletedCount != 0 || f.count(t, "reward_grants", "visitor_id=?", boot.VisitorID) != 0 {
					t.Fatal("提前关闭、重复上报或计数模式错误")
				}
				f.show(t, boot.VisitorToken, "mode-shown-0002", "entry_reward", "203")
				first := entryVIPClaim(status.Offer, "mode-completed-0001", "202")
				f.replays(t, "/api/v1/reward-grants", boot.VisitorToken, first, 200, 201)
				f.claim(t, boot.VisitorToken, entryVIPClaim(status.Offer, "mode-completed-0002", "203"), 201)
				for _, drama := range []string{"202", "203"} {
					progress := f.entry(t, boot.VisitorToken, drama)
					want := 2
					if scope == "user_drama" {
						want = 1
					}
					if countBy == "shown" && (progress.ShownCount != want || progress.CompletedCount != 0) || countBy == "completed" && (progress.CompletedCount != want || progress.ShownCount != 0) {
						t.Fatal("跨剧共享或按剧独立计数错误")
					}
				}
				if scope == "user_drama" {
					f.show(t, boot.VisitorToken, "mode-shown-0003", "entry_reward", "202")
					f.claim(t, boot.VisitorToken, entryVIPClaim(status.Offer, "mode-completed-0003", "202"), 201)
				}
				progress = f.entry(t, boot.VisitorToken, "202")
				if countBy == "shown" && progress.ShownCount != 2 || countBy == "completed" && progress.CompletedCount != 2 || progress.Offer == nil {
					t.Fatal("N 次达标状态错误，入口应继续供 Mini 自行判断")
				}
				isolated := f.entry(t, other.VisitorToken, "202")
				if isolated.ShownCount != 0 || isolated.CompletedCount != 0 {
					t.Fatal("入剧次数串到其他用户")
				}
				otherApp, _ := iaaLogin(t, f.server.URL, "102", "user-a")
				otherCount := entryVIPDecode[domain.AdShowStatus](t, iaaRequest(t, f.server.URL, "GET", "/api/v1/me/ad-shows?scene=entry_reward", otherApp, "", nil, 200))
				if otherCount.ShownCount != 0 {
					t.Fatal("展示事实串到其他应用")
				}
				iaaRequest(t, f.server.URL, "POST", "/api/v1/reward-grants", otherApp, "", entryVIPClaim(status.Offer, "wrong-app-claim", "202"), 409)
				period := monetization.RewardPeriodFor(f.now(), mustEntryVIPLocation(t))
				if progress.ProgressDate != period.Key || progress.ValidUntil != period.ExpiresAt.Format(time.RFC3339) {
					t.Fatal("自然日或下次重置时间错误")
				}
				f.clock.Store(period.ExpiresAt.UnixNano())
				next := f.entry(t, boot.VisitorToken, "202")
				if next.ShownCount != 0 || next.CompletedCount != 0 || next.ProgressDate == progress.ProgressDate {
					t.Fatal("应用当地午夜没有重置进度")
				}
				// 新事件即使携带昨天的手机时间，也按今天首次到达计数；原键重试保持昨天归属。
				historical := shown
				historical.EventID = "http-fixture-prior-day-backfill"
				f.request(t, "POST", "/api/v1/me/ad-shows", boot.VisitorToken, historical, 200)
				f.request(t, "POST", "/api/v1/me/ad-shows", boot.VisitorToken, shown, 200)
				f.claim(t, boot.VisitorToken, first, 200)
				wantNewDayShown := 0
				if countBy == "shown" {
					wantNewDayShown = 1
				}
				if next := f.entry(t, boot.VisitorToken, "202"); next.ShownCount != wantNewDayShown || next.CompletedCount != 0 {
					t.Fatal("首次到达或原键重试的服务器日期归属错误")
				}
				t.Log("通过：无策略补配置；提前关闭；8 次展示及完播重放；跨剧、用户、应用隔离；达标；当地午夜重置")
			})
		}
	}
}

func mustEntryVIPLocation(t *testing.T) *time.Location {
	t.Helper()
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	return location
}

// TestVIPCompatibilityHTTPIntegration 对缺省、false、立即强弹和倒计时强弹复用旧领奖接口，验证手动领取语义。
func TestVIPCompatibilityHTTPIntegration(t *testing.T) {
	for _, mode := range []string{"缺少新开关", "显式关闭", "开启强制", "倒计时强弹"} {
		t.Run(mode, func(t *testing.T) {
			f := newEntryVIPHTTPFixture(t)
			var flag *bool
			if mode != "缺少新开关" {
				require := mode == "开启强制" || mode == "倒计时强弹"
				flag = &require
			}
			rule := vipHTTPRule(flag)
			if mode == "倒计时强弹" {
				var config map[string]any
				if err := json.Unmarshal(rule.Config, &config); err != nil {
					t.Fatal(err)
				}
				config["entryAdCountdownSeconds"] = 7
				var err error
				rule.Config, err = json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
			}
			f.saveRules(t, rule)
			boot := f.bootstrap(t, "user-a")
			if boot.Monetization.VIP.RequireBeforePlayback != (flag != nil && *flag) || boot.Monetization.VIP.RequiredAdCount != 2 || boot.Monetization.VIP.ForceWatchCount != 2 || boot.Monetization.VIP.DelaySeconds != 5 || boot.VIPOffer == nil {
				t.Fatal("VIP 新旧字段投影错误")
			}
			if mode == "倒计时强弹" {
				loaded := entryVIPDecode[monetization.ApplicationStrategy](t, f.request(t, "GET", "/admin/v1/application-strategy", f.adminToken, nil, 200))
				var saved monetization.RewardedAdConfig
				if err := json.Unmarshal(loaded.Strategy.Document.Rules[0].Config, &saved); err != nil {
					t.Fatal(err)
				}
				if saved.EntryAdCountdownSeconds == nil || *saved.EntryAdCountdownSeconds != 7 || boot.Monetization.VIP.EntryAdCountdownSeconds == nil || *boot.Monetization.VIP.EntryAdCountdownSeconds != 7 || boot.VIPOffer.VIP == nil || boot.VIPOffer.VIP.EntryAdCountdownSeconds == nil || *boot.VIPOffer.VIP.EntryAdCountdownSeconds != 7 {
					t.Fatal("保存读回、Bootstrap 或 VIP Offer 未保留倒计时")
				}
			} else if boot.Monetization.VIP.EntryAdCountdownSeconds != nil {
				t.Fatal("未配置倒计时不能产生新字段")
			}
			raw, err := json.Marshal(boot.Monetization.VIP)
			if err != nil || strings.Contains(string(raw), "requireBeforePlayback") != (flag != nil && *flag) {
				t.Fatal("关闭的新开关不应出现在消费响应中")
			}
			if f.entry(t, boot.VisitorToken, "202").Enabled || f.play(t, boot.VisitorToken, "401").Status != domain.PlayStatusPlayable {
				t.Fatal("服务端不应把客户端 VIP 门禁变为免费集播放拦截")
			}
			f.request(t, "POST", "/api/v1/vip/claim", boot.VisitorToken, nil, 409)
			first := entryVIPClaim(boot.VIPOffer, "vip-completed-0001", "")
			cancelled := first
			cancelled.IsEnded = false
			f.request(t, "POST", "/api/v1/reward-grants", boot.VisitorToken, cancelled, 409)
			f.show(t, boot.VisitorToken, "vip-shown-0001", "vip_reward", "")
			if state := f.bootstrap(t, "user-a").VIPState; state.Active || state.AdCount != 0 {
				t.Fatal("展示或提前关闭不应增加 VIP 完播进度")
			}
			f.replays(t, "/api/v1/reward-grants", boot.VisitorToken, first, 200, 201)
			f.request(t, "POST", "/api/v1/vip/claim", boot.VisitorToken, nil, 409)
			state := f.bootstrap(t, "user-a").VIPState
			if state.AdCount != 1 || state.Active {
				t.Fatal("一次完整观看或并发重放错误达到阈值")
			}
			second := f.claim(t, boot.VisitorToken, entryVIPClaim(boot.VIPOffer, "vip-completed-0002", ""), 201)
			if second.VIPProgress == nil || second.VIPProgress.AdCount != 2 || second.VIPProgress.Active {
				t.Fatal("达标应仍等待手动领取")
			}
			if f.play(t, boot.VisitorToken, "402").Status == domain.PlayStatusPlayable {
				t.Fatal("只看完 VIP 广告就提前发放内容权益")
			}
			f.request(t, "POST", "/api/v1/reward-grants", boot.VisitorToken, entryVIPClaim(boot.VIPOffer, "vip-excess-0003", ""), 409)
			f.replays(t, "/api/v1/vip/claim", boot.VisitorToken, nil, 200)
			// 模拟稍早接收的请求排在较晚请求之后取得用户锁，仍须恢复同一笔 VIP 授予。
			claimedAt := f.now()
			f.clock.Store(claimedAt.Add(-time.Millisecond).UnixNano())
			reordered := entryVIPDecode[monetization.VIPProgress](t, f.request(t, "POST", "/api/v1/vip/claim", boot.VisitorToken, nil, 200))
			if !reordered.Active {
				t.Fatal("请求接收顺序与事务提交顺序不同时必须恢复原 VIP")
			}
			f.clock.Store(claimedAt.UnixNano())
			active := f.bootstrap(t, "user-a")
			period := monetization.RewardPeriodFor(f.now(), mustEntryVIPLocation(t))
			if !active.VIPState.Active || !active.VIPState.ValidUntil.Equal(period.ExpiresAt) || active.VIPOffer != nil || f.play(t, boot.VisitorToken, "402").Status != domain.PlayStatusPlayable {
				t.Fatal("领取后状态、截止时间或播放权益错误")
			}
			if f.count(t, "reward_grants", "visitor_id=?", boot.VisitorID) != 2 || f.count(t, "entitlement_grants", "visitor_id=?", boot.VisitorID) != 1 {
				t.Fatal("并发重复发奖或重复领取 VIP")
			}
			if other := f.bootstrap(t, "user-b"); other.VIPState.Active || other.VIPState.AdCount != 0 {
				t.Fatal("VIP 权益或次数串用户")
			}
			f.clock.Store(period.ExpiresAt.UnixNano())
			next := f.bootstrap(t, "user-a")
			if next.VIPState.Active || next.VIPState.AdCount != 0 || next.VIPOffer == nil || f.play(t, boot.VisitorToken, "402").Status == domain.PlayStatusPlayable {
				t.Fatal("当地午夜 VIP 权益或进度未过期")
			}
			t.Log("通过：旧字段保留；展示不计完播；未达标拒绝；2 次完播后手动领取；8 次重放/并发领取幂等；午夜过期")
		})
	}
}

// TestVIPEntryCountdownInvalidHTTPIntegration 验证错误配置在 HTTP 入口被拒绝且不会覆盖已保存策略。
func TestVIPEntryCountdownInvalidHTTPIntegration(t *testing.T) {
	f := newEntryVIPHTTPFixture(t)
	enabled := true
	f.saveRules(t, vipHTTPRule(&enabled))
	for _, value := range []any{-1, 301, 1.5, "5", nil} {
		rule := vipHTTPRule(&enabled)
		var config map[string]any
		if err := json.Unmarshal(rule.Config, &config); err != nil {
			t.Fatal(err)
		}
		config["entryAdCountdownSeconds"] = value
		var err error
		rule.Config, err = json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		input := monetization.ApplicationStrategyInput{Document: monetization.StrategyDocument{SchemaVersion: 1, Enabled: true, FreeEpisodeCount: 1, Rules: []monetization.UnlockRule{rule}}, Rules: map[string]monetization.RuleBinding{}}
		f.request(t, "PUT", "/admin/v1/application-strategy", f.adminToken, input, 400)
	}
	if f.bootstrap(t, "user-a").Monetization.VIP.EntryAdCountdownSeconds != nil {
		t.Fatal("非法倒计时配置覆盖了已保存策略")
	}
}

// TestEntryThenVIPHTTPIntegration 按 Mini 的顺序调用接口，验证两份账本独立以及已领取 VIP 仍需满足入剧策略。
func TestEntryThenVIPHTTPIntegration(t *testing.T) {
	f := newEntryVIPHTTPFixture(t)
	enabled := true
	// 即使 VIP 在策略数组中靠前，入剧入口也独立提供，实际执行顺序由 Mini 控制。
	f.saveRules(t, vipHTTPRule(&enabled), entryHTTPRule("user_drama", "completed", 2))
	boot := f.bootstrap(t, "user-a")
	entry := f.entry(t, boot.VisitorToken, "202")
	if entry.Offer == nil || boot.VIPOffer == nil {
		t.Fatal("同时开启后缺少独立广告入口")
	}
	for index := 1; index <= 2; index++ {
		result := f.claim(t, boot.VisitorToken, entryVIPClaim(entry.Offer, fmt.Sprintf("ordered-entry-%d", index), "202"), 201)
		if result.RewardType != "entry_progress" || result.Unlocked || result.Playback != nil || f.bootstrap(t, "user-a").VIPState.AdCount != 0 {
			t.Fatal("入剧广告错误增加 VIP 进度或发放内容权益")
		}
	}
	if f.entry(t, boot.VisitorToken, "202").CompletedCount != 2 {
		t.Fatal("入剧广告未先达到阈值")
	}
	for index := 1; index <= 2; index++ {
		f.claim(t, boot.VisitorToken, entryVIPClaim(boot.VIPOffer, fmt.Sprintf("ordered-vip-%d", index), ""), 201)
		if f.entry(t, boot.VisitorToken, "202").CompletedCount != 2 {
			t.Fatal("VIP 广告错误累计到入剧进度")
		}
	}
	if f.bootstrap(t, "user-a").VIPState.Active {
		t.Fatal("顺序流程未领取就激活 VIP")
	}
	f.request(t, "POST", "/api/v1/vip/claim", boot.VisitorToken, nil, 200)
	if f.play(t, boot.VisitorToken, "402").Status != domain.PlayStatusPlayable {
		t.Fatal("领取 VIP 后锁定集仍不可播放")
	}
	f.saveRules(t, entryHTTPRule("user_drama", "completed", 3), vipHTTPRule(&enabled))
	status := f.entry(t, boot.VisitorToken, "202")
	if status.RequiredCount != 3 || status.CompletedCount != 2 || !f.bootstrap(t, "user-a").VIPState.Active {
		t.Fatal("调整门槛丢失既有事实，或 VIP 绕过了入剧计数")
	}
	f.claim(t, boot.VisitorToken, entryVIPClaim(status.Offer, "ordered-entry-3", "202"), 201)
	if f.entry(t, boot.VisitorToken, "202").CompletedCount != 3 || f.entry(t, boot.VisitorToken, "203").CompletedCount != 0 {
		t.Fatal("持有 VIP 的入剧事实或按剧计数错误")
	}
	// 重建数据库连接和 HTTP 服务，已有身份、配置、事实与权益都必须可回源。
	f.server.Close()
	if err := f.store.data.pool.Close(); err != nil {
		t.Fatal(err)
	}
	f.open(t)
	f.server = httptest.NewServer(f.handlerWithClock(t, f.now))
	if f.bootstrap(t, "user-a").VisitorID != boot.VisitorID || !f.bootstrap(t, "user-a").VIPState.Active || f.entry(t, boot.VisitorToken, "202").CompletedCount != 3 {
		t.Fatal("重启后丢失身份、策略、进度或 VIP 权益")
	}
	entryRule := entryHTTPRule("user_drama", "completed", 3)
	entryRule.Enabled = false
	f.saveRules(t, entryRule, vipHTTPRule(&enabled))
	if f.entry(t, boot.VisitorToken, "202").Enabled || !f.bootstrap(t, "user-a").VIPState.Active {
		t.Fatal("关闭入剧规则未生效或错误撤销已领取 VIP")
	}
	t.Log("通过：入剧 2 次 → VIP 2 次 → 手动领取 → 锁定集可播放；账本独立；已有 VIP 不跳过入剧；动态配置及重启保持事实")
}

// TestVIPClaimHTTPRecoveryIntegration 验证开关切换中的旧 Offer 仍能结算，以及 VIP 发放事务故障不丢失达标进度。
func TestVIPClaimHTTPRecoveryIntegration(t *testing.T) {
	f := newEntryVIPHTTPFixture(t)
	f.saveRules(t, vipHTTPRule(nil))
	boot := f.bootstrap(t, "user-a")
	first := entryVIPClaim(boot.VIPOffer, "pending-vip-complete-0001", "")
	f.claim(t, boot.VisitorToken, first, 201)
	enabled := true
	f.saveRules(t, vipHTTPRule(&enabled))
	// 广告开始时拿到旧修订 Offer；开关变化后同一规则仍允许按当前配置结算。
	second := entryVIPClaim(boot.VIPOffer, "pending-vip-complete-0002", "")
	f.claim(t, boot.VisitorToken, second, 201)
	shard := f.userShardFor(t, boot.VisitorID)
	trigger := fmt.Sprintf("CREATE TRIGGER fixture_vip_grant_failure BEFORE INSERT ON entitlement_grants_%02d FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'fixture vip entitlement write failed'", shard)
	if _, err := f.store.data.pool.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.store.data.pool.Exec("DROP TRIGGER IF EXISTS fixture_vip_grant_failure"); err != nil {
			t.Errorf("清理 VIP 故障触发器失败: %v", err)
		}
	})
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	f.request(t, "POST", "/api/v1/vip/claim", boot.VisitorToken, nil, 503)
	slog.SetDefault(previous)
	state := f.bootstrap(t, "user-a").VIPState
	if state.Active || state.AdCount != 2 || f.count(t, "entitlement_grants", "visitor_id=?", boot.VisitorID) != 0 || f.count(t, "user_entitlement_state", "visitor_id=?", boot.VisitorID) != 0 {
		t.Fatal("VIP 发放失败留下半笔权益或丢失广告进度")
	}
	if !strings.Contains(logs.String(), "grant manually claimed vip failed") || !strings.Contains(logs.String(), "fixture vip entitlement write failed") || strings.Contains(logs.String(), boot.VisitorToken) {
		t.Fatal("VIP 发放故障没有可定位日志或泄漏凭据")
	}
	if _, err := f.store.data.pool.Exec("DROP TRIGGER fixture_vip_grant_failure"); err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", "/api/v1/vip/claim", boot.VisitorToken, nil, 200)
	f.claim(t, boot.VisitorToken, second, 200)
	if !f.bootstrap(t, "user-a").VIPState.Active || f.count(t, "entitlement_grants", "visitor_id=?", boot.VisitorID) != 1 {
		t.Fatal("故障恢复后领取失败或旧广告重放重复发 VIP")
	}
	t.Log("通过：缺省开关→强制开关，旧修订 Offer 仍可结算；VIP 事务故障 HTTP 503，2 次广告保留、权益零写入；恢复后领取与旧请求重放成功")
}

// TestEntryPolicyHTTPBoundaryIntegration 验证管理接口的 1–100 范围、非法配置不覆盖原策略以及最大次数真正达标。
func TestEntryPolicyHTTPBoundaryIntegration(t *testing.T) {
	f := newEntryVIPHTTPFixture(t)
	for _, count := range []int{1, 100} {
		f.saveRules(t, entryHTTPRule("user", "shown", count))
		boot := f.bootstrap(t, "user-a")
		if f.entry(t, boot.VisitorToken, "").RequiredCount != count {
			t.Fatal("合法边界没有读回")
		}
	}
	for _, config := range []string{
		`{"requiredAdCount":0}`, `{"requiredAdCount":101}`, `{"requiredAdCount":2,"scope":"global"}`,
		`{"requiredAdCount":2,"countBy":"watched"}`, `{"requiredAdCount":2,"scope":null}`,
		`{"requiredAdCount":2,"countBy":""}`, `{"requiredAdCount":2,"requireBeforePlayback":true}`,
	} {
		rule := entryHTTPRule("user", "shown", 2)
		rule.Config = json.RawMessage(config)
		f.request(t, "PUT", "/admin/v1/application-strategy", f.adminToken, monetization.ApplicationStrategyInput{
			Document: monetization.StrategyDocument{SchemaVersion: 1, Enabled: true, Rules: []monetization.UnlockRule{rule}},
		}, 400)
	}
	boot := f.bootstrap(t, "user-a")
	if f.entry(t, boot.VisitorToken, "").RequiredCount != 100 {
		t.Fatal("非法保存覆盖了原有策略")
	}
	for index := 1; index <= 100; index++ {
		f.show(t, boot.VisitorToken, fmt.Sprintf("max-count-show-%03d", index), "entry_reward", "")
		if index == 99 && f.entry(t, boot.VisitorToken, "").ShownCount != 99 {
			t.Fatal("99 次事实没有保持未达标状态")
		}
	}
	if f.entry(t, boot.VisitorToken, "").ShownCount != 100 {
		t.Fatal("100 次事实被服务端截断")
	}
	f.saveRules(t, entryHTTPRule("user", "completed", 100))
	status := f.entry(t, boot.VisitorToken, "")
	if status.CompletedCount != 0 || status.ShownCount != 0 {
		t.Fatal("展示次数不能被计入完整观看")
	}
	for index := 1; index <= 100; index++ {
		f.claim(t, boot.VisitorToken, entryVIPClaim(status.Offer, fmt.Sprintf("max-count-completed-%03d", index), ""), 201)
		if index == 99 && f.entry(t, boot.VisitorToken, "").CompletedCount != 99 {
			t.Fatal("完整观看 99 次应保持未达标")
		}
	}
	if f.entry(t, boot.VisitorToken, "").CompletedCount != 100 {
		t.Fatal("100 次完整观看被服务端截断")
	}
	for _, item := range []struct{ method, path string }{
		{"GET", "/api/v1/me/entry-ad"}, {"POST", "/api/v1/me/ad-shows"}, {"POST", "/api/v1/reward-grants"}, {"POST", "/api/v1/vip/claim"},
	} {
		f.request(t, item.method, item.path, "", nil, 401)
	}
	t.Log("通过：N=1/N=100 保存；7 类非法配置拒绝且原配置保留；展示及完播均经 99→100 实际达标；4 个业务接口鉴权")
}

// TestAdSceneHTTPReuseIntegration 验证未配置策略时首页场景也能使用通用展示表，剧目和剧集只作为可选事实。
func TestAdSceneHTTPReuseIntegration(t *testing.T) {
	f := newEntryVIPHTTPFixture(t)
	boot := f.bootstrap(t, "user-a")
	for index := 1; index <= 2; index++ {
		f.show(t, boot.VisitorToken, fmt.Sprintf("home-show-%d", index), "home_entry", "")
	}
	f.show(t, boot.VisitorToken, "scene-entry-show", "entry_reward", "202")
	f.show(t, boot.VisitorToken, "scene-vip-show", "vip_reward", "")
	episode := monetization.AdShowInput{EventID: "scene-episode-show", Scene: "episode_preview", AdUnitID: "fixture-interstitial", DramaID: "202", EpisodeID: "401", OccurredAt: f.now()}
	f.request(t, "POST", "/api/v1/me/ad-shows", boot.VisitorToken, episode, 200)
	for path, want := range map[string]int{
		"?scene=home_entry": 2, "?scene=entry_reward": 1, "?scene=vip_reward": 1, "?scene=episode_preview&dramaId=202&episodeId=401": 1,
	} {
		raw := f.request(t, "GET", "/api/v1/me/ad-shows"+path, boot.VisitorToken, nil, 200)
		status := entryVIPDecode[domain.AdShowStatus](t, raw)
		if status.ShownCount != want || strings.Contains(string(raw), "requiredCount") || strings.Contains(string(raw), "offer") || strings.Contains(string(raw), "allowed") {
			t.Fatal("通用场景串账或事实查询混入策略门槛")
		}
	}
	for _, change := range []func(*monetization.AdShowInput){
		func(in *monetization.AdShowInput) { in.AdUnitID = "unbound-unit" },
		func(in *monetization.AdShowInput) { in.DramaID = "" },
		func(in *monetization.AdShowInput) { in.Scene = "bad/scene" },
	} {
		invalid := episode
		invalid.EventID = "invalid-scene-fact"
		change(&invalid)
		f.request(t, "POST", "/api/v1/me/ad-shows", boot.VisitorToken, invalid, 400)
	}
	// 旧包仍可传手机时间，首次展示确认必须按服务器接收时间落账，不能再按手机时间拒绝。
	oldClock := episode
	oldClock.EventID, oldClock.OccurredAt = "old-client-clock-show", f.now().Add(-49*time.Hour)
	f.request(t, "POST", "/api/v1/me/ad-shows", boot.VisitorToken, oldClock, 200)
	var saved adShowModel
	if err := f.store.data.DB().Table(fmt.Sprintf("ad_shows_%02d", f.userShardFor(t, boot.VisitorID))).
		Where("application_id=? AND visitor_id=? AND event_id=?", f.app.ID, boot.VisitorID, oldClock.EventID).Take(&saved).Error; err != nil || !saved.OccurredAt.Equal(f.now().Truncate(time.Microsecond)) {
		t.Fatalf("旧包手机时间影响服务器展示归日: at=%v err=%v", saved.OccurredAt, err)
	}
	wrongEpisode := episode
	wrongEpisode.EventID, wrongEpisode.EpisodeID = "wrong-episode-fact", "411"
	f.request(t, "POST", "/api/v1/me/ad-shows", boot.VisitorToken, wrongEpisode, 404)
	if f.count(t, "reward_grants", "visitor_id=?", boot.VisitorID) != 0 || f.count(t, "entitlement_grants", "visitor_id=?", boot.VisitorID) != 0 {
		t.Fatal("通用展示事实错误发放奖励或权益")
	}
	t.Log("通过：首页无策略、无剧目标记录 2 次；入剧/VIP/剧集场景独立；插屏位支持；非法广告位与目标拒绝，旧包手机时间按服务器归日；不发权益")
}

// TestAdFactsHTTPRecoveryIntegration 在真实 MySQL 注入写入故障，验证完播不依赖展示确认，以及失败后复用原幂等键。
func TestAdFactsHTTPRecoveryIntegration(t *testing.T) {
	f := newEntryVIPHTTPFixture(t)
	f.saveRules(t, entryHTTPRule("user_drama", "completed", 2))
	boot := f.bootstrap(t, "user-a")
	status := f.entry(t, boot.VisitorToken, "202")
	shard := f.userShardFor(t, boot.VisitorID)
	createTrigger := func(name, table, message string) {
		t.Helper()
		statement := fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON %s_%02d FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = '%s'", name, table, shard, message)
		if _, err := f.store.data.pool.Exec(statement); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := f.store.data.pool.Exec("DROP TRIGGER IF EXISTS " + name); err != nil {
				t.Errorf("清理故障注入触发器失败: %v", err)
			}
		})
	}
	dropTrigger := func(name string) {
		t.Helper()
		if _, err := f.store.data.pool.Exec("DROP TRIGGER " + name); err != nil {
			t.Fatal(err)
		}
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	createTrigger("fixture_show_failure", "ad_shows", "fixture ad show write failed")
	show := monetization.AdShowInput{EventID: "recovery-show-0001", Scene: "entry_reward", AdUnitID: "fixture-reward", DramaID: "202", OccurredAt: f.now()}
	f.request(t, "POST", "/api/v1/me/ad-shows", boot.VisitorToken, show, 500)
	f.claim(t, boot.VisitorToken, entryVIPClaim(status.Offer, "recovery-complete-0001", "202"), 201)
	if f.entry(t, boot.VisitorToken, "202").CompletedCount != 1 {
		t.Fatal("展示写入失败错误阻塞了完整观看结算")
	}
	dropTrigger("fixture_show_failure")
	f.request(t, "POST", "/api/v1/me/ad-shows", boot.VisitorToken, show, 200)
	f.replays(t, "/api/v1/me/ad-shows", boot.VisitorToken, show, 200)
	createTrigger("fixture_reward_failure", "reward_grants", "fixture reward write failed")
	claim := entryVIPClaim(status.Offer, "recovery-complete-0002", "202")
	f.request(t, "POST", "/api/v1/reward-grants", boot.VisitorToken, claim, 503)
	if f.entry(t, boot.VisitorToken, "202").CompletedCount != 1 || f.count(t, "reward_grants", "visitor_id=?", boot.VisitorID) != 1 {
		t.Fatal("失败的奖励结算留下事实或提前达标")
	}
	dropTrigger("fixture_reward_failure")
	f.claim(t, boot.VisitorToken, claim, 201)
	if f.claim(t, boot.VisitorToken, claim, 200).Status != "already_granted" || f.entry(t, boot.VisitorToken, "202").CompletedCount != 2 {
		t.Fatal("奖励重试没有恢复原幂等键或重复计数")
	}
	if !strings.Contains(logs.String(), "save ad show failed") || !strings.Contains(logs.String(), "fixture ad show write failed") || !strings.Contains(logs.String(), "grant reward failed") || !strings.Contains(logs.String(), "fixture reward write failed") || strings.Contains(logs.String(), boot.VisitorToken) || strings.Contains(logs.String(), f.adminToken) {
		t.Fatal("实际故障缺少诊断或日志泄漏凭据")
	}
	f.saveRules(t, entryHTTPRule("user_drama", "shown", 2))
	if shown := f.entry(t, boot.VisitorToken, "202"); shown.ShownCount != 1 || shown.CompletedCount != 0 {
		t.Fatal("展示重试重复累计或切换模式错误读取完播账本")
	}
	t.Log("通过：展示故障 HTTP 500，完播仍可结算；奖励故障 HTTP 503 不累计；原键重试与 8 次重放不重复；错误日志可定位且无凭据")
}
