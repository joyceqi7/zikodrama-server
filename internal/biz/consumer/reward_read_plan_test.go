package consumer_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
)

// rewardReadStore 记录真实结算收到的当前规则与读取需求，不替换发奖逻辑。
type rewardReadStore struct {
	domain.Store
	directoryReads int
	catalogReads   int
	grant          monetization.RewardGrant
}

// FindDrama 统计本次领奖目录读取次数，仍返回真实测试目录。
func (s *rewardReadStore) FindDrama(ctx context.Context, id string) (*domain.Drama, error) {
	s.directoryReads++
	return s.Store.FindDrama(ctx, id)
}

// HasCatalogEntitlement 检查逐集准备是否重复读取目标验权已包含的目录权益。
func (s *rewardReadStore) HasCatalogEntitlement(ctx context.Context, userID string, now time.Time) (bool, error) {
	s.catalogReads++
	return s.Store.HasCatalogEntitlement(ctx, userID, now)
}

// GrantReward 保留进入用户锁前的构造需求，随后执行真实原子结算。
func (s *rewardReadStore) GrantReward(ctx context.Context, grant monetization.RewardGrant, now time.Time) (monetization.ClaimResult, error) {
	s.grant = grant
	return s.Store.GrantReward(ctx, grant, now)
}

// TestRewardReadsFollowAllGrants 验证完整授予组合决定数据范围，结算目录只批量读取本剧权益，不读全量用户权益。
func TestRewardReadsFollowAllGrants(t *testing.T) {
	episode := func(count int) monetization.GrantTemplate {
		raw, _ := json.Marshal(map[string]any{"selection": "next_locked_episodes", "count": count, "validity": "unlimited"})
		return monetization.GrantTemplate{Type: "episode_access", Config: raw}
	}
	drama := func(future bool) monetization.GrantTemplate {
		raw, _ := json.Marshal(map[string]any{"selection": "current_drama", "includeFutureEpisodes": future, "validity": "unlimited"})
		return monetization.GrantTemplate{Type: "drama_access", Config: raw}
	}
	for _, tc := range []struct {
		name       string
		grants     []monetization.GrantTemplate
		continuous bool
		candidates []string
		whole      []string
		awarded    []string
	}{
		{"单集", []monetization.GrantTemplate{episode(1)}, false, []string{"5014"}, nil, []string{"5014"}},
		{"组合逐集取最大候选需求", []monetization.GrantTemplate{episode(1), episode(3)}, false, []string{"5014", "5015", "5016", "5017"}, nil, []string{"5014", "5015", "5016"}},
		{"整剧快照", []monetization.GrantTemplate{drama(false)}, false, nil, []string{"5014", "5015", "5016", "5017"}, []string{"5014", "5015", "5016", "5017"}},
		{"整剧含未来仍保存当前事实", []monetization.GrantTemplate{drama(true)}, false, nil, []string{"5014", "5015", "5016", "5017"}, []string{"5014", "5015", "5016", "5017"}},
		{"单集组合整剧", []monetization.GrantTemplate{episode(1), drama(false)}, false, []string{"5014"}, []string{"5014", "5015", "5016", "5017"}, []string{"5014", "5015", "5016", "5017"}},
		{"单集连续提示", []monetization.GrantTemplate{episode(1)}, true, []string{"5014"}, nil, []string{"5014"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _, store, input := consistencyFixture(t, 1, 0, func(config *monetization.ApplicationMonetizationConfig) {
				config.Document.Rules[0].Grants = tc.grants
				config.Document.Rules[0].Config, _ = json.Marshal(map[string]any{"requiredAdCount": 1, "autoUnlock": map[string]any{"enabled": tc.continuous, "countdownSeconds": 3}})
			})
			counted := &rewardReadStore{Store: store}
			service := domain.NewService(counted, func() time.Time { return time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC) })
			result, err := service.ClaimReward(ctx, "visitor-1", input)
			if err != nil || result.Playback == nil {
				t.Fatalf("领奖和播放授权失败: %+v %v", result, err)
			}
			if counted.directoryReads != 1 || counted.catalogReads != 0 {
				t.Fatalf("目录应只读一次且目录级验权不重复: directory=%d catalog=%d", counted.directoryReads, counted.catalogReads)
			}
			grant := counted.grant
			if !reflect.DeepEqual(grant.BuildContext.EpisodeIDs, tc.candidates) || !reflect.DeepEqual(grant.OwnershipEpisodeIDs, []string{"5014", "5015", "5016", "5017"}) || !reflect.DeepEqual(grant.BuildContext.AllEpisodeIDs, tc.whole) {
				t.Fatalf("数据需求错误: candidates=%v ownership=%v whole=%v", grant.BuildContext.EpisodeIDs, grant.OwnershipEpisodeIDs, grant.BuildContext.AllEpisodeIDs)
			}
			settled, found, err := store.FindRewardGrant(ctx, "visitor-1", input.ClaimID)
			if err != nil || !found || !reflect.DeepEqual(settled.UnlockEpisodeIDs, tc.awarded) || settled.BuildRule != nil || settled.BuildContext != nil {
				t.Fatalf("应只保存实际授予事实而非构造规则: %+v %v", settled, err)
			}
		})
	}
}

// TestRewardClaimNeverFallsBackToAnotherRule 防止规则移除后将旧入口当同类广告的别名。
func TestRewardClaimNeverFallsBackToAnotherRule(t *testing.T) {
	ctx, service, store, input := consistencyFixture(t, 1, 0)
	config, err := store.GetApplicationMonetization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := config.Policy()
	input.OfferID = old.OfferIDForKind(monetization.AccessOfferRewardedEpisode)
	// 文档规则仍名为 episode；固定类型 rewarded_episode 不能映射到它。
	if result, err := service.ClaimReward(ctx, "visitor-1", input); err == nil || result != nil {
		t.Fatalf("旧规则别名不应领取另一规则: %+v %v", result, err)
	}
}
