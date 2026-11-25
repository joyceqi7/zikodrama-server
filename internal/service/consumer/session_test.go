package consumer_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/campaign"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/visitor"
	httpapi "drama-server/internal/service/consumer"
	"drama-server/internal/testsupport/memory"
)

// splitFailureStore 分别注入配置、个人状态、广告入口故障，验证新版请求之间的隔离。
type splitFailureStore struct {
	*memory.Store
	policyErr, stateErr, offerErr error
	stateReads                    int
}

// GetApplicationMonetization 注入应用规则故障，不影响认证仓储。
func (s *splitFailureStore) GetApplicationMonetization(ctx context.Context) (monetization.ApplicationMonetizationConfig, error) {
	if s.policyErr != nil {
		return monetization.ApplicationMonetizationConfig{}, s.policyErr
	}
	return s.Store.GetApplicationMonetization(ctx)
}

// FindVIPProgress 注入个人进度故障并记录读取次数。
func (s *splitFailureStore) FindVIPProgress(ctx context.Context, id string, policy monetization.MonetizationPolicy, period monetization.RewardPeriod, now time.Time) (monetization.VIPProgress, error) {
	s.stateReads++
	if s.stateErr != nil {
		return monetization.VIPProgress{}, s.stateErr
	}
	return s.Store.FindVIPProgress(ctx, id, policy, period, now)
}

// HasRewardedAdUnit 只影响广告入口，不能阻塞独立状态查询。
func (s *splitFailureStore) HasRewardedAdUnit(ctx context.Context, id string) (bool, error) {
	if s.offerErr != nil {
		return false, s.offerErr
	}
	return s.Store.HasRewardedAdUnit(ctx, id)
}

func TestSplitSessionConfigurationAndVIPIsolation(t *testing.T) {
	store := &splitFailureStore{Store: memory.NewStore(memory.Seed{Applications: []application.Application{{TikTokAppID: "1", Name: "Test", Status: application.StatusActive, AdUnits: application.AdUnits{RewardedVideo: []string{"vip-ad"}}}}, ApplicationMonetization: []monetization.ApplicationMonetizationConfig{{ApplicationID: memory.TestApplicationID, Status: monetization.MonetizationEnabled, Strategy: monetization.MonetizationStrategyEpisodeUnlock, VIPAdUnitCode: "vip-ad", VIP: monetization.ApplicationVIPPolicy{Enabled: true, RequiredAdCount: 2}}}})}
	provider := &countingLoginProvider{}
	visitors, err := visitor.NewService(&mutableIdentityResolver{applicationID: memory.TestApplicationID, provider: provider}, store, visitor.Config{RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "local-test-visitor-identity-protection-key"})
	if err != nil {
		t.Fatal(err)
	}
	handler := newConsumerHandler(domain.NewService(store, time.Now), visitors, httpapi.Config{})
	token := ""
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	store.policyErr = errors.New("policy unavailable")
	w := call("POST", "/api/v1/session", `{"tiktokAppId":"1","loginCode":"code"}`)
	if w.Code != 200 {
		t.Fatalf("login must not query policy: %d %s", w.Code, w.Body)
	}
	var session struct{ Data map[string]json.RawMessage }
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(session.Data["visitorToken"], &token)
	for _, key := range []string{"monetization", "vipState", "vipOffer", "playbackSdk", "launchTarget", "appName"} {
		if _, ok := session.Data[key]; ok {
			t.Fatalf("session leaked business projection %s", key)
		}
	}
	if token == "" || store.stateReads != 0 {
		t.Fatal("identity must be installed without state reads")
	}
	if w := call("GET", "/api/v1/open/app-config?tiktokAppId=1", ""); w.Code != 503 {
		t.Fatalf("config failure: %d", w.Code)
	}
	store.policyErr = nil
	store.stateErr = errors.New("state unavailable")
	if w := call("GET", "/api/v1/open/app-config?tiktokAppId=1", ""); w.Code != 200 || strings.Contains(w.Body.String(), "vipState") || strings.Contains(w.Body.String(), token) {
		t.Fatalf("public config coupled to state: %d %s", w.Code, w.Body)
	}
	if w := call("GET", "/api/v1/me/vip", ""); w.Code != 503 {
		t.Fatalf("state failure: %d", w.Code)
	}
	store.stateErr = nil
	store.offerErr = errors.New("offer unavailable")
	if w := call("GET", "/api/v1/me/vip", ""); w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("state coupled to offer: %d %s", w.Code, w.Body)
	}
	if w := call("GET", "/api/v1/me/vip/offer", ""); w.Code != 503 {
		t.Fatalf("offer failure: %d %s", w.Code, w.Body)
	}
	// 旧聚合接口保留原失败语义，不被新的分拆流程悄悄改变。
	if w := call("GET", "/api/v1/me/ad-experience", ""); w.Code != 503 {
		t.Fatalf("old aggregate unexpectedly changed: %d", w.Code)
	}
	store.offerErr = nil
	if w := call("GET", "/api/v1/me/vip/offer", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "rewarded_vip") {
		t.Fatalf("offer recovery: %d %s", w.Code, w.Body)
	}
	if provider.calls != 1 {
		t.Fatalf("reads triggered %d logins", provider.calls)
	}
	if w := call("POST", "/api/v1/launch/resolve", `{"launchQuery":{}}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"launchTarget":null`) {
		t.Fatalf("empty launch: %s", w.Body)
	}
	token = ""
	for _, path := range []string{"/api/v1/me/vip", "/api/v1/me/vip/offer"} {
		if w := call("GET", path, ""); w.Code != 401 {
			t.Fatalf("unprotected route %s", path)
		}
	}
	if w := call("POST", "/api/v1/session", `{"tiktokAppId":"1"}`); w.Code != 401 {
		t.Fatalf("login code requirement lost: %d", w.Code)
	}
}

func TestSplitEntryShowReturnsProgressAndPreservesLegacyResponse(t *testing.T) {
	store := memory.NewStore(memory.Seed{PublishedOwnerID: memory.TestApplicationID, Applications: []application.Application{{TikTokAppID: "1", Status: application.StatusActive, AdUnits: application.AdUnits{RewardedVideo: []string{"entry-ad"}}}}, Dramas: []domain.Drama{{ID: "5008", PublishStatus: "published", PlatformAlbumID: "album"}}, ApplicationMonetization: []monetization.ApplicationMonetizationConfig{{ApplicationID: memory.TestApplicationID, Status: monetization.MonetizationEnabled, StrategyID: "entry-strategy", Document: &monetization.StrategyDocument{SchemaVersion: 1, Enabled: true, Rules: []monetization.UnlockRule{{Code: "entry", Type: "entry_rewarded_ad", Enabled: true, Config: json.RawMessage(`{"requiredAdCount":2,"scope":"user_drama","countBy":"shown"}`)}}}, Bindings: map[string]monetization.RuleBinding{"entry": {AdUnitID: "entry-ad"}}}}})
	visitors := newTestVisitorService(t, store)
	identity, err := visitors.Bootstrap(t.Context(), visitor.BootstrapInput{TikTokAppID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	handler := newConsumerHandler(domain.NewService(store, time.Now), visitors, httpapi.Config{})
	call := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+identity.VisitorToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	body := `{"eventId":"split-entry-show-0001","scene":"entry_reward","dramaId":"5008","adUnitId":"entry-ad","occurredAt":"2099-01-01T00:00:00Z"}`
	for i := 0; i < 2; i++ {
		w := call("/api/v1/me/entry-ad/shows", body)
		var out struct {
			Data struct {
				Accepted      bool
				EntryAdStatus domain.EntryAdStatus
			}
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.Data.Accepted || out.Data.EntryAdStatus.ShownCount != 1 || strings.HasPrefix(out.Data.EntryAdStatus.ProgressDate, "2099") {
			t.Fatalf("show confirmation must return authoritative idempotent state: %d %s", w.Code, w.Body)
		}
	}
	w := callLegacyShow(call, body)
	if !strings.Contains(w.Body.String(), `"data":{"accepted":true}`) {
		t.Fatalf("legacy response changed: %s", w.Body)
	}
	if w := call("/api/v1/me/entry-ad/shows", strings.Replace(body, "entry_reward", "other_scene", 1)); w.Code != 400 {
		t.Fatalf("wrong scene accepted: %d", w.Code)
	}
}

// callLegacyShow 使用同一幂等键核对新旧入口共同引用原始事实。
func callLegacyShow(call func(string, string) *httptest.ResponseRecorder, body string) *httptest.ResponseRecorder {
	return call("/api/v1/me/ad-shows", body)
}

// TestSplitLaunchUsesSignedCampaign 验证拆分后的落地读取仍使用会话可信归因，不接受客户端替换。
func TestSplitLaunchUsesSignedCampaign(t *testing.T) {
	store := memory.NewStore(memory.Seed{PublishedOwnerID: "1", Applications: []application.Application{{TikTokAppID: "1", Status: application.StatusActive}}, Dramas: []domain.Drama{
		{ID: "5011", PublishStatus: "published", Episodes: []domain.Episode{{ID: "5017", DramaID: "5011", Number: 1}}},
		{ID: "5012", PublishStatus: "published", Episodes: []domain.Episode{{ID: "5018", DramaID: "5012", Number: 1}}},
	}})
	record := campaign.Campaign{ID: "5006", ApplicationID: "1", Code: "launch-test", Name: "Test", DramaID: "5011", EpisodeID: "5017", Status: campaign.StatusActive, LandingURL: "https://www.tiktok.com/minis/test", MinisPath: "/?campaignCode=launch-test"}
	if err := store.SaveCampaign(application.WithContext(t.Context(), "1"), &record); err != nil {
		t.Fatal(err)
	}
	visitors := newTestVisitorService(t, store)
	handler := newConsumerHandler(domain.NewService(store, time.Now), visitors, httpapi.Config{})
	boot := httptest.NewRecorder()
	handler.ServeHTTP(boot, httptest.NewRequest("POST", "/api/v1/session", strings.NewReader(`{"tiktokAppId":"1","launchQuery":{"campaignCode":"launch-test"}}`)))
	var session struct{ Data struct{ VisitorToken string } }
	if boot.Code != 200 || json.Unmarshal(boot.Body.Bytes(), &session) != nil {
		t.Fatalf("session failed: %s", boot.Body)
	}
	r := httptest.NewRequest("POST", "/api/v1/launch/resolve", strings.NewReader(`{"launchQuery":{"dramaId":"5012","episodeId":"5018","campaignCode":"forged"}}`))
	r.Header.Set("Authorization", "Bearer "+session.Data.VisitorToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"dramaId":"5011"`) || !strings.Contains(w.Body.String(), `"episodeId":"5017"`) {
		t.Fatalf("untrusted launch target won: %d %s", w.Code, w.Body)
	}
}
