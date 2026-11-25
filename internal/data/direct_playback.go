package data

import (
	"context"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/biz/mediaconfig"
	conf "drama-server/internal/conf"
	"drama-server/internal/data/persistence/mysqlstore"
	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod"
	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod/models/request"
	"log/slog"
	"time"
)

// DirectPlaybackProvider 使用官方 Go SDK 本地签名，不另建 Node 服务。
type DirectPlaybackProvider struct {
	config          *globalconfig.Service
	tokenTTLSeconds int
}

// NewDirectPlaybackProvider 注入持久化 VOD 凭据服务与已经校验的部署参数。
func NewDirectPlaybackProvider(config *globalconfig.Service, settings conf.Config) *DirectPlaybackProvider {
	return &DirectPlaybackProvider{config: config, tokenTTLSeconds: settings.DirectPlaybackTokenTTLSeconds}
}

// Configuration 仅返回通道与基础版能力；License 及其 SDK AppID 由 Mini 随包提供。
func (p *DirectPlaybackProvider) Configuration(ctx context.Context) (consumer.PlaybackSDKConfig, error) {
	return consumer.PlaybackSDKConfig{Provider: "byteplus_web", Channel: "byteplus", Capabilities: &consumer.PlaybackCapabilities{MediaPreload: false}}, nil
}

// Sign 按配置文件的有效期签发单个 VID 的 HTTPS 授权，空清晰度允许未转码原片；失败不返回凭据或过期时间。
func (p *DirectPlaybackProvider) Sign(ctx context.Context, e consumer.Episode) (string, time.Time, error) {
	config, err := p.config.Media(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "load direct playback media configuration failed", "error", err)
		return "", time.Time{}, err
	}
	// 资源来自已校验应用可见的不可变发布快照；历史版本保留原空间，不随当前主体配置改写。
	if config == nil || e.MediaSpaceName == "" {
		slog.WarnContext(ctx, "direct playback media configuration unavailable", "error", mediaconfig.ErrNotConfigured)
		return "", time.Time{}, mediaconfig.ErrNotConfigured
	}
	sdk := vod.NewInstanceWithRegion(config.Region)
	sdk.Client.SetAccessKey(config.AccessKeyID)
	sdk.Client.SetSecretKey(config.SecretAccessKey)
	token, err := sdk.GetPlayAuthToken(&request.VodGetPlayInfoRequest{Vid: e.VID, FileType: "video", Ssl: "1"}, p.tokenTTLSeconds)
	if err != nil {
		slog.ErrorContext(ctx, "sign direct playback authorization failed", "error", err)
		return "", time.Time{}, err
	}
	return token, time.Now().UTC().Add(time.Duration(p.tokenTTLSeconds) * time.Second), nil
}

// NewDirectPublicationService 组装直连版本上下架与已提交内容的缓存失效。
func NewDirectPublicationService(resources *Resources) *directplay.Service {
	return directplay.NewService(mysqlstore.NewDirectRepository(resources.store), (&invalidatingCatalogStore{cache: resources.catalogCache}).invalidate)
}
