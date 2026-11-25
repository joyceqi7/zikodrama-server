package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitee.com/linpbin/styx/requestmeta"
)

func TestHTTPAccessLogReportsResponseAndSeverity(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		status int
		level  slog.Level
	}{
		{name: "成功", status: http.StatusNoContent, level: slog.LevelInfo},
		{name: "重定向", status: http.StatusFound, level: slog.LevelInfo},
		{name: "未找到", status: http.StatusNotFound, level: slog.LevelWarn},
		{name: "限流", status: http.StatusTooManyRequests, level: slog.LevelWarn},
		{name: "服务失败", status: http.StatusInternalServerError, level: slog.LevelError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			capture := &auditLogCapture{}
			dependencies := auditDependencies(t, slog.New(capture))
			handler := HTTPAccessLog(dependencies)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
			}))
			request := httptest.NewRequest(http.MethodGet, "/missing?token=query-secret", nil)
			request.Header.Set("X-Request-ID", "request-404")
			request.Header.Set("Authorization", "Bearer header-secret")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			record, attributes := onlyAccessRecord(t, capture)
			if recorder.Code != testCase.status || attributes["status"].Int64() != int64(testCase.status) {
				t.Fatalf("响应与日志状态不一致：response=%d attributes=%v", recorder.Code, attributes)
			}
			if record.Level != testCase.level || attributes["method"].String() != http.MethodGet {
				t.Fatalf("访问日志级别或请求方法错误：level=%s attributes=%v", record.Level, attributes)
			}
			if attributes["path"].String() != "/missing" {
				t.Fatalf("访问日志必须包含不带查询参数的请求路径：%v", attributes)
			}
			if attributes["duration_ms"].Kind() != slog.KindFloat64 || attributes["duration_ms"].Float64() <= 0 {
				t.Fatalf("耗时必须为保留小数的正数毫秒：%v", attributes["duration_ms"])
			}
			if attributes["request_id"].String() != "request-404" || recorder.Header().Get("X-Request-ID") != "request-404" {
				t.Fatalf("日志和响应未复用上游请求标识：%v", attributes)
			}
			for _, value := range attributes {
				if strings.Contains(value.String(), "query-secret") || strings.Contains(value.String(), "header-secret") {
					t.Fatal("访问日志泄露查询参数或请求头中的凭据")
				}
			}
		})
	}
}

func TestHTTPAccessLogUsesGatewayClientIP(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		remoteAddr string
		realIP     string
		want       string
	}{
		{name: "网关注入IPv4", remoteAddr: "127.0.0.1:1234", realIP: "198.51.100.2", want: "198.51.100.2"},
		{name: "网关注入IPv6", remoteAddr: "127.0.0.1:1234", realIP: "2001:db8::7", want: "2001:db8::7"},
		{name: "缺少注入地址回退对端", remoteAddr: "192.0.2.10:1234", want: "192.0.2.10"},
		{name: "非法注入地址回退对端", remoteAddr: "192.0.2.11:1234", realIP: "unknown", want: "192.0.2.11"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			capture := &auditLogCapture{}
			handler := HTTPAccessLog(auditDependencies(t, slog.New(capture)))(http.NotFoundHandler())
			request := httptest.NewRequest(http.MethodGet, "/missing", nil)
			request.RemoteAddr = testCase.remoteAddr
			request.Header.Set("X-Real-IP", testCase.realIP)
			handler.ServeHTTP(httptest.NewRecorder(), request)
			_, attributes := onlyAccessRecord(t, capture)
			if got := attributes["client_ip"].String(); got != testCase.want {
				t.Fatalf("client_ip=%q，期望=%q，日志=%v", got, testCase.want, attributes)
			}
		})
	}
}

func TestHTTPAccessLogRequestIDMatchesApplicationResponse(t *testing.T) {
	capture := &auditLogCapture{}
	handler := HTTPAccessLog(auditDependencies(t, slog.New(capture)))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Request-ID", "req_application")
		w.WriteHeader(http.StatusOK)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	_, attributes := onlyAccessRecord(t, capture)
	if attributes["request_id"].String() != recorder.Header().Get("X-Request-ID") {
		t.Fatalf("访问日志未采用应用响应标识：%v", attributes)
	}
}

func TestHTTPAccessLogSharesRequestIDWithHandler(t *testing.T) {
	for _, upstream := range []string{"", "edge-safe", "Bearer private-value"} {
		capture := &auditLogCapture{}
		var observed string
		handler := HTTPAccessLog(auditDependencies(t, slog.New(capture)))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			observed = requestmeta.RequestID(r.Context())
			w.WriteHeader(http.StatusNoContent)
		}))
		request := httptest.NewRequest(http.MethodGet, "/example", nil)
		request.Header.Set(requestmeta.HeaderName, upstream)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		_, attributes := onlyAccessRecord(t, capture)
		if observed == "" || observed != response.Result().Header.Get(requestmeta.HeaderName) || observed != attributes["request_id"].String() {
			t.Fatal("独立使用访问日志时，业务 Context、响应与访问日志必须共享标识")
		}
	}
}

func TestHTTPAccessLogKeepsSentRequestID(t *testing.T) {
	capture := &auditLogCapture{}
	handler := HTTPAccessLog(auditDependencies(t, slog.New(capture)))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Request-ID", "req_sent")
		w.WriteHeader(http.StatusNoContent)
		w.Header().Set("X-Request-ID", "req_too_late")
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	_, attributes := onlyAccessRecord(t, capture)
	if attributes["request_id"].String() != recorder.Result().Header.Get("X-Request-ID") || attributes["request_id"].String() != "req_sent" {
		t.Fatalf("访问日志必须匹配实际已发送的响应头：%v", attributes)
	}
}

func TestHTTPAccessLogPreservesOriginalEscapedPath(t *testing.T) {
	capture := &auditLogCapture{}
	handler := HTTPAccessLog(auditDependencies(t, slog.New(capture)))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = "/rewritten"
		r.URL.RawPath = ""
		r.Method = http.MethodPost
		w.WriteHeader(http.StatusNotFound)
	}))
	request := httptest.NewRequest(http.MethodGet, "/users/path%2Ftoken?access_token=query-secret", strings.NewReader("body-secret"))
	handler.ServeHTTP(httptest.NewRecorder(), request)
	_, attributes := onlyAccessRecord(t, capture)
	if attributes["path"].String() != "/users/path%2Ftoken" || attributes["method"].String() != http.MethodGet {
		t.Fatalf("访问日志必须保留改写前的方法和转义路径：%v", attributes)
	}
	for _, value := range attributes {
		if strings.Contains(value.String(), "query-secret") || strings.Contains(value.String(), "body-secret") {
			t.Fatal("访问日志不得包含查询参数或请求正文")
		}
	}
}

func TestHTTPAccessLogReplacesUnsafeRequestID(t *testing.T) {
	for _, value := range []string{"", "Bearer sensitive-value", "line\ninjection", strings.Repeat("x", 97)} {
		capture := &auditLogCapture{}
		handler := HTTPAccessLog(auditDependencies(t, slog.New(capture)))(http.NotFoundHandler())
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("X-Request-ID", value)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		_, attributes := onlyAccessRecord(t, capture)
		requestID := attributes["request_id"].String()
		if requestID == value || !requestmeta.ValidRequestID(requestID) || requestID != recorder.Header().Get("X-Request-ID") {
			t.Fatal("不安全或缺失的请求标识必须替换，并与响应保持一致")
		}
	}
}

func TestHTTPAccessLogReportsRecoveredPanic(t *testing.T) {
	capture := &auditLogCapture{}
	dependencies := auditDependencies(t, slog.New(capture))
	handler := HTTPAccessLog(dependencies)(HTTPRecovery("test", dependencies)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("panic-secret")
	})))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))
	record, attributes := onlyAccessRecord(t, capture)
	if recorder.Code != http.StatusInternalServerError || attributes["status"].Int64() != http.StatusInternalServerError || record.Level != slog.LevelError {
		t.Fatalf("panic 恢复后的访问日志必须为 ERROR/500：level=%s attributes=%v", record.Level, attributes)
	}
	if strings.Contains(recorder.Body.String(), "panic-secret") {
		t.Fatal("panic 响应泄露内部信息")
	}
}

func TestHTTPAccessLogKeepsFirstResponseStatus(t *testing.T) {
	capture := &auditLogCapture{}
	handler := HTTPAccessLog(auditDependencies(t, slog.New(capture)))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
		w.WriteHeader(http.StatusInternalServerError)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	record, attributes := onlyAccessRecord(t, capture)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "ok" || attributes["status"].Int64() != http.StatusOK || record.Level != slog.LevelInfo {
		t.Fatalf("后续重复状态不能改变实际响应或误报错误：level=%s attributes=%v", record.Level, attributes)
	}
}

func TestHTTPAccessLogPreservesFlushError(t *testing.T) {
	capture := &auditLogCapture{}
	handler := HTTPAccessLog(auditDependencies(t, slog.New(capture)))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).Flush(); !errors.Is(err, http.ErrNotSupported) {
			t.Fatalf("ResponseController 必须收到底层刷新错误：%v", err)
		}
	}))
	// 只公开 ResponseWriter 的方法，模拟不支持流式刷新的底层实现。
	writer := struct{ http.ResponseWriter }{httptest.NewRecorder()}
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/", nil))
}

// onlyAccessRecord 验证每次请求只输出一条访问日志，允许另有独立的恢复诊断日志。
func onlyAccessRecord(t *testing.T, capture *auditLogCapture) (slog.Record, map[string]slog.Value) {
	t.Helper()
	var records []slog.Record
	for _, record := range capture.records {
		if record.Message == "http access" {
			records = append(records, record)
		}
	}
	if len(records) != 1 {
		t.Fatalf("访问日志数量=%d，期望恰好一条", len(records))
	}
	attributes := make(map[string]slog.Value)
	records[0].Attrs(func(attribute slog.Attr) bool {
		attributes[attribute.Key] = attribute.Value
		return true
	})
	return records[0], attributes
}
