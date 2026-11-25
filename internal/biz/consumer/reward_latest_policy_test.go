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
	"drama-server/internal/testsupport/memory"
)

// vipRecoveryStore 仅在恢复已完成领取时注入依赖失败，实际权益与幂等仍由内存仓储处理。
type vipRecoveryStore struct {
	domain.Store
	policyError    error
	reconcileError error
	claimError     error
}

// GetApplicationMonetization 返回当前配置或指定故障，不回退历史领取条款。
func (s *vipRecoveryStore) GetApplicationMonetization(ctx context.Context) (monetization.ApplicationMonetizationConfig, error) {
	if s.policyError != nil {
		return monetization.ApplicationMonetizationConfig{}, s.policyError
	}
	return s.Store.GetApplicationMonetization(ctx)
}

// FindVIPProgress 返回当前阈值下的只读进度；故障不会改写已结算领取。
func (s *vipRecoveryStore) FindVIPProgress(ctx context.Context, visitorID string, policy monetization.MonetizationPolicy, period monetization.RewardPeriod, now time.Time) (monetization.VIPProgress, error) {
	if s.reconcileError != nil {
		return monetization.VIPProgress{}, s.reconcileError
	}
	return s.Store.FindVIPProgress(ctx, visitorID, policy, period, now)
}

// ReconcileVIPProgress 在显式领取写入时注入故障，验证错误传播和诊断日志。
func (s *vipRecoveryStore) ReconcileVIPProgress(ctx context.Context, visitorID string, policy monetization.MonetizationPolicy, period monetization.RewardPeriod, now time.Time) (monetization.VIPProgress, error) {
	if s.claimError != nil {
		return monetization.VIPProgress{}, s.claimError
	}
	return s.Store.ReconcileVIPProgress(ctx, visitorID, policy, period, now)
}

// TestManualVIPClaimFailureLogsAndPreservesProgress 验证领取写入失败不激活权益，重试仍可领取。
func TestManualVIPClaimFailureLogsAndPreservesProgress(t *testing.T) {
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	store := &vipRecoveryStore{Store: memory.NewStore(monetizationSeed(applicationMonetization(true, 0, 1)))}
	service := domain.NewService(store, func() time.Time { return now })
	ctx := application.WithTimeZone(context.Background(), memory.TestApplicationID, "UTC")
	play, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5015")
	if err != nil || play.VIPOffer == nil {
		t.Fatalf("缺少 VIP 广告入口: %+v %v", play, err)
	}
	_, err = service.ClaimReward(ctx, "visitor-1", monetization.ClaimRewardInput{
		ClaimID: "vip-manual-claim-log", OfferID: play.VIPOffer.ID,
		CompletedAdUnitID: play.VIPOffer.AdUnitID, IsEnded: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	failure := errors.New("vip entitlement write refused")
	store.claimError = failure
	if _, err := service.ClaimVIP(ctx, "visitor-1"); err == nil || !strings.Contains(logs.String(), "grant manually claimed vip failed") || !strings.Contains(logs.String(), failure.Error()) {
		t.Fatalf("领取故障缺少实际错误日志或传播: err=%v logs=%s", err, logs.String())
	}
	state, err := service.VIPProgress(ctx, "visitor-1")
	if err != nil || state.Active || state.AdCount != 1 {
		t.Fatalf("失败后进度或权益异常: %+v %v", state, err)
	}
	logs.Reset()
	store.claimError = nil
	state, err = service.ClaimVIP(ctx, "visitor-1")
	if err != nil || !state.Active || strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Fatalf("重试成功应激活且无错误日志: %+v %v %s", state, err, logs.String())
	}
}

// TestVIPRewardRetryUsesCurrentRule 验证已成功领取的重试按当前同一规则展示进度，不重复广告次数。
func TestVIPRewardRetryUsesCurrentRule(t *testing.T) {
	for _, tc := range []struct {
		name       string
		required   int
		removeRule bool
		active     bool
	}{
		{"提高阈值", 5, false, false},
		{"降低阈值等待手动领取", 1, false, false},
		{"删除后不借其他规则", 0, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
			config := applicationMonetization(true, 0, 3)
			config.StrategyID = "strategy-a"
			config.Document = &monetization.StrategyDocument{SchemaVersion: 1, Enabled: true, Rules: []monetization.UnlockRule{{
				Code: "daily_vip", Type: "rewarded_ad", Enabled: true, Config: json.RawMessage(`{"requiredAdCount":3}`),
				Grants: []monetization.GrantTemplate{{Type: "vip", Config: json.RawMessage(`{"scope":"application","validity":"day"}`)}},
			}}}
			config.Bindings = map[string]monetization.RuleBinding{"daily_vip": {AdUnitID: "vip-ad"}}
			store := memory.NewStore(monetizationSeed(config))
			dependencies := &vipRecoveryStore{Store: store}
			service := domain.NewService(dependencies, func() time.Time { return now })
			ctx := application.WithTimeZone(context.Background(), memory.TestApplicationID, "UTC")
			play, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5014")
			if err != nil || play.VIPOffer == nil {
				t.Fatalf("缺少 VIP 入口: %+v %v", play, err)
			}
			input := monetization.ClaimRewardInput{ClaimID: "vip-claim", OfferID: play.VIPOffer.ID, CompletedAdUnitID: "vip-ad", IsEnded: true}
			first, err := service.ClaimReward(ctx, "visitor-1", input)
			if err != nil || first.VIPProgress.AdCount != 1 || first.VIPProgress.Active {
				t.Fatalf("首次累计错误: %+v %v", first, err)
			}
			config.Revision++
			config.Document.Rules[0].Config, _ = json.Marshal(map[string]int{"requiredAdCount": max(1, tc.required)})
			if tc.removeRule {
				config.Document.Rules[0].Code = "another_vip"
				config.Bindings = map[string]monetization.RuleBinding{"another_vip": {AdUnitID: "vip-ad"}}
			}
			if err := store.SaveApplicationMonetization(ctx, config); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				result, err := service.ClaimReward(ctx, "visitor-1", input)
				if err != nil || result.Status != monetization.ClaimStatusAlreadyGranted || result.VIPProgress.AdCount != 1 || result.VIPProgress.RequiredCount != tc.required || result.VIPProgress.Active != tc.active {
					t.Fatalf("恢复应使用同规则最新状态且不重复累计: %+v %v", result, err)
				}
			}
			settled, _, err := store.FindRewardGrant(ctx, "visitor-1", input.ClaimID)
			if err != nil || settled.RequiredCount != 3 {
				t.Fatalf("原结算事实不应被最新配置改写: %+v %v", settled, err)
			}
			if tc.name == "提高阈值" {
				assertVIPRecoveryFailureLogging(t, ctx, service, dependencies, input)
			}
		})
	}
}

// assertVIPRecoveryFailureLogging 验证恢复失败保留操作、实际错误与原领取，成功恢复不输出错误。
func assertVIPRecoveryFailureLogging(t *testing.T, ctx context.Context, service *domain.Service, store *vipRecoveryStore, input monetization.ClaimRewardInput) {
	t.Helper()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, policyFailure := range []bool{true, false} {
		logs.Reset()
		failure := errors.New("vip recovery dependency refused")
		operation := "find settled rule vip progress failed"
		if policyFailure {
			store.policyError = failure
			operation = "read current vip reward policy failed"
		} else {
			store.reconcileError = failure
		}
		result, err := service.ClaimReward(ctx, "visitor-1", input)
		if err == nil || result != nil || !strings.Contains(logs.String(), operation) || !strings.Contains(logs.String(), failure.Error()) {
			t.Fatalf("恢复失败缺少诊断或错误未传播: %+v %v %s", result, err, logs.String())
		}
		store.policyError, store.reconcileError = nil, nil
	}
	logs.Reset()
	result, err := service.ClaimReward(ctx, "visitor-1", input)
	if err != nil || result.Status != monetization.ClaimStatusAlreadyGranted || result.VIPProgress.AdCount != 1 || strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Fatalf("恢复成功应保持一次累计且无错误日志: %+v %v %s", result, err, logs.String())
	}
}
