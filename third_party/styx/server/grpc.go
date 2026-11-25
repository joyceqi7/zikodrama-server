package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/metadata"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	googlegrpc "google.golang.org/grpc"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/internal/tlsutil"
	styxmw "gitee.com/linpbin/styx/middleware"
	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

const (
	defaultGRPCTimeout    = 10 * time.Second
	defaultMaxRecvMsgSize = 4 * 1024 * 1024 // 4MB
	defaultMaxSendMsgSize = 4 * 1024 * 1024 // 4MB
)

// NewGRPCServer 创建固定接入 Recovery、Tracing、Metadata、日志和校验的 gRPC Server。
func NewGRPCServer(cfg *configv1.GRPCServerConfig, runtime *styxruntime.Runtime) (*grpc.Server, error) {
	if runtime == nil {
		return nil, fmt.Errorf("server: gRPC runtime is required")
	}
	serviceName := runtime.Name()
	dependencies := runtime.Dependencies()
	if err := dependencies.Validate(); err != nil {
		return nil, fmt.Errorf("server: gRPC runtime dependencies: %w", err)
	}
	if strings.TrimSpace(serviceName) == "" {
		return nil, fmt.Errorf("server: service name is required")
	}
	// 1. Validate config
	address := strings.TrimSpace(cfg.GetAddr())
	if address == "" {
		return nil, fmt.Errorf("server: gRPC addr is required")
	}
	if cfg.GetMaxRecvMsgSize() < 0 || cfg.GetMaxSendMsgSize() < 0 {
		return nil, fmt.Errorf("server: gRPC message size limits must not be negative")
	}

	// 2. Build middleware chain
	mws := buildGRPCServerMiddlewares(cfg.GetMiddleware(), serviceName, dependencies)

	// 3. Build ServerOptions
	serverOpts := []grpc.ServerOption{
		grpc.Address(address),
		grpc.Middleware(mws...),
	}

	// Network
	network := cfg.GetNetwork()
	if network == "" {
		network = defaultNetwork
	}
	serverOpts = append(serverOpts, grpc.Network(network))

	// Timeout
	timeout := defaultGRPCTimeout
	if cfg.GetTimeout() != nil {
		timeout = cfg.GetTimeout().AsDuration()
		if timeout <= 0 {
			return nil, fmt.Errorf("server: gRPC timeout must be positive")
		}
	}
	serverOpts = append(serverOpts, grpc.Timeout(timeout))

	// Endpoint (for service registry)
	if endpoint := strings.TrimSpace(cfg.GetEndpoint()); endpoint != "" {
		ep, err := parseEndpoint(endpoint)
		if err != nil {
			return nil, fmt.Errorf("server: invalid gRPC endpoint: %w", err)
		}
		expectedScheme := "grpc"
		if cfg.GetTls() != nil {
			expectedScheme = "grpcs"
		}
		if ep.Scheme != expectedScheme {
			return nil, fmt.Errorf("server: gRPC endpoint scheme must be %s", expectedScheme)
		}
		serverOpts = append(serverOpts, grpc.Endpoint(ep))
	}

	// TLS
	if cfg.GetTls() != nil {
		tlsConfig, err := tlsutil.LoadServerTLSConfig(cfg.GetTls())
		if err != nil {
			return nil, fmt.Errorf("server: failed to load TLS config: %w", err)
		}
		serverOpts = append(serverOpts, grpc.TLSConfig(tlsConfig))
	}

	// gRPC specific options
	serverOpts = append(serverOpts, grpc.Options(
		buildGRPCOptions(cfg)...,
	))
	if !cfg.GetEnableReflection() {
		serverOpts = append(serverOpts, grpc.DisableReflection())
	}

	// 4. Create Server
	srv := grpc.NewServer(serverOpts...)

	return srv, nil
}

// buildGRPCServerMiddlewares builds the gRPC server middleware chain.
// Order: Recovery -> Tracing -> Metadata -> Logging -> Validate
// Note: Recovery is always enabled and cannot be disabled.
func buildGRPCServerMiddlewares(cfg *configv1.MiddlewareConfig, serviceName string, dependencies observe.Dependencies) []middleware.Middleware {
	var mws []middleware.Middleware

	// Recovery is always enabled (cannot be disabled)
	mws = append(mws, styxmw.Recovery(serviceName, dependencies))

	if !cfg.GetDisableTracing() {
		mws = append(mws, tracing.Server(
			tracing.WithTracerProvider(dependencies.TracerProvider()),
			tracing.WithPropagator(dependencies.Propagator()),
		))
	}
	if !cfg.GetDisableMetadata() {
		mws = append(mws, metadata.Server())
	}
	if !cfg.GetDisableLogging() {
		mws = append(mws, styxmw.Logging(dependencies))
	}
	if !cfg.GetDisableValidate() {
		mws = append(mws, styxmw.Validate())
	}

	return mws
}

// buildGRPCOptions builds native gRPC server options.
func buildGRPCOptions(cfg *configv1.GRPCServerConfig) []googlegrpc.ServerOption {
	var opts []googlegrpc.ServerOption

	// Max receive message size
	maxRecvMsgSize := int(cfg.GetMaxRecvMsgSize())
	if maxRecvMsgSize <= 0 {
		maxRecvMsgSize = defaultMaxRecvMsgSize
	}
	opts = append(opts, googlegrpc.MaxRecvMsgSize(maxRecvMsgSize))

	// Max send message size
	maxSendMsgSize := int(cfg.GetMaxSendMsgSize())
	if maxSendMsgSize <= 0 {
		maxSendMsgSize = defaultMaxSendMsgSize
	}
	opts = append(opts, googlegrpc.MaxSendMsgSize(maxSendMsgSize))

	return opts
}
