package mysqlstore

import (
	"testing"
	"time"

	"drama-server/internal/biz/monetization"
)

// TestEntrySatisfiedReplayHTTPIntegration 回归达标后响应丢失：不同原键各自落账，跨日重试不再计入今天。
func TestEntrySatisfiedReplayHTTPIntegration(t *testing.T) {
	for _, provider := range []string{"tiktok_minis", "byteplus_web"} {
		for _, mode := range []string{"completed", "shown"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				f := newSettlementChannelFixture(t, provider)
				f.saveRules(t, entryHTTPRule("user_drama", mode, 1))
				boot := settlementBootstrap(t, f, "user-a")
				state := f.entry(t, boot.VisitorToken, "201")
				var replay any
				path := "/api/v1/reward-grants"
				firstStatus := 201
				factTable := "reward_grants"
				if mode == "completed" {
					f.claim(t, boot.VisitorToken, entryVIPClaim(state.Offer, "cap-probe-first", "201"), 201)
					replay = entryVIPClaim(state.Offer, "cap-probe-dropped", "201")
				} else {
					path = "/api/v1/me/ad-shows"
					firstStatus, factTable = 200, "ad_shows"
					show := monetization.AdShowInput{EventID: "cap-probe-first-show", Scene: "entry_reward", AdUnitID: state.Offer.AdUnitID, DramaID: "201", OccurredAt: f.now()}
					f.request(t, "POST", path, boot.VisitorToken, show, 200)
					show.EventID = "cap-probe-dropped-show"
					replay = show
				}
				f.request(t, "POST", path, boot.VisitorToken, replay, firstStatus)
				before := f.entry(t, boot.VisitorToken, "201")
				if mode == "completed" && before.CompletedCount != 2 || mode == "shown" && before.ShownCount != 2 {
					t.Fatal("达标后的第二条有效事实仍被丢弃")
				}
				f.clock.Store(f.now().Add(24 * time.Hour).UnixNano())
				boot = settlementBootstrap(t, f, "user-a")
				// 跨日并发原键重放只能返回已确认结果，不能产生任何新的流水。
				f.replays(t, path, boot.VisitorToken, replay, 200)
				after := f.entry(t, boot.VisitorToken, "201")
				if after.CompletedCount != 0 || after.ShownCount != 0 {
					t.Fatalf("昨日已达标请求次日重试被重新计数: mode=%s completed=%d shown=%d; want 0/0", mode, after.CompletedCount, after.ShownCount)
				}
				if f.count(t, factTable, "visitor_id=?", boot.VisitorID) != 2 {
					t.Fatal("跨日原键重试改变了事实总数")
				}
				if mode == "completed" {
					claim := replay.(monetization.ClaimRewardInput)
					result := f.claim(t, boot.VisitorToken, claim, 200)
					if result.Status != "already_granted" || result.EntryAdStatus == nil || result.EntryAdStatus.CompletedCount != 0 {
						t.Fatal("幂等回包未提供今天的零进度")
					}
					claim.DramaID = "202"
					f.request(t, "POST", path, boot.VisitorToken, claim, 409)
				} else {
					show := replay.(monetization.AdShowInput)
					show.AdUnitID = "another-ad"
					f.request(t, "POST", path, boot.VisitorToken, show, 400)
				}
			})
		}
	}
}
