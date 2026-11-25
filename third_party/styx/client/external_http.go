package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"gitee.com/linpbin/styx/internal/lifecycle"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

var errExternalHTTPDependency = errors.New("external HTTP dependency failure")
var errExternalHTTPResponse = errors.New("external HTTP transport returned nil response")

// NewExternalHTTPClient 创建访问第三方原生 HTTP API 的标准库 Client。
//
// Kratos HTTP Client 适合遵循 Kratos 编解码合同的服务间 RPC；OAuth、云厂商开放 API
// 等接口通常需要直接控制 form、header 和原始响应。该工厂为这类场景提供与 Styx 一致
// 的显式 TracerProvider、Propagator、超时和资源生命周期，业务项目不应再自行封装
// otelhttp.Transport 或向业务包传播 Runtime。
func NewExternalHTTPClient(
	ctx context.Context,
	name string,
	timeout time.Duration,
	runtime *styxruntime.Runtime,
) (*http.Client, error) {
	if ctx == nil {
		return nil, fmt.Errorf("client: external HTTP context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("client: external HTTP context: %w", err)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("client: external HTTP name is required")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("client: external HTTP timeout must be positive")
	}
	if runtime == nil {
		return nil, fmt.Errorf("client: external HTTP runtime is required")
	}
	dependencies := runtime.Dependencies()
	if err := dependencies.Validate(); err != nil {
		return nil, fmt.Errorf("client: external HTTP runtime dependencies: %w", err)
	}

	transport := cloneDefaultTransport()
	instrumented := &externalHTTPTransport{
		base:       transport,
		tracer:     dependencies.TracerProvider().Tracer("gitee.com/linpbin/styx/client/external-http"),
		propagator: dependencies.Propagator(),
	}
	client := &http.Client{Transport: instrumented, Timeout: timeout}
	shutdown := styxruntime.BoundedClose(func() error {
		transport.CloseIdleConnections()
		return nil
	})
	if err := runtime.Own("external HTTP client "+name, shutdown); err != nil {
		return nil, lifecycle.Rollback("external HTTP client "+name, shutdown, err)
	}
	return client, nil
}

// externalHTTPTransport 只记录稳定方法、状态码和固定错误分类。
// 标准 otelhttp Transport 会把 url.full（包含 query）写入 Span，并自动创建 HTTP 指标；
// Styx 的安全默认值禁止这两种行为，因此这里显式实现最小 Trace 与上下文传播。
type externalHTTPTransport struct {
	// base 执行真实网络请求并独立持有连接池。
	base http.RoundTripper
	// tracer 创建不包含 URL 和正文的客户端 Span。
	tracer trace.Tracer
	// propagator 向第三方请求注入标准链路上下文。
	propagator propagation.TextMapPropagator
}

func (transport *externalHTTPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil {
		return nil, fmt.Errorf("client: external HTTP request is required")
	}
	ctx, span := transport.tracer.Start(
		request.Context(),
		"HTTP "+request.Method,
		trace.WithSpanKind(trace.SpanKindClient),
	)
	defer span.End()
	span.SetAttributes(attribute.String("http.request.method", request.Method))

	forwarded := request.Clone(ctx)
	transport.propagator.Inject(ctx, propagation.HeaderCarrier(forwarded.Header))
	response, err := transport.base.RoundTrip(forwarded)
	if err != nil {
		span.RecordError(errExternalHTTPDependency)
		span.SetStatus(codes.Error, errExternalHTTPDependency.Error())
		return nil, err
	}
	if response == nil {
		span.RecordError(errExternalHTTPDependency)
		span.SetStatus(codes.Error, errExternalHTTPDependency.Error())
		return nil, errExternalHTTPResponse
	}
	span.SetAttributes(attribute.Int("http.response.status_code", response.StatusCode))
	if response.StatusCode >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, "server error")
	}
	return response, nil
}

func cloneDefaultTransport() *http.Transport {
	if transport, ok := http.DefaultTransport.(*http.Transport); ok && transport != nil {
		return transport.Clone()
	}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}
