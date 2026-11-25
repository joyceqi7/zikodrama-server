package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

type coverMediaProviderStub struct {
	upload operations.Upload
	bytes  []byte
}

func (provider *coverMediaProviderStub) Delete(context.Context, string, string, string, string) error {
	return nil
}

func (provider *coverMediaProviderStub) UploadCover(_ context.Context, upload operations.Upload) (operations.CoverAsset, error) {
	provider.upload = upload
	provider.bytes, _ = io.ReadAll(upload.Reader)
	return operations.CoverAsset{MID: "cover-1", SpaceName: "short-drama", ProviderFileName: "covers/cover-1.png", URL: "https://cover.example.com/covers/cover-1.png"}, nil
}

func TestUploadCoverValidatesContentAndNormalizesFileName(t *testing.T) {
	t.Parallel()

	provider := &coverMediaProviderStub{}
	service := newValidationTestService(memory.OperationsRepositories(memory.NewStore(memory.Seed{})), provider)
	content := []byte("\x89PNG\r\n\x1a\ncover")
	asset, err := service.UploadCover(context.Background(), operations.Upload{
		FileName: "../../cover.png", Size: int64(len(content)), Reader: bytes.NewReader(content),
	})
	if err != nil {
		t.Fatal(err)
	}
	if asset.ID != "cover-1" || provider.upload.FileName != "cover.png" || !bytes.Equal(provider.bytes, content) {
		t.Fatalf("unexpected normalized cover upload: asset=%+v upload=%+v", asset, provider.upload)
	}

	for name, upload := range map[string]operations.Upload{
		"unsupported extension": {FileName: "cover.gif", Size: int64(len(content)), Reader: bytes.NewReader(content)},
		"mismatched content":    {FileName: "cover.png", Size: 4, Reader: strings.NewReader("text")},
		"oversized declaration": {FileName: "cover.png", Size: operations.MaxCoverUploadBytes + 1, Reader: bytes.NewReader(content)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.UploadCover(context.Background(), upload); !errors.Is(err, operations.ErrInvalidInput) {
				t.Fatalf("invalid cover must be rejected, got %v", err)
			}
		})
	}
}

// TestServiceManagesApplicationsWhileCatalogRemainsGlobal 验证应用标识与配置约束，并保持短剧目录跨应用共享。
func TestServiceManagesApplicationsWhileCatalogRemainsGlobal(t *testing.T) {
	store := memory.NewStore(memory.Seed{Dramas: []domain.Drama{
		{ID: "5008", Title: "One", PublishStatus: "published"},
		{ID: "5009", Title: "Two", PublishStatus: "published"},
	}})
	service := newValidationTestService(memory.OperationsRepositories(store), nil)

	created, err := service.CreateApplication(context.Background(), operations.ApplicationInput{SubjectID: "1",
		Name: "Alpha Drama", LogoURL: "https://cdn.example/alpha.png", MonetizationMode: application.MonetizationModeIAA, TikTokAppID: "7637420375425239060",
		ClientKey: "client-alpha", ClientSecret: "test-secret", SecretRef: "TIKTOK_CLIENT_SECRET_ALPHA",

		ComplianceBaseURL: "https://legal.alpha.test", Status: "active",
		RegionCode: "ID", TimeZone: "Asia/Jakarta",
		AdUnits: application.AdUnits{RewardedVideo: []string{"reward"}, Interstitial: []string{"interstitial"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.TikTokAppID == "" || created.LogoURL != "https://cdn.example/alpha.png" || created.TikTokAppID != "7637420375425239060" || created.SecretRef == "" ||
		created.RegionCode != "ID" || created.TimeZone != "Asia/Jakarta" {
		t.Fatalf("unexpected application: %+v", created)
	}
	payload, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	for _, removedField := range []string{`"code"`, `"identityHashNamespace"`, `"surface"`, `"channels"`} {
		if strings.Contains(string(payload), removedField) {
			t.Fatalf("TikTok-only application contract must not expose %s: %s", removedField, payload)
		}
	}
	if _, err := service.CreateApplication(context.Background(), operations.ApplicationInput{SubjectID: "1",
		Name: "Missing TikTok", MonetizationMode: application.MonetizationModeIAA, RegionCode: "BR", TimeZone: "America/Sao_Paulo",
		ComplianceBaseURL: "https://legal.example", Status: application.StatusActive,
	}); !errors.Is(err, operations.ErrInvalidInput) {
		t.Fatalf("every application must require TikTok credentials, got %v", err)
	}
	if _, err := service.UpdateApplication(context.Background(), created.ID, operations.ApplicationInput{SubjectID: "1",
		Name: created.Name, LogoURL: created.LogoURL, MonetizationMode: application.MonetizationModeIAA, TikTokAppID: created.TikTokAppID, ClientKey: created.ClientKey,
		SecretRef: created.SecretRef, RegionCode: created.RegionCode,
		TimeZone:          created.TimeZone,
		AdUnits:           created.AdUnits,
		ComplianceBaseURL: created.ComplianceBaseURL, Status: created.Status,
	}); err != nil {
		t.Fatalf("移除业务编码后应可正常更新应用，实际为 %v", err)
	}
	for name, input := range map[string]operations.ApplicationInput{
		"region":   {Name: "Bad", MonetizationMode: application.MonetizationModeIAA, TikTokAppID: "bad-region-id", ClientKey: "bad-region-key", ComplianceBaseURL: "https://legal.example", Status: "active", RegionCode: "Indonesia", TimeZone: "UTC"},
		"timezone": {Name: "Bad", MonetizationMode: application.MonetizationModeIAA, TikTokAppID: "bad-timezone-id", ClientKey: "bad-timezone-key", ComplianceBaseURL: "https://legal.example", Status: "active", RegionCode: "ID", TimeZone: "Jakarta"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.CreateApplication(context.Background(), input); !errors.Is(err, operations.ErrInvalidInput) {
				t.Fatalf("expected invalid regional application input, got %v", err)
			}
		})
	}
	if _, err := service.CreateApplication(context.Background(), operations.ApplicationInput{SubjectID: "1",
		Name: "Duplicate", MonetizationMode: application.MonetizationModeIAA, TikTokAppID: "another-id", ClientKey: "client-alpha", ClientSecret: "test-secret",

		ComplianceBaseURL: "https://legal.duplicate.test", Status: "disabled",
	}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("expected duplicate client key conflict, got %v", err)
	}
	if _, err := service.CreateApplication(context.Background(), operations.ApplicationInput{SubjectID: "1",
		Name: "Bad Logo", LogoURL: "http://cdn.example/logo.png", MonetizationMode: application.MonetizationModeIAA, TikTokAppID: "bad-logo-id", ClientKey: "bad-logo-key",
		ComplianceBaseURL: "https://legal.bad-logo.test", Status: "active",
	}); !errors.Is(err, operations.ErrInvalidInput) {
		t.Fatalf("expected insecure logo URL rejection, got %v", err)
	}
	consumer := domain.NewService(store, time.Now)
	dramas, err := consumer.ListDramas(application.WithContext(context.Background(), created.TikTokAppID))
	if err != nil {
		t.Fatal(err)
	}
	if len(dramas) != 0 {
		t.Fatalf("新建应用尚未拥有或获授权，不应读取全局短剧：%+v", dramas)
	}
}

func TestDramaAggregateRejectsNonContiguousEpisodes(t *testing.T) {
	t.Parallel()

	service := newValidationTestService(memory.OperationsRepositories(memory.NewStore(memory.Seed{})), nil)
	for _, input := range []operations.DramaInput{
		{Title: "Gap", Description: "简介", Episodes: []operations.EpisodeInput{{Number: 1, Title: "One"}, {Number: 3, Title: "Three"}}},
	} {
		if _, err := service.CreateDrama(context.Background(), input); !errors.Is(err, operations.ErrInvalidInput) {
			t.Fatalf("invalid aggregate should be rejected: input=%+v err=%v", input, err)
		}
	}
}

func TestDramaMetadataDefaultsAndValidation(t *testing.T) {
	t.Parallel()

	service := newValidationTestService(memory.OperationsRepositories(memory.NewStore(memory.Seed{})), nil)
	created, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "Defaults", Description: "简介", Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode 1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != operations.DramaStatusUpdating || created.Language != "zh" || created.DramaType != 4 {
		t.Fatalf("unexpected defaults: %+v", created)
	}

	valid, err := service.CreateDrama(context.Background(), operations.DramaInput{
		Title: "Localized", Description: "简介", Status: operations.DramaStatusCompleted, Year: 2026, Language: "pt", DramaType: 3,
		TagIDs: []string{"21", "10", "9"}, Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode 1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if valid.Language != "pt" || len(valid.TagIDs) != 3 || valid.TagIDs[0] != "21" {
		t.Fatalf("valid metadata was not preserved: %+v", valid)
	}

	invalidInputs := []operations.DramaInput{
		{Title: "Bad status", Description: "简介", Status: "ongoing", Language: "zh", DramaType: 4, Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode 1"}}},
		{Title: "Bad language", Description: "简介", Status: operations.DramaStatusUpdating, Language: "pt-BR", DramaType: 4, Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode 1"}}},
		{Title: "Bad type", Description: "简介", Status: operations.DramaStatusUpdating, Language: "zh", DramaType: 5, Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode 1"}}},
		{Title: "Unknown tag", Description: "简介", Status: operations.DramaStatusUpdating, Language: "zh", DramaType: 4, TagIDs: []string{"32"}, Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode 1"}}},
		{Title: "Too many tags", Description: "简介", Status: operations.DramaStatusUpdating, Language: "zh", DramaType: 4, TagIDs: []string{"1", "2", "3", "4"}, Episodes: []operations.EpisodeInput{{Number: 1, Title: "Episode 1"}}},
	}
	for _, input := range invalidInputs {
		if _, err := service.CreateDrama(context.Background(), input); !errors.Is(err, operations.ErrInvalidInput) {
			t.Fatalf("invalid metadata should be rejected: input=%+v err=%v", input, err)
		}
	}
}

func TestVIPStrategyIsOptionalAndMayReuseEpisodeAdUnitCode(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{Applications: []application.Application{{
		MonetizationMode: application.MonetizationModeIAA, TikTokAppID: "5004", Name: "Indonesia", Status: application.StatusActive,

		AdUnits: application.AdUnits{RewardedVideo: []string{"rewarded"}},
	}}})
	service := newValidationTestService(memory.OperationsRepositories(store), nil)
	ctx := application.WithContext(context.Background(), "5004")

	disabled, err := service.UpdateApplicationMonetization(ctx, operations.ApplicationMonetizationInput{
		FreeEpisodeCount: 3, EpisodeUnlockAdUnitCode: "rewarded",
		VIPStrategy: operations.ApplicationVIPStrategyInput{AdUnitCode: "missing", RequiredAdCount: 99},
	})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.VIPStrategy.Enabled || disabled.VIPStrategy.AdUnitCode != "" {
		t.Fatalf("disabled VIP strategy must clear runtime fields: %+v", disabled.VIPStrategy)
	}

	enabled, err := service.UpdateApplicationMonetization(ctx, operations.ApplicationMonetizationInput{
		FreeEpisodeCount: 3, EpisodeUnlockAdUnitCode: "rewarded",
		VIPStrategy: operations.ApplicationVIPStrategyInput{
			Enabled: true, RequiredAdCount: 5, AdUnitCode: "rewarded",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if enabled.VIPStrategy.AdUnitCode != "rewarded" {
		t.Fatalf("VIP strategy may reuse episode advertising code: %+v", enabled.VIPStrategy)
	}

	_, err = service.UpdateApplicationMonetization(ctx, operations.ApplicationMonetizationInput{
		FreeEpisodeCount: 3, EpisodeUnlockAdUnitCode: "rewarded",
		VIPStrategy: operations.ApplicationVIPStrategyInput{
			Enabled: true, RequiredAdCount: 11, AdUnitCode: "rewarded",
		},
	})
	if !errors.Is(err, operations.ErrInvalidInput) {
		t.Fatalf("VIP target outside one to ten must be rejected: %v", err)
	}

	_, err = service.UpdateApplicationMonetization(ctx, operations.ApplicationMonetizationInput{
		FreeEpisodeCount: 3, EpisodeUnlockAdUnitCode: "rewarded",
		VIPStrategy: operations.ApplicationVIPStrategyInput{
			Enabled: true, ForceWatchCount: 6, RequiredAdCount: 5, AdUnitCode: "rewarded",
		},
	})
	if !errors.Is(err, operations.ErrInvalidInput) {
		t.Fatalf("投放强看数量不能超过 VIP 达成数量: %v", err)
	}
}
