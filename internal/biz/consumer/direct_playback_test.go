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
	"log/slog"
	"strings"
	"testing"
	"time"
)

// directSigningFixture 只替换本地签名端口，不访问真实 VOD。
type directSigningFixture struct {
	calls int
	fail  bool
	vid   string
}

// TestDirectMissingProviderLogs 验证缺失直连依赖拒绝签发并记录实际原因，恢复后的正常路径不误报。
func TestDirectMissingProviderLogs(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	svc, provider, ctx := directConsumer(t)
	svc.ConfigureDirectPlayback(nil)
	if _, err := svc.PlaybackConfiguration(ctx); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatal("缺失直连配置依赖应拒绝", err)
	}
	if _, err := svc.ResolveOfficialPlay(ctx, "viewer", "5008", "5014"); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatal("缺失签名依赖应拒绝", err)
	}
	for _, expected := range []string{"direct consumer playback configuration unavailable", "direct consumer playback signing unavailable", domain.ErrServiceUnavailable.Error()} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatal("日志缺少具体操作或原因", expected)
		}
	}
	logs.Reset()
	svc.ConfigureDirectPlayback(provider)
	if _, err := svc.PlaybackConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveOfficialPlay(ctx, "viewer", "5008", "5014"); err != nil || strings.Contains(logs.String(), `"level":"ERROR"`) || strings.Contains(logs.String(), `"level":"WARN"`) || strings.Contains(logs.String(), "test-only-temporary-token") {
		t.Fatal("成功路径误报日志", err)
	}
}

// Configuration 返回基础版公开配置。
func (p *directSigningFixture) Configuration(context.Context) (domain.PlaybackSDKConfig, error) {
	return domain.PlaybackSDKConfig{Provider: "byteplus_web", Channel: "byteplus", Capabilities: &domain.PlaybackCapabilities{MediaPreload: false}}, nil
}

// Sign 记录是否仅在验权后被调用。
func (p *directSigningFixture) Sign(_ context.Context, episode domain.Episode) (string, time.Time, error) {
	p.calls++
	p.vid = episode.VID
	if p.fail {
		return "", time.Time{}, errors.New("test signing dependency unavailable")
	}
	return "test-only-temporary-token", time.Now().Add(time.Minute), nil
}

type previewStoreFixture struct {
	*memory.Store
	vid string
}

// FindOrQueueEntryPreview 模拟后台准备直连片段，不调用真实 VOD。
func (s *previewStoreFixture) FindOrQueueEntryPreview(context.Context, domain.Episode, int) (string, error) {
	return s.vid, nil
}

// TestLockedFirstEpisodePreview 验证两通道保持锁定状态，直连签名只接触独立短片。
func TestLockedFirstEpisodePreview(t *testing.T) {
	for _, provider := range []string{"tiktok_minis", "byteplus_web"} {
		t.Run(provider, func(t *testing.T) {
			config := applicationMonetization(false, 0, 0)
			doc, bindings := monetization.StrategyFromConfig(config)
			doc.FreeEpisodeCount, doc.EntryAdDelaySeconds = 0, 5
			config.Document, config.Bindings = &doc, bindings
			seed := monetizationSeed(config)
			if provider == "byteplus_web" {
				seed.Dramas[0].PlaybackProvider = provider
				for i := range seed.Dramas[0].Episodes {
					seed.Dramas[0].Episodes[i].MediaSpaceName = "test-space"
				}
			}
			store := &previewStoreFixture{Store: memory.NewStore(seed)}
			svc := domain.NewService(store, time.Now)
			signer := &directSigningFixture{}
			svc.ConfigureDirectPlayback(signer)
			ctx := application.WithPlayback(application.WithTimeZone(context.Background(), memory.TestApplicationID, "UTC"), provider)

			legacy, err := svc.ResolveOfficialPlay(ctx, "viewer", "5008", "5014")
			if err != nil || legacy.Status != domain.PlayStatusAccessRequired || legacy.EntryPreview != nil {
				t.Fatalf("未声明片段能力的请求保持原锁定响应: %+v %v", legacy, err)
			}
			ctx = domain.WithEntryPreview(ctx)
			first, err := svc.ResolveOfficialPlay(ctx, "viewer", "5008", "5014")
			if err != nil || first.Status != domain.PlayStatusAccessRequired || first.Playback != nil ||
				first.EntryPreview == nil || first.EntryPreview.Seconds != 5 || first.Offer == nil || signer.calls != 0 {
				t.Fatalf("首集片段破坏锁定响应: %+v calls=%d err=%v", first, signer.calls, err)
			}
			if provider == "tiktok_minis" {
				if first.EntryPreview.Playback == nil || first.EntryPreview.Playback.AlbumID != "album-1" || first.EntryPreview.Playback.PlayAuthToken != "" {
					t.Fatalf("官方首集片段未使用原官方标识: %+v", first.EntryPreview)
				}
			} else {
				if first.EntryPreview.Playback != nil || first.Preload != nil {
					t.Fatalf("等待直连片段时下发了媒体授权: %+v", first)
				}
				store.vid = "vid-1"
				if _, err := svc.ResolveOfficialPlay(ctx, "viewer", "5008", "5014"); !errors.Is(err, domain.ErrServiceUnavailable) || signer.calls != 0 {
					t.Fatalf("直连片段不得复用正片 VID: %v", err)
				}
				store.vid = "clip-1"
				ready, err := svc.ResolveOfficialPlay(ctx, "viewer", "5008", "5014")
				if err != nil || ready.Playback != nil || ready.EntryPreview == nil || ready.EntryPreview.Playback == nil ||
					ready.EntryPreview.Playback.VID != "clip-1" || signer.vid != "clip-1" || signer.calls != 1 {
					t.Fatalf("直连片段只能签发独立 VID: %+v signer=%+v err=%v", ready, signer, err)
				}
			}
			entry, err := svc.ResolvePlaybackEntry(ctx, "viewer", "5008", "5014")
			if err != nil || entry == nil || entry.Play == nil || entry.Play.Playback != nil ||
				entry.Play.EntryPreview == nil || entry.Play.EntryPreview.Playback == nil ||
				entry.Play.EntryPreview.Playback.VID != firstPreviewVID(provider) {
				t.Fatal("聚合决策必须只提供锁定首集的授权试看", err)
			}
			later, err := svc.ResolveOfficialPlay(ctx, "viewer", "5008", "5015")
			if err != nil || later.Status != domain.PlayStatusAccessRequired || later.EntryPreview != nil {
				t.Fatalf("后续集不应收到首集片段: %+v %v", later, err)
			}
		})
	}
}

func firstPreviewVID(provider string) string {
	if provider == "byteplus_web" {
		return "clip-1"
	}
	return "vid-1"
}

// TestPlaybackEntryKeepsVIPGate 与旧包播放合同对照，聚合请求只有门禁通过才签发正片。
func TestPlaybackEntryKeepsVIPGate(t *testing.T) {
	for _, provider := range []string{"tiktok_minis", "byteplus_web"} {
		t.Run(provider, func(t *testing.T) {
			config := applicationMonetization(true, 0, 2)
			config.VIP.RequireBeforePlayback = true
			seed := monetizationSeed(config)
			if provider == "byteplus_web" {
				seed.Dramas[0].PlaybackProvider = provider
				seed.Dramas[0].Episodes[0].MediaSpaceName = "test-space"
			}
			service := domain.NewService(memory.NewStore(seed), time.Now)
			signer := &directSigningFixture{}
			service.ConfigureDirectPlayback(signer)
			ctx := application.WithPlayback(application.WithTimeZone(t.Context(), memory.TestApplicationID, "UTC"), provider)
			entry, err := service.ResolvePlaybackEntry(ctx, "viewer", "5008", "")
			if err != nil || entry == nil || entry.Target.EpisodeID != "5014" || entry.VIPState.Active || entry.Play != nil || signer.calls != 0 {
				t.Fatal("未完成强制 VIP 时不应签发起播凭据", err)
			}
			legacy, err := service.ResolveOfficialPlay(ctx, "viewer", "5008", "5014")
			if err != nil || legacy.Status != domain.PlayStatusPlayable || legacy.Playback == nil {
				t.Fatal("旧包播放查询应保留原合同", err)
			}
		})
	}
}
func directConsumer(t *testing.T) (*domain.Service, *directSigningFixture, context.Context) {
	t.Helper()
	seed := monetizationSeed(applicationMonetization(true, 0, 2))
	d := &seed.Dramas[0]
	d.PlaybackProvider = "byteplus_web"
	d.PlatformAlbumID = ""
	for i := range d.Episodes {
		d.Episodes[i].PlatformEpisodeID = ""
		d.Episodes[i].MediaSpaceName = "test-space"
	}
	svc := domain.NewService(memory.NewStore(seed), time.Now)
	provider := &directSigningFixture{}
	svc.ConfigureDirectPlayback(provider)
	return svc, provider, application.WithPlayback(application.WithTimeZone(context.Background(), memory.TestApplicationID, "UTC"), "byteplus_web")
}

// TestDirectPlaybackEntitlements 验证无官方映射原片、锁定无 Token、签名故障拒绝播放和旧公开配置。
func TestDirectPlaybackEntitlements(t *testing.T) {
	svc, p, ctx := directConsumer(t)
	locked, err := svc.ResolveOfficialPlay(ctx, "viewer", "5008", "5015")
	if err != nil || locked.Status != domain.PlayStatusAccessRequired || locked.Playback != nil || locked.Preload != nil || p.calls != 0 {
		t.Fatalf("锁定集泄露授权: %+v calls=%d err=%v", locked, p.calls, err)
	}
	free, err := svc.ResolveOfficialPlay(ctx, "viewer", "5008", "5014")
	if err != nil || free.Status != domain.PlayStatusPlayable || free.Playback == nil || p.calls != 1 {
		t.Fatalf("无官方映射不可播放: %+v %v", free, err)
	}
	raw, _ := json.Marshal(free.Playback)
	if !strings.Contains(string(raw), "playAuthToken") || strings.Contains(string(raw), "albumId") || strings.Contains(string(raw), "episodeId") {
		t.Fatalf("直连合同混用: %s", raw)
	}
	p.fail = true
	if _, err := svc.ResolveOfficialPlay(ctx, "viewer", "5008", "5014"); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("签名失败未关闭: %v", err)
	}
	old, err := svc.PlaybackConfiguration(application.WithContext(context.Background(), memory.TestApplicationID))
	raw, _ = json.Marshal(old)
	if err != nil || string(raw) != `{"provider":"tiktok_minis","channel":"byteplus"}` {
		t.Fatalf("旧启动合同变化: %s %v", raw, err)
	}
	cfg, err := svc.PlaybackConfiguration(ctx)
	if err != nil || cfg.Capabilities == nil || cfg.Capabilities.MediaPreload {
		t.Fatal("基础版能力错误", cfg, err)
	}
}

// TestDirectRewardSurvivesSigningFailure 保证首次结算和幂等恢复在签名失败后仍确认已授予，无须重新看广告。
func TestDirectRewardSurvivesSigningFailure(t *testing.T) {
	svc, p, ctx := directConsumer(t)
	locked, err := svc.ResolveOfficialPlay(ctx, "viewer", "5008", "5015")
	if err != nil || locked.Offer == nil {
		t.Fatal(err)
	}
	input := monetization.ClaimRewardInput{ClaimID: "direct-sign-failure", OfferID: locked.Offer.ID, DramaID: "5008", EpisodeID: "5015", CompletedAdUnitID: locked.Offer.AdUnitID, IsEnded: true}
	p.fail = true
	for i := 0; i < 2; i++ {
		claim, err := svc.ClaimReward(ctx, "viewer", input)
		if err != nil || !claim.Unlocked || claim.Playback != nil {
			t.Fatalf("签名故障损坏奖励事实: %+v %v", claim, err)
		}
		if i == 1 && claim.Status != monetization.ClaimStatusAlreadyGranted {
			t.Fatal("重试不是原结算", claim)
		}
	}
	p.fail = false
	retry, err := svc.ClaimReward(ctx, "viewer", input)
	if err != nil || retry.Playback == nil || retry.Playback.Playback == nil || retry.Playback.Playback.PlayAuthToken == "" {
		t.Fatal("结算后独立恢复失败", retry, err)
	}
	foreign := application.WithPlayback(application.WithContext(context.Background(), "foreign"), "byteplus_web")
	if _, err := svc.ResolveOfficialPlay(foreign, "viewer", "5008", "5015"); err == nil {
		t.Fatal("跨应用获得旧权益")
	}
}
