package data

import (
	"context"
	"drama-server/internal/biz/mediaconfig"
	"drama-server/internal/biz/operations"
	"drama-server/internal/data/media/byteplusmedia"
	"errors"
	"go.opentelemetry.io/otel"
	"log/slog"
	"sync"
	"time"
)

// currentDramaMediaProvider 在剧操作开始时读取配置，复用同配置的 SDK 客户端。
// 已开始的调用持有旧客户端，配置更新不会修改其内部状态。
type currentDramaMediaProvider struct {
	config   *mediaconfig.Service
	mu       sync.Mutex
	last     mediaconfig.Config
	provider *byteplusmedia.Provider
}

var _ operations.ProxiedMediaUploadProvider = (*currentDramaMediaProvider)(nil)

func (p *currentDramaMediaProvider) current(ctx context.Context) (*byteplusmedia.Provider, error) {
	config, err := p.config.Current(ctx)
	if errors.Is(err, mediaconfig.ErrNotConfigured) {
		return nil, operations.ErrMediaNotConfigured
	}
	if err != nil {
		slog.ErrorContext(ctx, "load drama media provider configuration failed", "error", err)
		return nil, err
	}
	if space := operations.MediaSpaceFromContext(ctx); space != "" {
		config.SpaceName = space
	}
	if config.SpaceName == "" {
		slog.WarnContext(ctx, "shared media space unavailable", "error", operations.ErrMediaNotConfigured)
		return nil, operations.ErrMediaNotConfigured
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.provider != nil && p.last == config {
		return p.provider, nil
	}
	provider, err := byteplusmedia.New(byteplusmedia.Config{
		AccessKeyID: config.AccessKeyID, SecretAccessKey: config.SecretAccessKey,
		AppID: config.VODAppID, Region: config.Region, SpaceName: config.SpaceName,
		MaxFileBytes: config.MaxFileBytes, UploadTokenTTL: time.Duration(config.UploadTokenTTLSeconds) * time.Second,
		CoverDomain: config.CoverDomain, ProcessingTimeout: time.Duration(config.ProcessingTimeoutSeconds) * time.Second,
		Tracer: otel.Tracer("drama-api/byteplus"),
	})
	if err != nil {
		slog.ErrorContext(ctx, "create drama media provider failed", "error", err)
		return nil, err
	}
	p.provider, p.last = provider, config
	return provider, nil
}

// Delete 使用本次剧操作的配置快照调用官方能力。
func (p *currentDramaMediaProvider) Delete(ctx context.Context, class, space, id, file string) error {
	provider, err := p.current(operations.WithMediaSpace(ctx, space))
	if err != nil {
		return err
	}
	return provider.Delete(ctx, class, space, id, file)
}

// UploadCover 使用本次剧操作的配置快照调用官方能力。
func (p *currentDramaMediaProvider) UploadCover(ctx context.Context, input operations.Upload) (operations.CoverAsset, error) {
	provider, err := p.current(ctx)
	if err != nil {
		return operations.CoverAsset{}, err
	}
	return provider.UploadCover(ctx, input)
}

// UploadAuthorization 使用本次剧操作的配置快照调用官方能力。
func (p *currentDramaMediaProvider) UploadAuthorization(ctx context.Context) (operations.MediaUploadAuthorization, error) {
	provider, err := p.current(ctx)
	if err != nil {
		return operations.MediaUploadAuthorization{}, err
	}
	return provider.UploadAuthorization(ctx)
}

// UploadEpisodeMedia 使用本次剧操作的配置快照转发服务端视频流上传，供浏览器 multipart 初始化失败时恢复。
func (p *currentDramaMediaProvider) UploadEpisodeMedia(ctx context.Context, input operations.Upload) (operations.MediaAsset, error) {
	provider, err := p.current(ctx)
	if err != nil {
		return operations.MediaAsset{}, err
	}
	asset, err := provider.UploadEpisodeMedia(ctx, input)
	if err != nil {
		slog.ErrorContext(ctx, "upload episode media with current provider failed", "error", err)
		return operations.MediaAsset{}, err
	}
	return asset, nil
}

// FinalizeUpload 使用本次剧操作的配置快照调用官方能力。
func (p *currentDramaMediaProvider) FinalizeUpload(ctx context.Context, space, vid string) (operations.MediaAsset, error) {
	provider, err := p.current(ctx)
	if err != nil {
		return operations.MediaAsset{}, err
	}
	return provider.FinalizeUpload(ctx, space, vid)
}

// ObserveMedia 使用本次剧操作的配置快照调用官方能力。
func (p *currentDramaMediaProvider) ObserveMedia(ctx context.Context, vid string) (operations.MediaObservation, error) {
	provider, err := p.current(ctx)
	if err != nil {
		return operations.MediaObservation{}, err
	}
	return provider.ObserveMedia(ctx, vid)
}

// SyncVODEpisodeMetadata 使用本次剧操作的配置快照调用官方能力。
func (p *currentDramaMediaProvider) SyncVODEpisodeMetadata(ctx context.Context, input operations.VODEpisodeMetadata, observed operations.MediaObservation) error {
	provider, err := p.current(ctx)
	if err != nil {
		return err
	}
	return provider.SyncVODEpisodeMetadata(ctx, input, observed)
}

// ObserveMediaResource 使用本次剧操作的配置快照调用官方能力。
func (p *currentDramaMediaProvider) ObserveMediaResource(ctx context.Context, input operations.MediaResource) (operations.MediaResourceObservation, error) {
	provider, err := p.current(ctx)
	if err != nil {
		return operations.MediaResourceObservation{}, err
	}
	return provider.ObserveMediaResource(ctx, input)
}

// PublicationMediaScope 使用本次剧操作的配置快照调用官方能力。
func (p *currentDramaMediaProvider) PublicationMediaScope(ctx context.Context) (string, string, error) {
	provider, err := p.current(ctx)
	if err != nil {
		return "", "", err
	}
	return provider.PublicationMediaScope(ctx)
}

// ProcessingTimeout 使用本次剧操作的配置快照调用官方能力。
func (p *currentDramaMediaProvider) ProcessingTimeout(ctx context.Context) (time.Duration, error) {
	provider, err := p.current(ctx)
	if err != nil {
		return 0, err
	}
	return provider.ProcessingTimeout(ctx)
}

// SubmitEntryPreview 使用任务冻结的空间提交独立片段剪辑。
func (p *currentDramaMediaProvider) SubmitEntryPreview(ctx context.Context, vid string, seconds int) (string, error) {
	provider, err := p.current(ctx)
	if err != nil {
		return "", err
	}
	return provider.SubmitEntryPreview(ctx, vid, seconds)
}

// EntryPreviewResult 在任务冻结的空间确认并发布片段。
func (p *currentDramaMediaProvider) EntryPreviewResult(ctx context.Context, requestID, originalVID string, seconds int) (string, error) {
	provider, err := p.current(ctx)
	if err != nil {
		return "", err
	}
	return provider.EntryPreviewResult(ctx, requestID, originalVID, seconds)
}
