package requestmeta_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"gitee.com/linpbin/styx/requestmeta"
	"go.opentelemetry.io/otel/trace"
)

func TestHTTPMiddlewareCreatesAndPropagatesRequestID(t *testing.T) {
	t.Parallel()
	var previous string
	for range 2 {
		var observed string
		handler := requestmeta.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			observed = requestmeta.RequestID(r.Context())
			w.WriteHeader(http.StatusNoContent)
		}))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/example", nil))
		if observed == "" || response.Code != http.StatusNoContent || response.Result().Header.Get(requestmeta.HeaderName) != observed {
			t.Fatalf("Context 和响应头必须携带同一请求标识：context=%q header=%q", observed, response.Header().Get(requestmeta.HeaderName))
		}
		if len(observed) < 26 || strings.Trim(observed, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") != "" || observed == previous {
			t.Fatalf("独立请求必须生成无前缀的随机标识：%q", observed)
		}
		previous = observed
	}
}

func TestHTTPMiddlewareValidatesUpstreamRequestID(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "可见关联字符", value: "edge-01_SAFE.trace:2", valid: true},
		{name: "最大长度", value: strings.Repeat("a", 96), valid: true},
		{name: "缺失"},
		{name: "超长", value: strings.Repeat("a", 97)},
		{name: "空白", value: "Bearer private-value"},
		{name: "首尾空白", value: " edge-id "},
		{name: "换行", value: "line\ninjection"},
		{name: "控制字符", value: "line\x00injection"},
		{name: "非ASCII", value: "请求-id"},
		{name: "非关联符号", value: "id/secret"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := requestmeta.ValidRequestID(testCase.value); got != testCase.valid {
				t.Fatalf("请求标识校验结果=%v，期望=%v", got, testCase.valid)
			}
			var observed string
			handler := requestmeta.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				observed = requestmeta.RequestID(r.Context())
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodGet, "/example", nil)
			request.Header.Set(requestmeta.HeaderName, testCase.value)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			got := response.Result().Header.Get(requestmeta.HeaderName)
			if got != observed || !requestmeta.ValidRequestID(got) || (got == testCase.value) != testCase.valid {
				t.Fatalf("合法上游应复用，非法上游应替换，且 Context 与响应一致：context=%q header=%q", observed, got)
			}
		})
	}
}

func TestHTTPMiddlewareReusesContextWhenNested(t *testing.T) {
	t.Parallel()
	var outer, inner string
	handler := requestmeta.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outer = requestmeta.RequestID(r.Context())
		// 即使后续 Filter 改写了请求头，已建立的 Context 标识也应保持稳定。
		r.Header.Set(requestmeta.HeaderName, "changed-upstream")
		requestmeta.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inner = requestmeta.RequestID(r.Context())
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(w, r)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/example", nil))
	if outer == "" || inner != outer || response.Result().Header.Get(requestmeta.HeaderName) != outer {
		t.Fatalf("重复装配不能重新生成或覆盖已有标识：outer=%q inner=%q", outer, inner)
	}
}

func TestLogFieldsUsesRequestContextAndTrace(t *testing.T) {
	t.Parallel()
	for _, ctx := range []context.Context{nil, context.Background()} {
		if requestmeta.RequestID(ctx) != "" || requestmeta.TraceID(ctx) != "" {
			t.Fatal("未建立请求或链路时不应伪造标识")
		}
	}
	span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	handler := requestmeta.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := []any{"request_id", "edge-safe", "trace_id", span.TraceID().String()}
		if got := requestmeta.LogFields(r.Context()); !reflect.DeepEqual(got, want) {
			t.Fatalf("业务日志关联字段未读取同一 Context：%v", got)
		}
	}))
	request := httptest.NewRequest(http.MethodGet, "/example", nil)
	request.Header.Set(requestmeta.HeaderName, "edge-safe")
	handler.ServeHTTP(httptest.NewRecorder(), request.WithContext(trace.ContextWithSpanContext(request.Context(), span)))
}
