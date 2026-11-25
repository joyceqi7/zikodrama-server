package client

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-kratos/aegis/circuitbreaker"
	"github.com/go-kratos/kratos/v2/middleware"
	cb "github.com/go-kratos/kratos/v2/middleware/circuitbreaker"
	"github.com/go-kratos/kratos/v2/middleware/metadata"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	googlegrpc "google.golang.org/grpc"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/internal/lifecycle"
	"gitee.com/linpbin/styx/internal/tlsutil"
	styxmw "gitee.com/linpbin/styx/middleware"
	"gitee.com/linpbin/styx/observe"
	styxnacos "gitee.com/linpbin/styx/registry/nacos"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

const (
	defaultGRPCClientTimeout  = 5 * time.Second
	defaultGRPCMaxRecvMsgSize = 4 * 1024 * 1024 // 4MB
	defaultGRPCMaxSendMsgSize = 4 * 1024 * 1024 // 4MB
)

// NewGRPCClient 创建固定接入 Tracing、Metadata、日志和可选熔断器的 gRPC 连接。
// 直连地址来自配置，服务发现能力来自 Runtime；成功连接后由 Runtime 统一关闭。
func NewGRPCClient(
	ctx context.Context,
	cfg *configv1.GRPCClientConfig,
	runtime *styxruntime.Runtime,
) (*googlegrpc.ClientConn, error) {
	if ctx == nil {
		return nil, fmt.Errorf("client: gRPC context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("client: gRPC context: %w", err)
	}
	if runtime == nil {
		return nil, fmt.Errorf("client: gRPC runtime is required")
	}
	dependencies := runtime.Dependencies()
	discovery := runtime.Discovery()
	if err := dependencies.Validate(); err != nil {
		return nil, fmt.Errorf("client: gRPC runtime dependencies: %w", err)
	}
	if err := validateGRPCConfig(cfg); err != nil {
		return nil, err
	}
	endpoint := strings.TrimSpace(cfg.GetEndpoint())
	discoveryName := strings.TrimSpace(cfg.GetDiscovery())
	// 1. Validate config - endpoint and discovery are mutually exclusive
	if endpoint == "" && discoveryName == "" {
		return nil, fmt.Errorf("client: gRPC endpoint or discovery is required")
	}
	if endpoint != "" && discoveryName != "" {
		return nil, fmt.Errorf("client: gRPC endpoint and discovery are mutually exclusive")
	}

	// 2. Build middleware chain
	mws := buildGRPCClientMiddlewares(cfg.GetMiddleware(), dependencies)

	// 3. Build ClientOptions
	clientOpts := []grpc.ClientOption{
		grpc.WithMiddleware(mws...),
	}

	// Timeout
	timeout := defaultGRPCClientTimeout
	if cfg.GetTimeout() != nil {
		timeout = cfg.GetTimeout().AsDuration()
		if timeout <= 0 {
			return nil, fmt.Errorf("client: gRPC timeout must be positive")
		}
	}
	clientOpts = append(clientOpts, grpc.WithTimeout(timeout))

	// Direct connection mode vs Service discovery mode
	if endpoint != "" {
		clientOpts = append(clientOpts, grpc.WithEndpoint(endpoint))
	} else {
		// Service discovery mode
		if discovery == nil {
			return nil, fmt.Errorf("client: discovery is required for service discovery mode")
		}

		// Cross-group discovery: if group is specified, create a group-override registry
		disc := discovery
		if group := strings.TrimSpace(cfg.GetGroup()); group != "" {
			if nacosReg, ok := discovery.(*styxnacos.Registry); ok {
				override, err := nacosReg.WithGroupOverride(group)
				if err != nil {
					return nil, err
				}
				disc = override
			} else {
				return nil, fmt.Errorf("client: group override requires Nacos discovery")
			}
		}

		clientOpts = append(clientOpts,
			grpc.WithDiscovery(disc),
			grpc.WithEndpoint("discovery:///"+discoveryName),
		)

		// Subset load balancing
		subsetSize := int(cfg.GetSubsetSize())
		if subsetSize <= 0 {
			subsetSize = defaultSubsetSize
		}
		clientOpts = append(clientOpts, grpc.WithSubset(subsetSize))
		// Note: Global selector should be set by user in main.go if needed
		// e.g., selector.SetGlobalSelector(wrr.NewBuilder())
	}

	// TLS configuration
	var useTLS bool
	if cfg.GetTls() != nil {
		if cfg.GetInsecure() {
			return nil, fmt.Errorf("client: gRPC TLS and insecure mode are mutually exclusive")
		}
		tlsConfig, err := tlsutil.LoadClientTLSConfig(cfg.GetTls())
		if err != nil {
			return nil, fmt.Errorf("client: failed to load TLS config: %w", err)
		}
		clientOpts = append(clientOpts, grpc.WithTLSConfig(tlsConfig))
		useTLS = true
	} else if !cfg.GetInsecure() {
		return nil, fmt.Errorf("client: gRPC requires TLS config or insecure=true")
	}

	// gRPC specific options
	grpcOpts := buildGRPCClientOptions(cfg)
	if len(grpcOpts) > 0 {
		clientOpts = append(clientOpts, grpc.WithOptions(grpcOpts...))
	}

	// Health check
	if cfg.GetHealthCheck() {
		clientOpts = append(clientOpts, grpc.WithHealthCheck(true))
	}

	// 4. Create Client Connection
	var conn *googlegrpc.ClientConn
	var err error
	if useTLS {
		conn, err = grpc.Dial(ctx, clientOpts...)
	} else {
		conn, err = grpc.DialInsecure(ctx, clientOpts...)
	}
	if err != nil {
		return nil, fmt.Errorf("client: failed to create gRPC client: %w", err)
	}
	shutdown := styxruntime.BoundedClose(conn.Close)
	if err := runtime.Own("gRPC client", shutdown); err != nil {
		return nil, lifecycle.Rollback("gRPC client", shutdown, err)
	}
	return conn, nil
}

// buildGRPCClientMiddlewares builds the gRPC client middleware chain.
// Order: Tracing -> Metadata -> ClientLogging -> CircuitBreaker
func buildGRPCClientMiddlewares(cfg *configv1.ClientMiddlewareConfig, dependencies observe.Dependencies) []middleware.Middleware {
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

// buildGRPCClientOptions builds native gRPC dial options.
func buildGRPCClientOptions(cfg *configv1.GRPCClientConfig) []googlegrpc.DialOption {
	var opts []googlegrpc.DialOption

	// Max receive message size
	maxRecvMsgSize := int(cfg.GetMaxRecvMsgSize())
	if maxRecvMsgSize <= 0 {
		maxRecvMsgSize = defaultGRPCMaxRecvMsgSize
	}
	opts = append(opts, googlegrpc.WithDefaultCallOptions(
		googlegrpc.MaxCallRecvMsgSize(maxRecvMsgSize),
	))

	// Max send message size
	maxSendMsgSize := int(cfg.GetMaxSendMsgSize())
	if maxSendMsgSize <= 0 {
		maxSendMsgSize = defaultGRPCMaxSendMsgSize
	}
	opts = append(opts, googlegrpc.WithDefaultCallOptions(
		googlegrpc.MaxCallSendMsgSize(maxSendMsgSize),
	))

	return opts
}
