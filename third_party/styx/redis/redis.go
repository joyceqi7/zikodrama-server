// Package redis 提供纳入 Runtime 生命周期的 Redis 客户端工厂。
package redis

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/internal/lifecycle"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

// Default configuration values
const (
	defaultProtocol        = 2
	DefaultSlowThreshold   = 100 * time.Millisecond
	DefaultPoolSize        = 10
	DefaultMinIdleConns    = 3
	DefaultMaxIdleConns    = 10
	DefaultConnMaxLifetime = time.Hour
	DefaultConnMaxIdleTime = 30 * time.Minute
	DefaultDialTimeout     = 5 * time.Second
	DefaultReadTimeout     = 3 * time.Second
	DefaultWriteTimeout    = 3 * time.Second
)

// New 创建带 OTel 插桩的 Redis 客户端，支持单机、哨兵和集群模式。
// 成功连接后由 Runtime 统一关闭。
func New(ctx context.Context, name string, cfg *configv1.RedisConfig, runtime *styxruntime.Runtime) (redis.UniversalClient, error) {
	if ctx == nil {
		return nil, fmt.Errorf("redis: context is required")
	}
	if cfg == nil {
		return nil, fmt.Errorf("redis: config is nil")
	}
	if len(cfg.Addrs) == 0 {
		return nil, fmt.Errorf("redis: addrs is required")
	}
	for _, address := range cfg.Addrs {
		if strings.TrimSpace(address) == "" {
			return nil, fmt.Errorf("redis: address must not be empty")
		}
	}
	if cfg.Db < 0 || cfg.PoolSize < 0 || cfg.MinIdleConns < 0 || cfg.MaxIdleConns < 0 || cfg.MaxActiveConns < 0 {
		return nil, fmt.Errorf("redis: database and pool limits must not be negative")
	}

	// Apply defaults
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = "standalone"
	}
	if mode == "standalone" && len(cfg.Addrs) != 1 {
		return nil, fmt.Errorf("redis: standalone mode requires exactly one address")
	}
	if mode == "cluster" && cfg.Db != 0 {
		return nil, fmt.Errorf("redis: cluster mode does not support a database index")
	}

	poolSize := int(cfg.PoolSize)
	if poolSize <= 0 {
		poolSize = DefaultPoolSize
	}

	minIdleConns := int(cfg.MinIdleConns)
	if minIdleConns <= 0 {
		minIdleConns = min(DefaultMinIdleConns, poolSize)
	}

	maxIdleConns := int(cfg.MaxIdleConns)
	if maxIdleConns <= 0 {
		maxIdleConns = min(DefaultMaxIdleConns, poolSize)
	}
	if minIdleConns > poolSize || maxIdleConns > poolSize || minIdleConns > maxIdleConns {
		return nil, fmt.Errorf("redis: idle connection limits must fit within pool_size")
	}

	maxActiveConns := int(cfg.MaxActiveConns)
	// 0 means unlimited

	connMaxLifetime := DefaultConnMaxLifetime
	if cfg.ConnMaxLifetime != nil {
		connMaxLifetime = cfg.ConnMaxLifetime.AsDuration()
		if connMaxLifetime <= 0 {
			return nil, fmt.Errorf("redis: connection max lifetime must be positive")
		}
	}

	connMaxIdleTime := DefaultConnMaxIdleTime
	if cfg.ConnMaxIdleTime != nil {
		connMaxIdleTime = cfg.ConnMaxIdleTime.AsDuration()
		if connMaxIdleTime <= 0 {
			return nil, fmt.Errorf("redis: connection max idle time must be positive")
		}
	}

	dialTimeout := DefaultDialTimeout
	if cfg.DialTimeout != nil {
		dialTimeout = cfg.DialTimeout.AsDuration()
		if dialTimeout <= 0 {
			return nil, fmt.Errorf("redis: dial timeout must be positive")
		}
	}

	readTimeout := DefaultReadTimeout
	if cfg.ReadTimeout != nil {
		readTimeout = cfg.ReadTimeout.AsDuration()
		if readTimeout <= 0 {
			return nil, fmt.Errorf("redis: read timeout must be positive")
		}
	}

	writeTimeout := DefaultWriteTimeout
	if cfg.WriteTimeout != nil {
		writeTimeout = cfg.WriteTimeout.AsDuration()
		if writeTimeout <= 0 {
			return nil, fmt.Errorf("redis: write timeout must be positive")
		}
	}

	// Build TLS config if enabled
	var tlsConfig *tls.Config
	if cfg.Tls != nil && cfg.Tls.Enabled {
		var err error
		tlsConfig, err = buildRedisTLSConfig(cfg.Tls)
		if err != nil {
			return nil, fmt.Errorf("redis: failed to build TLS config: %w", err)
		}
	}

	var client redis.UniversalClient

	switch mode {
	case "standalone":
		client = redis.NewClient(newStandaloneOptions(
			cfg,
			tlsConfig,
			poolSize,
			minIdleConns,
			maxIdleConns,
			maxActiveConns,
			connMaxLifetime,
			connMaxIdleTime,
			dialTimeout,
			readTimeout,
			writeTimeout,
		))

	case "sentinel":
		if cfg.MasterName == "" {
			return nil, fmt.Errorf("redis: master_name is required for sentinel mode")
		}
		client = redis.NewFailoverClient(newFailoverOptions(
			cfg,
			tlsConfig,
			poolSize,
			minIdleConns,
			maxIdleConns,
			maxActiveConns,
			connMaxLifetime,
			connMaxIdleTime,
			dialTimeout,
			readTimeout,
			writeTimeout,
		))

	case "cluster":
		client = redis.NewClusterClient(newClusterOptions(
			cfg,
			tlsConfig,
			poolSize,
			minIdleConns,
			maxIdleConns,
			maxActiveConns,
			connMaxLifetime,
			connMaxIdleTime,
			dialTimeout,
			readTimeout,
			writeTimeout,
		))

	default:
		return nil, fmt.Errorf("redis: unsupported mode: %s", mode)
	}

	slowThreshold := DefaultSlowThreshold
	if cfg.SlowThreshold != nil && cfg.SlowThreshold.AsDuration() > 0 {
		slowThreshold = cfg.SlowThreshold.AsDuration()
	} else if cfg.SlowThreshold != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: slow threshold must be positive")
	}
	return initializeClient(ctx, name, mode, client, dialTimeout, !cfg.DisableTracing, !cfg.DisableMetrics, slowThreshold, runtime)
}

// NewWithOptions 使用 go-redis 原生 UniversalOptions 创建客户端。
func NewWithOptions(ctx context.Context, name string, options *redis.UniversalOptions, runtime *styxruntime.Runtime) (redis.UniversalClient, error) {
	if options == nil {
		return nil, fmt.Errorf("redis: options are required")
	}
	client := redis.NewUniversalClient(options)
	return initializeClient(ctx, name, "universal", client, options.DialTimeout, true, true, DefaultSlowThreshold, runtime)
}

// OpenURL 使用 go-redis 原生 URL 解析器保留 URL 中的全部连接参数。
func OpenURL(ctx context.Context, name, rawURL string, runtime *styxruntime.Runtime) (redis.UniversalClient, error) {
	options, err := redis.ParseURL(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("redis: invalid URL configuration")
	}
	client := redis.NewClient(options)
	return initializeClient(ctx, name, "url", client, options.DialTimeout, true, true, DefaultSlowThreshold, runtime)
}

func initializeClient(
	ctx context.Context,
	name string,
	mode string,
	client redis.UniversalClient,
	dialTimeout time.Duration,
	tracingEnabled bool,
	metricsEnabled bool,
	slowThreshold time.Duration,
	runtime *styxruntime.Runtime,
) (redis.UniversalClient, error) {
	if ctx == nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: context is required")
	}
	if err := ctx.Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: initialization context: %w", err)
	}
	if runtime == nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: runtime is required")
	}
	dependencies := runtime.Dependencies()
	if err := dependencies.Validate(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: runtime dependencies: %w", err)
	}
	if dialTimeout <= 0 {
		dialTimeout = DefaultDialTimeout
	}
	if tracingEnabled {
		if err := redisotel.InstrumentTracing(
			client,
			redisotel.WithTracerProvider(dependencies.TracerProvider()),
			redisotel.WithDBStatement(false),
		); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("redis: enable tracing failed: %w", err)
		}
	}
	if metricsEnabled {
		if err := redisotel.InstrumentMetrics(client, redisotel.WithMeterProvider(dependencies.MeterProvider())); err != nil {
			dependencies.Logger().Warn("redis: enable metrics failed", "error", err)
		}
	}
	client.AddHook(NewLoggingHook(name, slowThreshold, dependencies))

	pingContext, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	if err := client.Ping(pingContext).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: ping failed: %w", err)
	}
	dependencies.Logger().Info("redis: connected", "name", name, "mode", mode)

	shutdown := styxruntime.BoundedClose(client.Close)
	if err := runtime.Own("redis "+name, shutdown); err != nil {
		return nil, lifecycle.Rollback("redis "+name, shutdown, err)
	}
	return client, nil
}

func newStandaloneOptions(
	cfg *configv1.RedisConfig,
	tlsConfig *tls.Config,
	poolSize, minIdleConns, maxIdleConns, maxActiveConns int,
	connMaxLifetime, connMaxIdleTime, dialTimeout, readTimeout, writeTimeout time.Duration,
) *redis.Options {
	return &redis.Options{
		Addr:            cfg.Addrs[0],
		Username:        cfg.Username,
		Password:        cfg.Password,
		DB:              int(cfg.Db),
		Protocol:        defaultProtocol,
		PoolSize:        poolSize,
		MinIdleConns:    minIdleConns,
		MaxIdleConns:    maxIdleConns,
		MaxActiveConns:  maxActiveConns,
		ConnMaxLifetime: connMaxLifetime,
		ConnMaxIdleTime: connMaxIdleTime,
		DialTimeout:     dialTimeout,
		ReadTimeout:     readTimeout,
		WriteTimeout:    writeTimeout,
		TLSConfig:       tlsConfig,
		DisableIdentity: cfg.DisableIdentity,
	}
}

func newFailoverOptions(
	cfg *configv1.RedisConfig,
	tlsConfig *tls.Config,
	poolSize, minIdleConns, maxIdleConns, maxActiveConns int,
	connMaxLifetime, connMaxIdleTime, dialTimeout, readTimeout, writeTimeout time.Duration,
) *redis.FailoverOptions {
	return &redis.FailoverOptions{
		MasterName:       cfg.MasterName,
		SentinelAddrs:    cfg.Addrs,
		SentinelUsername: cfg.SentinelUsername,
		SentinelPassword: cfg.SentinelPassword,
		Username:         cfg.Username,
		Password:         cfg.Password,
		DB:               int(cfg.Db),
		Protocol:         defaultProtocol,
		PoolSize:         poolSize,
		MinIdleConns:     minIdleConns,
		MaxIdleConns:     maxIdleConns,
		MaxActiveConns:   maxActiveConns,
		ConnMaxLifetime:  connMaxLifetime,
		ConnMaxIdleTime:  connMaxIdleTime,
		DialTimeout:      dialTimeout,
		ReadTimeout:      readTimeout,
		WriteTimeout:     writeTimeout,
		TLSConfig:        tlsConfig,
		DisableIdentity:  cfg.DisableIdentity,
	}
}

func newClusterOptions(
	cfg *configv1.RedisConfig,
	tlsConfig *tls.Config,
	poolSize, minIdleConns, maxIdleConns, maxActiveConns int,
	connMaxLifetime, connMaxIdleTime, dialTimeout, readTimeout, writeTimeout time.Duration,
) *redis.ClusterOptions {
	return &redis.ClusterOptions{
		Addrs:           cfg.Addrs,
		Username:        cfg.Username,
		Password:        cfg.Password,
		Protocol:        defaultProtocol,
		PoolSize:        poolSize,
		MinIdleConns:    minIdleConns,
		MaxIdleConns:    maxIdleConns,
		MaxActiveConns:  maxActiveConns,
		ConnMaxLifetime: connMaxLifetime,
		ConnMaxIdleTime: connMaxIdleTime,
		DialTimeout:     dialTimeout,
		ReadTimeout:     readTimeout,
		WriteTimeout:    writeTimeout,
		TLSConfig:       tlsConfig,
		ReadOnly:        cfg.ReadOnly,
		RouteByLatency:  cfg.RouteByLatency,
		RouteRandomly:   cfg.RouteRandomly,
		DisableIdentity: cfg.DisableIdentity,
	}
}

// buildRedisTLSConfig builds TLS configuration for Redis.
func buildRedisTLSConfig(cfg *configv1.RedisTLSConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		ServerName:         cfg.ServerName,
		MinVersion:         tls.VersionTLS12,
	}

	// Load CA certificate
	if cfg.CaFile != "" {
		caCert, err := os.ReadFile(cfg.CaFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = caCertPool
	}

	// Load client certificate
	if (cfg.CertFile == "") != (cfg.KeyFile == "") {
		return nil, fmt.Errorf("cert_file and key_file must be configured together")
	}
	if cfg.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}
