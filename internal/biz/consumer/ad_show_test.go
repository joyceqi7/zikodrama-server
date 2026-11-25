package consumer_test

import (
	"bytes"
	"context"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/testsupport/memory"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestAdShowHomeScene 验证首页示例复用展示事实，不依赖奖励规则、Offer、JSON 或剧目标。
func TestAdShowHomeScene(t *testing.T) {
	now := time.Date(2026, 10, 8, 15, 58, 0, 0, time.UTC)
	seed := monetizationSeed(applicationMonetization(false, 0, 0))
	seed.ApplicationMonetization = nil
	seed.Applications[0].AdUnits.Interstitial = []string{"home-ad"}
	store := memory.NewStore(seed)
	service := domain.NewService(store, func() time.Time { return now })
	ctx := application.WithTimeZone(context.Background(), memory.TestApplicationID, "Asia/Shanghai")
	for _, in := range []monetization.AdShowInput{
		{EventID: "home-event-0001", Scene: "home_entry", AdUnitID: "home-ad", OccurredAt: now},
		{EventID: "home-event-0002", Scene: "home_entry", AdUnitID: "home-ad", OccurredAt: now},
		{EventID: "entry-event-0001", Scene: monetization.AdSceneEntryReward, AdUnitID: "episode-ad", DramaID: "5008", OccurredAt: now},
		{EventID: "episode-event-0001", Scene: monetization.AdSceneEpisodeUnlock, AdUnitID: "episode-ad", DramaID: "5008", EpisodeID: "5014", OccurredAt: now},
		{EventID: "vip-event-0001", Scene: monetization.AdSceneVIPReward, AdUnitID: "vip-ad", OccurredAt: now},
	} {
		for range 2 {
			if err := service.RecordAdShow(ctx, "viewer", in); err != nil {
				t.Fatal(err)
			}
		}
	}
	status, err := service.AdShowStatus(ctx, "viewer", "home_entry", "", "")
	if err != nil || status.ShownCount != 2 || status.ProgressDate != "2026-10-08" || status.ValidUntil != "2026-10-08T16:00:00Z" {
		t.Fatalf("首页事实错误: %+v %v", status, err)
	}
	other, err := service.AdShowStatus(ctx, "another", "home_entry", "", "")
	if err != nil || other.ShownCount != 0 {
		t.Fatal("首页事实串用户", other, err)
	}
	entry, err := service.AdShowStatus(ctx, "viewer", monetization.AdSceneEntryReward, "5008", "")
	if err != nil || entry.ShownCount != 1 {
		t.Fatal("场景或剧目混计", entry, err)
	}
	ep, err := service.AdShowStatus(ctx, "viewer", monetization.AdSceneEpisodeUnlock, "5008", "5014")
	if err != nil || ep.ShownCount != 1 {
		t.Fatal("分集展示归属错误", ep, err)
	}
	completed, err := store.CountEntryAdCompletions(ctx, "viewer", "", "", now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil || completed != 0 {
		t.Fatal("展示不应产生奖励", completed, err)
	}
	if err := service.RecordAdShow(ctx, "viewer", monetization.AdShowInput{EventID: "invalid-unit-0001", Scene: "home_entry", AdUnitID: "unbound-ad", OccurredAt: now}); !errors.Is(err, monetization.ErrInvalidAdShow) {
		t.Fatalf("跨应用广告位未拒绝: %v", err)
	}
	if err := service.RecordAdShow(ctx, "viewer", monetization.AdShowInput{EventID: "invalid-episode-0001", Scene: "home_entry", AdUnitID: "home-ad", DramaID: "5008", EpisodeID: "9999", OccurredAt: now}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("错误内容关联未拒绝: %v", err)
	}
	now = now.Add(3 * time.Minute)
	next, err := service.AdShowStatus(ctx, "viewer", "home_entry", "", "")
	if err != nil || next.ShownCount != 0 {
		t.Fatal("新自然日未清零", next, err)
	}
}

// adUnitFailureStore 注入广告位依赖故障，展示业务不能忽略实际错误。
type adUnitFailureStore struct {
	*memory.Store
	failure error
}

// HasAdUnit 只为聚焦故障测试返回实际依赖错误。
func (s *adUnitFailureStore) HasAdUnit(context.Context, string) (bool, error) {
	return false, s.failure
}

// TestAdShowFailureLogs 验证广告位读取和通用计数故障传播，日志含操作与具体错误且不泄漏事件。
func TestAdShowFailureLogs(t *testing.T) {
	now := time.Now().UTC()
	store, _, ctx := entryModeFixture("user", "shown", func() time.Time { return now })
	failure := errors.New("ad facts database connection lost")
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	service := domain.NewService(&adUnitFailureStore{Store: store, failure: failure}, func() time.Time { return now })
	err := service.RecordAdShow(ctx, "viewer", monetization.AdShowInput{EventID: "private-event-0001", Scene: "home_entry", AdUnitID: "home-ad", OccurredAt: now})
	if !errors.Is(err, failure) {
		t.Fatal("广告位故障被吞掉", err)
	}
	service = domain.NewService(&entryFactFailureStore{Store: store, failure: failure}, func() time.Time { return now })
	if _, err := service.AdShowStatus(ctx, "viewer", "home_entry", "", ""); !errors.Is(err, failure) {
		t.Fatal("通用计数故障被吞掉", err)
	}
	for _, required := range []string{"check ad show unit failed", "count ad shows failed", failure.Error()} {
		if !strings.Contains(logs.String(), required) {
			t.Fatal("故障诊断缺失", required)
		}
	}
	if strings.Contains(logs.String(), "private-event-0001") {
		t.Fatal("日志泄漏完整事件")
	}
}
