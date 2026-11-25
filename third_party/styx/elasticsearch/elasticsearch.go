// Package elasticsearch 提供纳入 Runtime 生命周期的 Elasticsearch 客户端工厂。
package elasticsearch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/internal/lifecycle"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

// Default configuration values
const (
	DefaultMaxRetries            = 3
	DefaultMaxIdleConnsPerHost   = 10
	DefaultResponseHeaderTimeout = 30 * time.Second
)

// Default retry status codes
var DefaultRetryOnStatus = []int{502, 503, 504}

// NewElasticsearch 创建带 OTel Tracing 的 TypedClient，并把 Transport 关闭交给 Runtime。
func NewElasticsearch(ctx context.Context, name string, c *configv1.ElasticsearchConfig, runtime *styxruntime.Runtime) (*elasticsearch.TypedClient, error) {
	if ctx == nil {
		return nil, fmt.Errorf("elasticsearch: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("elasticsearch: initialization context: %w", err)
	}
	if runtime == nil {
		return nil, fmt.Errorf("elasticsearch: runtime is required")
	}
	dependencies := runtime.Dependencies()
	if err := dependencies.Validate(); err != nil {
		return nil, fmt.Errorf("elasticsearch: runtime dependencies: %w", err)
	}
	if c == nil {
		return nil, fmt.Errorf("elasticsearch: config is nil")
	}
	if len(c.Addresses) == 0 {
		return nil, fmt.Errorf("elasticsearch: addresses is required")
	}
	for _, address := range c.Addresses {
		parsed, err := url.Parse(strings.TrimSpace(address))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return nil, fmt.Errorf("elasticsearch: every address must be an HTTP(S) URL without embedded credentials")
		}
	}
	if c.MaxRetries < 0 || c.MaxIdleConnsPerHost < 0 {
		return nil, fmt.Errorf("elasticsearch: retry and connection pool limits must not be negative")
	}
	for _, status := range c.RetryOnStatus {
		if status < 100 || status > 599 {
			return nil, fmt.Errorf("elasticsearch: retry status must be a valid HTTP status code")
		}
	}

	// Apply defaults
	maxRetries := int(c.MaxRetries)
	if maxRetries <= 0 {
		maxRetries = DefaultMaxRetries
	}

	retryOnStatus := make([]int, 0, len(c.RetryOnStatus))
	for _, s := range c.RetryOnStatus {
		retryOnStatus = append(retryOnStatus, int(s))
	}
	if len(retryOnStatus) == 0 {
		retryOnStatus = DefaultRetryOnStatus
	}

	maxIdleConnsPerHost := int(c.MaxIdleConnsPerHost)
	if maxIdleConnsPerHost <= 0 {
		maxIdleConnsPerHost = DefaultMaxIdleConnsPerHost
	}

	responseHeaderTimeout := DefaultResponseHeaderTimeout
	if c.ResponseHeaderTimeout != nil {
		responseHeaderTimeout = c.ResponseHeaderTimeout.AsDuration()
		if responseHeaderTimeout <= 0 {
			return nil, fmt.Errorf("elasticsearch: response header timeout must be positive")
		}
	}

	// Build HTTP transport
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          maxIdleConnsPerHost * len(c.Addresses),
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}

	// TLS configuration
	if c.Tls != nil && c.Tls.Enabled {
		tlsConfig, err := buildESTLSConfig(c.Tls)
		if err != nil {
			return nil, fmt.Errorf("elasticsearch: failed to build TLS config: %w", err)
		}
		transport.TLSClientConfig = tlsConfig
	}

	// Build Elasticsearch config
	cfg := elasticsearch.Config{
		Addresses:     c.Addresses,
		Username:      c.Username,
		Password:      c.Password,
		MaxRetries:    maxRetries,
		RetryOnStatus: retryOnStatus,
		Transport:     transport,
	}

	// OTel instrumentation using official Elasticsearch instrumentation
	if !c.DisableTracing {
		cfg.Instrumentation = elasticsearch.NewOpenTelemetryInstrumentation(
			dependencies.TracerProvider(),
			false,
		)
	}

	client, err := elasticsearch.NewTypedClient(cfg)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("elasticsearch: client creation failed: %w", err)
	}

	// Verify connection by pinging the cluster
	ok, err := client.Ping().Do(ctx)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("elasticsearch: connection check failed: %w", err)
	}
	if !ok {
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("elasticsearch: ping returned false")
	}

	dependencies.Logger().Info("elasticsearch: connected", "name", name, "address_count", len(c.Addresses))

	shutdown := styxruntime.BoundedClose(func() error {
		transport.CloseIdleConnections()
		return nil
	})
	if err := runtime.Own("elasticsearch "+name, shutdown); err != nil {
		return nil, lifecycle.Rollback("elasticsearch "+name, shutdown, err)
	}
	return client, nil
}

// buildESTLSConfig builds TLS configuration for Elasticsearch.
func buildESTLSConfig(cfg *configv1.ElasticsearchTLSConfig) (*tls.Config, error) {
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
