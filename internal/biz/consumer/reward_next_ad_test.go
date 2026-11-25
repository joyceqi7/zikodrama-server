package consumer_test

import (
	"bytes"
	"context"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/testsupport/memory"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// nextAdFailureStore 仅在恢复既有领取后模拟提示查询故障，不影响原奖励事务。
type nextAdFailureStore struct {
	domain.Store
	fail       bool
	listCalls  int
	countCalls int
}

// ListEntitlements 在故障开关打开时拒绝提示查询，其余行为交给原仓储。
func (s *nextAdFailureStore) ListEntitlements(ctx context.Context, visitorID string, now time.Time) ([]monetization.EntitlementGrant, error) {
	s.listCalls++
	if s.fail {
		return nil, errors.New("next ad ownership unavailable")
	}
	return s.Store.ListEntitlements(ctx, visitorID, now)
}

// CountRewardGrants 记录业务层是否在事务完成后重复读取日限额。
func (s *nextAdFailureStore) CountRewardGrants(ctx context.Context, visitorID, rewardType string, since time.Time, progressKey ...string) (int, error) {
	s.countCalls++
	return s.Store.CountRewardGrants(ctx, visitorID, rewardType, since, progressKey...)
}

// TestRewardNextAdDecision 验证结算后由服务端统一决定继续、批量跳集、关闭和限额。
func TestRewardNextAdDecision(t *testing.T) {
	for _, tc := range []struct {
		name         string
		enabled      bool
		count, limit int
		want         string
	}{
		{"继续下一集", true, 1, 0, "5015"}, {"批量解锁跳两集", true, 2, 0, "5016"},
		{"全部解锁停止", true, 3, 0, ""}, {"配置关闭停止", false, 1, 0, ""}, {"达到日限额停止", true, 1, 1, ""},
		{"提示查询失败保留结算", true, 1, 0, "5015"}, {"下一集媒资未就绪", true, 1, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			defer slog.SetDefault(previous)
			config := applicationMonetization(false, 0, 1)
			config.VIPAdUnitCode = ""
			config.Document = &monetization.StrategyDocument{SchemaVersion: 1, Enabled: true, Rules: []monetization.UnlockRule{{
				Code: "episode", Type: "rewarded_ad", Enabled: true,
				Config: json.RawMessage(fmt.Sprintf(`{"requiredAdCount":1,"dailyRewardLimit":%d,"autoUnlock":{"enabled":%t,"countdownSeconds":3}}`, tc.limit, tc.enabled)),
				Grants: []monetization.GrantTemplate{{Type: "episode_access", Config: json.RawMessage(fmt.Sprintf(`{"selection":"next_locked_episodes","count":%d,"validity":"unlimited"}`, tc.count))}},
			}}}
			config.Bindings = map[string]monetization.RuleBinding{"episode": {AdUnitID: "episode-ad"}}
			seed := monetizationSeed(config)
			seed.Dramas[0].Episodes = append(seed.Dramas[0].Episodes, domain.Episode{ID: "5016", DramaID: "5008", Number: 3, PlatformEpisodeID: "p3", VID: "v3", MediaStatus: "ready"})
			if tc.name == "下一集媒资未就绪" {
				seed.Dramas[0].Episodes[1].MediaStatus = "pending"
			}
			store := &nextAdFailureStore{Store: memory.NewStore(seed)}
			service := domain.NewService(store, func() time.Time { return time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC) })
			ctx := application.WithTimeZone(context.Background(), memory.TestApplicationID, "UTC")
			play, err := service.ResolveOfficialPlay(ctx, "visitor-1", "5008", "5014")
			if err != nil || play.Offer == nil {
				t.Fatalf("无法取得广告入口: %v %+v", err, play)
			}
			input := monetization.ClaimRewardInput{ClaimID: "next-ad-test", OfferID: play.Offer.ID, DramaID: "5008", EpisodeID: "5014", CompletedAdUnitID: play.Offer.AdUnitID, IsEnded: true}
			for i := 0; i < 2; i++ {
				callsBefore := store.listCalls
				countCallsBefore := store.countCalls
				store.fail = i == 1 && tc.name == "提示查询失败保留结算"
				result, err := service.ClaimReward(ctx, "visitor-1", input)
				if err != nil || !result.Unlocked {
					t.Fatalf("结算失败: %v", err)
				}
				if result.Playback == nil || result.Playback.Status != domain.PlayStatusPlayable {
					t.Fatalf("逐集奖励应直接返回当前集播放授权: %+v", result.Playback)
				}
				if i == 0 && store.listCalls-callsBefore != 0 {
					t.Fatalf("首次结算应只在事务内读取权益，业务层额外读取次数=%d", store.listCalls-callsBefore)
				}
				if i == 0 && store.countCalls != countCallsBefore {
					t.Fatalf("首次结算不应在事务后重复读取奖励次数")
				}
				if store.fail {
					if result.NextAd != nil || result.Status != monetization.ClaimStatusAlreadyGranted || !strings.Contains(logs.String(), "next ad ownership unavailable") {
						t.Fatalf("提示故障影响了结算或缺失诊断: %+v", result)
					}
					continue
				}
				if tc.want == "" {
					if result.NextAd != nil {
						t.Fatalf("不应继续提示: %+v", result.NextAd)
					}
					continue
				}
				if result.NextAd == nil || result.NextAd.EpisodeID != tc.want || result.NextAd.UnlockedCount != tc.count {
					t.Fatalf("下一步决策错误: %+v", result.NextAd)
				}
			}
		})
	}
}
