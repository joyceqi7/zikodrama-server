package mysqlstore

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	biz "drama-server/internal/biz"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/visitor"
)

// newSettlementChannelFixture 仅切换隔离库的应用通道；真实 HTTP、策略、发布快照和结算事务保持不变。
func newSettlementChannelFixture(t *testing.T, provider string) *entryVIPHTTPFixture {
	t.Helper()
	requireUIDIntegration(t)
	f := &entryVIPHTTPFixture{iaaFixture: newIAAFixture(t), app: application.Application{SubjectID: "1", ID: "102", TikTokAppID: "102"}}
	if provider == "byteplus_web" {
		repository, _ := seedDirect(t, f.iaaFixture)
		f.direct = directplay.NewService(repository, nil)
		f.signer = &directHTTPPlayback{}
		submitDirect(t, repository, "publish", "")
	}
	f.clock.Store(time.Now().UTC().UnixNano())
	f.server = httptest.NewServer(f.handlerWithClock(t, f.now))
	t.Cleanup(f.server.Close)
	login := entryVIPDecode[struct {
		Token string `json:"token"`
	}](t, f.request(t, "POST", "/admin/v1/session", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200))
	f.adminToken = login.Token
	return f
}

// settlementBootstrap 登录测试应用，不把平台凭据或播放授权输出到测试日志。
func settlementBootstrap(t *testing.T, f *entryVIPHTTPFixture, user string) biz.BootstrapResult {
	t.Helper()
	return entryVIPDecode[biz.BootstrapResult](t, f.request(t, "POST", "/api/v1/bootstrap", "", visitor.BootstrapInput{
		TikTokAppID: f.app.TikTokAppID, LoginCode: user,
	}, 200))
}

// TestSettlementProgressHTTPIntegration 验证原请求兼容、结算内进度、达标后事实以及跨日幂等。
func TestSettlementProgressHTTPIntegration(t *testing.T) {
	f := newEntryVIPHTTPFixture(t)
	f.saveRules(t, entryHTTPRule("user_drama", "completed", 1), vipHTTPRule(nil))
	boot := f.bootstrap(t, "user-a")
	entry := f.entry(t, boot.VisitorToken, "202")
	input := entryVIPClaim(entry.Offer, "response-state-entry", "202")
	first := f.claim(t, boot.VisitorToken, input, 201)
	if first.EntryAdStatus == nil || first.EntryAdStatus.CompletedCount != 1 || first.EntryAdStatus.RequiredCount != 1 {
		t.Fatalf("首次结算缺少进度: %+v", first)
	}
	extra := f.claim(t, boot.VisitorToken, entryVIPClaim(entry.Offer, "response-state-cap", "202"), 201)
	if extra.Status != "granted" || extra.EntryAdStatus == nil || extra.EntryAdStatus.CompletedCount != 2 {
		t.Fatalf("达标后仍须记录事实并返回当前进度: %+v", extra)
	}
	vipInput := entryVIPClaim(boot.VIPOffer, "response-state-vip", "")
	vip := f.claim(t, boot.VisitorToken, vipInput, 201)
	if vip.VIPProgress == nil || vip.VIPProgress.AdCount != 1 || vip.VIPProgress.Active {
		t.Fatalf("VIP 回包未返回已提交进度: %+v", vip)
	}
	f.clock.Store(f.now().Add(24 * time.Hour).UnixNano())
	boot = f.bootstrap(t, "user-a")
	retry := f.claim(t, boot.VisitorToken, input, 200)
	if retry.EntryAdStatus == nil || retry.EntryAdStatus.CompletedCount != 0 || retry.EntryAdStatus.ProgressDate == first.EntryAdStatus.ProgressDate {
		t.Fatalf("昨日原键重试不应增加今日次数: %+v", retry)
	}
	vip = f.claim(t, boot.VisitorToken, vipInput, 200)
	if vip.VIPProgress == nil || vip.VIPProgress.AdCount != 0 {
		t.Fatalf("VIP 原键跨日重计: %+v", vip)
	}
	if f.count(t, "reward_grants", "visitor_id=?", boot.VisitorID) != 3 {
		t.Fatal("重试重复结算或有效事实丢失")
	}
}

// TestSettlementDirectoryHTTPIntegration 验证首次结算与原键恢复均返回完整访问状态，旧响应字段可独立解码。
func TestSettlementDirectoryHTTPIntegration(t *testing.T) {
	for _, provider := range []string{"tiktok_minis", "byteplus_web"} {
		t.Run(provider, func(t *testing.T) {
			f := newSettlementChannelFixture(t, provider)
			token, user := iaaLogin(t, f.server.URL, "102", "user-a")
			input := iaaClaim(iaaPlay(t, f.server.URL, token, 2), "response-directory")
			for _, status := range []int{201, 200} {
				raw := iaaRequest(t, f.server.URL, "POST", "/api/v1/reward-grants", token, "", input, status)
				var result domain.RewardClaimResult
				if err := json.Unmarshal(raw, &result); err != nil {
					t.Fatal(err)
				}
				if result.Drama == nil || result.Playback == nil || len(result.Drama.Episodes) != result.Drama.EpisodeCount {
					t.Fatal("结算回包未包含完整目录与播放结果")
				}
				found := false
				for _, episode := range result.Drama.Episodes {
					if episode.ID == input.EpisodeID {
						found = episode.AccessMode == "unlocked"
					}
				}
				if !found {
					t.Fatal("目标集未返回已解锁状态")
				}
				// 回包覆盖全剧：本次奖励解锁两集，后续未获权的集仍必须保持锁定。
				for index, episode := range result.Drama.Episodes {
					want := "rewarded"
					if index == 0 {
						want = "free"
					} else if index < 3 {
						want = "unlocked"
					}
					if episode.AccessMode != want {
						t.Fatalf("第 %d 集 accessMode=%s，期望 %s", index+1, episode.AccessMode, want)
					}
				}
				if result.Playback.Playback == nil {
					t.Fatal("结算后缺少播放描述符")
				}
				descriptor := result.Playback.Playback
				if provider == "byteplus_web" {
					if descriptor.PlayAuthToken == "" || descriptor.VID == "" || descriptor.AlbumID != "" || descriptor.EpisodeID != "" {
						t.Fatal("直连结算未返回独立播放器授权")
					}
				} else if descriptor.PlayAuthToken != "" || descriptor.AlbumID == "" || descriptor.EpisodeID == "" {
					t.Fatal("旧官方播放器合同被改变")
				}
				var old monetization.ClaimResult
				if err := json.Unmarshal(raw, &old); err != nil || !old.Unlocked || old.ClaimID != input.ClaimID {
					t.Fatalf("旧字段不兼容: %+v %v", old, err)
				}
			}
			if f.count(t, "reward_grants", "visitor_id=?", user) != 1 {
				t.Fatal("幂等恢复重复结算")
			}
			// 同一应用的其他用户和同平台身份的其他应用均不能继承本次解锁。
			for _, identity := range []struct{ app, user string }{{"102", "user-b"}, {"101", "user-a"}} {
				other, _ := iaaLogin(t, f.server.URL, identity.app, identity.user)
				if play := iaaPlay(t, f.server.URL, other, 2); play.Status != "access_required" || play.Playback != nil {
					t.Fatal("解锁越过应用或用户边界")
				}
			}
		})
	}
}

// TestSettlementChannelProgressHTTPIntegration 验证两种播放器下回包进度、并发重试、跨日归属和 VIP 达标领取。
func TestSettlementChannelProgressHTTPIntegration(t *testing.T) {
	for _, provider := range []string{"tiktok_minis", "byteplus_web"} {
		for _, countBy := range []string{"completed", "shown"} {
			t.Run(provider+"/"+countBy, func(t *testing.T) {
				f := newSettlementChannelFixture(t, provider)
				f.saveRules(t, entryHTTPRule("user_drama", countBy, 2), vipHTTPRule(nil))
				// 模拟应用时区午夜前后，请求接收日由服务端控制，不调整机器时钟。
				local := f.now().In(time.FixedZone("fixture-Asia-Shanghai", 8*60*60))
				before := time.Date(local.Year(), local.Month(), local.Day(), 15, 59, 30, 0, time.UTC)
				f.clock.Store(before.UnixNano())
				boot := settlementBootstrap(t, f, "user-a")
				entry := f.entry(t, boot.VisitorToken, "201")
				input := entryVIPClaim(entry.Offer, "channel-entry-first", "201")
				cancelled := input
				cancelled.IsEnded = false
				f.request(t, "POST", "/api/v1/reward-grants", boot.VisitorToken, cancelled, 409)
				// 展示上报故意携带偏差两天的手机时间，仍应归入服务端收到的当天。
				shown := monetization.AdShowInput{EventID: "channel-entry-show-first", Scene: "entry_reward", AdUnitID: input.CompletedAdUnitID, DramaID: "201", OccurredAt: before.Add(-48 * time.Hour)}
				f.request(t, "POST", "/api/v1/me/ad-shows", boot.VisitorToken, shown, 200)
				first := f.claim(t, boot.VisitorToken, input, 201)
				assertProgress := func(result domain.RewardClaimResult, completed, shows int, day string) {
					t.Helper()
					state := result.EntryAdStatus
					if state == nil || state.CompletedCount != completed || state.ShownCount != shows || state.ProgressDate != day || state.RequiredCount != 2 {
						t.Fatalf("入剧结算回包进度不符: %+v", state)
					}
				}
				showCount := 0
				completedCount := 1
				if countBy == "shown" {
					showCount = 1
					completedCount = 0
				}
				assertProgress(first, completedCount, showCount, local.Format("2006-01-02"))
				f.replays(t, "/api/v1/reward-grants", boot.VisitorToken, input, 200)
				assertProgress(f.claim(t, boot.VisitorToken, input, 200), completedCount, showCount, local.Format("2006-01-02"))
				second := input
				second.ClaimID = "channel-entry-second"
				shown.EventID = "channel-entry-show-second"
				shown.OccurredAt = before.Add(48 * time.Hour)
				f.request(t, "POST", "/api/v1/me/ad-shows", boot.VisitorToken, shown, 200)
				assertProgress(f.claim(t, boot.VisitorToken, second, 201), completedCount*2, showCount*2, local.Format("2006-01-02"))
				capInput := input
				capInput.ClaimID = "channel-entry-cap"
				capResult := f.claim(t, boot.VisitorToken, capInput, 201)
				if capResult.Status != "granted" || capResult.Unlocked || capResult.Playback != nil || capResult.VIPProgress != nil {
					t.Fatal("入剧达标后未记录事实或错误授予内容权益")
				}
				assertProgress(capResult, completedCount*3, showCount*2, local.Format("2006-01-02"))
				vipInput := entryVIPClaim(boot.VIPOffer, "channel-vip-first", "")
				vip := f.claim(t, boot.VisitorToken, vipInput, 201)
				if vip.VIPProgress == nil || vip.VIPProgress.AdCount != 1 || vip.VIPProgress.Active {
					t.Fatal("VIP 首次结算进度不正确")
				}
				f.replays(t, "/api/v1/reward-grants", boot.VisitorToken, vipInput, 200)
				vipSecond := vipInput
				vipSecond.ClaimID = "channel-vip-second"
				vip = f.claim(t, boot.VisitorToken, vipSecond, 201)
				if vip.VIPProgress == nil || vip.VIPProgress.AdCount != 2 || vip.VIPProgress.RequiredCount != 2 || vip.VIPProgress.Active {
					t.Fatal("VIP 回包未反映达标可领取")
				}
				active := entryVIPDecode[monetization.VIPProgress](t, f.request(t, "POST", "/api/v1/vip/claim", boot.VisitorToken, nil, 200))
				if !active.Active || active.AdCount != 2 {
					t.Fatal("VIP 领取未返回当前生效状态")
				}
				f.clock.Store(before.Add(time.Minute).UnixNano())
				boot = settlementBootstrap(t, f, "user-a")
				nextDay := local.Add(24 * time.Hour).Format("2006-01-02")
				f.request(t, "POST", "/api/v1/me/ad-shows", boot.VisitorToken, shown, 200)
				assertProgress(f.claim(t, boot.VisitorToken, input, 200), 0, 0, nextDay)
				vip = f.claim(t, boot.VisitorToken, vipInput, 200)
				if vip.VIPProgress == nil || vip.VIPProgress.AdCount != 0 || vip.VIPProgress.Active || vip.VIPProgress.ProgressDate != nextDay {
					t.Fatal("昨日 VIP 原键重试复用了旧日进度或权益")
				}
				if f.count(t, "reward_grants", "visitor_id=?", boot.VisitorID) != 5 {
					t.Fatal("取消、并发重试或跨日重试重复结算")
				}
				// 昨天开始的广告今天首次完播到达，必须在今天新增事实；不沿用 Offer 的取得日期。
				input.ClaimID = "channel-entry-arrived-next-day"
				assertProgress(f.claim(t, boot.VisitorToken, input, 201), completedCount, 0, nextDay)
				vipInput.ClaimID = "channel-vip-arrived-next-day"
				vip = f.claim(t, boot.VisitorToken, vipInput, 201)
				if vip.VIPProgress == nil || vip.VIPProgress.AdCount != 1 || vip.VIPProgress.ProgressDate != nextDay {
					t.Fatal("首次到达的 VIP 完播未归服务端接收日")
				}
			})
		}
	}
}
