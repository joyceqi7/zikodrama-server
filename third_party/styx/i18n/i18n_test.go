package i18n

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/go-kratos/kratos/v2/transport"
)

func testBundle(t *testing.T) *Bundle {
	t.Helper()
	files := fstest.MapFS{
		"messages.en.json": {Data: []byte(`{"hello":"Hello {{.Name}}","missing":"English fallback"}`)},
		"messages.zh.json": {Data: []byte(`{"hello":"你好 {{.Name}}"}`)},
	}
	bundle, err := NewBundle("en", WithMessageFiles(files, "messages.en.json", "messages.zh.json"))
	if err != nil {
		t.Fatalf("NewBundle() error = %v", err)
	}
	return bundle
}

func TestBundleLocalizesAndFallsBack(t *testing.T) {
	bundle := testBundle(t)
	ctx := bundle.WithLocale(context.Background(), "zh-CN")
	if got := LocaleFromContext(ctx); got != "zh-CN" {
		t.Fatalf("LocaleFromContext() = %q, want zh-CN", got)
	}
	got, err := Localize(ctx, "hello", map[string]any{"Name": "林"})
	if err != nil || got != "你好 林" {
		t.Fatalf("Localize(hello) = %q, %v", got, err)
	}
	got, err = Localize(ctx, "missing", nil)
	if err != nil || got != "English fallback" {
		t.Fatalf("Localize(missing) = %q, %v", got, err)
	}
}

func TestWithLocaleRebindsAnExistingBundle(t *testing.T) {
	bundle := testBundle(t)
	ctx := bundle.WithAcceptLanguage(context.Background(), "en")
	ctx = WithLocale(ctx, "zh-CN")
	message, err := Localize(ctx, "hello", map[string]any{"Name": "林"})
	if err != nil || message != "你好 林" || LocaleFromContext(ctx) != "zh-CN" {
		t.Fatalf("rebound localization = %q, %q, %v", LocaleFromContext(ctx), message, err)
	}
}

func TestNegotiateHonorsQualityAndPolicy(t *testing.T) {
	if got := Negotiate("fr;q=0.9, es-MX;q=0.8", "en", []string{"en", "es"}); got != "es-MX" {
		t.Fatalf("Negotiate() = %q, want es-MX", got)
	}
	if got := Negotiate("pt-PT", "en", []string{"en", "pt-BR"}); got != "pt-BR" {
		t.Fatalf("regional-only Negotiate() = %q, want pt-BR", got)
	}
	if got := Negotiate("invalid value", "zh", []string{"en", "zh"}); got != "zh" {
		t.Fatalf("invalid Negotiate() = %q, want zh", got)
	}
}

func TestPreferredLocaleDoesNotDependOnMessageCatalog(t *testing.T) {
	if got := PreferredLocale("fr-FR;q=0.9, es;q=0.8", "en"); got != "fr-FR" {
		t.Fatalf("PreferredLocale() = %q, want fr-FR", got)
	}
	if got := PreferredLocale("invalid value", "en"); got != "en" {
		t.Fatalf("invalid PreferredLocale() = %q, want en", got)
	}
}

func TestBundleKeepsUnsupportedRequestLocaleForBusinessContent(t *testing.T) {
	bundle := testBundle(t)
	ctx := bundle.WithAcceptLanguage(context.Background(), "fr-FR")
	if got := LocaleFromContext(ctx); got != "fr-FR" {
		t.Fatalf("LocaleFromContext() = %q, want fr-FR", got)
	}
	localizer, ok := FromContext(ctx)
	if !ok {
		t.Fatal("message localizer is missing")
	}
	if got := localizer.Locale(); got != "en" {
		t.Fatalf("message locale = %q, want en", got)
	}
	message, err := Localize(ctx, "hello", map[string]any{"Name": "Lin"})
	if err != nil || message != "Hello Lin" {
		t.Fatalf("Localize() = %q, %v", message, err)
	}
}

func TestHTTPFilterCoversRawHandlers(t *testing.T) {
	bundle := testBundle(t)
	handler := HTTPFilter(bundle)(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		message, err := Localize(request.Context(), "hello", map[string]any{"Name": "林"})
		if err != nil {
			t.Fatalf("Localize() error = %v", err)
		}
		_, _ = response.Write([]byte(message))
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Language", "zh-CN")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Body.String(); got != "你好 林" {
		t.Fatalf("body = %q", got)
	}
	if got := response.Header().Get("Vary"); got != "Accept-Language" {
		t.Fatalf("Vary = %q", got)
	}
}

type testTransport struct{ header transport.Header }

func (t testTransport) Kind() transport.Kind            { return transport.KindHTTP }
func (t testTransport) Endpoint() string                { return "" }
func (t testTransport) Operation() string               { return "" }
func (t testTransport) RequestHeader() transport.Header { return t.header }
func (t testTransport) ReplyHeader() transport.Header   { return t.header }

type testHeader map[string][]string

func (h testHeader) Get(key string) string { return http.Header(h).Get(key) }
func (h testHeader) Set(key, value string) { http.Header(h).Set(key, value) }
func (h testHeader) Add(key, value string) { http.Header(h).Add(key, value) }
func (h testHeader) Keys() []string {
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	return keys
}
func (h testHeader) Values(key string) []string { return http.Header(h).Values(key) }

func TestMiddlewareReadsKratosTransportHeader(t *testing.T) {
	bundle := testBundle(t)
	header := testHeader{}
	header.Set("Accept-Language", "zh")
	ctx := transport.NewServerContext(context.Background(), testTransport{header: header})
	handler := Middleware(bundle)(func(ctx context.Context, _ any) (any, error) {
		return LocaleFromContext(ctx), nil
	})
	result, err := handler(ctx, nil)
	if err != nil || result != "zh" {
		t.Fatalf("middleware result = %#v, %v", result, err)
	}
}
