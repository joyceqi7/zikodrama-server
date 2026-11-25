package consumer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
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

// countingLoginProvider 记录官方换码次数，不连接真实 TikTok。
type countingLoginProvider struct{ calls int }

// ExchangeCode 为测试建立固定官方身份，后续查询不得再次调用。
func (p *countingLoginProvider) ExchangeCode(context.Context, string) (visitor.Identity, error) {
	p.calls++
	return visitor.Identity{OpenID: "existing-tiktok-user"}, nil
}

func TestAdExperienceReusesAuthenticatedSessionAndPreservesBootstrap(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "VIP关闭", true: "VIP开启"}[enabled], func(t *testing.T) {
			store := memory.NewStore(memory.Seed{Applications: []application.Application{{
				TikTokAppID: memory.TestApplicationID, Name: "Test", Status: application.StatusActive,
				AdUnits: application.AdUnits{RewardedVideo: []string{"vip-ad"}},
			}}, ApplicationMonetization: []monetization.ApplicationMonetizationConfig{{
				ApplicationID: memory.TestApplicationID, Status: monetization.MonetizationEnabled,
				Strategy: monetization.MonetizationStrategyEpisodeUnlock, VIPAdUnitCode: "vip-ad",
				VIP: monetization.ApplicationVIPPolicy{Enabled: enabled, RequiredAdCount: 2},
			}}})
			provider := &countingLoginProvider{}
			visitors, err := visitor.NewService(&mutableIdentityResolver{applicationID: memory.TestApplicationID, provider: provider}, store, visitor.Config{
				RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "local-test-visitor-identity-protection-key",
			})
			if err != nil {
				t.Fatal(err)
			}
			handler := newConsumerHandler(domain.NewService(store, time.Now), visitors, httpapi.Config{})
			boot := httptest.NewRecorder()
			handler.ServeHTTP(boot, httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap", strings.NewReader(`{"tiktokAppId":"1","loginCode":"initial-code"}`)))
			if boot.Code != 200 {
				t.Fatalf("bootstrap: %d %s", boot.Code, boot.Body.String())
			}
			var initial struct{ Data map[string]json.RawMessage }
			if err := json.Unmarshal(boot.Body.Bytes(), &initial); err != nil {
				t.Fatal(err)
			}
			var token string
			if err := json.Unmarshal(initial.Data["visitorToken"], &token); err != nil {
				t.Fatal(err)
			}
			for range 3 {
				request := httptest.NewRequest(http.MethodGet, "/api/v1/me/ad-experience", nil)
				request.Header.Set("Authorization", "Bearer "+token)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != 200 {
					t.Fatalf("query: %d %s", response.Code, response.Body.String())
				}
				if response.Header().Get("Cache-Control") != "private, no-store" {
					t.Fatal("personal state must not be cached")
				}
				var result struct {
					ServerTime string
					Data       map[string]json.RawMessage
				}
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.ServerTime == "" || len(result.Data) != 3 {
					t.Fatalf("unexpected query contract: %s", response.Body.String())
				}
				for _, key := range []string{"monetization", "vipState", "vipOffer"} {
					if !reflect.DeepEqual(initial.Data[key], result.Data[key]) {
						t.Fatalf("%s changed on read", key)
					}
				}
				if bytes.Contains(response.Body.Bytes(), []byte(token)) {
					t.Fatal("query reissued credential")
				}
			}
			if provider.calls != 1 {
				t.Fatalf("read query triggered %d logins", provider.calls)
			}
			for _, badToken := range []string{"", "invalid"} {
				request := httptest.NewRequest(http.MethodGet, "/api/v1/me/ad-experience", nil)
				request.Header.Set("Authorization", "Bearer "+badToken)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				assertIdentityError(t, response, 401, "unauthorized")
			}
			// 保留旧协议：启动仍校验官方登录码，权益查询不能变成绕过登录的入口。
			request := httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap", strings.NewReader(`{"tiktokAppId":"1","visitorToken":"`+token+`"}`))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assertIdentityError(t, response, 401, "bootstrap_failed")
		})
	}
}
