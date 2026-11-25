package consumer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/testsupport/memory"
)

// entryModeFixture 保留两个独立剧目和真实测试奖励仓储，不调用平台服务。
func entryModeFixture(scope, countBy string, now func() time.Time) (*memory.Store, *domain.Service, context.Context) {
	config := applicationMonetization(false, 0, 0)
	raw, _ := json.Marshal(monetization.EntryRewardedAdConfig{Scope: scope, CountBy: countBy, RequiredAdCount: 2})
	config.StrategyID = "entry-strategy"
	config.Document = &monetization.StrategyDocument{SchemaVersion: 1, Enabled: true, FreeEpisodeCount: 1, Rules: []monetization.UnlockRule{{Code: "entry_ad", Type: "entry_rewarded_ad", Enabled: true, Config: raw}}}
	config.Bindings = map[string]monetization.RuleBinding{"entry_ad": {AdUnitID: "episode-ad"}}
	seed := monetizationSeed(config)
	drama := seed.Dramas[0]
	drama.ID = "5009"
	drama.Episodes = nil
	seed.Dramas = append(seed.Dramas, drama)
	store := memory.NewStore(seed)
	return store, domain.NewService(store, now), application.WithTimeZone(context.Background(), memory.TestApplicationID, "Asia/Kuala_Lumpur")
}

type parallelEntryReads struct {
	*memory.Store
	vipStarted chan struct{}
}

// CountAdShows 等待另一条独立查询开始，串行入口只能在请求截止后失败。
func (s *parallelEntryReads) CountAdShows(ctx context.Context, visitorID string, query monetization.AdShowQuery) (int, error) {
	select {
	case <-s.vipStarted:
		return s.Store.CountAdShows(ctx, visitorID, query)
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// FindVIPProgress 记录进入 VIP 读取，不改变测试仓储返回的权益。
func (s *parallelEntryReads) FindVIPProgress(ctx context.Context, visitorID string, policy monetization.MonetizationPolicy, period monetization.RewardPeriod, now time.Time) (monetization.VIPProgress, error) {
	close(s.vipStarted)
	return s.Store.FindVIPProgress(ctx, visitorID, policy, period, now)
}

func TestPlaybackEntryOverlapsAdAndVIPReads(t *testing.T) {
	store, _, app := entryModeFixture("user", "shown", time.Now)
	reads := &parallelEntryReads{Store: store, vipStarted: make(chan struct{})}
	ctx, cancel := context.WithTimeout(app, time.Second)
	defer cancel()
	entry, err := domain.NewService(reads, time.Now).ResolvePlaybackEntry(ctx, "viewer", "5008", "5014")
	if err != nil || entry == nil || !entry.EntryAdStatus.Enabled || entry.Play != nil {
		t.Fatalf("并行读取仍应保留入剧广告门槛: entry=%+v err=%v", entry, err)
	}
}

// TestEntryAdFourModesCounts 验证展示与完播独立、两种范围、幂等和跨天。
func TestEntryAdFourModesCounts(t *testing.T) {
	for _, scope := range []string{"user", "user_drama"} {
		for _, countBy := range []string{"shown", "completed"} {
			t.Run(scope+"/"+countBy, func(t *testing.T) {
				now := time.Date(2026, 10, 8, 15, 58, 0, 0, time.UTC)
				_, service, ctx := entryModeFixture(scope, countBy, func() time.Time { return now })
				status, err := service.EntryAdStatus(ctx, "viewer", "5008")
				if err != nil || status.Scope != scope || status.CountBy != countBy || status.Offer == nil {
					t.Fatalf("新版规则缺失: %+v %v", status, err)
				}
				offer := status.Offer
				for index := 1; index <= 2; index++ {
					show := monetization.AdShowInput{EventID: fmt.Sprintf("mode-show-%d", index), Scene: monetization.AdSceneEntryReward, AdUnitID: offer.AdUnitID, DramaID: "5008", OccurredAt: now}
					if err := service.RecordAdShow(ctx, "viewer", show); err != nil {
						t.Fatal(err)
					}
					if err := service.RecordAdShow(ctx, "viewer", show); err != nil {
						t.Fatal(err)
					}
				}
				status, err = service.EntryAdStatus(ctx, "viewer", "5008")
				expectedShown := 0
				if countBy == "shown" {
					expectedShown = 2
				}
				if err != nil || status.ShownCount != expectedShown || status.CompletedCount != 0 || status.Offer == nil {
					t.Fatalf("展示不应伪造完播，入口供 Mini 自行判断: %+v %v", status, err)
				}
				for _, dramaID := range []string{"5008", "5009"} {
					_, err := service.ClaimReward(ctx, "viewer", monetization.ClaimRewardInput{ClaimID: "complete-" + dramaID, OfferID: offer.ID, CompletedAdUnitID: offer.AdUnitID, DramaID: dramaID, IsEnded: true})
					if err != nil {
						t.Fatal(err)
					}
				}
				other, err := service.EntryAdStatus(ctx, "viewer", "5009")
				if err != nil {
					t.Fatal(err)
				}
				expectedCompleted := 0
				if countBy == "completed" {
					expectedCompleted = 2
				}
				if scope == "user" && (other.ShownCount != expectedShown || other.CompletedCount != expectedCompleted) {
					t.Fatalf("所有剧应共享当前用户计数: %+v", other)
				}
				if countBy == "completed" {
					expectedCompleted = 1
				}
				if scope == "user_drama" && (other.ShownCount != 0 || other.CompletedCount != expectedCompleted) {
					t.Fatalf("每部剧应独立计数: %+v", other)
				}
				isolated, _ := service.EntryAdStatus(ctx, "another", "5008")
				if isolated.ShownCount != 0 || isolated.CompletedCount != 0 {
					t.Fatal("次数串到另一个用户")
				}
				now = now.Add(3 * time.Minute)
				next, err := service.EntryAdStatus(ctx, "viewer", "5008")
				if err != nil || next.ShownCount != 0 || next.CompletedCount != 0 {
					t.Fatalf("当地自然日未清零: %+v %v", next, err)
				}
			})
		}
	}
}

// entryFactFailureStore 注入展示写入和事实读取故障，用于验证诊断及错误传播。
type entryFactFailureStore struct {
	*memory.Store
	failure error
}

// RecordAdShow 仅在故障注入期间拒绝写入，不输出输入中的完整业务对象。
func (s *entryFactFailureStore) RecordAdShow(context.Context, string, monetization.AdShow) error {
	return s.failure
}

// CountAdShows 在展示读取边界返回实际依赖错误，不影响奖励表读取。
func (s *entryFactFailureStore) CountAdShows(context.Context, string, monetization.AdShowQuery) (int, error) {
	return 0, s.failure
}

// TestEntryAdFactFailureLogs 验证失败有具体日志且不会吞掉错误或泄漏完整广告请求。
func TestEntryAdFactFailureLogs(t *testing.T) {
	now := time.Now()
	store, service, ctx := entryModeFixture("user", "shown", func() time.Time { return now })
	status, err := service.EntryAdStatus(ctx, "viewer", "5008")
	if err != nil {
		t.Fatal(err)
	}
	broken := &entryFactFailureStore{Store: store, failure: errors.New("database write connection lost")}
	service = domain.NewService(broken, func() time.Time { return now })
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	if _, err := service.EntryAdStatus(ctx, "viewer", "5008"); !errors.Is(err, broken.failure) {
		t.Fatal("读取失败被吞掉")
	}
	err = service.RecordAdShow(ctx, "viewer", monetization.AdShowInput{EventID: "private-show-context", Scene: monetization.AdSceneEntryReward, OccurredAt: now, AdUnitID: status.Offer.AdUnitID, DramaID: "5008"})
	if !errors.Is(err, broken.failure) || !strings.Contains(logs.String(), "count entry ad facts failed") || !strings.Contains(logs.String(), "save ad show failed") || !strings.Contains(logs.String(), broken.failure.Error()) || strings.Contains(logs.String(), "private-show-context") {
		t.Fatal("故障诊断或脱敏不正确")
	}
	completeStore, _, completeCtx := entryModeFixture("user_drama", "completed", func() time.Time { return now })
	completeService := domain.NewService(&entryFactFailureStore{Store: completeStore, failure: broken.failure}, func() time.Time { return now })
	if status, err := completeService.EntryAdStatus(completeCtx, "viewer", "5008"); err != nil || status.CountBy != "completed" {
		t.Fatalf("完整观看不应依赖展示表: %+v %v", status, err)
	}
}
