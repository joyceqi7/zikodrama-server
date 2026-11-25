package nacos

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/internal/lifecycle"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

const (
	defaultTimeout = 5 * time.Second
	defaultPort    = 8848
)

// NewRegistry 根据配置创建 Nacos Registry，并安装到 Runtime 的注册、发现和关闭能力中。
func NewRegistry(ctx context.Context, cfg *configv1.NacosConfig, runtime *styxruntime.Runtime, opts ...Option) (*Registry, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nacos: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("nacos: initialization context: %w", err)
	}
	if runtime == nil {
		return nil, fmt.Errorf("nacos: runtime is required")
	}
	dependencies := runtime.Dependencies()
	if err := dependencies.Validate(); err != nil {
		return nil, fmt.Errorf("nacos: runtime dependencies: %w", err)
	}
	if cfg == nil {
		return nil, fmt.Errorf("nacos: config is required")
	}

	if len(cfg.Servers) == 0 {
		return nil, fmt.Errorf("nacos: at least one server is required")
	}

	// Build server configs
	serverConfigs := make([]constant.ServerConfig, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		if strings.TrimSpace(s.IpAddr) == "" {
			return nil, fmt.Errorf("nacos: server ip_addr is required")
		}

		port := uint64(s.Port)
		if port == 0 {
			port = defaultPort
		}
		grpcPort := uint64(s.GrpcPort)
		if grpcPort == 0 {
			grpcPort = port + 1000
		}
		if port > 65535 || grpcPort > 65535 {
			return nil, fmt.Errorf("nacos: server port must be between 1 and 65535")
		}

		scheme := strings.ToLower(strings.TrimSpace(s.Scheme))
		if scheme == "" {
			scheme = "http"
		}
		if scheme != "http" && scheme != "https" {
			return nil, fmt.Errorf("nacos: server scheme must be http or https")
		}

		serverConfigs = append(serverConfigs, constant.ServerConfig{
			IpAddr:   strings.TrimSpace(s.IpAddr),
			Port:     port,
			GrpcPort: grpcPort,
			Scheme:   scheme,
		})
	}

	// Build client config
	timeout := defaultTimeout
	if cfg.Timeout != nil {
		timeout = cfg.Timeout.AsDuration()
		if timeout < time.Millisecond {
			return nil, fmt.Errorf("nacos: timeout must be at least one millisecond")
		}
	}

	logLevel := strings.ToLower(strings.TrimSpace(cfg.LogLevel))
	if logLevel == "" {
		logLevel = "warn"
	}
	if _, ok := levelMap[logLevel]; !ok {
		return nil, fmt.Errorf("nacos: log level must be debug, info, warn, or error")
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return nil, fmt.Errorf("nacos: username and password must be configured together")
	}
	if (cfg.AccessKey == "") != (cfg.SecretKey == "") {
		return nil, fmt.Errorf("nacos: access_key and secret_key must be configured together")
	}
	notLoadCacheAtStart, err := resolveNotLoadCacheAtStart(cfg.GetCacheStartMode())
	if err != nil {
		return nil, err
	}

	clientConfig := constant.ClientConfig{
		TimeoutMs:           uint64(timeout.Milliseconds()),
		NamespaceId:         cfg.NamespaceId,
		Username:            cfg.Username,
		Password:            cfg.Password,
		LogDir:              cfg.LogDir,
		CacheDir:            cfg.CacheDir,
		LogLevel:            logLevel,
		NotLoadCacheAtStart: notLoadCacheAtStart,
		AccessKey:           cfg.AccessKey,
		SecretKey:           cfg.SecretKey,
	}

	initializationContext, cancelInitialization := context.WithTimeout(ctx, timeout)
	defer cancelInitialization()

	client, shutdown, err := newManagedNamingClient(initializationContext, runtime, logLevel, dependencies.Logger(), []string{
		cfg.Username, cfg.Password, cfg.AccessKey, cfg.SecretKey,
	}, func() (naming_client.INamingClient, error) {
		return clients.NewNamingClient(vo.NacosClientParam{
			ClientConfig:  &clientConfig,
			ServerConfigs: serverConfigs,
		})
	})
	if err != nil {
		return nil, err
	}

	// Apply config options
	if strings.TrimSpace(cfg.Group) != "" {
		opts = append(opts, WithGroup(cfg.Group))
	}
	if strings.TrimSpace(cfg.Cluster) != "" {
		opts = append(opts, WithCluster(cfg.Cluster))
	}
	if cfg.Weight != 0 {
		opts = append(opts, WithWeight(cfg.Weight))
	}

	registry, err := New(client, runtime, opts...)
	if err != nil {
		return nil, lifecycle.Rollback("nacos", shutdown, err)
	}
	if err := runtime.AttachRegistry("nacos", registry, registry, shutdown); err != nil {
		return nil, lifecycle.Rollback("nacos", shutdown, err)
	}
	return registry, nil
}

func newManagedNamingClient(
	ctx context.Context,
	runtime *styxruntime.Runtime,
	logLevel string,
	logger *slog.Logger,
	secrets []string,
	factory func() (naming_client.INamingClient, error),
) (naming_client.INamingClient, styxruntime.ShutdownFunc, error) {
	// Nacos SDK Logger 是进程全局状态，必须由单个 Runtime 独占并在关闭时恢复。
	releaseLogger, err := acquireSlogAdapter(runtime, logLevel, logger, secrets...)
	if err != nil {
		return nil, nil, err
	}
	client, err, cleanupDeferred := newNamingClientWithContext(ctx, factory, func(created namingClientResult) {
		if created.client != nil {
			created.client.CloseClient()
		}
		releaseLogger()
	})
	if err != nil {
		if !cleanupDeferred {
			if client != nil {
				client.CloseClient()
			}
			releaseLogger()
		}
		return nil, nil, fmt.Errorf("nacos: create naming client failed: %w", err)
	}
	shutdown := styxruntime.BoundedClose(func() error {
		client.CloseClient()
		releaseLogger()
		return nil
	})
	healthy, err := namingClientHealthyWithContext(ctx, client, func() {
		_ = shutdown(context.Background())
	})
	if err != nil {
		return nil, nil, fmt.Errorf("nacos: check naming server health: %w", err)
	}
	if !healthy {
		return nil, nil, lifecycle.Rollback("nacos", shutdown, fmt.Errorf("nacos: naming server is unavailable"))
	}
	return client, shutdown, nil
}

func namingClientHealthyWithContext(ctx context.Context, client naming_client.INamingClient, cleanup func()) (bool, error) {
	result := make(chan bool, 1)
	go func() {
		result <- client.ServerHealthy()
	}()
	select {
	case healthy := <-result:
		return healthy, nil
	case <-ctx.Done():
		go func() {
			<-result
			cleanup()
		}()
		return false, ctx.Err()
	}
}

type namingClientResult struct {
	client naming_client.INamingClient
	err    error
}

func newNamingClientWithContext(
	ctx context.Context,
	factory func() (naming_client.INamingClient, error),
	cleanup func(namingClientResult),
) (naming_client.INamingClient, error, bool) {
	result := make(chan namingClientResult, 1)
	go func() {
		client, err := factory()
		result <- namingClientResult{client: client, err: err}
	}()
	select {
	case created := <-result:
		return created.client, created.err, false
	case <-ctx.Done():
		go func() {
			cleanup(<-result)
		}()
		return nil, ctx.Err(), true
	}
}

func resolveNotLoadCacheAtStart(mode configv1.NacosCacheStartMode) (bool, error) {
	switch mode {
	case configv1.NacosCacheStartMode_NACOS_CACHE_START_MODE_UNSPECIFIED,
		configv1.NacosCacheStartMode_NACOS_CACHE_START_MODE_SKIP:
		return true, nil
	case configv1.NacosCacheStartMode_NACOS_CACHE_START_MODE_LOAD:
		return false, nil
	default:
		return false, fmt.Errorf("nacos: unsupported cache_start_mode %d", mode)
	}
}
