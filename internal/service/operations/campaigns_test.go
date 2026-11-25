package operations_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/campaign"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
)

func TestAdminCampaignHTTPContractUsesSelectedApplication(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{PublishedOwnerID: "5001", Dramas: []domain.Drama{{
		ID: "5008", Title: "Published", PublishStatus: "published", Episodes: []domain.Episode{{ID: "5014", DramaID: "5008", Number: 1}},
	}}})
	now := time.Now().UTC()
	for _, app := range []application.Application{
		{SubjectID: "1", TikTokAppID: "5001", Name: "App A", Status: application.StatusActive, CreatedAt: now, UpdatedAt: now},
		{SubjectID: "1", TikTokAppID: "5002", Name: "App B", Status: application.StatusActive, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.SaveApplication(context.Background(), app); err != nil {
			t.Fatal(err)
		}
	}
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return &publicationProviderStub{} }, operations.PublicationJobConfig{}); err != nil {
		t.Fatal(err)
	}
	handler := operationshttp.NewHandler(service, newRBACService(t, store), operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "",
		strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)

	createdResponse := requestWithHeaders(t, handler, http.MethodPost, "/admin/v1/campaigns", "application/json",
		credentials.Token, strings.NewReader(`{"name":"Launch A","dramaId":"5008","episodeId":"5014","status":"active"}`),
		map[string]string{"X-Application-ID": "5001"})
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create campaign failed: %d %s", createdResponse.Code, createdResponse.Body.String())
	}
	var created campaign.Campaign
	decodeData(t, createdResponse, &created)
	if created.ApplicationID != "5001" || !strings.HasPrefix(created.Code, "launch_") {
		t.Fatalf("unexpected campaign response: %+v", created)
	}
	landing, err := url.Parse(created.MinisPath)
	if err != nil || landing.Query().Get("campaignCode") != created.Code || created.LandingURL != "https://www.tiktok.com/minis/test-official-link" {
		t.Fatalf("campaign landing URL = %q, err = %v", created.LandingURL, err)
	}

	for appID, expectedCount := range map[string]int{"5001": 1, "5002": 0} {
		response := requestWithHeaders(t, handler, http.MethodGet, "/admin/v1/campaigns", "", credentials.Token, nil,
			map[string]string{"X-Application-ID": appID})
		if response.Code != http.StatusOK {
			t.Fatalf("list %s campaigns failed: %d %s", appID, response.Code, response.Body.String())
		}
		var items []campaign.Campaign
		decodeData(t, response, &items)
		if len(items) != expectedCount {
			t.Fatalf("%s campaign count = %d, want %d", appID, len(items), expectedCount)
		}
	}

	// 列表快捷入口复用当前应用第一集链接，不重复创建；缺少应用上下文必须拒绝。
	reusedResponse := requestWithHeaders(t, handler, http.MethodPost, "/admin/v1/dramas/5008/campaign-link", "", credentials.Token, nil, map[string]string{"X-Application-ID": "5001"})
	var reused campaign.Campaign
	if reusedResponse.Code != http.StatusOK {
		t.Fatalf("快捷复制失败：%d %s", reusedResponse.Code, reusedResponse.Body.String())
	}
	decodeData(t, reusedResponse, &reused)
	if reused.ID != created.ID {
		t.Fatalf("未复用当前应用链接：%s", reused.ID)
	}
	missingApp := request(t, handler, http.MethodPost, "/admin/v1/dramas/5008/campaign-link", "", credentials.Token, nil)
	if missingApp.Code != http.StatusBadRequest {
		t.Fatalf("缺少应用未拒绝：%d", missingApp.Code)
	}
	invalidPath := requestWithHeaders(t, handler, http.MethodPost, "/admin/v1/dramas/invalid/campaign-link", "", credentials.Token, nil, map[string]string{"X-Application-ID": "5001"})
	if invalidPath.Code != http.StatusBadRequest {
		t.Fatalf("无效短剧标识未拒绝：%d", invalidPath.Code)
	}
	unauthorized := requestWithHeaders(t, handler, http.MethodPost, "/admin/v1/dramas/5008/campaign-link", "", credentials.Token, nil, map[string]string{"X-Application-ID": "5002"})
	if unauthorized.Code != http.StatusBadRequest {
		t.Fatalf("未授权目标未拒绝：%d", unauthorized.Code)
	}

	foreignUpdate := requestWithHeaders(t, handler, http.MethodPut, "/admin/v1/campaigns/"+created.ID, "application/json",
		credentials.Token, strings.NewReader(`{"code":"launch-a","name":"Hijacked","status":"disabled","dramaId":"5008","episodeId":"5014"}`),
		map[string]string{"X-Application-ID": "5002"})
	if foreignUpdate.Code != http.StatusNotFound {
		t.Fatalf("foreign application update should be hidden: %d %s", foreignUpdate.Code, foreignUpdate.Body.String())
	}
}
