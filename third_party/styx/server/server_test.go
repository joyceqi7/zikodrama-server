package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/types/known/durationpb"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

func TestNewHTTPServerRejectsNonPositiveTimeout(t *testing.T) {
	_, err := NewHTTPServer(&configv1.HTTPServerConfig{
		Addr:    ":0",
		Timeout: durationpb.New(0),
	}, newServerRuntime(t))
	if err == nil {
		t.Fatal("显式配置非正超时时间时必须失败")
	}
}

func TestNewGRPCServerControlsReflectionExplicitly(t *testing.T) {
	disabled, err := NewGRPCServer(
		&configv1.GRPCServerConfig{Addr: ":0"},
		newServerRuntime(t),
	)
	if err != nil {
		t.Fatalf("创建关闭反射的 gRPC 服务：%v", err)
	}
	if _, exists := disabled.GetServiceInfo()["grpc.reflection.v1.ServerReflection"]; exists {
		t.Fatal("未显式开启时不应注册 gRPC 反射")
	}

	enabled, err := NewGRPCServer(
		&configv1.GRPCServerConfig{Addr: ":0", EnableReflection: true},
		newServerRuntime(t),
	)
	if err != nil {
		t.Fatalf("创建开启反射的 gRPC 服务：%v", err)
	}
	if _, exists := enabled.GetServiceInfo()["grpc.reflection.v1.ServerReflection"]; !exists {
		t.Fatal("显式开启时应注册 gRPC 反射")
	}
}

func TestParseEndpointRequiresSchemeAndHost(t *testing.T) {
	if _, err := parseEndpoint("127.0.0.1:9000"); err == nil {
		t.Fatal("缺少 scheme 的服务注册地址必须失败")
	}
	if _, err := parseEndpoint("http://127.0.0.1"); err == nil {
		t.Fatal("服务注册地址必须显式配置端口")
	}
	if _, err := parseEndpoint("http://127.0.0.1:8000/path"); err == nil {
		t.Fatal("服务注册地址不能包含路径")
	}
}

func TestServerRejectsInvalidIdentityAndSizeConfiguration(t *testing.T) {
	if _, err := NewHTTPServer(&configv1.HTTPServerConfig{Addr: ":0"}, nil); err == nil {
		t.Fatal("HTTP 服务必须配置 Runtime")
	}
	if _, err := NewGRPCServer(&configv1.GRPCServerConfig{Addr: ":0", MaxRecvMsgSize: -1}, newServerRuntime(t)); err == nil {
		t.Fatal("gRPC 消息大小不能为负数")
	}
	if _, err := parseEndpoint("https://user:secret@example.com"); err == nil {
		t.Fatal("服务注册地址不能嵌入凭据")
	}
	if _, err := NewHTTPServer(&configv1.HTTPServerConfig{Addr: ":0", Endpoint: "https://127.0.0.1:8443"}, newServerRuntime(t)); err == nil {
		t.Fatal("未启用 TLS 的 HTTP 服务不能注册 https endpoint")
	}
	if _, err := NewGRPCServer(&configv1.GRPCServerConfig{Addr: ":0", Endpoint: "http://127.0.0.1:9000"}, newServerRuntime(t)); err == nil {
		t.Fatal("gRPC 服务不能注册 HTTP endpoint")
	}
}

func TestHTTPApplicationFilterCannotReplaceRecovery(t *testing.T) {
	server, err := NewHTTPServer(
		&configv1.HTTPServerConfig{Addr: ":0"},
		newServerRuntime(t),
		WithHTTPFilter(func(http.Handler) http.Handler {
			return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("secret panic") })
		}),
	)
	if err != nil {
		t.Fatalf("NewHTTPServer() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
}

func newServerRuntime(t *testing.T) *styxruntime.Runtime {
	t.Helper()
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity:     styxruntime.Identity{Name: "audit", Version: "test"},
		Dependencies: observe.NoopDependencies(),
	})
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}
	return runtime
}
