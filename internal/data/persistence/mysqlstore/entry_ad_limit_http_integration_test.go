package mysqlstore

import (
	"fmt"
	"sync"
	"testing"

	"drama-server/internal/biz/monetization"
)

// TestEntryAdFactsAfterThresholdHTTPIntegration 验证 N=3 不丢弃在途事实，幂等、跨剧与模式切换继续准确。
func TestEntryAdFactsAfterThresholdHTTPIntegration(t *testing.T) {
	for _, scope := range []string{"user", "user_drama"} {
		for _, countBy := range []string{"shown", "completed"} {
			t.Run(scope+"/"+countBy, func(t *testing.T) {
				f := newEntryVIPHTTPFixture(t)
				f.saveRules(t, entryHTTPRule(scope, countBy, 3))
				boot := f.bootstrap(t, "user-a")
				status := f.entry(t, boot.VisitorToken, "202")
				// 每个事件和 ClaimID 都不同，即使并发超过 N，每个有效事实也应恰好保存一次。
				for _, phase := range []string{"show", "claim"} {
					var wg sync.WaitGroup
					failures := make(chan error, 12)
					for i := 0; i < 12; i++ {
						wg.Add(1)
						go func(i int) {
							defer wg.Done()
							var path string
							var input any
							if phase == "show" {
								path = "/api/v1/me/ad-shows"
								input = monetization.AdShowInput{EventID: fmt.Sprintf("http-fixture-cap-show-%d", i), Scene: monetization.AdSceneEntryReward, AdUnitID: status.Offer.AdUnitID, DramaID: "202", OccurredAt: f.now()}
							} else {
								path, input = "/api/v1/reward-grants", entryVIPClaim(status.Offer, fmt.Sprintf("cap-claim-%d", i), "202")
							}
							code, _, err := f.exchange("POST", path, boot.VisitorToken, input)
							if err == nil && code != 200 && code != 201 {
								err = fmt.Errorf("并发写入返回 HTTP %d", code)
							}
							failures <- err
						}(i)
					}
					wg.Wait()
					close(failures)
					for err := range failures {
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				progress := f.entry(t, boot.VisitorToken, "202")
				if countBy == "shown" && progress.ShownCount != 12 || countBy == "completed" && progress.CompletedCount != 12 {
					t.Fatalf("N=3 时并发的 12 条有效事实丢失: %+v", progress)
				}
				shows := f.count(t, "ad_shows", "visitor_id=?", boot.VisitorID)
				grants := f.count(t, "reward_grants", "visitor_id=?", boot.VisitorID)
				f.show(t, boot.VisitorToken, "after-cap-show", monetization.AdSceneEntryReward, "202")
				extraInput := entryVIPClaim(status.Offer, "after-cap-claim", "202")
				extra := f.claim(t, boot.VisitorToken, extraInput, 201)
				if extra.Status != monetization.ClaimStatusGranted || f.count(t, "ad_shows", "visitor_id=?", boot.VisitorID) != shows+1 || f.count(t, "reward_grants", "visitor_id=?", boot.VisitorID) != grants+1 {
					t.Fatal("达标后的展示或完播没有落库")
				}
				f.show(t, boot.VisitorToken, "after-cap-show", monetization.AdSceneEntryReward, "202")
				f.replays(t, "/api/v1/reward-grants", boot.VisitorToken, extraInput, 200)
				if f.count(t, "ad_shows", "visitor_id=?", boot.VisitorID) != 13 || f.count(t, "reward_grants", "visitor_id=?", boot.VisitorID) != 13 || f.count(t, "entitlement_grants", "visitor_id=?", boot.VisitorID) != 0 {
					t.Fatal("幂等重试重复记账或入剧错误授予内容权益")
				}
				other := f.entry(t, boot.VisitorToken, "203")
				if scope == "user_drama" && (other.ShownCount != 0 || other.CompletedCount != 0) {
					t.Fatal("第一部剧达标污染第二部剧")
				}
				if scope == "user" && (countBy == "shown" && other.ShownCount != 13 || countBy == "completed" && other.CompletedCount != 13) {
					t.Fatal("全局范围未共享达标次数")
				}
				f.show(t, boot.VisitorToken, "other-cap-show", monetization.AdSceneEntryReward, "203")
				f.claim(t, boot.VisitorToken, entryVIPClaim(status.Offer, "other-cap-claim", "203"), 201)
				if scope == "user_drama" {
					other = f.entry(t, boot.VisitorToken, "203")
					if countBy == "shown" && other.ShownCount != 1 || countBy == "completed" && other.CompletedCount != 1 {
						t.Fatal("第二部剧不能独立累计次数")
					}
				}
				if countBy == "shown" {
					// 切换为完播模式只读取已独立落账的完播事实，不从展示条数合成。
					f.saveRules(t, entryHTTPRule(scope, "completed", 3))
					changed := f.entry(t, boot.VisitorToken, "202")
					wantCompleted := 14
					if scope == "user_drama" {
						wantCompleted = 13
					}
					if changed.CompletedCount != wantCompleted {
						t.Fatal("切换模式后没有读取已保存的完播事实")
					}
					f.claim(t, boot.VisitorToken, entryVIPClaim(changed.Offer, "mode-changed-claim", "202"), 201)
					if f.entry(t, boot.VisitorToken, "202").CompletedCount != wantCompleted+1 {
						t.Fatal("新模式未独立累计完整观看")
					}
				}
			})
		}
	}
}
