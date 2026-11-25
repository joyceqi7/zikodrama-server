package consumer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/pkg/requesttime"
	"drama-server/internal/testsupport/memory"
)

// settlementStateFailure 仅使附加状态读取失败，奖励事务与主响应仍使用真实测试仓储。
type settlementStateFailure struct{ domain.Store }

// CountEntryAdCompletions 注入结算后的进度读取故障，不影响底层事务写入。
func (s settlementStateFailure) CountEntryAdCompletions(context.Context, string, string, string, time.Time, time.Time) (int, error) {
	return 0, errors.New("settled entry state unavailable")
}

// FindOwnedEpisodeIDs 注入幂等恢复时的目录权益查询故障。
func (s settlementStateFailure) FindOwnedEpisodeIDs(context.Context, string, string, []string, time.Time) (map[string]bool, error) {
	return nil, errors.New("settled ownership unavailable")
}

// TestSettlementStateFailureKeepsCommittedReward 验证新增状态失败有实际日志，且不把已成功奖励返回为失败。
func TestSettlementStateFailureKeepsCommittedReward(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	now := time.Date(2026, 10, 24, 1, 0, 0, 0, time.UTC)
	store, service, ctx := entryModeFixture("user", "completed", func() time.Time { return now })
	entry, err := service.EntryAdStatus(ctx, "viewer", "5008")
	if err != nil {
		t.Fatal(err)
	}
	faulty := domain.NewService(settlementStateFailure{Store: store}, func() time.Time { return now })
	input := monetization.ClaimRewardInput{ClaimID: "entry-state-failure", OfferID: entry.Offer.ID, CompletedAdUnitID: entry.Offer.AdUnitID, DramaID: "5008", IsEnded: true}
	first, err := faulty.ClaimReward(ctx, "viewer", input)
	if err != nil || first == nil || first.Status != "granted" || first.EntryAdStatus != nil || !strings.Contains(logs.String(), "settled entry state unavailable") {
		t.Fatalf("附加进度故障不能抹掉成功结算: %+v %v %s", first, err, logs.String())
	}
	logs.Reset()
	episodeCtx, episodeService, episodeStore, episodeInput := consistencyFixture(t, 1, 0)
	if _, err := episodeService.ClaimReward(episodeCtx, "visitor-1", episodeInput); err != nil {
		t.Fatal(err)
	}
	faulty = domain.NewService(settlementStateFailure{Store: episodeStore}, func() time.Time { return time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC) })
	retry, err := faulty.ClaimReward(episodeCtx, "visitor-1", episodeInput)
	if err != nil || retry == nil || retry.Status != "already_granted" || retry.Drama != nil || retry.Playback == nil || !strings.Contains(logs.String(), "settled ownership unavailable") {
		t.Fatalf("附加目录故障不能抹掉幂等结果: %+v %v %s", retry, err, logs.String())
	}
}

// TestEntrySettlementReturnsReceiptDayState 验证回包直接给出已提交进度，跨日重试只返回今日状态而不重计旧事实。
func TestEntrySettlementReturnsReceiptDayState(t *testing.T) {
	for _, mode := range []string{"completed", "shown"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Date(2026, 10, 23, 15, 59, 0, 0, time.UTC)
			_, service, ctx := entryModeFixture("user_drama", mode, func() time.Time { return now })
			status, err := service.EntryAdStatus(ctx, "viewer", "5008")
			if err != nil {
				t.Fatal(err)
			}
			input := monetization.ClaimRewardInput{ClaimID: "receipt-state", OfferID: status.Offer.ID, CompletedAdUnitID: status.Offer.AdUnitID, DramaID: "5008", IsEnded: true}
			if mode == "shown" {
				if err := service.RecordAdShow(ctx, "viewer", monetization.AdShowInput{EventID: input.ClaimID, AdUnitID: input.CompletedAdUnitID, Scene: monetization.AdSceneEntryReward, DramaID: input.DramaID}); err != nil {
					t.Fatal(err)
				}
			}
			first, err := service.ClaimReward(ctx, "viewer", input)
			if err != nil || first.EntryAdStatus == nil {
				t.Fatalf("缺少结算状态: %+v %v", first, err)
			}
			state := first.EntryAdStatus
			if state.ProgressDate != "2026-10-23" || state.CountBy != mode || mode == "completed" && state.CompletedCount != 1 || mode == "shown" && state.ShownCount != 1 {
				t.Fatalf("结算状态错误: %+v", state)
			}
			now = now.Add(2 * time.Minute)
			retry, err := service.ClaimReward(ctx, "viewer", input)
			if err != nil || retry.Status != "already_granted" || retry.EntryAdStatus == nil || retry.EntryAdStatus.ProgressDate != "2026-10-24" || retry.EntryAdStatus.CompletedCount != 0 || retry.EntryAdStatus.ShownCount != 0 {
				t.Fatalf("重试须返回今日零进度，旧事实不得重计: %+v %v", retry, err)
			}
			// 新完播今天首次到达；即使广告使用昨天的 Offer，也只能计入今天。
			input.ClaimID = "new-day-state"
			fresh, err := service.ClaimReward(ctx, "viewer", input)
			if err != nil || fresh.EntryAdStatus == nil || fresh.EntryAdStatus.ProgressDate != "2026-10-24" || mode == "completed" && fresh.EntryAdStatus.CompletedCount != 1 {
				t.Fatalf("新事实未归接收日: %+v %v", fresh, err)
			}
		})
	}
}

// TestEpisodeSettlementReturnsCurrentDirectory 验证初次和重试都返回权威目录，撤销权益后不能复用原解锁快照。
func TestEpisodeSettlementReturnsCurrentDirectory(t *testing.T) {
	ctx, service, store, input := consistencyFixture(t, 2, 0)
	first, err := service.ClaimReward(ctx, "visitor-1", input)
	if err != nil || first.Drama == nil || first.Playback == nil {
		t.Fatalf("缺少结算状态: %+v %v", first, err)
	}
	assertModes := func(state *domain.RewardDramaState, modes ...string) {
		t.Helper()
		if state == nil || len(state.Episodes) != len(modes) {
			t.Fatalf("目录不完整: %+v", state)
		}
		for i, mode := range modes {
			if state.Episodes[i].AccessMode != mode {
				t.Fatalf("第 %d 集状态错误: %+v", i+1, state.Episodes[i])
			}
		}
	}
	assertModes(first.Drama, "unlocked", "unlocked", "rewarded", "rewarded")
	retry, err := service.ClaimReward(ctx, "visitor-1", input)
	if err != nil || retry.Status != "already_granted" {
		t.Fatalf("恢复失败: %+v %v", retry, err)
	}
	assertModes(retry.Drama, "unlocked", "unlocked", "rewarded", "rewarded")
	if err := store.Store.(*memory.Store).RevokeEntitlementSource(ctx, "visitor-1", "reward_grant", input.ClaimID, time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	revoked, err := service.ClaimReward(ctx, "visitor-1", input)
	if err != nil || revoked.Playback != nil {
		t.Fatalf("撤销后不能返回起播许可: %+v %v", revoked, err)
	}
	assertModes(revoked.Drama, "rewarded", "rewarded", "rewarded", "rewarded")
	// 旧包按原结构解码；新增字段不改变旧字段名称、类型或已结算语义。
	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var old monetization.ClaimResult
	if err := json.Unmarshal(raw, &old); err != nil || old.ClaimID != input.ClaimID || !old.Unlocked || old.Status != "granted" {
		t.Fatalf("旧合同不兼容: %+v %v", old, err)
	}
}

// TestVIPSettlementReturnsCurrentDayState 验证 VIP 回包计数取自服务器账本，跨日原键不增加新日进度。
func TestVIPSettlementReturnsCurrentDayState(t *testing.T) {
	now := time.Date(2026, 10, 23, 23, 59, 0, 0, time.UTC)
	store := memory.NewStore(monetizationSeed(applicationMonetization(true, 0, 2)))
	service := domain.NewService(store, func() time.Time { return now })
	ctx := application.WithTimeZone(context.Background(), memory.TestApplicationID, "UTC")
	play, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5015")
	if err != nil || play.VIPOffer == nil {
		t.Fatalf("缺少入口: %v", err)
	}
	input := monetization.ClaimRewardInput{ClaimID: "vip-state", OfferID: play.VIPOffer.ID, CompletedAdUnitID: play.VIPOffer.AdUnitID, IsEnded: true}
	first, err := service.ClaimReward(ctx, "visitor-1", input)
	if err != nil || first.VIPProgress == nil || first.VIPProgress.AdCount != 1 {
		t.Fatalf("进度错误: %+v %v", first, err)
	}
	now = now.Add(2 * time.Minute)
	retry, err := service.ClaimReward(ctx, "visitor-1", input)
	if err != nil || retry.VIPProgress == nil || retry.VIPProgress.AdCount != 0 || retry.VIPProgress.ProgressDate != "2026-10-24" {
		t.Fatalf("昨日幂等应返回今日状态: %+v %v", retry, err)
	}
	input.ClaimID = "vip-before-midnight"
	before := now.Add(-2 * time.Minute)
	late, err := service.ClaimReward(requesttime.WithReceivedAt(ctx, before), "visitor-1", input)
	if err != nil || late.VIPProgress == nil || late.VIPProgress.AdCount != 2 || late.VIPProgress.ProgressDate != "2026-10-23" {
		t.Fatalf("处理跨日不改变接收日: %+v %v", late, err)
	}
}
