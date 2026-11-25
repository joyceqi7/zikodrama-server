package consumer_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	httpapi "drama-server/internal/service/consumer"
	"drama-server/internal/testsupport/memory"
)

func TestBootstrapAndResolvePlayExposeCurrentMonetizationContract(t *testing.T) {
	t.Parallel()

	config := monetization.ApplicationMonetizationConfig{
		ApplicationID: memory.TestApplicationID, Status: monetization.MonetizationEnabled,
		Strategy: monetization.MonetizationStrategyEpisodeUnlock, EpisodeRewardAdUnitCode: "episode-ad",
		VIPAdUnitCode: "vip-ad", FreeEpisodeCount: 1,
		EpisodeReward: monetization.EpisodeRewardPolicy{UnlockEpisodeCount: 1},
		VIP: monetization.ApplicationVIPPolicy{
			Enabled: true, StrongWatchAdCount: 2, RequiredAdCount: 5, EntryDelaySeconds: 3,
			Title: "Today's VIP", Content: "Watch ads", FailureMessage: "Try again",
		},
	}
	store := memory.NewStore(memory.Seed{
		PublishedOwnerID: memory.TestApplicationID,
		Applications: []application.Application{{
			TikTokAppID: memory.TestApplicationID, Name: "Drama Minis", Status: application.StatusActive,
			ComplianceBaseURL: "https://legal.example/apps/1",
			AdUnits:           application.AdUnits{RewardedVideo: []string{"episode-ad", "vip-ad"}},
		}},
		Dramas: []domain.Drama{{
			ID: "5008", Title: "Drama", PublishStatus: "published", PlatformAlbumID: "album-1",
			Episodes: []domain.Episode{
				{ID: "5014", DramaID: "5008", Number: 1, PlatformEpisodeID: "platform-episode-1", VID: "vid-1", MediaStatus: "ready"},
				{ID: "5015", DramaID: "5008", Number: 2, PlatformEpisodeID: "platform-episode-2", VID: "vid-2", MediaStatus: "ready"},
			},
		}},
		ApplicationMonetization: []monetization.ApplicationMonetizationConfig{config},
	})
	service := domain.NewService(store, time.Now)
	server := httptest.NewServer(newConsumerHandler(
		service,
		newTestVisitorService(t, store), httpapi.Config{},
	))
	defer server.Close()

	bootstrap, err := http.Post(server.URL+"/api/v1/bootstrap", "application/json", bytes.NewBufferString(`{"tiktokAppId":"1","launchQuery":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrap.Body.Close()
	var bootstrapBody struct {
		Data struct {
			PlaybackSDK       map[string]string `json:"playbackSdk"`
			VisitorID         string            `json:"visitorId"`
			VisitorToken      string            `json:"visitorToken"`
			ComplianceBaseURL string            `json:"complianceBaseUrl"`
			Monetization      struct {
				FreeEpisodeCount int `json:"freeEpisodeCount"`
				VIP              struct {
					Enabled         bool `json:"enabled"`
					ForceWatchCount int  `json:"forceWatchCount"`
					RequiredAdCount int  `json:"requiredAdCount"`
				} `json:"vip"`
			} `json:"monetization"`
		} `json:"data"`
	}
	if err := json.NewDecoder(bootstrap.Body).Decode(&bootstrapBody); err != nil {
		t.Fatal(err)
	}
	if len(bootstrapBody.Data.PlaybackSDK) != 2 || bootstrapBody.Data.PlaybackSDK["provider"] != "tiktok_minis" || bootstrapBody.Data.PlaybackSDK["channel"] != "byteplus" {
		t.Fatalf("启动必须只返回固定官方播放器配置：%+v", bootstrapBody.Data.PlaybackSDK)
	}
	if bootstrap.StatusCode != http.StatusOK || bootstrapBody.Data.VisitorID == "" || bootstrapBody.Data.VisitorToken == "" ||
		bootstrapBody.Data.ComplianceBaseURL != "https://legal.example/apps/1" ||
		bootstrapBody.Data.Monetization.FreeEpisodeCount != 1 || !bootstrapBody.Data.Monetization.VIP.Enabled ||
		bootstrapBody.Data.Monetization.VIP.ForceWatchCount != 2 || bootstrapBody.Data.Monetization.VIP.RequiredAdCount != 5 {
		t.Fatalf("bootstrap contract mismatch: status=%d data=%+v", bootstrap.StatusCode, bootstrapBody.Data)
	}

	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/play/resolve",
		bytes.NewBufferString(`{"dramaId":"5008","episodeId":"5015"}`))
	request.Header.Set("content-type", "application/json")
	request.Header.Set("authorization", "Bearer "+bootstrapBody.Data.VisitorToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var play struct {
		Data struct {
			Status   string                    `json:"status"`
			Offer    *monetization.AccessOffer `json:"offer"`
			VIPOffer *monetization.AccessOffer `json:"vipOffer"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&play); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || play.Data.Status != domain.PlayStatusAccessRequired ||
		play.Data.Offer == nil || play.Data.Offer.Kind != monetization.AccessOfferRewardedEpisode ||
		play.Data.VIPOffer == nil || play.Data.VIPOffer.Kind != monetization.AccessOfferRewardedVIP {
		t.Fatalf("resolve contract mismatch: status=%d data=%+v", response.StatusCode, play.Data)
	}

	batchRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/play/resolve-batch",
		bytes.NewBufferString(`{"dramaId":"5008","episodeIds":["5014","5015"]}`))
	batchRequest.Header.Set("content-type", "application/json")
	batchRequest.Header.Set("authorization", "Bearer "+bootstrapBody.Data.VisitorToken)
	batchResponse, err := http.DefaultClient.Do(batchRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer batchResponse.Body.Close()
	var batch struct {
		Data []struct {
			Status  string                 `json:"status"`
			Episode *domain.EpisodeSummary `json:"episode"`
		} `json:"data"`
	}
	if err := json.NewDecoder(batchResponse.Body).Decode(&batch); err != nil {
		t.Fatal(err)
	}
	if batchResponse.StatusCode != http.StatusOK || len(batch.Data) != 2 ||
		batch.Data[0].Status != domain.PlayStatusPlayable || batch.Data[0].Episode == nil || batch.Data[0].Episode.ID != "5014" ||
		batch.Data[1].Status != domain.PlayStatusAccessRequired || batch.Data[1].Episode == nil || batch.Data[1].Episode.ID != "5015" {
		t.Fatalf("batch resolve contract mismatch: status=%d data=%+v", batchResponse.StatusCode, batch.Data)
	}

	claimVIP := func() (int, monetization.VIPProgress) {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/vip/claim", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("authorization", "Bearer "+bootstrapBody.Data.VisitorToken)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var body struct {
			Data monetization.VIPProgress `json:"data"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, body.Data
	}
	if status, _ := claimVIP(); status != http.StatusConflict {
		t.Fatalf("VIP 广告未达标时领取应被拒绝: status=%d", status)
	}
	ctx := application.WithTimeZone(t.Context(), memory.TestApplicationID, "UTC")
	for count := 1; count <= 5; count++ {
		_, err := service.ClaimReward(ctx, bootstrapBody.Data.VisitorID, monetization.ClaimRewardInput{
			ClaimID: "vip-http-claim-" + strconv.Itoa(count), OfferID: play.Data.VIPOffer.ID,
			CompletedAdUnitID: play.Data.VIPOffer.AdUnitID, IsEnded: true,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if status, state := claimVIP(); status != http.StatusOK || !state.Active || state.AdCount != 5 {
		t.Fatalf("达标后手动领取失败: status=%d state=%+v", status, state)
	}
	if status, state := claimVIP(); status != http.StatusOK || !state.Active || state.AdCount != 5 {
		t.Fatalf("手动领取重试未幂等: status=%d state=%+v", status, state)
	}
}
