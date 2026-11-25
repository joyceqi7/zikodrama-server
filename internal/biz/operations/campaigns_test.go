package operations_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/campaign"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

func TestCampaignsAreApplicationScopedAndValidateTargets(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	store := memory.NewStore(memory.Seed{
		PublishedOwnerID: "7637420375425239060", PublishedTargets: []string{"7637420375425239061"},
		Applications: []application.Application{
			{Name: "Indonesia", TikTokAppID: "7637420375425239060", ClientKey: "client-id", Status: application.StatusActive, CreatedAt: now, UpdatedAt: now},
			{Name: "Thailand", TikTokAppID: "7637420375425239061", ClientKey: "client-th", Status: application.StatusActive, CreatedAt: now, UpdatedAt: now},
		},
		Dramas: []domain.Drama{
			{ID: "5008", Title: "Shared drama", PublishStatus: "published", Episodes: []domain.Episode{{ID: "5014", DramaID: "5008", Number: 1}}},
			{ID: "5009", Title: "Other drama", PublishStatus: "published", Episodes: []domain.Episode{{ID: "5015", DramaID: "5009", Number: 1}}},
			{ID: "drama-draft", Title: "Draft drama", PublishStatus: "draft", Episodes: []domain.Episode{{ID: "episode-draft", DramaID: "drama-draft", Number: 1}}},
		},
	})
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	provider := &campaignLinkStub{}
	if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, operations.PublicationJobConfig{}); err != nil {
		t.Fatal(err)
	}
	ctxID := application.WithContext(context.Background(), "7637420375425239060")
	ctxTH := application.WithContext(context.Background(), "7637420375425239061")
	input := operations.CampaignInput{
		Code: "tiktok-id-2026q3", Name: "TikTok ID Q3",
		ExternalCampaignID: "238500000001", ExternalAdGroupID: "238500000002",
		ExternalAdID: "238500000003", ExternalCreativeID: "238500000004",
		DramaID: "5008", EpisodeID: "5014", ExperimentKey: "hook-a", Status: campaign.StatusActive,
	}

	created, err := service.CreateCampaign(ctxID, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.ApplicationID != "7637420375425239060" || created.Code != input.Code {
		t.Fatalf("unexpected campaign: %+v", created)
	}
	items, err := service.Campaigns(ctxID)
	if err != nil || len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("application campaign list mismatch: items=%+v err=%v", items, err)
	}
	items, err = service.Campaigns(ctxTH)
	if err != nil || len(items) != 0 {
		t.Fatalf("campaign leaked across applications: items=%+v err=%v", items, err)
	}
	if _, err := service.UpdateCampaign(ctxTH, created.ID, input); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign application must not update campaign, got %v", err)
	}
	renamed := input
	renamed.Code = "renamed"
	if _, err := service.UpdateCampaign(ctxID, created.ID, renamed); !errors.Is(err, operations.ErrInvalidInput) {
		t.Fatalf("campaign code must remain stable after creation, got %v", err)
	}
	if _, err := service.CreateCampaign(ctxTH, input); err != nil {
		t.Fatalf("campaign code uniqueness must be application scoped: %v", err)
	}

	invalidTarget := input
	invalidTarget.Code = "bad-target"
	invalidTarget.EpisodeID = "5015"
	if _, err := service.CreateCampaign(ctxID, invalidTarget); !errors.Is(err, operations.ErrInvalidInput) {
		t.Fatalf("episode outside the target drama must be rejected, got %v", err)
	}
	draftTarget := input
	draftTarget.Code = "draft-target"
	draftTarget.DramaID = "drama-draft"
	draftTarget.EpisodeID = "episode-draft"
	if _, err := service.CreateCampaign(ctxID, draftTarget); !errors.Is(err, operations.ErrInvalidInput) {
		t.Fatalf("active campaign must not target unpublished content, got %v", err)
	}
}

func TestCampaignLinkGeneratesStableURLAndPreventsTargetRepointing(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	store := memory.NewStore(memory.Seed{
		PublishedOwnerID: "7637420375425239060", PublishedTargets: []string{"7637420375425239061"},
		Applications: []application.Application{{
			Name: "Indonesia", TikTokAppID: "7637420375425239060", ClientKey: "client-id",
			Status: application.StatusActive, CreatedAt: now, UpdatedAt: now,
		}},
		Dramas: []domain.Drama{{
			ID: "5008", Title: "Shared drama", PublishStatus: "published", Episodes: []domain.Episode{
				{ID: "5014", DramaID: "5008", Number: 1},
				{ID: "5015", DramaID: "5008", Number: 2},
			},
		}},
	})
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	provider := &campaignLinkStub{}
	if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, operations.PublicationJobConfig{}); err != nil {
		t.Fatal(err)
	}
	ctx := application.WithContext(context.Background(), "7637420375425239060")
	created, err := service.CreateCampaign(ctx, operations.CampaignInput{
		Name: "Revenge hook", DramaID: "5008", EpisodeID: "5014", Status: campaign.StatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Code, "launch_") {
		t.Fatalf("campaign code = %q, want generated launch code", created.Code)
	}
	landing, err := url.Parse(created.MinisPath)
	if err != nil {
		t.Fatalf("parse landing URL: %v", err)
	}
	if landing.Query().Get("campaignCode") != created.Code || created.LandingURL != "https://www.tiktok.com/minis/generated-test-link" || provider.calls != 1 {
		t.Fatalf("unexpected generated URL %q", created.LandingURL)
	}

	repoint := operations.CampaignInput{
		Code: created.Code, Name: created.Name, DramaID: "5008", EpisodeID: "5015", Status: campaign.StatusActive,
	}
	if _, err := service.UpdateCampaign(ctx, created.ID, repoint); !errors.Is(err, operations.ErrInvalidInput) {
		t.Fatalf("live campaign target must be immutable, got %v", err)
	}
}

// TestCampaignCoverPreviewUsesPublishedTarget 验证用原活动生成独立官方链接，旧记录保持原样。
func TestCampaignCoverPreviewUsesPublishedTarget(t *testing.T) {
	t.Parallel()

	const appID = "7637420375425239060"
	const coverKey = "covers/first.jpg~tplv-vod-noop.image"
	existing := campaign.Campaign{ID: "5020", ApplicationID: appID, Code: "campaign-live", DramaID: "5008", EpisodeID: "5014",
		Status: campaign.StatusActive, LandingURL: "https://www.tiktok.com/minis/old-link", MinisPath: "/?campaignCode=campaign-live"}
	store := memory.NewStore(memory.Seed{
		PublishedOwnerID: appID,
		Applications:     []application.Application{{Name: "Test", TikTokAppID: appID, ClientKey: "client-id", Status: application.StatusActive}},
		Dramas: []domain.Drama{{ID: "5008", Title: "Published drama", CoverURL: "https://img.zikodrama-tv.com/" + coverKey,
			PublishStatus: "published", Episodes: []domain.Episode{{ID: "5014", DramaID: "5008", Number: 1}}}},
	})
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	provider := &campaignLinkStub{}
	if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, operations.PublicationJobConfig{}); err != nil {
		t.Fatal(err)
	}
	ctx := application.WithContext(context.Background(), appID)
	if err := store.SaveCampaign(ctx, &existing); err != nil {
		t.Fatal(err)
	}
	result, err := service.PreviewCampaignLink(ctx, existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	url, err := url.Parse(result.MinisPath)
	if err != nil {
		t.Fatal(err)
	}
	query := url.Query()
	if len(query) != 7 || query.Get("campaignCode") != existing.Code || query.Get("coverDramaId") != existing.DramaID ||
		query.Get("coverEpisodeId") != existing.EpisodeID || query.Get("coverKey") != coverKey ||
		query.Get("coverTitle") != "Published drama" || query.Get("coverEpisodeNumber") != "1" || query.Get("coverEpisodeCount") != "1" {
		t.Fatalf("unexpected preview query: %v", query)
	}
	if result.LandingURL == existing.LandingURL || provider.calls != 1 {
		t.Fatalf("preview did not obtain a separate official link: %+v", result)
	}
	persisted, err := store.FindCampaign(ctx, existing.ID)
	if err != nil || persisted.LandingURL != existing.LandingURL || persisted.MinisPath != existing.MinisPath {
		t.Fatalf("existing campaign changed: %+v, %v", persisted, err)
	}
}

func TestOfficialLinkDoesNotRequireManuallyConfiguredLaunchURL(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	store := memory.NewStore(memory.Seed{
		PublishedOwnerID: "7637420375425239060", PublishedTargets: []string{"7637420375425239061"},
		Applications: []application.Application{{
			Name: "Indonesia", TikTokAppID: "7637420375425239060", ClientKey: "client-id", Status: application.StatusActive, CreatedAt: now, UpdatedAt: now,
		}},
		Dramas: []domain.Drama{{
			ID: "5008", Title: "Shared drama", PublishStatus: "published", Episodes: []domain.Episode{{ID: "5014", DramaID: "5008", Number: 1}},
		}},
	})
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	provider := &campaignLinkStub{}
	if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, operations.PublicationJobConfig{}); err != nil {
		t.Fatal(err)
	}
	_, err := service.CreateCampaign(application.WithContext(context.Background(), "7637420375425239060"), operations.CampaignInput{
		Name: "Revenge hook", DramaID: "5008", EpisodeID: "5014", Status: campaign.StatusActive,
	})
	if err != nil {
		t.Fatalf("官方链接不依赖人工粘贴启动 URL：%v", err)
	}
}

// campaignLinkStub 记录官方调用次数，验证读取和改名不重新生成链接。
type campaignLinkStub struct {
	publicationProviderStub
	// calls 是本地模拟生成次数。
	calls int
	// failure 用于验证官方失败时没有伪造链接或写入半成品活动。
	failure error
}

// GenerateMinisLink 模拟真实返回的不透明链接，不向链接后面自行添加参数。
func (provider *campaignLinkStub) GenerateMinisLink(context.Context, operations.MinisLinkInput) (string, error) {
	provider.calls++
	if provider.failure != nil {
		return "", provider.failure
	}
	return "https://www.tiktok.com/minis/generated-test-link", nil
}

func TestCampaignOfficialFailureDoesNotCreateRecord(t *testing.T) {
	store := memory.NewStore(memory.Seed{PublishedOwnerID: "7637420375425239060", Applications: []application.Application{{TikTokAppID: "7637420375425239060", Status: "active"}},
		Dramas: []domain.Drama{{ID: "5008", PublishStatus: "published", Episodes: []domain.Episode{{ID: "5014", DramaID: "5008", Number: 1}}}},
	})
	provider := &campaignLinkStub{failure: &operations.PlatformCallError{Code: "api_rejected"}}
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, operations.PublicationJobConfig{}); err != nil {
		t.Fatal(err)
	}
	ctx := application.WithContext(context.Background(), "7637420375425239060")
	input := operations.CampaignInput{Code: "launch-test", Name: "Campaign", DramaID: "5008", EpisodeID: "5014", Status: "active"}
	if _, err := service.CreateCampaign(ctx, input); !errors.Is(err, operations.ErrCampaignLinkUnavailable) {
		t.Fatalf("failure=%v", err)
	}
	items, err := service.Campaigns(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("官方失败不得保存假活动：%+v %v", items, err)
	}
	provider.failure = nil
	created, err := service.CreateCampaign(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Name = "Renamed"
	if _, err := service.UpdateCampaign(ctx, created.ID, input); err != nil {
		t.Fatal(err)
	}
	items, err = service.Campaigns(ctx)
	if err != nil || len(items) != 1 || items[0].LandingURL != created.LandingURL || provider.calls != 2 {
		t.Fatalf("读取或改名重复调用平台：items=%+v calls=%d err=%v", items, provider.calls, err)
	}
}
