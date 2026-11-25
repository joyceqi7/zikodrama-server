package apierror

import (
	"context"
	"embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitee.com/linpbin/styx/i18n"
)

//go:embed testdata/messages.*.json
var messages embed.FS

func TestDefinitionSupportsSingleAndMultipleLanguages(t *testing.T) {
	definition := Must(http.StatusBadRequest, "invalid_request", "Request is invalid")
	if got := definition.Message(context.Background(), nil); got != "Request is invalid" {
		t.Fatalf("single-language message = %q", got)
	}
	bundle, err := i18n.NewBundle("en", i18n.WithMessageFiles(
		messages, "testdata/messages.en.json", "testdata/messages.zh.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	ctx := bundle.WithLocale(context.Background(), "zh-CN")
	if got := definition.Message(ctx, nil); got != "请求内容无效" {
		t.Fatalf("multilingual message = %q", got)
	}
}

func TestWriteHTTPUsesStableCodeAndLocalizedMessage(t *testing.T) {
	bundle, err := i18n.NewBundle("en", i18n.WithMessageFiles(
		messages, "testdata/messages.en.json", "testdata/messages.zh.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	definition := Must(http.StatusUnauthorized, "unauthorized", "Authentication is required")
	response := httptest.NewRecorder()
	WriteHTTP(bundle.WithLocale(context.Background(), "zh"), response, definition)
	var body envelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusUnauthorized || body.Error.Code != "unauthorized" || body.Error.Message != "需要认证" {
		t.Fatalf("unexpected response: status=%d body=%+v", response.Code, body)
	}
}

func TestHTTPFilterLocalizesCatalogErrorAtResponseExit(t *testing.T) {
	bundle, err := i18n.NewBundle("en", i18n.WithMessageFiles(
		messages, "testdata/messages.en.json", "testdata/messages.zh.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	definition := MustCatalog(http.StatusBadRequest, "invalid_request")
	response := httptest.NewRecorder()
	handler := i18n.HTTPFilter(bundle)(HTTPFilter()(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			WriteHTTP(request.Context(), writer, definition)
			_, _ = writer.Write([]byte(`{"data":"must be suppressed"}`))
			if response.Body.Len() != 0 {
				t.Fatal("错误不应在业务 Handler 内编码")
			}
		},
	)))
	request := httptest.NewRequest(http.MethodGet, "/resource", nil)
	request.Header.Set("accept-language", "zh-CN")
	handler.ServeHTTP(response, request)

	var body envelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusBadRequest || body.Error.Message != "请求内容无效" {
		t.Fatalf("unexpected response: status=%d body=%+v", response.Code, body)
	}
}

func TestHTTPFilterSupportsSingleLanguageCatalog(t *testing.T) {
	bundle, err := i18n.NewBundle("en", i18n.WithMessageFiles(messages, "testdata/messages.en.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler := i18n.HTTPFilter(bundle)(HTTPFilter()(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			WriteHTTP(request.Context(), response, MustCatalog(http.StatusUnauthorized, "unauthorized"))
		},
	)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/resource", nil))
	var body envelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Message != "Authentication is required" {
		t.Fatalf("single-language message = %q", body.Error.Message)
	}
}

func TestHTTPFilterPropagatesStatusToInnerResponseRecorder(t *testing.T) {
	bundle, err := i18n.NewBundle("en", i18n.WithMessageFiles(messages, "testdata/messages.en.json"))
	if err != nil {
		t.Fatal(err)
	}
	var innerStatus int
	handler := i18n.HTTPFilter(bundle)(HTTPFilter()(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			inner := httptest.NewRecorder()
			WriteHTTP(request.Context(), inner, MustCatalog(http.StatusConflict, "invalid_request"))
			innerStatus = inner.Code
			response.WriteHeader(inner.Code)
			_, _ = response.Write(inner.Body.Bytes())
		},
	)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/resource", nil))
	if innerStatus != http.StatusConflict || response.Code != http.StatusConflict {
		t.Fatalf("status: inner=%d response=%d", innerStatus, response.Code)
	}
	var body envelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "invalid_request" || body.Error.Message != "Request is invalid" {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestRegistryRejectsDuplicateCodes(t *testing.T) {
	first := Must(http.StatusBadRequest, "invalid", "Invalid request")
	second := MustWithMessageID(http.StatusConflict, "invalid", "conflict", "Request conflicts")
	if _, err := NewRegistry(first, second); err == nil {
		t.Fatal("duplicate code was accepted")
	}
}
