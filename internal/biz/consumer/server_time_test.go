package consumer_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/pkg/requesttime"
)

// TestServerTimeAdShowKeepsReceiptDayAndOriginalRetry 验证手机偏差、请求处理跨日和次日补报均不改写首次接收日。
func TestServerTimeAdShowKeepsReceiptDayAndOriginalRetry(t *testing.T) {
	base, _, store, _ := rewardPoolFixture(t)
	base = application.WithTimeZone(base, application.IDFromContext(base), "Asia/Shanghai")
	before := time.Date(2026, 10, 23, 15, 59, 59, 0, time.UTC)
	after := before.Add(2 * time.Second)
	service := domain.NewService(store, func() time.Time { return after })
	first := requesttime.WithReceivedAt(base, before)
	next := requesttime.WithReceivedAt(base, after)
	show := monetization.AdShowInput{EventID: "midnight-original", Scene: "home_entry", AdUnitID: "interstitial", OccurredAt: before.AddDate(-1, 0, 0)}
	if err := service.RecordAdShow(first, "3", show); err != nil {
		t.Fatal(err)
	}
	show.OccurredAt = after.AddDate(1, 0, 0)
	if err := service.RecordAdShow(next, "3", show); err != nil {
		t.Fatal(err)
	}
	saved, found, err := store.FindAdShow(base, "3", show.EventID)
	if err != nil || !found || !saved.OccurredAt.Equal(before) {
		t.Fatalf("重试改写了原时间: %+v %v", saved, err)
	}
	for _, test := range []struct {
		ctx   context.Context
		date  string
		count int
	}{{first, "2026-10-23", 1}, {next, "2026-10-24", 0}} {
		status, err := service.AdShowStatus(test.ctx, "3", "home_entry", "", "")
		if err != nil || status.ProgressDate != test.date || status.ShownCount != test.count {
			t.Fatalf("展示未按服务器接收日计数: %+v %v", status, err)
		}
	}
	for _, change := range []func(*monetization.AdShowInput){
		func(in *monetization.AdShowInput) { in.AdUnitID = "ad-b" },
		func(in *monetization.AdShowInput) { in.Scene = "another_scene" },
		func(in *monetization.AdShowInput) { in.DramaID = "5008" },
		func(in *monetization.AdShowInput) { in.DramaID, in.EpisodeID = "5008", "5014" },
	} {
		conflict := show
		change(&conflict)
		if err := service.RecordAdShow(next, "3", conflict); !errors.Is(err, monetization.ErrInvalidAdShow) {
			t.Fatalf("同键修改不可变内容未拒绝: %v", err)
		}
	}
}

// TestServerTimeRewardReceiptAndRetry 验证完播独立按请求接收日结算，跨日重试恢复原账本且拒绝替换目标。
func TestServerTimeRewardReceiptAndRetry(t *testing.T) {
	base, _, store, policy := rewardPoolFixture(t)
	base = application.WithTimeZone(base, application.IDFromContext(base), "Asia/Shanghai")
	before := time.Date(2026, 10, 23, 15, 59, 59, 0, time.UTC)
	after := before.Add(2 * time.Second)
	service := domain.NewService(store, func() time.Time { return after })
	offer, err := service.RewardOffer(base, "3", policy, monetization.AccessOfferRewardedEntry, nil)
	if err != nil {
		t.Fatal(err)
	}
	input := monetization.ClaimRewardInput{ClaimID: "receipt-claim", OfferID: offer.ID, CompletedAdUnitID: offer.AdUnitID, DramaID: "5008", IsEnded: true}
	if _, err := service.ClaimReward(requesttime.WithReceivedAt(base, before), "3", input); err != nil {
		t.Fatal(err)
	}
	repeat, err := service.ClaimReward(requesttime.WithReceivedAt(base, after), "3", input)
	if err != nil || repeat.Status != monetization.ClaimStatusAlreadyGranted {
		t.Fatalf("跨日重试未恢复: %+v %v", repeat, err)
	}
	grant, found, err := store.FindRewardGrant(base, "3", input.ClaimID)
	if err != nil || !found || !grant.GrantedAt.Equal(before) || grant.PeriodKey != "2026-10-23" {
		t.Fatalf("奖励未保留请求接收时间: %+v %v", grant, err)
	}
	for _, change := range []func(*monetization.ClaimRewardInput){
		func(in *monetization.ClaimRewardInput) { in.CompletedAdUnitID = "ad-b" },
		func(in *monetization.ClaimRewardInput) { in.DramaID = "other-drama" },
		func(in *monetization.ClaimRewardInput) { in.EpisodeID = "5014" },
		func(in *monetization.ClaimRewardInput) { in.OfferID = "invalid-offer" },
	} {
		conflict := input
		change(&conflict)
		if _, err := service.ClaimReward(requesttime.WithReceivedAt(base, after), "3", conflict); !errors.Is(err, monetization.ErrInvalidRewardClaim) {
			t.Fatalf("同键改写奖励内容未拒绝: %v", err)
		}
	}
	status, err := service.EntryAdStatus(requesttime.WithReceivedAt(base, after), "3", "5008")
	if err != nil || status.CompletedCount != 0 {
		t.Fatalf("昨日重试不得占用今日进度: %+v %v", status, err)
	}
	// 展示与完播是两次独立请求：同一广告跨午夜结束时分别保留各自接收日。
	show := monetization.AdShowInput{EventID: "midnight-separated", Scene: monetization.AdSceneEntryReward, AdUnitID: input.CompletedAdUnitID, DramaID: "5008"}
	if err := service.RecordAdShow(requesttime.WithReceivedAt(base, before), "3", show); err != nil {
		t.Fatal(err)
	}
	input.ClaimID = show.EventID
	if _, err := service.ClaimReward(requesttime.WithReceivedAt(base, after), "3", input); err != nil {
		t.Fatal(err)
	}
	savedShow, _, err := store.FindAdShow(base, "3", show.EventID)
	if err != nil || !savedShow.OccurredAt.Equal(before) {
		t.Fatalf("展示应保留午夜前的接收日: %+v %v", savedShow, err)
	}
	newGrant, _, err := store.FindRewardGrant(base, "3", input.ClaimID)
	if err != nil || !newGrant.GrantedAt.Equal(after) || newGrant.PeriodKey != "2026-10-24" {
		t.Fatalf("完播应独立计入午夜后: %+v %v", newGrant, err)
	}
}

type originalShowFailureStore struct{ domain.Store }

// FindAdShow 模拟原事实读取失败，业务层须记录实际原因并保留可重试错误。
func (s originalShowFailureStore) FindAdShow(context.Context, string, string) (monetization.AdShow, bool, error) {
	return monetization.AdShow{}, false, errors.New("original show storage unavailable")
}

// TestServerTimeOriginalShowFailureLog 验证新增幂等读取失败的诊断、传播及隐私。
func TestServerTimeOriginalShowFailureLog(t *testing.T) {
	ctx, _, store, _ := rewardPoolFixture(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	service := domain.NewService(originalShowFailureStore{store}, time.Now)
	err := service.RecordAdShow(ctx, "3", monetization.AdShowInput{EventID: "private-event", AdUnitID: "interstitial", Scene: "home_entry"})
	if err == nil || !strings.Contains(logs.String(), "find original ad show failed") || !strings.Contains(logs.String(), "original show storage unavailable") || strings.Contains(logs.String(), "private-event") {
		t.Fatalf("原事实失败日志不完整或泄露事件: %v %s", err, logs.String())
	}
}
