package biz_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	application "drama-server/internal/biz"
	domainapplication "drama-server/internal/biz/application"
	"drama-server/internal/biz/visitor"
)

type identityProvider struct{ openID string }

func (provider identityProvider) ExchangeCode(context.Context, string) (visitor.Identity, error) {
	return visitor.Identity{OpenID: provider.openID}, nil
}

type applicationRepository struct {
	mu   sync.RWMutex
	apps map[string]domainapplication.Application
}

func (repository *applicationRepository) FindApplicationByTikTokAppID(_ context.Context, id string) (domainapplication.Application, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	for _, app := range repository.apps {
		if app.TikTokAppID == id {
			return app, nil
		}
	}
	return domainapplication.Application{}, domainapplication.ErrUnavailable
}

func (repository *applicationRepository) save(app domainapplication.Application) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.apps[app.TikTokAppID] = app
}

func TestResolverReadsSharedApplicationStateOnEveryRequest(t *testing.T) {
	t.Parallel()

	repository := &applicationRepository{apps: map[string]domainapplication.Application{
		"drama-jp": {
			TikTokAppID: "drama-jp", Name: "Drama JP", LogoURL: "https://cdn.example/drama-jp.png",
			ComplianceBaseURL: "https://legal.example/apps/drama-jp",
			ClientKey:         "jp-key", SecretRef: "JP_SECRET",
			Status: domainapplication.StatusActive,
		},
	}}
	resolver, err := application.NewApplicationResolver(repository, func(app domainapplication.Application) visitor.IdentityProvider {
		return identityProvider{openID: app.TikTokAppID + "-openid"}
	})
	if err != nil {
		t.Fatal(err)
	}

	resolved, provider, err := resolver.Resolve(context.Background(), "drama-jp")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := provider.ExchangeCode(context.Background(), "login-code")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.TikTokAppID != "drama-jp" || resolved.Name != "Drama JP" || resolved.ComplianceBaseURL != "https://legal.example/apps/drama-jp" || identity.OpenID != "drama-jp-openid" {
		t.Fatalf("解析了错误的应用：context=%+v identity=%+v", resolved, identity)
	}

	updated := repository.apps["drama-jp"]
	updated.Status = domainapplication.StatusDisabled
	repository.save(updated)
	if _, _, err := resolver.Resolve(context.Background(), "drama-jp"); !errors.Is(err, domainapplication.ErrUnavailable) {
		t.Fatalf("解析器没有观察到共享仓储中的停用状态：%v", err)
	}
}

func TestResolverDoesNotInjectProcessConfigurationIntoApplications(t *testing.T) {
	t.Parallel()

	repository := &applicationRepository{apps: map[string]domainapplication.Application{
		"1":     {TikTokAppID: "1", ClientKey: "test-key", Status: domainapplication.StatusActive},
		"other": {TikTokAppID: "other", ClientKey: "other-key", Status: domainapplication.StatusActive},
	}}
	secrets := map[string]string{}
	resolver, err := application.NewApplicationResolver(repository, func(app domainapplication.Application) visitor.IdentityProvider {
		secrets[app.TikTokAppID] = app.ClientSecret
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolver.Resolve(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolver.Resolve(context.Background(), "other"); err != nil {
		t.Fatal(err)
	}
	if secrets["1"] != "" || secrets["other"] != "" {
		t.Fatalf("应用解析器不应注入进程级默认密钥：%+v", secrets)
	}
}

// TestIAPResolverOmitsInterstitial 确认纯内购模式即使保留旧广告编码也不触发启动插屏。
func TestIAPResolverOmitsInterstitial(t *testing.T) {
	for _, mode := range []domainapplication.MonetizationMode{"IAA", "IAP", "IAA+IAP"} {
		repo := &applicationRepository{apps: map[string]domainapplication.Application{"1": {TikTokAppID: "1", Status: domainapplication.StatusActive, MonetizationMode: mode, AdUnits: domainapplication.AdUnits{Interstitial: []string{"old-ad"}}}}}
		resolver, err := application.NewApplicationResolver(repo, func(domainapplication.Application) visitor.IdentityProvider { return nil })
		if err != nil {
			t.Fatal(err)
		}
		app, _, err := resolver.Resolve(t.Context(), "1")
		if err != nil || (app.InterstitialAdUnitID == "") != (mode == "IAP") {
			t.Fatal("应用模式未控制插屏资源", mode, err)
		}
	}
}

// TestResolverDirectGate 隔离直连依赖故障，官方解析不调用新配置门禁且未知通道不能回退。
func TestResolverDirectGate(t *testing.T) {
	repo := &applicationRepository{apps: map[string]domainapplication.Application{
		"official": {TikTokAppID: "official", Status: "active"},
		"direct":   {TikTokAppID: "direct", Status: "active", PlaybackProvider: "byteplus_web"},
		"unknown":  {TikTokAppID: "unknown", Status: "active", PlaybackProvider: "unknown"},
	}}
	resolver, err := application.NewApplicationResolver(repo, func(domainapplication.Application) visitor.IdentityProvider { return nil })
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	resolver.ConfigureDirectPlaybackGate(func(context.Context) error { calls++; return errors.New("test direct configuration disabled") })
	if _, _, err := resolver.Resolve(t.Context(), "official"); err != nil || calls != 0 {
		t.Fatal("官方依赖新配置", err)
	}
	if _, _, err := resolver.Resolve(t.Context(), "direct"); !errors.Is(err, visitor.ErrServiceUnavailable) || calls != 1 {
		t.Fatal("直连门禁没有关闭", err)
	}
	if _, _, err := resolver.Resolve(t.Context(), "unknown"); !errors.Is(err, domainapplication.ErrUnavailable) {
		t.Fatal("未知通道回退", err)
	}
	resolver.ConfigureDirectPlaybackGate(func(context.Context) error { return nil })
	if app, _, err := resolver.Resolve(t.Context(), "direct"); err != nil || app.PlaybackProvider != "byteplus_web" {
		t.Fatal("直连配置有效后未放行", err)
	}
}
