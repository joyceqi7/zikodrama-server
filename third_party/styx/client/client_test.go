package client

import (
	"context"
	"testing"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
	"github.com/go-kratos/kratos/v2/registry"
)

func TestNewHTTPClientRejectsEndpointDiscoveryConflict(t *testing.T) {
	_, err := NewHTTPClient(context.Background(), &configv1.HTTPClientConfig{
		Endpoint:  "http://127.0.0.1:8000",
		Discovery: "user-service",
	}, newClientRuntime(t, nil))
	if err == nil {
		t.Fatal("同时配置 endpoint 和 discovery 时必须失败")
	}
}

func TestClientRejectsBlankAddressAndUnsupportedGroupOverride(t *testing.T) {
	if _, err := NewHTTPClient(context.Background(), &configv1.HTTPClientConfig{Endpoint: "  "}, newClientRuntime(t, nil)); err == nil {
		t.Fatal("空白 HTTP endpoint 必须失败")
	}
	if _, err := NewGRPCClient(context.Background(), &configv1.GRPCClientConfig{
		Discovery: "service", Group: "group", Insecure: true,
	}, newClientRuntime(t, noopDiscovery{})); err == nil {
		t.Fatal("非 Nacos discovery 不能静默忽略 group")
	}
}

type noopDiscovery struct{}

func (noopDiscovery) GetService(context.Context, string) ([]*registry.ServiceInstance, error) {
	return nil, nil
}

func (noopDiscovery) Watch(context.Context, string) (registry.Watcher, error) {
	return nil, nil
}

func TestNewGRPCClientRequiresExplicitTransportSecurity(t *testing.T) {
	_, err := NewGRPCClient(context.Background(), &configv1.GRPCClientConfig{
		Endpoint: "127.0.0.1:9000",
	}, newClientRuntime(t, nil))
	if err == nil {
		t.Fatal("gRPC 未配置 TLS 或 insecure=true 时必须失败")
	}
}

func TestNewGRPCClientRejectsTLSInsecureConflict(t *testing.T) {
	_, err := NewGRPCClient(context.Background(), &configv1.GRPCClientConfig{
		Endpoint: "127.0.0.1:9000",
		Insecure: true,
		Tls:      &configv1.TLSConfig{},
	}, newClientRuntime(t, nil))
	if err == nil {
		t.Fatal("gRPC 同时配置 TLS 和 insecure 时必须失败")
	}
}

func TestClientConfigRejectsInvalidEndpointAndCircuitBreaker(t *testing.T) {
	for _, config := range []*configv1.HTTPClientConfig{
		{Endpoint: "http://user:secret@127.0.0.1:8000"},
		{Endpoint: "http://127.0.0.1:8000", Middleware: &configv1.ClientMiddlewareConfig{
			Circuitbreaker: &configv1.CircuitBreakerConfig{SuccessThreshold: 1.1},
		}},
	} {
		if _, err := NewHTTPClient(context.Background(), config, newClientRuntime(t, nil)); err == nil {
			t.Fatal("无效 HTTP 客户端配置必须失败")
		}
	}
}

func newClientRuntime(t *testing.T, discovery registry.Discovery) *styxruntime.Runtime {
	t.Helper()
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity:     styxruntime.Identity{Name: "client-test", Version: "test"},
		Dependencies: observe.NoopDependencies(),
		Discovery:    discovery,
	})
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}
	return runtime
}
