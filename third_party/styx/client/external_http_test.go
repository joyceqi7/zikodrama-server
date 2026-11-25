package client

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type externalRoundTripperStub struct{}

func (externalRoundTripperStub) RoundTrip(*http.Request) (*http.Response, error) { return nil, nil }

func TestNewExternalHTTPClientUsesManagedIndependentTransport(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = externalRoundTripperStub{}
	t.Cleanup(func() { http.DefaultTransport = original })

	runtime := newClientRuntime(t, nil)
	client, err := NewExternalHTTPClient(context.Background(), "oauth", 3*time.Second, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if client.Timeout != 3*time.Second || client.Transport == nil {
		t.Fatalf("external HTTP client = %+v", client)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Runtime 关闭外部 HTTP Client 失败: %v", err)
	}
}

func TestExternalHTTPClientPropagatesTraceWithoutRecordingURL(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSyncer(exporter),
	)
	dependencies, err := observe.NewDependencies(observe.Config{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		TracerProvider: tracerProvider,
		MeterProvider:  metricnoop.NewMeterProvider(),
		Propagator:     propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity: styxruntime.Identity{Name: "external-http-trace-test"}, Dependencies: dependencies,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.Shutdown(context.Background())
		_ = tracerProvider.Shutdown(context.Background())
	})

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("traceparent") == "" {
			t.Error("第三方 HTTP 请求缺少 traceparent")
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := NewExternalHTTPClient(context.Background(), "oauth", time.Second, runtime)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/oauth/token?client_secret=query-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, parent := tracerProvider.Tracer("test").Start(context.Background(), "parent")
	request = request.WithContext(ctx)
	response, err := client.Do(request)
	parent.End()
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	for _, span := range exporter.GetSpans() {
		for _, item := range span.Attributes {
			key := string(item.Key)
			encoded := key + "=" + item.Value.Emit()
			if strings.Contains(encoded, "query-secret") || key == "url.full" || key == "url.path" || key == "url.query" {
				t.Fatalf("第三方 HTTP Span 泄露 URL 或 query：%s", encoded)
			}
		}
	}
}

func TestNewExternalHTTPClientRejectsInvalidInput(t *testing.T) {
	runtime := newClientRuntime(t, nil)
	for name, test := range map[string]struct {
		ctx     context.Context
		name    string
		timeout time.Duration
		runtime bool
	}{
		"context": {name: "oauth", timeout: time.Second, runtime: true},
		"name":    {ctx: context.Background(), timeout: time.Second, runtime: true},
		"timeout": {ctx: context.Background(), name: "oauth", runtime: true},
		"runtime": {ctx: context.Background(), name: "oauth", timeout: time.Second},
	} {
		selected := runtime
		if !test.runtime {
			selected = nil
		}
		if _, err := NewExternalHTTPClient(test.ctx, test.name, test.timeout, selected); err == nil {
			t.Fatalf("%s 无效输入必须失败", name)
		}
	}
}
