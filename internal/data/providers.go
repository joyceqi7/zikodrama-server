package data

import (
	"context"
	"crypto/sha256"
	"net/http"
	"sync"
	"time"

	styxclient "gitee.com/linpbin/styx/client"
	styxruntime "gitee.com/linpbin/styx/runtime"

	applicationroot "drama-server/internal/biz"
	domainapplication "drama-server/internal/biz/application"
	"drama-server/internal/biz/mediaconfig"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/visitor"
	conf "drama-server/internal/conf"
	tiktokadapter "drama-server/internal/data/identity/tiktok"
	"drama-server/internal/data/platform/tiktokmedia"
)

const tiktokMediaRequestTimeout = 30 * time.Second

// TikTokMediaClientPool 按应用复用官方 Media Asset 客户端及其两小时 Access Token 缓存。
// 缓存指纹不保存明文密钥；应用 Client Key 或 Secret 轮换后会自动创建新客户端。
type TikTokMediaClientPool struct {
	mu            sync.Mutex
	httpClient    *http.Client
	legacySecrets map[string]string
	clients       map[string]tikTokMediaClientEntry
}

type tikTokMediaClientEntry struct {
	fingerprint [32]byte
	client      *tiktokmedia.Client
}

// MediaResources 聚合运营侧 BytePlus VOD 媒资写入端口。
type MediaResources struct {
	// 保存当前业务依赖的外部服务提供器。
	Provider operations.MediaProvider
}

// NewMediaResources 创建由启动配置驱动的官方 BytePlus VOD 适配器。
// 视频由管理端直接上传 VOD，服务端不保存任何实例本地文件。
func NewMediaResources(mediaConfig *mediaconfig.Service, _ *Resources) (*MediaResources, error) {
	provider := &currentDramaMediaProvider{config: mediaConfig}
	return &MediaResources{Provider: provider}, nil
}

// NewMediaProvider 只向 biz 层暴露媒资能力接口。
func NewMediaProvider(resources *MediaResources) operations.MediaProvider { return resources.Provider }

// IdentityProviderFactory 根据应用配置创建 TikTok OAuth Provider。
type IdentityProviderFactory = applicationroot.IdentityProviderFactory

// 创建并返回身份信息服务提供器实例。
func NewIdentityProviderFactory(ctx context.Context, runtime *styxruntime.Runtime, settings conf.Config) (IdentityProviderFactory, error) {
	httpClient, err := styxclient.NewExternalHTTPClient(ctx, "tiktok-oauth", 8*time.Second, runtime)
	if err != nil {
		return nil, err
	}
	factory := func(app domainapplication.Application) visitor.IdentityProvider {
		secret := applicationClientSecret(app, settings.LegacyTikTokClientSecrets)
		if !conf.Configured(app.ClientKey) || !conf.Configured(secret) {
			return nil
		}
		return tiktokadapter.NewOAuthClient(app.ClientKey, secret, httpClient)
	}
	return factory, nil
}

// NewTikTokMediaClientPool 创建使用受托管 HTTP Client 的官方媒资客户端池。
// Album 原声语言直接使用官方字符串代码，不维护自定义整数映射。
func NewTikTokMediaClientPool(ctx context.Context, runtime *styxruntime.Runtime, settings conf.Config) (*TikTokMediaClientPool, error) {
	httpClient, err := styxclient.NewExternalHTTPClient(ctx, "tiktok-media", tiktokMediaRequestTimeout, runtime)
	if err != nil {
		return nil, err
	}
	return &TikTokMediaClientPool{
		httpClient:    httpClient,
		legacySecrets: settings.LegacyTikTokClientSecrets,
		clients:       make(map[string]tikTokMediaClientEntry),
	}, nil
}

// ClientForApplication 返回与当前应用凭据匹配的官方客户端。
// Client Secret 只从服务端字段或配置文件中的历史引用映射读取，不进入日志或领域对象。
func (pool *TikTokMediaClientPool) ClientForApplication(app domainapplication.Application) *tiktokmedia.Client {
	if pool == nil || pool.httpClient == nil {
		return nil
	}
	app = app.Normalized()
	secret := applicationClientSecret(app, pool.legacySecrets)
	if !conf.Configured(app.ClientKey) || !conf.Configured(secret) {
		return nil
	}
	cacheKey := app.TikTokAppID
	if cacheKey == "" {
		cacheKey = app.ClientKey
	}
	fingerprint := sha256.Sum256([]byte(app.ClientKey + "\x00" + secret))
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if cached, ok := pool.clients[cacheKey]; ok && cached.fingerprint == fingerprint {
		return cached.client
	}
	client := tiktokmedia.NewClient(app.ClientKey, secret, pool.httpClient)
	pool.clients[cacheKey] = tikTokMediaClientEntry{fingerprint: fingerprint, client: client}
	return client
}

// applicationClientSecret 优先使用应用保存的密钥，再解析配置文件中的历史引用。
func applicationClientSecret(app domainapplication.Application, legacySecrets map[string]string) string {
	if app.ClientSecret != "" {
		return app.ClientSecret
	}
	if app.SecretRef == "" {
		return ""
	}
	return legacySecrets[app.SecretRef]
}

// NewTikTokMediaProviderFactory 把共享客户端池适配为运营发布用的官方媒资工厂。
func NewTikTokMediaProviderFactory(pool *TikTokMediaClientPool) operations.TikTokMediaProviderFactory {
	return func(app domainapplication.Application) operations.TikTokMediaProvider {
		client := pool.ClientForApplication(app)
		if client == nil {
			return nil
		}
		return client
	}
}
