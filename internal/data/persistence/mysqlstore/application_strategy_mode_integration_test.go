package mysqlstore

import (
	"context"
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
)

// TestCurrentSchemaIntegrationStrategyMode 验证当前应用归属、模式约束、旧接口拒绝和模式切换修订。
func TestCurrentSchemaIntegrationStrategyMode(t *testing.T) {
	f := newIAAFixture(t)
	defer f.store.data.pool.Close()
	handler := operationshttp.NewHandler(operations.NewService(operations.Repositories{Application: operations.ApplicationPorts{Subjects: f.store, Registry: f.store}, Monetization: operations.MonetizationPorts{Settings: f.store}, Reporting: operations.ReportingPorts{Audits: f.store}}, nil), adminrbac.NewService(f.store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")}), operationshttp.Config{})
	server := httptest.NewServer(handler)
	defer server.Close()
	var session struct {
		Token string `json:"token"`
	}
	mustDecodeApplicationStrategy(t, iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200), &session)
	ad := monetization.UnlockRule{Code: "ad", Type: "rewarded_ad", Enabled: true, Config: json.RawMessage(`{"requiredAdCount":1}`), Grants: []monetization.GrantTemplate{{Type: "episode_access", Config: json.RawMessage(`{"selection":"next_locked_episodes","count":1,"validity":"unlimited"}`)}}}
	ad.Config = json.RawMessage(`{"requiredAdCount":1,"autoUnlock":{"enabled":true,"countdownSeconds":5,"title":"继续解锁","content":"取消后播放当前集"}}`)
	paid := monetization.UnlockRule{Code: "buy", Type: "one_time_purchase", Config: json.RawMessage(`{"productCode":"drama"}`), Grants: []monetization.GrantTemplate{{Type: "drama_access", Config: json.RawMessage(`{"selection":"current_drama","validity":"unlimited"}`)}}}
	for _, mode := range []string{"IAA", "IAP", "IAA+IAP"} {
		t.Run(mode, func(t *testing.T) {
			units := application.AdUnits{}
			if mode != "IAP" {
				units = application.AdUnits{RewardedVideo: []string{"reward"}, Interstitial: []string{"interstitial"}}
			}
			body := map[string]any{"clientSecret": "strategy-test-secret", "subjectId": "1", "tiktokAppId": "mode-strategy-" + map[string]string{"IAA": "ads", "IAP": "paid", "IAA+IAP": "both"}[mode], "name": mode, "clientKey": "fixture-" + mode, "status": "active", "monetizationMode": mode, "adUnits": units}
			var app application.Application
			mustDecodeApplicationStrategy(t, iaaRequest(t, server.URL, "POST", "/admin/v1/applications", session.Token, "", body, 201), &app)
			ctx := application.WithContext(context.Background(), app.ID)
			snapshot, err := f.store.GetApplicationStrategy(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if string(snapshot.MonetizationMode) != mode || snapshot.Strategy != nil {
				t.Fatal("初始快照模式或归属错误")
			}
			rules := []monetization.UnlockRule{ad}
			if mode == "IAP" {
				rules = []monetization.UnlockRule{paid}
			} else if mode == "IAA+IAP" {
				rules = append(rules, paid)
			}
			input := monetization.ApplicationStrategyInput{Document: monetization.StrategyDocument{SchemaVersion: 1, Enabled: true, FreeEpisodeCount: 3, Rules: rules}, Rules: map[string]monetization.RuleBinding{}}
			var saved monetization.ApplicationStrategy
			mustDecodeApplicationStrategy(t, iaaRequest(t, server.URL, "PUT", "/admin/v1/application-strategy", session.Token, app.ID, input, 200), &saved)
			if saved.ApplicationID != app.ID || saved.Strategy == nil {
				t.Fatal("新增策略未自动归属当前应用")
			}
			if mode != "IAP" {
				reloaded, err := f.store.GetApplicationStrategy(ctx)
				if err != nil {
					t.Fatal(err)
				}
				var config monetization.RewardedAdConfig
				if err := json.Unmarshal(reloaded.Strategy.Document.Rules[0].Config, &config); err != nil {
					t.Fatal(err)
				}
				if config.AutoUnlock == nil || !config.AutoUnlock.Enabled || config.AutoUnlock.CountdownSeconds != 5 || config.AutoUnlock.Title != "继续解锁" || config.AutoUnlock.Content != "取消后播放当前集" {
					t.Fatal("MySQL 回读丢失自动解锁配置")
				}
			}
			if mode != "IAA+IAP" {
				bad := input
				bad.Document.Rules = []monetization.UnlockRule{paid}
				if mode == "IAP" {
					bad.Document.Rules = []monetization.UnlockRule{ad}
				}
				bad.Document.Rules[0].Enabled = false
				iaaRequest(t, server.URL, "PUT", "/admin/v1/application-strategy", session.Token, app.ID, bad, 409)
			}
			nextMode := "IAP"
			if mode == "IAP" {
				nextMode = "IAA"
				body["adUnits"] = application.AdUnits{RewardedVideo: []string{"r"}, Interstitial: []string{"i"}}
			}
			body["monetizationMode"] = nextMode
			iaaRequest(t, server.URL, "PUT", "/admin/v1/applications/"+app.ID, session.Token, "", body, 409)
			current, err := f.store.GetApplicationStrategy(ctx)
			if err != nil || current.Strategy.ID != saved.Strategy.ID {
				t.Fatal("冲突改变了策略", err)
			}
			input.Document.Rules = []monetization.UnlockRule{}
			mustDecodeApplicationStrategy(t, iaaRequest(t, server.URL, "PUT", "/admin/v1/application-strategy", session.Token, app.ID, input, 200), &saved)
			iaaRequest(t, server.URL, "PUT", "/admin/v1/applications/"+app.ID, session.Token, "", body, 200)
			current, err = f.store.GetApplicationStrategy(ctx)
			if err != nil || string(current.MonetizationMode) != nextMode {
				t.Fatal("模式切换没有更新快照和修订", err)
			}
			// 模式切换已递增修订，但旧页面提交兼容新模式的策略时仍应保存成功。
			var latest monetization.ApplicationStrategy
			mustDecodeApplicationStrategy(t, iaaRequest(t, server.URL, "PUT", "/admin/v1/application-strategy", session.Token, app.ID, input, 200), &latest)
			if string(latest.MonetizationMode) != nextMode {
				t.Fatal("旧页面未按切换后的最新模式保存", latest)
			}
		})
	}
	untouched, err := f.store.FindApplication(context.Background(), "101")
	if err != nil || untouched.MonetizationMode != application.MonetizationModeIAA {
		t.Fatal("修改影响了其他应用", err)
	}
	// 模式变更与广告策略保存并发时只能有一个成功，不能提交 IAP + 广告的组合。
	raceApp, err := f.store.FindApplication(t.Context(), "102")
	if err != nil {
		t.Fatal(err)
	}
	raceApp.MonetizationMode = application.MonetizationModeHybrid
	if err := f.store.SaveApplication(t.Context(), raceApp); err != nil {
		t.Fatal(err)
	}
	raceCtx := application.WithContext(t.Context(), "102")
	if _, err := f.store.GetApplicationStrategy(raceCtx); err != nil {
		t.Fatal(err)
	}
	empty := monetization.ApplicationStrategyInput{Document: monetization.StrategyDocument{SchemaVersion: 1, Enabled: true, Rules: []monetization.UnlockRule{}}, Rules: map[string]monetization.RuleBinding{}}
	_, err = f.store.SaveApplicationStrategy(raceCtx, empty)
	if err != nil {
		t.Fatal(err)
	}
	empty.Document.Rules = []monetization.UnlockRule{ad}
	raceApp.MonetizationMode = application.MonetizationModeIAP
	start := make(chan struct{})
	outcomes := make(chan error, 2)
	go func() { <-start; outcomes <- f.store.SaveApplication(raceCtx, raceApp) }()
	go func() { <-start; _, err := f.store.SaveApplicationStrategy(raceCtx, empty); outcomes <- err }()
	close(start)
	successes := 0
	for i := 0; i < 2; i++ {
		err := <-outcomes
		if err == nil {
			successes++
		} else if !errors.Is(err, operations.ErrConflict) && !errors.Is(err, monetization.ErrApplicationModeConflict) {
			t.Fatal("并发请求出现非预期故障", err)
		}
	}
	if successes != 1 {
		t.Fatal("并发模式与策略都提交成功或均失败", successes)
	}
	final, err := f.store.GetApplicationStrategy(raceCtx)
	if err != nil || final.Strategy == nil || monetization.ValidateApplicationMode(final.MonetizationMode, final.Strategy.Document) != nil {
		t.Fatal("并发结果不兼容", err)
	}

}
