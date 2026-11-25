package mysqlstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/sharding"
)

// TestVIPMultiUserRepeatedClaimsIntegration 验证跨全部分片用户的三天领取、广告重放、超额广告与并发领取不会重复或串账。
func TestVIPMultiUserRepeatedClaimsIntegration(t *testing.T) {
	f := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	// 隔离测试共用有限连接池，避免并发模拟挤占测试服务器业务连接。
	f.store.data.pool.SetMaxOpenConns(64)
	f.store.data.pool.SetMaxIdleConns(32)
	start := time.Date(2026, 10, 1, 7, 40, 0, 0, time.UTC)
	for index := 0; index < 32; index++ {
		t.Run(fmt.Sprintf("user-%d", index), func(t *testing.T) {
			t.Parallel()
			for day := 0; day < 3; day++ {
				now := start.AddDate(0, 0, day)
				period := monetization.RewardPeriodFor(now, time.UTC)
				bucket := uint16(index)
				id := fmt.Sprint(800000 + index)
				ctx := sharding.WithBucketID(application.WithTimeZone(context.Background(), "101", "UTC"), bucket)
				if day == 0 {
					if err := f.store.data.DB().Exec("INSERT INTO "+userTable("users", bucket)+" (id,bucket_id,application_id) VALUES (?,?,101)", id, bucket).Error; err != nil {
						t.Fatal(err)
					}
				}
				policy := monetization.MonetizationPolicy{ApplicationID: "101", Status: monetization.MonetizationEnabled, VIP: monetization.ApplicationVIPPolicy{Enabled: true, RequiredAdCount: 5}, ProgressNamespace: "multiuser-vip"}
				active, err := f.store.HasCatalogEntitlement(ctx, id, now)
				if err != nil || active {
					t.Fatalf("用户 %s 第 %d 天领前权益错误: active=%v err=%v", id, day, active, err)
				}
				state, err := f.store.ReconcileVIPProgress(ctx, id, policy, period, now)
				if err != nil || state.Active || state.AdCount != 0 {
					t.Fatalf("未看广告领取异常: %+v %v", state, err)
				}
				for ad := 0; ad < 5; ad++ {
					grant := monetization.RewardGrant{ClaimID: fmt.Sprintf("vip-%s-%d-%d", id, day, ad), ApplicationID: "101", VisitorID: id, AdUnitID: "fixture-reward", Scene: monetization.AdSceneVIPReward, RewardType: monetization.RewardTypeVIPProgress, PeriodKey: period.Key, PeriodStartsAt: period.StartsAt, PeriodExpiresAt: period.ExpiresAt, RequiredCount: 5, EntitlementScope: monetization.EntitlementScopeCatalog, GrantedAt: now, ProgressKey: "multiuser-vip"}
					// 每天最后一次广告并发重放四次，前四次正常结算，验证达标边界不会重复累计。
					var wg sync.WaitGroup
					repeats := 1
					if ad == 4 {
						repeats = 4
					}
					errs := make(chan error, repeats)
					for repeat := 0; repeat < repeats; repeat++ {
						wg.Add(1)
						go func() { defer wg.Done(); _, e := f.store.GrantReward(ctx, grant, now); errs <- e }()
					}
					wg.Wait()
					close(errs)
					for e := range errs {
						if e != nil {
							t.Fatal(e)
						}
					}
					state, err = f.store.FindVIPProgress(ctx, id, policy, period, now)
					if err != nil || state.AdCount != ad+1 || state.Active {
						t.Fatalf("广告重放重复计数或提前发 VIP: user=%s day=%d ad=%d state=%+v err=%v", id, day, ad, state, err)
					}
					if ad < 4 {
						state, err = f.store.ReconcileVIPProgress(ctx, id, policy, period, now)
						if err != nil || state.Active {
							t.Fatalf("未达标提前发放: %+v %v", state, err)
						}
					}
				}
				extra := monetization.RewardGrant{ClaimID: fmt.Sprintf("extra-%s-%d", id, day), ApplicationID: "101", VisitorID: id, Scene: monetization.AdSceneVIPReward, RewardType: monetization.RewardTypeVIPProgress, PeriodKey: period.Key, PeriodStartsAt: period.StartsAt, PeriodExpiresAt: period.ExpiresAt, RequiredCount: 5, GrantedAt: now, ProgressKey: "multiuser-vip"}
				if _, err := f.store.GrantReward(ctx, extra, now); !errors.Is(err, monetization.ErrInvalidRewardClaim) {
					t.Fatalf("超额广告未拒绝: %v", err)
				}
				var wg sync.WaitGroup
				errs := make(chan error, 4)
				for repeat := 0; repeat < 4; repeat++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						s, e := f.store.ReconcileVIPProgress(ctx, id, policy, period, now)
						if e == nil && (!s.Active || !s.ValidUntil.Equal(period.ExpiresAt)) {
							e = fmt.Errorf("VIP 截止错误: %+v", s)
						}
						errs <- e
					}()
				}
				wg.Wait()
				close(errs)
				for e := range errs {
					if e != nil {
						t.Fatal(e)
					}
				}
				for _, base := range []string{"entitlement_grants", "user_entitlement_state", "application_vip_progress"} {
					if n := f.count(t, userTable(base, bucket), "bucket_id=? AND application_id=101 AND visitor_id=?", bucket, id); n != day+1 {
						t.Fatalf("%s 用户 %s 记录数 %d, 应为 %d", base, id, n, day+1)
					}
				}
				if n := f.count(t, userTable("reward_grants", bucket), "bucket_id=? AND visitor_id=?", bucket, id); n != 5*(day+1) {
					t.Fatalf("广告流水数异常: %d", n)
				}
				var states []entitlementStateModel
				if err := f.store.data.DB().Table(userTable("user_entitlement_state", bucket)).Find(&states, "bucket_id=? AND visitor_id=?", bucket, id).Error; err != nil {
					t.Fatal(err)
				}
				for _, s := range states {
					if s.ValidUntil == nil || !s.ValidUntil.Equal(monetization.RewardPeriodFor(s.ValidFrom, time.UTC).ExpiresAt) {
						t.Fatalf("历史有效期被延长或错误: %+v", s)
					}
					var n int64
					if err := f.store.data.DB().Table(userTable("entitlement_grants", bucket)).Where("id=? AND bucket_id=? AND visitor_id=? AND application_id=101", s.GrantID, bucket, id).Count(&n).Error; err != nil || n != 1 {
						t.Fatalf("投影串用户或关联缺失: %d %v", n, err)
					}
				}
				active, err = f.store.HasCatalogEntitlement(ctx, id, period.ExpiresAt)
				if err != nil || active {
					t.Fatalf("截止瞬间仍有效: %v %v", active, err)
				}
				t.Logf("用户 %d 第 %d 天: 5 广告，最后一次 4 次并发重放，4 次并发领取，流水、投影及截止时间正确", index, day+1)
			}
		})
	}
}
