package consumer_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/testsupport/memory"
)

// entryShowPolicyFailureStore 在新的展示策略读取边界注入依赖故障。
type entryShowPolicyFailureStore struct {
	*memory.Store
	failure error
}

// GetApplicationMonetization 返回实际数据库故障，不能绕过入剧范围校验。
func (s *entryShowPolicyFailureStore) GetApplicationMonetization(context.Context) (monetization.ApplicationMonetizationConfig, error) {
	return monetization.ApplicationMonetizationConfig{}, s.failure
}

// TestEntryAdShowPolicyFailureLog 验证读取策略失败可定位、保留错误且不输出完整广告对象。
func TestEntryAdShowPolicyFailureLog(t *testing.T) {
	now := time.Now()
	store, _, ctx := entryModeFixture("user_drama", "shown", func() time.Time { return now })
	failure := errors.New("database policy connection lost")
	service := domain.NewService(&entryShowPolicyFailureStore{Store: store, failure: failure}, func() time.Time { return now })
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	err := service.RecordAdShow(ctx, "viewer", monetization.AdShowInput{EventID: "private-show-context", Scene: monetization.AdSceneEntryReward, AdUnitID: "episode-ad", DramaID: "5008", OccurredAt: now})
	if !errors.Is(err, failure) || !strings.Contains(logs.String(), "resolve ad show entry policy failed") || !strings.Contains(logs.String(), failure.Error()) || !strings.Contains(logs.String(), `"level":"ERROR"`) || strings.Contains(logs.String(), "private-show-context") {
		t.Fatal("展示策略依赖故障的日志、脱敏或错误传播不正确")
	}
}

// TestEntryAdRecordsFactsAfterSatisfied 验证四种模式达标后仍保存有效事实，同键重试不新增记录。
func TestEntryAdRecordsFactsAfterSatisfied(t *testing.T) {
	for _, scope := range []string{"user", "user_drama"} {
		for _, countBy := range []string{"shown", "completed"} {
			t.Run(scope+"/"+countBy, func(t *testing.T) {
				now := time.Date(2026, 10, 9, 3, 0, 0, 0, time.UTC)
				store, service, ctx := entryModeFixture(scope, countBy, func() time.Time { return now })
				status, err := service.EntryAdStatus(ctx, "viewer", "5008")
				if err != nil {
					t.Fatal(err)
				}
				show := monetization.AdShowInput{EventID: "shown-first", Scene: monetization.AdSceneEntryReward, AdUnitID: status.Offer.AdUnitID, DramaID: "5008", OccurredAt: now}
				claim := monetization.ClaimRewardInput{ClaimID: "claim-first", OfferID: status.Offer.ID, CompletedAdUnitID: status.Offer.AdUnitID, DramaID: "5008", IsEnded: true}
				// N=2，两次展示和两次完整观看独立保留；展示先达标不能丢弃后续完播。
				for i := 0; i < 2; i++ {
					show.EventID, claim.ClaimID = fmt.Sprintf("shown-%d", i), fmt.Sprintf("claim-%d", i)
					if err := service.RecordAdShow(ctx, "viewer", show); err != nil {
						t.Fatal(err)
					}
					if _, err := service.ClaimReward(ctx, "viewer", claim); err != nil {
						t.Fatal(err)
					}
				}
				period := monetization.RewardPeriodFor(now, application.LocationFromContext(ctx))
				query := monetization.AdShowQuery{Scene: monetization.AdSceneEntryReward, From: period.StartsAt, Until: period.ExpiresAt}
				shownBefore, err := store.CountAdShows(ctx, "viewer", query)
				if err != nil {
					t.Fatal(err)
				}
				policy, err := service.ResolveMonetizationPolicy(ctx)
				if err != nil {
					t.Fatal(err)
				}
				progressKey := policy.RulePolicies()[0].ProgressKey()
				completedBefore, err := store.CountEntryAdCompletions(ctx, "viewer", progressKey, "", period.StartsAt, period.ExpiresAt)
				if err != nil {
					t.Fatal(err)
				}
				if shownBefore != 2 || completedBefore != 2 {
					t.Fatal("达到门槛时展示或完播事实丢失")
				}
				var wg sync.WaitGroup
				failures := make(chan error, 16)
				for i := 0; i < 8; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						extraShow, extraClaim := show, claim
						extraShow.EventID, extraClaim.ClaimID = fmt.Sprintf("extra-show-%d", i), fmt.Sprintf("extra-claim-%d", i)
						failures <- service.RecordAdShow(ctx, "viewer", extraShow)
						result, err := service.ClaimReward(ctx, "viewer", extraClaim)
						if err == nil && result.Status != monetization.ClaimStatusGranted {
							err = fmt.Errorf("达标后的完播没有落账: %s", result.Status)
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
				// 对每个已落账的事实重放，覆盖达标后的幂等路径。
				for i := 0; i < 8; i++ {
					extraShow, extraClaim := show, claim
					extraShow.EventID, extraClaim.ClaimID = fmt.Sprintf("extra-show-%d", i), fmt.Sprintf("extra-claim-%d", i)
					if err := service.RecordAdShow(ctx, "viewer", extraShow); err != nil {
						t.Fatal(err)
					}
					if result, err := service.ClaimReward(ctx, "viewer", extraClaim); err != nil || result.Status != monetization.ClaimStatusAlreadyGranted {
						t.Fatalf("达标后重试没有恢复原记录: %v", err)
					}
				}
				shownAfter, err := store.CountAdShows(ctx, "viewer", query)
				if err != nil {
					t.Fatal(err)
				}
				completedAfter, err := store.CountEntryAdCompletions(ctx, "viewer", progressKey, "", period.StartsAt, period.ExpiresAt)
				if err != nil || shownAfter != shownBefore+8 || completedAfter != completedBefore+8 {
					t.Fatalf("有效事实丢失或重复: 展示 %d→%d，完播 %d→%d，%v", shownBefore, shownAfter, completedBefore, completedAfter, err)
				}
				// 按剧统计时另一部剧仍需独立完成；按用户统计时共享已达标次数。
				show.EventID, show.DramaID, claim.ClaimID, claim.DramaID = "other-drama-show", "5009", "other-drama-claim", "5009"
				if err := service.RecordAdShow(ctx, "viewer", show); err != nil {
					t.Fatal(err)
				}
				otherClaim, err := service.ClaimReward(ctx, "viewer", claim)
				if err != nil {
					t.Fatal(err)
				}
				if otherClaim.Status != monetization.ClaimStatusGranted {
					t.Fatalf("其他剧有效完播未记录: %s", otherClaim.Status)
				}
				other, err := service.EntryAdStatus(ctx, "viewer", "5009")
				if err != nil {
					t.Fatal(err)
				}
				if scope == "user_drama" && (other.ShownCount >= 2 || other.CompletedCount >= 2) {
					t.Fatal("另一部剧被第一部剧的达标状态放行")
				}
			})
		}
	}
}
