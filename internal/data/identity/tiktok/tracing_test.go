package tiktok

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	styxclient "gitee.com/linpbin/styx/client"
	styxobserve "gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestOAuthClientInjectedHTTPClientPropagatesTraceparent(t *testing.T) {
	httpClient, tracer := newTikTokTestHTTPClient(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("traceparent") == "" {
			t.Fatal("TikTok OAuth 请求必须携带 traceparent")
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"open_id":"open-id","access_token":"access-token"}`))
	}))
	defer server.Close()

	client := NewOAuthClient("client-key", "client-secret", httpClient)
	client.endpoint = server.URL
	ctx, span := tracer.Start(context.Background(), "oauth-parent")
	defer span.End()
	if _, err := client.ExchangeCode(ctx, "authorization-code"); err != nil {
		t.Fatal(err)
	}
}

func newTikTokTestHTTPClient(t *testing.T) (*http.Client, trace.Tracer) {
	t.Helper()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tracetest.NewNoopExporter()))
	dependencies, err := styxobserve.NewDependencies(styxobserve.Config{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		TracerProvider: provider,
		MeterProvider:  metricnoop.NewMeterProvider(),
		Propagator:     propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity: styxruntime.Identity{Name: "tiktok-test"}, Dependencies: dependencies,
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := styxclient.NewExternalHTTPClient(context.Background(), "tiktok-test", 2*time.Second, runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.Shutdown(context.Background())
		_ = provider.Shutdown(context.Background())
	})
	return client, provider.Tracer("test")
}
