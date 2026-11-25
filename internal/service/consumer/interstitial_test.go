package consumer_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	biz "drama-server/internal/biz"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/visitor"
	httpapi "drama-server/internal/service/consumer"
	"drama-server/internal/testsupport/memory"
)

// TestInterstitialSwitchFiltersBothConfigurationContracts 验证新旧 Mini 都通过空插屏编码停止展示，原有广告位不被删除。
func TestInterstitialSwitchFiltersBothConfigurationContracts(t *testing.T) {
	enabled, disabled := true, false
	for _, test := range []struct {
		name string
		flag *bool
		mode application.MonetizationMode
		want string
	}{
		{"legacy", nil, application.MonetizationModeIAA, "launch-ad"},
		{"enabled", &enabled, application.MonetizationModeIAA, "launch-ad"},
		{"disabled", &disabled, application.MonetizationModeIAA, ""},
		{"hybrid disabled", &disabled, application.MonetizationModeHybrid, ""},
		{"iap", &enabled, application.MonetizationModeIAP, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := memory.NewStore(memory.Seed{Applications: []application.Application{{ID: "1", SubjectID: "1", TikTokAppID: "1", Name: "Test", Status: application.StatusActive, MonetizationMode: test.mode, AdUnits: application.AdUnits{RewardedVideo: []string{"reward"}, Interstitial: []string{"launch-ad"}}}}, ApplicationMonetization: []monetization.ApplicationMonetizationConfig{{ApplicationID: "1", Document: &monetization.StrategyDocument{SchemaVersion: 1, Enabled: true, InterstitialEnabled: test.flag}}}})
			resolver, err := biz.NewApplicationResolver(store, func(application.Application) visitor.IdentityProvider { return nil })
			if err != nil {
				t.Fatal(err)
			}
			visitors := newTestVisitorServiceWithResolver(t, resolver, store)
			handler := newConsumerHandler(domain.NewService(store, time.Now), visitors, httpapi.Config{})
			for _, path := range []string{"/api/v1/open/app-config?tiktokAppId=1", "/api/v1/bootstrap"} {
				method, body := http.MethodGet, ""
				if path == "/api/v1/bootstrap" {
					method, body = http.MethodPost, `{"tiktokAppId":"1"}`
				}
				out := httptest.NewRecorder()
				handler.ServeHTTP(out, httptest.NewRequest(method, path, strings.NewReader(body)))
				var result struct{ Data map[string]json.RawMessage }
				if out.Code != http.StatusOK || json.Unmarshal(out.Body.Bytes(), &result) != nil {
					t.Fatalf("配置读取失败: %d %s", out.Code, out.Body)
				}
				var slot string
				if raw, exists := result.Data["interstitialAdUnitId"]; exists {
					if err := json.Unmarshal(raw, &slot); err != nil {
						t.Fatal(err)
					}
				} else if test.want != "" {
					t.Fatal("启用时缺少插屏广告位")
				}
				if slot != test.want {
					t.Fatalf("%s 插屏编码不符: got=%q want=%q", path, slot, test.want)
				}
				if test.want == "" {
					if _, exists := result.Data["interstitialAdUnitId"]; exists {
						t.Fatal("关闭时必须省略插屏编码")
					}
				}
			}
			app, err := store.FindApplication(t.Context(), "1")
			if err != nil || len(app.AdUnits.Interstitial) != 1 || app.AdUnits.Interstitial[0] != "launch-ad" {
				t.Fatal("关闭插屏删除了已有广告位")
			}
		})
	}
}
