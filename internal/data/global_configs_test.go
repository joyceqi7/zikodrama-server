package data

import (
	"context"
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/biz/mediaconfig"
	"drama-server/internal/biz/operations"
	"encoding/json"
	"errors"
	"testing"
)

type mediaConfigRepository struct {
	row globalconfig.Entry
	err error
}

func (r *mediaConfigRepository) List(context.Context) ([]globalconfig.Entry, error) {
	return nil, r.err
}
func (r *mediaConfigRepository) Get(context.Context, string) (globalconfig.Entry, error) {
	return r.row, r.err
}
func (r *mediaConfigRepository) Put(context.Context, globalconfig.Entry) error {
	return errors.New("unexpected write")
}
func (r *mediaConfigRepository) Delete(context.Context, string) error {
	return errors.New("unexpected delete")
}

func TestStoredMediaConfigurationIsOnlyRuntimeSource(t *testing.T) {
	row := globalconfig.Entry{Key: "media", Value: json.RawMessage(`{"access_key_id":"fixture-ak","secret_access_key":"fixture-sk","vod_app_id":"001","region":"ap-southeast-1","max_file_bytes":1073741824,"upload_token_ttl_seconds":600,"cover_domain":"https://cover.example.test","processing_timeout_seconds":900,"callback_url":"","callback_private_key":""}`)}
	service, err := NewStoredMediaConfigService(context.Background(), globalconfig.NewService(&mediaConfigRepository{row: row}))
	if err != nil {
		t.Fatal(err)
	}
	config, err := service.Current(context.Background())
	if err != nil || config.VODAppID != "001" || config.UploadTokenTTLSeconds != 600 {
		t.Fatal("stored configuration not used")
	}
}

func TestMissingStoredMediaDisablesMediaWithoutWritingDefaults(t *testing.T) {
	service, err := NewStoredMediaConfigService(context.Background(), globalconfig.NewService(&mediaConfigRepository{err: globalconfig.ErrNotFound}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Current(context.Background()); !errors.Is(err, mediaconfig.ErrNotConfigured) {
		t.Fatal("missing media unexpectedly enabled")
	}
}

func TestStoredMediaDatabaseFailureIsNotSilentlyIgnored(t *testing.T) {
	failure := errors.New("configuration database unavailable")
	_, err := NewStoredMediaConfigService(context.Background(), globalconfig.NewService(&mediaConfigRepository{err: failure}))
	if !errors.Is(err, failure) {
		t.Fatal("configuration failure was ignored")
	}
}

// TestMediaConfigurationChangesWithoutRestart 验证 API/Worker 使用同一配置源并在删除、故障时拒绝旧值。
func TestMediaConfigurationChangesWithoutRestart(t *testing.T) {
	ctx := operations.WithMediaSpace(context.Background(), "first")
	repo := &mediaConfigRepository{err: globalconfig.ErrNotFound}
	config, err := NewStoredMediaConfigService(ctx, globalconfig.NewService(repo))
	if err != nil {
		t.Fatal(err)
	}
	resources, err := NewMediaResources(config, &Resources{})
	if err != nil {
		t.Fatal(err)
	}
	provider := resources.Provider.(*currentDramaMediaProvider)
	if _, _, err := provider.PublicationMediaScope(ctx); !errors.Is(err, operations.ErrMediaNotConfigured) {
		t.Fatal("missing config accepted")
	}
	repo.err = nil
	repo.row = globalconfig.Entry{Key: "media", Value: json.RawMessage(`{"access_key_id":"fixture-ak","secret_access_key":"fixture-sk","vod_app_id":"001","region":"ap-southeast-1","max_file_bytes":1073741824,"upload_token_ttl_seconds":600,"cover_domain":"https://cover.example.test","processing_timeout_seconds":900,"callback_url":"","callback_private_key":""}`)}
	_, space, err := provider.PublicationMediaScope(ctx)
	if err != nil || space != "first" {
		t.Fatalf("initial config: %v", err)
	}
	first := provider.provider
	ctx = operations.WithMediaSpace(ctx, "second")
	_, space, err = provider.PublicationMediaScope(ctx)
	if err != nil || space != "second" || first == provider.provider {
		t.Fatalf("config not refreshed: %v", err)
	}
	_, oldSpace, err := first.PublicationMediaScope(ctx)
	if err != nil || oldSpace != "first" {
		t.Fatal("in-flight provider mutated")
	}
	repo.err = errors.New("redis unavailable")
	if _, _, err := provider.PublicationMediaScope(ctx); err == nil {
		t.Fatal("stale credentials used during outage")
	}
	repo.err = globalconfig.ErrNotFound
	if _, _, err := provider.PublicationMediaScope(ctx); !errors.Is(err, operations.ErrMediaNotConfigured) {
		t.Fatal("removed config still used")
	}
}
