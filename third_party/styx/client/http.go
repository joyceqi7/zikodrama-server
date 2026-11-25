// Package client 提供带固定治理链路的 Kratos HTTP/gRPC 客户端工厂。
package client

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-kratos/aegis/circuitbreaker"
	"github.com/go-kratos/aegis/circuitbreaker/sre"
	"github.com/go-kratos/kratos/v2/middleware"
	cb "github.com/go-kratos/kratos/v2/middleware/circuitbreaker"
	"github.com/go-kratos/kratos/v2/middleware/metadata"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/go-kratos/kratos/v2/transport/http"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/internal/lifecycle"
	"gitee.com/linpbin/styx/internal/tlsutil"
	styxmw "gitee.com/linpbin/styx/middleware"
	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

const (
	defaultClientTimeout    = 5 * time.Second
	defaultSubsetSize       = 25
	defaultSuccessThreshold = 0.5
	defaultRequestThreshold = 100
)

// NewHTTPClient 创建固定接入 Tracing、Metadata、日志和熔断器的 HTTP 客户端。
// 直连地址来自配置，服务发现能力来自 Runtime；成功连接后由 Runtime 统一关闭。
func NewHTTPClient(
	ctx context.Context,
	cfg *configv1.HTTPClientConfig,
	runtime *styxruntime.Runtime,
) (*http.Client, error) {
	if ctx == nil {
		return nil, fmt.Errorf("client: HTTP context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("client: HTTP context: %w", err)
	}
	if runtime == nil {
		return nil, fmt.Errorf("client: HTTP runtime is required")
	}
	dependencies := runtime.Dependencies()
	discovery := runtime.Discovery()
	if err := dependencies.Validate(); err != nil {
		return nil, fmt.Errorf("client: HTTP runtime dependencies: %w", err)
	}
	if err := validateHTTPConfig(cfg); err != nil {
		return nil, err
	}
	endpoint := strings.TrimSpace(cfg.GetEndpoint())
	discoveryName := strings.TrimSpace(cfg.GetDiscovery())
	// 1. Validate config - endpoint and discovery are mutually exclusive
	if endpoint == "" && discoveryName == "" {
		return nil, fmt.Errorf("client: HTTP endpoint or discovery is required")
	}
	if endpoint != "" && discoveryName != "" {
		return nil, fmt.Errorf("client: HTTP endpoint and discovery are mutually exclusive")
	}

	// 2. Build middleware chain
	mws := buildHTTPClientMiddlewares(cfg.GetMiddleware(), dependencies)

	// 3. Build ClientOptions
	clientOpts := []http.ClientOption{
		http.WithMiddleware(mws...),
	}

	// Timeout
	timeout := defaultClientTimeout
	if cfg.GetTimeout() != nil {
		timeout = cfg.GetTimeout().AsDuration()
		if timeout <= 0 {
			return nil, fmt.Errorf("client: HTTP timeout must be positive")
		}
	}
	clientOpts = append(clientOpts, http.WithTimeout(timeout))

	// Direct connection mode vs Service discovery mode
	if endpoint != "" {
		clientOpts = append(clientOpts, http.WithEndpoint(endpoint))
	} else {
		// Service discovery mode
		if discovery == nil {
			return nil, fmt.Errorf("client: discovery is required for service discovery mode")
		}
		clientOpts = append(clientOpts,
			http.WithDiscovery(discovery),
			http.WithEndpoint("discovery:///"+discoveryName),
		)

		// Subset load balancing
		subsetSize := int(cfg.GetSubsetSize())
		if subsetSize <= 0 {
			subsetSize = defaultSubsetSize
		}
		clientOpts = append(clientOpts, http.WithSubset(subsetSize))
		// Note: Global selector should be set by user in main.go if needed
		// e.g., selector.SetGlobalSelector(wrr.NewBuilder())
	}

	// TLS
	if cfg.GetTls() != nil {
		tlsConfig, err := tlsutil.LoadClientTLSConfig(cfg.GetTls())
		if err != nil {
			return nil, fmt.Errorf("client: failed to load TLS config: %w", err)
		}
		clientOpts = append(clientOpts, http.WithTLSConfig(tlsConfig))
	}

	// User-Agent
	if cfg.GetUserAgent() != "" {
		clientOpts = append(clientOpts, http.WithUserAgent(cfg.GetUserAgent()))
	}

	// Block mode
	if cfg.GetBlock() {
		clientOpts = append(clientOpts, http.WithBlock())
	}

	// 4. Create Client
	client, err := http.NewClient(ctx, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("client: failed to create HTTP client: %w", err)
	}
	shutdown := styxruntime.BoundedClose(client.Close)
	if err := runtime.Own("HTTP client", shutdown); err != nil {
		return nil, lifecycle.Rollback("HTTP client", shutdown, err)
	}
	return client, nil
}

// buildHTTPClientMiddlewares builds the HTTP client middleware chain.
// Order: Tracing -> Metadata -> ClientLogging -> CircuitBreaker
func buildHTTPClientMiddlewares(cfg *configv1.ClientMiddlewareConfig, dependencies observe.Dependencies) []middleware.Middleware {
	var mws []middleware.Middleware

	if !cfg.GetDisableTracing() {
		mws = append(mws, tracing.Client(
			tracing.WithTracerProvider(dependencies.TracerProvider()),
			tracing.WithPropagator(dependencies.Propagator()),
		))
	}
	if !cfg.GetDisableMetadata() {
		mws = append(mws, metadata.Client())
	}

	// ClientLogging 只记录下游操作、状态码、原因码和耗时。
	mws = append(mws, styxmw.ClientLogging(dependencies))

	if cfg.GetEnableCircuitbreaker() {
		// Use factory function to create per-operation breakers.
		// Kratos internally caches breakers by operation path (e.g., /Service/Method),
		// so each method gets its own isolated circuit breaker instance.
		cbCfg := cfg.GetCircuitbreaker()
		mws = append(mws, cb.Client(cb.WithCircuitBreaker(func() circuitbreaker.CircuitBreaker {
			return buildCircuitBreaker(cbCfg)
		})))
	}

	return mws
}

// buildCircuitBreaker builds a circuit breaker using Google SRE Breaker algorithm.
func buildCircuitBreaker(cfg *configv1.CircuitBreakerConfig) circuitbreaker.CircuitBreaker {
	var opts []sre.Option

	successThreshold := defaultSuccessThreshold
	if cfg != nil && cfg.GetSuccessThreshold() > 0 {
		successThreshold = cfg.GetSuccessThreshold()
	}
	opts = append(opts, sre.WithSuccess(successThreshold))

	requestThreshold := int64(defaultRequestThreshold)
	if cfg != nil && cfg.GetRequestThreshold() > 0 {
		requestThreshold = int64(cfg.GetRequestThreshold())
	}
	opts = append(opts, sre.WithRequest(requestThreshold))

	if cfg != nil && cfg.GetWindow() != nil {
		opts = append(opts, sre.WithWindow(cfg.GetWindow().AsDuration()))
	}
	if cfg != nil && cfg.GetBucket() > 0 {
		opts = append(opts, sre.WithBucket(int(cfg.GetBucket())))
	}

	return sre.NewBreaker(opts...)
}
