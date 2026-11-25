// Package server 提供带固定治理链路的 Kratos HTTP/gRPC Server 工厂。
package server

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/metadata"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/go-kratos/kratos/v2/transport/http"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/internal/tlsutil"
	styxmw "gitee.com/linpbin/styx/middleware"
	"gitee.com/linpbin/styx/observe"
	"gitee.com/linpbin/styx/requestmeta"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

const (
	defaultHTTPTimeout = 10 * time.Second
	defaultNetwork     = "tcp"
)

// NewHTTPServer 创建固定接入请求标识、Recovery、Tracing、Metadata、校验和访问日志的 HTTP Server。
// 请求标识始终写入 Context 和响应头，不受访问日志与链路追踪开关影响。
// HTTPOption 只能注入 Listener 或在内建 Recovery 内层追加业务 Filter。
func NewHTTPServer(cfg *configv1.HTTPServerConfig, runtime *styxruntime.Runtime, options ...HTTPOption) (*http.Server, error) {
	if runtime == nil {
		return nil, fmt.Errorf("server: HTTP runtime is required")
	}
	serviceName := runtime.Name()
	dependencies := runtime.Dependencies()
	if err := dependencies.Validate(); err != nil {
		return nil, fmt.Errorf("server: HTTP runtime dependencies: %w", err)
	}
	if strings.TrimSpace(serviceName) == "" {
		return nil, fmt.Errorf("server: service name is required")
	}
	// 1. Validate config
	resolvedOptions, err := resolveHTTPOptions(options...)
	if err != nil {
		return nil, err
	}
	address := strings.TrimSpace(cfg.GetAddr())
	if address == "" && resolvedOptions.listener != nil {
		address = resolvedOptions.listener.Addr().String()
	}
	if address == "" {
		return nil, fmt.Errorf("server: HTTP addr is required")
	}
	if prefix := cfg.GetPathPrefix(); prefix != "" && !strings.HasPrefix(prefix, "/") {
		return nil, fmt.Errorf("server: HTTP path_prefix must start with '/'")
	}
	// 2. Build middleware chain
	mws := buildServerMiddlewares(cfg.GetMiddleware(), serviceName, dependencies)

	// 3. Build ServerOptions
	serverOpts := []http.ServerOption{
		http.Address(address),
		http.Middleware(mws...),
	}
	if resolvedOptions.listener != nil {
		serverOpts = append(serverOpts, http.Listener(resolvedOptions.listener))
	}

	// HTTP filters — tracing + access log (covers all routes including raw HTTP handlers)
	// Note: http.Filter replaces (not appends), so all filters must be in one call.
	// FilterChain applies in array order: first filter is outermost.
	// 请求标识先于其他治理 Filter 建立，恢复、业务 Filter 和关闭日志的请求均可读取。
	httpFilters := []http.FilterFunc{requestmeta.HTTPMiddleware}
	if !cfg.GetMiddleware().GetDisableTracing() {
		httpFilters = append(httpFilters, styxmw.HTTPTracing(dependencies))
	}
	if !cfg.GetMiddleware().GetDisableLogging() {
		httpFilters = append(httpFilters, styxmw.HTTPAccessLog(dependencies))
	}
	httpFilters = append(httpFilters, styxmw.HTTPRecovery(serviceName, dependencies))
	httpFilters = append(httpFilters, resolvedOptions.filters...)
	serverOpts = append(serverOpts, http.Filter(httpFilters...))

	// Network
	network := cfg.GetNetwork()
	if network == "" {
		network = defaultNetwork
	}
	serverOpts = append(serverOpts, http.Network(network))

	// Timeout
	timeout := defaultHTTPTimeout
	if cfg.GetTimeout() != nil {
		timeout = cfg.GetTimeout().AsDuration()
		if timeout <= 0 {
			return nil, fmt.Errorf("server: HTTP timeout must be positive")
		}
	}
	serverOpts = append(serverOpts, http.Timeout(timeout))

	// Endpoint (for service registry)
	if endpoint := strings.TrimSpace(cfg.GetEndpoint()); endpoint != "" {
		ep, err := parseEndpoint(endpoint)
		if err != nil {
			return nil, fmt.Errorf("server: invalid HTTP endpoint: %w", err)
		}
		expectedScheme := "http"
		if cfg.GetTls() != nil {
			expectedScheme = "https"
		}
		if ep.Scheme != expectedScheme {
			return nil, fmt.Errorf("server: HTTP endpoint scheme must be %s", expectedScheme)
		}
		serverOpts = append(serverOpts, http.Endpoint(ep))
	}

	// TLS
	if cfg.GetTls() != nil {
		tlsConfig, err := tlsutil.LoadServerTLSConfig(cfg.GetTls())
		if err != nil {
			return nil, fmt.Errorf("server: failed to load TLS config: %w", err)
		}
		serverOpts = append(serverOpts, http.TLSConfig(tlsConfig))
	}

	// Path prefix
	if cfg.GetPathPrefix() != "" {
		serverOpts = append(serverOpts, http.PathPrefix(cfg.GetPathPrefix()))
	}

	// 4. Create Server
	return http.NewServer(serverOpts...), nil
}

// buildServerMiddlewares builds the HTTP server middleware chain.
// Order: Recovery -> Tracing -> Metadata -> Validate
// Note: Recovery is always enabled and cannot be disabled.
// Note: Logging is handled by HTTPAccessLog filter at the HTTP transport level.
// It covers raw HTTP handlers without recording query strings; request and response bodies are bounded.
func buildServerMiddlewares(cfg *configv1.MiddlewareConfig, serviceName string, dependencies observe.Dependencies) []middleware.Middleware {
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
	if !cfg.GetDisableValidate() {
		mws = append(mws, styxmw.Validate())
	}

	return mws
}

// parseEndpoint parses endpoint URL string.
func parseEndpoint(endpoint string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("endpoint must contain only scheme, host, and port")
	}
	_, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return nil, fmt.Errorf("endpoint must include an explicit port")
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return nil, fmt.Errorf("endpoint port must be between 1 and 65535")
	}
	return parsed, nil
}
