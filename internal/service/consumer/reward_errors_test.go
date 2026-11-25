package consumer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/visitor"
	httpapi "drama-server/internal/service/consumer"
	"drama-server/internal/testsupport/memory"
)

// rewardFailureStore 在策略、广告位查询及奖励事务处注入故障，结算与权益仍使用真实测试仓储。
type rewardFailureStore struct {
	*memory.Store
	// policy 是当前请求读到的权威策略快照。
	policy monetization.ApplicationMonetizationConfig
	// policyErr 和 adUnitErr 分别模拟两个已知的吞错边界。
	policyErr, adUnitErr error
	// grantErr 模拟已认证用户进入奖励事务后的故障。
	grantErr error
	// adUnitRegistered 表示广告位查询成功后的真实业务结果。
	adUnitRegistered bool
}

func (store *rewardFailureStore) GetApplicationMonetization(context.Context) (monetization.ApplicationMonetizationConfig, error) {
	return store.policy, store.policyErr
}

func (store *rewardFailureStore) HasRewardedAdUnit(context.Context, string) (bool, error) {
	return store.adUnitRegistered, store.adUnitErr
}

// GrantReward 在指定故障消除后恢复真实结算，验证原领取请求可重试。
func (store *rewardFailureStore) GrantReward(ctx context.Context, grant monetization.RewardGrant, now time.Time) (monetization.ClaimResult, error) {
	if store.grantErr != nil {
		return monetization.ClaimResult{}, store.grantErr
	}
	return store.Store.GrantReward(ctx, grant, now)
}

// TestRewardMissingUserRemainsRetryable 验证内部路由故障保留诊断、返回503并允许原请求恢复。
func TestRewardMissingUserRemainsRetryable(t *testing.T) {
	store, handler, token, input := newRewardErrorFixture(t)
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)
	store.grantErr = fmt.Errorf("reward user row missing in bucket 727: %w: record not found", domain.ErrServiceUnavailable)
	response := requestReward(t, handler, token, input)
	assertIdentityError(t, response, 503, "service_unavailable")
	if !strings.Contains(logs.String(), "grant reward failed") || !strings.Contains(logs.String(), "record not found") || !strings.Contains(logs.String(), "bucket 727") {
		t.Fatal("奖励故障日志缺少操作或具体原因")
	}
	if strings.Contains(logs.String(), token) || strings.Contains(response.Body.String(), "bucket 727") {
		t.Fatal("内部诊断或凭据泄漏")
	}
	store.grantErr = nil
	logs.Reset()
	if response := requestReward(t, handler, token, input); response.Code != 201 {
		t.Fatal("故障恢复后原领取请求未成功")
	}
	if response := requestReward(t, handler, token, input); response.Code != 200 {
		t.Fatal("原领取请求重试未保持幂等")
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Fatal("正常结算误报错误日志")
	}
}

// TestRewardDependencyFailureRemainsRetryable 覆盖旧409反例，故障恢复后使用原ClaimID结算且不重复发奖。
func TestRewardDependencyFailureRemainsRetryable(t *testing.T) {
	for _, stage := range []string{"policy", "ad_unit"} {
		for _, failure := range []struct {
			name, code string
			status     int
			err        error
		}{
			{"DB不可用", "service_unavailable", 503, errors.New("database-secret")},
			{"DB超时", "request_timeout", 504, context.DeadlineExceeded},
			{"DB取消", "request_cancelled", 408, context.Canceled},
		} {
			t.Run(stage+"/"+failure.name, func(t *testing.T) {
				store, handler, token, input := newRewardErrorFixture(t)
				if stage == "policy" {
					store.policyErr = failure.err
				} else {
					store.adUnitErr = failure.err
				}
				response := requestReward(t, handler, token, input)
				assertIdentityError(t, response, failure.status, failure.code)
				store.policyErr, store.adUnitErr = nil, nil
				response = requestReward(t, handler, token, input)
				if response.Code != http.StatusCreated {
					t.Fatalf("依赖恢复后原领取请求未结算: %d %s", response.Code, response.Body.String())
				}
				response = requestReward(t, handler, token, input)
				if response.Code != http.StatusOK {
					t.Fatalf("同ClaimID重复请求未幂等恢复: %d %s", response.Code, response.Body.String())
				}
			})
		}
	}
}

// TestInvalidRewardStillReturnsConflict 保留真实业务拒绝，不把无效Offer或停用策略当作可重试依赖故障。
func TestInvalidRewardStillReturnsConflict(t *testing.T) {
	for _, scenario := range []string{"disabled_policy", "missing_policy", "invalid_offer", "unregistered_ad", "wrong_completed_ad", "not_ended"} {
		t.Run(scenario, func(t *testing.T) {
			store, handler, token, input := newRewardErrorFixture(t)
			switch scenario {
			case "disabled_policy":
				store.policy.Status = monetization.MonetizationDisabled
			case "missing_policy":
				store.policyErr = domain.ErrNotFound
			case "invalid_offer":
				input.OfferID = "invalid"
			case "unregistered_ad":
				store.adUnitRegistered = false
			case "wrong_completed_ad":
				input.CompletedAdUnitID = "other-ad"
			case "not_ended":
				input.IsEnded = false
			}
			assertIdentityError(t, requestReward(t, handler, token, input), http.StatusConflict, "invalid_reward_claim")
		})
	}
}

// TestRewardClaimUsesLatestPolicy 验证 Offer 生成后策略变化不会造成版本冲突，领取按最新策略执行。
func TestRewardClaimUsesLatestPolicy(t *testing.T) {
	store, handler, token, input := newRewardErrorFixture(t)
	store.policy.VIP.RequiredAdCount = 2
	if response := requestReward(t, handler, token, input); response.Code != http.StatusCreated {
		t.Fatalf("策略更新后未按最新配置发放: %d %s", response.Code, response.Body.String())
	}
}

// TestDailyRewardBusinessLimitIsPreserved 每日奖励次数是运营业务规则，不随入口限流一起删除。
func TestDailyRewardBusinessLimitIsPreserved(t *testing.T) {
	_, handler, token, input := newRewardErrorFixture(t)
	if response := requestReward(t, handler, token, input); response.Code != http.StatusCreated {
		t.Fatalf("第一次有效奖励未成功: %d %s", response.Code, response.Body.String())
	}
	input.ClaimID, input.EpisodeID = "claim_daily_limit_0002", "5015"
	assertIdentityError(t, requestReward(t, handler, token, input), http.StatusTooManyRequests, "rate_limited")
}

// newRewardErrorFixture 装配有效凭据、已上线剧集和真实内存结算，测试替身只在指定依赖边界注入故障。
func newRewardErrorFixture(t *testing.T) (*rewardFailureStore, http.Handler, string, monetization.ClaimRewardInput) {
	t.Helper()
	policy := monetization.ApplicationMonetizationConfig{
		ApplicationID: memory.TestApplicationID, Status: monetization.MonetizationEnabled,
		Strategy: monetization.MonetizationStrategyEpisodeUnlock, EpisodeRewardAdUnitCode: "rewarded-ad",
		EpisodeReward: monetization.EpisodeRewardPolicy{UnlockEpisodeCount: 1, DailyRewardLimit: 1},
	}
	store := &rewardFailureStore{policy: policy, adUnitRegistered: true, Store: memory.NewStore(memory.Seed{
		PublishedOwnerID:        memory.TestApplicationID,
		ApplicationMonetization: []monetization.ApplicationMonetizationConfig{policy},
		Applications: []application.Application{{TikTokAppID: memory.TestApplicationID, Status: application.StatusActive,
			AdUnits: application.AdUnits{RewardedVideo: []string{"rewarded-ad"}},
		}},
		Dramas: []domain.Drama{{ID: "5008", Title: "Test drama", PublishStatus: "published", PlatformAlbumID: "album-1", Episodes: []domain.Episode{
			{ID: "5014", DramaID: "5008", Number: 1, PlatformEpisodeID: "platform-1", VID: "vid-1", MediaStatus: "ready"},
			{ID: "5015", DramaID: "5008", Number: 2, PlatformEpisodeID: "platform-2", VID: "vid-2", MediaStatus: "ready"},
		}}},
	})}
	visitors := newTestVisitorService(t, store)
	created, err := visitors.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: memory.TestApplicationID})
	if err != nil {
		t.Fatal(err)
	}
	handler := newConsumerHandler(domain.NewService(store, func() time.Time { return time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC) }), visitors, httpapi.Config{})
	return store, handler, created.VisitorToken, monetization.ClaimRewardInput{
		ClaimID: "claim_dependency_0001", OfferID: policy.Policy().OfferIDForKind(monetization.AccessOfferRewardedEpisode),
		CompletedAdUnitID: "rewarded-ad", IsEnded: true, DramaID: "5008", EpisodeID: "5014",
	}
}

// requestReward 通过真实消费者Handler发起领取，不跳过身份、解码或错误映射层。
func requestReward(t *testing.T, handler http.Handler, token string, input monetization.ClaimRewardInput) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/reward-grants", bytes.NewReader(body))
	request.Header.Set("authorization", "Bearer "+token)
	request.Header.Set("accept-language", "zh")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
