package consumer_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	httpapi "drama-server/internal/service/consumer"
	"drama-server/internal/testsupport/memory"
)

func TestDiscoveryHTTPContractResumesOpaqueCursorWithoutDuplicateIDs(t *testing.T) {
	t.Parallel()
	store := memory.NewStore(memory.Seed{
		PublishedOwnerID: memory.TestApplicationID,
		ApplicationMonetization: []monetization.ApplicationMonetizationConfig{{
			ApplicationID: memory.TestApplicationID, Status: monetization.MonetizationEnabled,
			Strategy: monetization.MonetizationStrategyEpisodeUnlock, EpisodeRewardAdUnitCode: "unused",
			EpisodeReward: monetization.EpisodeRewardPolicy{UnlockEpisodeCount: 1},
		}},

		Dramas: []domain.Drama{
			{ID: "100000000000000001", Title: "First Mystery", TagIDs: []string{"21"}, Status: "completed", PublishStatus: "published", Episodes: []domain.Episode{{ID: "200000000000000001", Number: 1}}},
			{ID: "100000000000000002", Title: "Second Mystery", TagIDs: []string{"21"}, Status: "completed", PublishStatus: "published", Episodes: []domain.Episode{{ID: "200000000000000002", Number: 1}}},
		},
	})
	server := httptest.NewServer(newConsumerHandler(domain.NewService(store, time.Now), newTestVisitorService(t, store), httpapi.Config{}))
	defer server.Close()

	bootstrap, err := http.Post(server.URL+"/api/v1/bootstrap", "application/json", bytes.NewBufferString(`{"tiktokAppId":"1","launchQuery":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	var sessionBody struct {
		Data struct {
			VisitorToken string `json:"visitorToken"`
		} `json:"data"`
	}
	if err := json.NewDecoder(bootstrap.Body).Decode(&sessionBody); err != nil {
		t.Fatal(err)
	}
	bootstrap.Body.Close()

	load := func(cursor string) struct {
		Data domain.DiscoveryPage `json:"data"`
	} {
		t.Helper()
		query := url.Values{"query": {"mystery"}, "tag": {"悬疑推理"}, "status": {"completed"}, "limit": {"1"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/discovery?"+query.Encode(), nil)
		request.Header.Set("authorization", "Bearer "+sessionBody.Data.VisitorToken)
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("unexpected status %d", response.StatusCode)
		}
		var body struct {
			Data domain.DiscoveryPage `json:"data"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	first := load("")
	second := load(first.Data.NextCursor)
	if first.Data.NextCursor == "" || len(first.Data.Items) != 1 || len(second.Data.Items) != 1 {
		t.Fatalf("unexpected discovery pages: first=%+v second=%+v", first.Data, second.Data)
	}
	if first.Data.Items[0].ID == second.Data.Items[0].ID || first.Data.Items[0].ID != "100000000000000001" {
		t.Fatalf("IDs must remain strings and pages must not overlap: first=%+v second=%+v", first.Data.Items, second.Data.Items)
	}
}

func TestDiscoveryHTTPRejectsInvalidLimit(t *testing.T) {
	t.Parallel()
	store := memory.NewStore(memory.Seed{
		ApplicationMonetization: []monetization.ApplicationMonetizationConfig{{
			ApplicationID: memory.TestApplicationID, Status: monetization.MonetizationEnabled,
			Strategy: monetization.MonetizationStrategyEpisodeUnlock, EpisodeRewardAdUnitCode: "unused",
			EpisodeReward: monetization.EpisodeRewardPolicy{UnlockEpisodeCount: 1},
		}},
	})
	server := httptest.NewServer(newConsumerHandler(domain.NewService(store, time.Now), newTestVisitorService(t, store), httpapi.Config{}))
	defer server.Close()
	bootstrap, _ := http.Post(server.URL+"/api/v1/bootstrap", "application/json", bytes.NewBufferString(`{"tiktokAppId":"1","launchQuery":{}}`))
	var sessionBody struct {
		Data struct {
			VisitorToken string `json:"visitorToken"`
		} `json:"data"`
	}
	_ = json.NewDecoder(bootstrap.Body).Decode(&sessionBody)
	bootstrap.Body.Close()
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/discovery?limit=zero", nil)
	request.Header.Set("authorization", "Bearer "+sessionBody.Data.VisitorToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.StatusCode)
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(response.Body).Decode(&body)
	if body.Reason != "invalid_request" {
		t.Fatalf("expected invalid_request, got %+v", body)
	}
}
