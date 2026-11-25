package mysqlstore

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/visitor"
)

// TestApplicationPlaybackSwitchHTTPIntegration 贯通现有官方应用切入直连及回切，验证同一用户、原配置和通道隔离，全部平台依赖使用替身。
func TestApplicationPlaybackSwitchHTTPIntegration(t *testing.T) {
	f := newIAAFixture(t)
	r, _ := seedDirect(t, f)
	f.direct = directplay.NewService(r, nil)
	f.signer = &directHTTPPlayback{}
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	raw := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var admin adminrbac.LoginResult
	if err := json.Unmarshal(raw, &admin); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.FindApplication(t.Context(), "101")
	if err != nil {
		t.Fatal(err)
	}
	var startup struct {
		VisitorID    string                     `json:"visitorId"`
		VisitorToken string                     `json:"visitorToken"`
		PlaybackSDK  consumer.PlaybackSDKConfig `json:"playbackSdk"`
	}
	bootstrap := func(provider string) {
		raw := iaaRequest(t, server.URL, "POST", "/api/v1/bootstrap", "", "", visitor.BootstrapInput{TikTokAppID: before.TikTokAppID, LoginCode: "user-a"}, 200)
		if err := json.Unmarshal(raw, &startup); err != nil {
			t.Fatal(err)
		}
		if startup.PlaybackSDK.Provider != provider {
			t.Fatalf("启动通道不符：%s", startup.PlaybackSDK.Provider)
		}
	}
	bootstrap("tiktok_minis")
	userID, originalVisitorToken := startup.VisitorID, startup.VisitorToken
	revision := int64(0)
	input := operations.ApplicationInput{SubjectID: "1", Name: before.Name, LogoURL: before.LogoURL, TikTokAppID: before.TikTokAppID, ClientKey: before.ClientKey, RegionCode: before.RegionCode, TimeZone: before.TimeZone, ComplianceBaseURL: before.ComplianceBaseURL, AdUnits: before.AdUnits, MonetizationMode: before.MonetizationMode, Status: before.Status, PlaybackProvider: "byteplus_web", PlaybackRevision: &revision}
	switchMode := func(provider string, version int64) {
		revision = version
		input.PlaybackProvider = provider
		raw := iaaRequest(t, server.URL, "PUT", "/admin/v1/applications/101", admin.Token, "", input, 200)
		var saved application.Application
		if err := json.Unmarshal(raw, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.PlaybackRevision != version+1 || saved.PlaybackProvider != provider {
			t.Fatal("响应未返回新播放修订和配置")
		}
		persisted, err := f.store.FindApplication(t.Context(), "101")
		if err != nil {
			t.Fatal(err)
		}
		if persisted.ID != before.ID || persisted.TikTokAppID != before.TikTokAppID || persisted.IdentityHashNamespace != before.IdentityHashNamespace || persisted.ClientKey != before.ClientKey || persisted.ClientSecret != before.ClientSecret || persisted.MonetizationMode != before.MonetizationMode || !persisted.CreatedAt.Equal(before.CreatedAt) {
			t.Fatal("切换改变应用身份或业务配置")
		}
		beforeAds, _ := json.Marshal(before.AdUnits)
		afterAds, _ := json.Marshal(persisted.AdUnits)
		if string(beforeAds) != string(afterAds) {
			t.Fatal("切换改变广告位")
		}
		bootstrap(provider)
		if startup.VisitorID != userID {
			t.Fatal("切换产生了不同的用户身份")
		}
	}
	switchMode("byteplus_web", 0)
	// 新通道不继承官方目录授权，直连发布须显式关联目标应用。
	iaaRequest(t, server.URL, "POST", "/api/v1/play/resolve", originalVisitorToken, "", map[string]string{"dramaId": "201", "episodeId": "301"}, 404)
	submitDirect(t, r, "publish", "")
	assertPlay := func(direct bool) {
		raw := iaaRequest(t, server.URL, "POST", "/api/v1/play/resolve", originalVisitorToken, "", map[string]string{"dramaId": "201", "episodeId": "301"}, 200)
		var play consumer.PlayResult
		if err := json.Unmarshal(raw, &play); err != nil {
			t.Fatal(err)
		}
		if play.Status != "playable" || play.Playback == nil {
			t.Fatal("切换后的播放不可用")
		}
		if direct && (play.Playback.PlayAuthToken == "" || play.Playback.AlbumID != "") {
			t.Fatal("直连播放混入官方合同")
		}
		if !direct && (play.Playback.AlbumID != "fixture-album" || play.Playback.PlayAuthToken != "") {
			t.Fatal("官方播放混入直连合同")
		}
	}
	assertPlay(true)
	switchMode("tiktok_minis", 1)
	assertPlay(false)
	// 往返后仍保留修订；过期读取不能通过普通资料保存覆盖模式，失败事务也不能改名称。
	stale := before
	stale.Name = "过期编辑不得写入"
	if err := f.store.SaveApplication(t.Context(), stale); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("过期仓储更新未拒绝：%v", err)
	}
	input.Name = "过期 HTTP 编辑不得写入"
	revision = 0
	iaaRequest(t, server.URL, "PUT", "/admin/v1/applications/101", admin.Token, "", input, 409)
	persisted, err := f.store.FindApplication(t.Context(), "101")
	if err != nil || persisted.Name != before.Name || persisted.PlaybackRevision != 2 {
		t.Fatal("过期更新破坏当前状态")
	}
	input.Name = before.Name
	switchMode("byteplus_web", 2)
	assertPlay(true)
}
