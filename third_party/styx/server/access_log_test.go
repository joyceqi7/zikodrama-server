package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gitee.com/linpbin/styx"
	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/observe"
	styxotel "gitee.com/linpbin/styx/otel"
	"gitee.com/linpbin/styx/requestmeta"
	styxruntime "gitee.com/linpbin/styx/runtime"
	"gitee.com/linpbin/styx/server"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestHTTPAccessAndBusinessLogsShareLocalTraceID(t *testing.T) {
	output := &accessLogBuffer{}
	traces, err := styxotel.New(context.Background(), &configv1.OTelConfig{
		Trace: &configv1.TraceConfig{Enabled: true},
	}, &configv1.AppInfo{Name: "local-trace-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = traces.Shutdown(context.Background()) })
	logger := styx.NewLogger(&configv1.LogConfig{Console: true, Level: "info"}, styx.WithConsoleOutput(output))
	dependencies, err := observe.NewDependencies(observe.Config{
		Logger: logger, TracerProvider: traces.TracerProvider(),
		MeterProvider: metricnoop.NewMeterProvider(), Propagator: propagation.TraceContext{},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity: styxruntime.Identity{Name: "local-trace-test"}, Dependencies: dependencies,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	engine, err := server.NewHTTPServer(&configv1.HTTPServerConfig{Addr: ":0"}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	engine.HandleFunc("/example", func(w http.ResponseWriter, r *http.Request) {
		logger.InfoContext(r.Context(), "business handled")
		logger.WarnContext(r.Context(), "mysql slow query", "db.system", "mysql")
		w.WriteHeader(http.StatusNoContent)
	})
	endpoint := httptest.NewServer(engine)
	t.Cleanup(endpoint.Close)
	response, err := endpoint.Client().Get(endpoint.URL + "/example")
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := response.Body.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("请求状态=%d，期望 204", response.StatusCode)
	}
	var observedTraceID string
	seen := map[string]bool{}
	decoder := json.NewDecoder(strings.NewReader(output.String()))
	for decoder.More() {
		var entry map[string]any
		if err := decoder.Decode(&entry); err != nil {
			t.Fatal(err)
		}
		message, _ := entry["msg"].(string)
		if message != "business handled" && message != "mysql slow query" && message != "http access" {
			continue
		}
		traceID, _ := entry["trace_id"].(string)
		if len(traceID) != 32 || traceID == strings.Repeat("0", 32) || observedTraceID != "" && traceID != observedTraceID {
			t.Fatalf("同一请求的日志缺少相同 Trace ID：%v", entry)
		}
		observedTraceID = traceID
		seen[message] = true
	}
	if len(seen) != 3 {
		t.Fatalf("缺少访问、业务或数据库日志：%v", seen)
	}
}

func TestHTTPAccessLogOverNetwork(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		path    string
		status  int
		level   string
		handler http.HandlerFunc
	}{
		{name: "成功", path: "/response", status: http.StatusNoContent, level: "INFO", handler: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}},
		{name: "未匹配路由", path: "/missing", status: http.StatusNotFound, level: "WARN"},
		{name: "业务失败", path: "/response", status: http.StatusServiceUnavailable, level: "ERROR", handler: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}},
		{name: "恢复异常", path: "/response", status: http.StatusInternalServerError, level: "ERROR", handler: func(http.ResponseWriter, *http.Request) {
			panic("panic-secret")
		}},
		{name: "临时响应后失败", path: "/response", status: http.StatusServiceUnavailable, level: "ERROR", handler: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusEarlyHints)
			w.WriteHeader(http.StatusServiceUnavailable)
		}},
		{name: "刷新后状态不再改变", path: "/response", status: http.StatusOK, level: "INFO", handler: func(w http.ResponseWriter, _ *http.Request) {
			w.(http.Flusher).Flush()
			w.WriteHeader(http.StatusInternalServerError)
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			output := &accessLogBuffer{}
			traces := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
			t.Cleanup(func() {
				if err := traces.Shutdown(context.Background()); err != nil {
					t.Errorf("关闭测试 Trace Provider：%v", err)
				}
			})
			dependencies, err := observe.NewDependencies(observe.Config{
				Logger:         styx.NewLogger(nil, styx.WithConsoleOutput(output)),
				TracerProvider: traces,
				MeterProvider:  metricnoop.NewMeterProvider(),
				Propagator:     propagation.TraceContext{},
			})
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := styxruntime.New(styxruntime.Config{
				Identity:     styxruntime.Identity{Name: "access-log-test", Version: "test"},
				Dependencies: dependencies,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := runtime.Shutdown(context.Background()); err != nil {
					t.Errorf("关闭测试 Runtime：%v", err)
				}
			})
			engine, err := server.NewHTTPServer(&configv1.HTTPServerConfig{Addr: ":0"}, runtime)
			if err != nil {
				t.Fatal(err)
			}
			if testCase.handler != nil {
				engine.HandleFunc(testCase.path, testCase.handler)
			}
			endpoint := httptest.NewServer(engine)
			t.Cleanup(endpoint.Close)
			request, err := http.NewRequest(http.MethodGet, endpoint.URL+testCase.path+"?access_token=query-secret", nil)
			if err != nil {
				t.Fatal(err)
			}
			const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
			request.Header.Set("Traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
			request.Header.Set("X-Request-ID", "req_network_test")
			request.Header.Set("Authorization", "Bearer header-secret")
			client := endpoint.Client()
			client.Timeout = 2 * time.Second
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := io.Copy(io.Discard, response.Body)
			closeErr := response.Body.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("读取或关闭 HTTP 响应失败：read=%v close=%v", readErr, closeErr)
			}
			if response.StatusCode != testCase.status {
				t.Fatalf("实际响应状态=%d，期望=%d", response.StatusCode, testCase.status)
			}
			// HTTP 连接结束后读取 JSON，验证真实治理链与日志器共同产生的字段。
			serialized := output.String()
			for _, secret := range []string{"query-secret", "header-secret", "panic-secret"} {
				if strings.Contains(serialized, secret) {
					t.Fatal("日志泄露查询参数、请求头或 panic 内容")
				}
			}
			decoder := json.NewDecoder(strings.NewReader(serialized))
			var entries []map[string]any
			for decoder.More() {
				var entry map[string]any
				if err := decoder.Decode(&entry); err != nil {
					t.Fatalf("访问日志不是合法 JSON：%v", err)
				}
				if entry["msg"] == "http access" {
					entries = append(entries, entry)
				}
			}
			if len(entries) != 1 {
				t.Fatalf("访问日志数量=%d，期望=1", len(entries))
			}
			entry := entries[0]
			if entry["path"] != testCase.path || entry["status"] != float64(testCase.status) || entry["level"] != testCase.level {
				t.Fatalf("访问日志与实际响应不一致：%v", entry)
			}
			if entry["request_id"] != response.Header.Get("X-Request-ID") || entry["request_id"] != "req_network_test" || entry["trace_id"] != traceID {
				t.Fatalf("访问日志关联标识错误：%v", entry)
			}
			if entry["client_ip"] != "127.0.0.1" {
				t.Fatalf("访问日志客户端 IP 错误：%v", entry)
			}
			if duration, ok := entry["duration_ms"].(float64); !ok || duration <= 0 {
				t.Fatalf("访问日志缺少有效耗时：%v", entry)
			}
			t.Logf("已验证 HTTP 状态、路径、关联标识和 JSON 日志：%s", serialized)
		})
	}
}

func TestHTTPRequestIDWithoutObservabilityOverNetwork(t *testing.T) {
	for _, upstream := range []string{"", "edge-safe", "Bearer private-value"} {
		t.Run(upstream, func(t *testing.T) {
			runtime, err := styxruntime.New(styxruntime.Config{
				Identity:     styxruntime.Identity{Name: "request-id-test", Version: "test"},
				Dependencies: observe.NoopDependencies(),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := runtime.Shutdown(context.Background()); err != nil {
					t.Errorf("关闭测试 Runtime 失败：%v", err)
				}
			})
			engine, err := server.NewHTTPServer(&configv1.HTTPServerConfig{
				Addr:       ":0",
				Middleware: &configv1.MiddlewareConfig{DisableLogging: true, DisableTracing: true},
			}, runtime, server.WithHTTPFilter(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Filter-Request-ID", requestmeta.RequestID(r.Context()))
					next.ServeHTTP(w, r)
				})
			}))
			if err != nil {
				t.Fatal(err)
			}
			engine.HandleFunc("/example", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Handler-Request-ID", requestmeta.RequestID(r.Context()))
				w.WriteHeader(http.StatusNoContent)
			})
			engine.HandleFunc("/panic", func(http.ResponseWriter, *http.Request) { panic("private-panic") })
			endpoint := httptest.NewServer(engine)
			t.Cleanup(endpoint.Close)
			for path, status := range map[string]int{"/example": http.StatusNoContent, "/missing": http.StatusNotFound, "/panic": http.StatusInternalServerError} {
				request, err := http.NewRequest(http.MethodGet, endpoint.URL+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set(requestmeta.HeaderName, upstream)
				response, err := endpoint.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				if err := response.Body.Close(); err != nil {
					t.Fatalf("关闭 HTTP 响应失败：%v", err)
				}
				id := response.Header.Get(requestmeta.HeaderName)
				if response.StatusCode != status || !requestmeta.ValidRequestID(id) || id != response.Header.Get("X-Filter-Request-ID") {
					t.Fatalf("关闭日志和追踪后，请求标识必须覆盖业务 Filter、404 与恢复响应：path=%s status=%d id=%q", path, response.StatusCode, id)
				}
				if path == "/example" && response.Header.Get("X-Handler-Request-ID") != id {
					t.Fatal("处理器与响应未共享请求标识")
				}
				if requestmeta.ValidRequestID(upstream) && id != upstream || !requestmeta.ValidRequestID(upstream) && id == upstream {
					t.Fatal("上游标识未按安全规则复用或替换")
				}
			}
		})
	}
}

// accessLogBuffer 隔离测试服务的并发日志写入与断言读取。
type accessLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *accessLogBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(value)
}

func (buffer *accessLogBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}
