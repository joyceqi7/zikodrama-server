package consumer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	applicationroot "drama-server/internal/biz"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/campaign"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/visitor"
	httpapi "drama-server/internal/service/consumer"
	"drama-server/internal/testsupport/memory"
)

func TestCampaignTargetOverridesUntrustedLaunchTarget(t *testing.T) {
	t.Parallel()

	app := application.Application{SubjectID: "1",
		TikTokAppID: "official-id-app", Name: "ID App",
		Status:  application.StatusActive,
		AdUnits: application.AdUnits{RewardedVideo: []string{"episode-ad", "vip-ad"}},
	}
	store := memory.NewStore(memory.Seed{
		PublishedOwnerID: app.TikTokAppID,
		Applications:     []application.Application{app, {TikTokAppID: "another-app", Name: "Other App", Status: application.StatusActive}},
		ApplicationMonetization: []monetization.ApplicationMonetizationConfig{{
			ApplicationID: app.TikTokAppID, Status: monetization.MonetizationEnabled,
			Strategy:                monetization.MonetizationStrategyEpisodeUnlock,
			EpisodeRewardAdUnitCode: "episode-ad", VIPAdUnitCode: "vip-ad",
			EpisodeReward: monetization.EpisodeRewardPolicy{UnlockEpisodeCount: 1},
			VIP: monetization.ApplicationVIPPolicy{
				Enabled: true, StrongWatchAdCount: 2, RequiredAdCount: 5,
			},
		}},
		Dramas: []domain.Drama{
			{ID: "5012", Title: "Query", PublishStatus: "published", Episodes: []domain.Episode{{ID: "5018", DramaID: "5012", Number: 1}}},
			{ID: "5011", Title: "Campaign", CoverURL: "https://img.tkdrama.site/covers/5011.jpg", PublishStatus: "published", Episodes: []domain.Episode{
				{ID: "5017", DramaID: "5011", Number: 1, VID: "private-media-id"},
			}},
		},
	})
	// 消费者合同只读取已创建的官方投放记录，链接生成由管理端适配器测试覆盖。
	createdCampaign := campaign.Campaign{ID: "5006", ApplicationID: app.TikTokAppID, Code: "tiktok-id", Name: "TikTok ID", DramaID: "5011", EpisodeID: "5017", Status: campaign.StatusActive, LandingURL: "https://www.tiktok.com/minis/test-link", MinisPath: "/?campaignCode=tiktok-id"}
	if err := store.SaveCampaign(application.WithContext(context.Background(), app.TikTokAppID), &createdCampaign); err != nil {
		t.Fatal(err)
	}
	resolver, err := applicationroot.NewApplicationResolver(
		store,
		func(application.Application) visitor.IdentityProvider { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newConsumerHandler(
		domain.NewService(store, time.Now), newTestVisitorServiceWithResolver(t, resolver, store), httpapi.Config{},
	))
	defer server.Close()

	// 旧短链也能在无凭据时准备公开首屏，正文永远不带媒体或会话凭据。
	prepared, err := server.Client().Get(server.URL + "/api/v1/open/play/prepare?tiktokAppId=official-id-app&campaignCode=tiktok-id&dramaId=5012")
	if err != nil {
		t.Fatal(err)
	}
	var preparation struct {
		Data *domain.CampaignPlaybackPreparation `json:"data"`
	}
	var raw json.RawMessage
	err = json.NewDecoder(prepared.Body).Decode(&raw)
	prepared.Body.Close()
	if err != nil || prepared.StatusCode != http.StatusOK || prepared.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("公开投放准备失败: status=%d err=%v", prepared.StatusCode, err)
	}
	if err := json.Unmarshal(raw, &preparation); err != nil {
		t.Fatal(err)
	}
	if got := preparation.Data; got == nil || got.Target != (domain.LaunchTarget{DramaID: "5011", EpisodeID: "5017"}) ||
		got.EpisodeNumber != 1 || got.EpisodeCount != 1 || got.Title != "Campaign" || got.CoverURL != "https://img.tkdrama.site/covers/5011.jpg" {
		t.Fatalf("公开准备未使用可信投放目标: %+v", got)
	}
	for _, forbidden := range []string{"private-media-id", "visitorToken", "playAuthToken", "vipState", "entryAdStatus"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatalf("公开准备响应意外包含 %s", forbidden)
		}
	}
	foreign, err := server.Client().Get(server.URL + "/api/v1/open/play/prepare?tiktokAppId=another-app&campaignCode=tiktok-id")
	if err != nil {
		t.Fatal(err)
	}
	var otherApp struct {
		Data *domain.CampaignPlaybackPreparation `json:"data"`
	}
	err = json.NewDecoder(foreign.Body).Decode(&otherApp)
	foreign.Body.Close()
	if err != nil || foreign.StatusCode != http.StatusOK || otherApp.Data != nil {
		t.Fatalf("准备接口泄露其他应用的投放目标: status=%d err=%v", foreign.StatusCode, err)
	}
	anonymousPlayback, err := server.Client().Post(server.URL+"/api/v1/play/entry", "application/json",
		bytes.NewBufferString(`{"source":"launch","launchQuery":{"campaignCode":"tiktok-id"}}`))
	if err != nil {
		t.Fatal(err)
	}
	anonymousPlayback.Body.Close()
	if anonymousPlayback.StatusCode != http.StatusUnauthorized {
		t.Fatalf("公开准备不能替代起播授权: status=%d", anonymousPlayback.StatusCode)
	}

	response, err := http.Post(server.URL+"/api/v1/bootstrap", "application/json", bytes.NewBufferString(
		`{"tiktokAppId":"official-id-app","launchQuery":{"campaignCode":"tiktok-id","dramaId":"5012","episodeId":"5018"}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		Data struct {
			Campaign     *campaign.Attribution `json:"campaign"`
			LaunchTarget *domain.LaunchTarget  `json:"launchTarget"`
			VisitorToken string                `json:"visitorToken"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || body.Data.Campaign == nil || body.Data.Campaign.ID != createdCampaign.ID {
		t.Fatalf("campaign was not returned: status=%d body=%+v", response.StatusCode, body)
	}
	if body.Data.LaunchTarget == nil || body.Data.LaunchTarget.DramaID != "5011" || body.Data.LaunchTarget.EpisodeID != "5017" {
		t.Fatalf("campaign target did not win: %+v", body.Data.LaunchTarget)
	}
	for _, tc := range []struct {
		payload, drama, episode string
	}{
		{`{"source":"launch","launchQuery":{"dramaId":"5012","episodeId":"5018"}}`, "5011", "5017"},
		{`{"source":"selection","dramaId":"5012","episodeId":"5018"}`, "5012", "5018"},
		{`{"source":"selection","dramaId":"5011","episodeId":"5017"}`, "5011", "5017"},
	} {
		req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/play/entry", bytes.NewBufferString(tc.payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+body.Data.VisitorToken)
		req.Header.Set("Content-Type", "application/json")
		got, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Data struct {
				Entry *domain.PlaybackEntry `json:"entry"`
			} `json:"data"`
		}
		err = json.NewDecoder(got.Body).Decode(&result)
		got.Body.Close()
		if err != nil || got.StatusCode != http.StatusOK || got.Header.Get("Cache-Control") != "private, no-store" ||
			result.Data.Entry == nil || result.Data.Entry.Target != (domain.LaunchTarget{DramaID: tc.drama, EpisodeID: tc.episode}) {
			t.Fatalf("起播目标错误: status=%d, target=%v, error=%v", got.StatusCode, result.Data.Entry, err)
		}
	}
}
