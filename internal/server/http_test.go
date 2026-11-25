package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	styxapierror "gitee.com/linpbin/styx/apierror"
	styxi18n "gitee.com/linpbin/styx/i18n"
	styxobserve "gitee.com/linpbin/styx/observe"
	"gitee.com/linpbin/styx/requestmeta"
	styxruntime "gitee.com/linpbin/styx/runtime"
	styxserver "gitee.com/linpbin/styx/server"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	config "drama-server/internal/conf"
	"drama-server/internal/pkg/apimessages"
	apprequestmeta "drama-server/internal/pkg/requestmeta"
	service "drama-server/internal/service"
)

// Dependencies 仅用于本包测试，把具名业务 Handler 投影为生产构造函数的参数。
type Dependencies struct {
	Consumer http.Handler
	Admin    http.Handler
	Styx     *styxruntime.Runtime
	Config   *configv1.HTTPServerConfig
	Listener net.Listener
}

func TestNewEngineRejectsMissingConsumerHandler(t *testing.T) {
	t.Parallel()

	_, err := buildTestHTTPServer(Dependencies{Admin: http.NotFoundHandler()})
	if err == nil {
		t.Fatal("缺少消费者处理器时应拒绝创建公开路由")
	}
}

func TestEngineUsesStyxTracing(t *testing.T) {
	t.Parallel()

	observed := make(chan string, 1)
	_, address := newTestEngine(t, Dependencies{
		Consumer: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			observed <- oteltrace.SpanContextFromContext(request.Context()).TraceID().String()
			writer.WriteHeader(http.StatusNoContent)
		}),
		Admin: http.NotFoundHandler(),
	})

	const inboundTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	response := perform(t, address, http.MethodGet, "/api/v1/home", http.Header{
		"traceparent": {inboundTraceparent},
	})
	if response.status != http.StatusNoContent {
		t.Fatalf("响应状态=%d，期望=%d", response.status, http.StatusNoContent)
	}
	if got := <-observed; got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("Styx 下游 trace ID=%q", got)
	}
}

func TestEngineUsesStyxRecovery(t *testing.T) {
	t.Parallel()

	_, address := newTestEngine(t, Dependencies{
		Consumer: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("secret panic detail")
		}),
		Admin: http.NotFoundHandler(),
	})

	response := perform(t, address, http.MethodGet, "/api/v1/home", nil)
	if response.status != http.StatusInternalServerError {
		t.Fatalf("panic 状态=%d，期望=%d", response.status, http.StatusInternalServerError)
	}
	if strings.Contains(response.body, "secret panic detail") {
		t.Fatalf("panic 响应泄露内部细节：%s", response.body)
	}
}

func TestEngineRoutesAdminRequestToAdminHandler(t *testing.T) {
	t.Parallel()

	_, address := newTestEngine(t, Dependencies{
		Consumer: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusTeapot)
			_, _ = writer.Write([]byte("consumer"))
		}),
		Admin: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte("admin"))
		}),
	})

	response := perform(t, address, http.MethodGet, "/admin/v1/dramas", nil)
	if response.status != http.StatusUnauthorized || response.body != "admin" {
		t.Fatalf("管理员路径错误地落入消费者处理器：status=%d body=%q", response.status, response.body)
	}
}

func TestEngineDoesNotMountLegacyBusinessPrefixes(t *testing.T) {
	t.Parallel()

	_, address := newTestEngine(t, Dependencies{
		Consumer: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusTeapot)
		}),
		Admin: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusUnauthorized)
		}),
	})

	// 项目尚未上线，旧路径不保留重定向或别名；误用旧合同必须立即暴露为 404。
	for _, path := range []string{"/v1/home", "/v1/admin/dramas", "/"} {
		response := perform(t, address, http.MethodGet, path, nil)
		if response.status != http.StatusNotFound {
			t.Fatalf("旧路径或根路径仍然被业务 Handler 接收：path=%s status=%d", path, response.status)
		}
	}
}

func TestEngineUsesFixedEnglishBusinessError(t *testing.T) {
	t.Parallel()

	_, address := newTestEngine(t, testContractDependencies(t))
	response := perform(t, address, http.MethodGet, "/admin/v1/dramas", http.Header{
		"Accept-Language": {"ja-JP"},
	})
	if response.status != http.StatusUnauthorized {
		t.Fatalf("未认证响应状态=%d body=%s", response.status, response.body)
	}
	if !strings.Contains(response.body, `"reason":"unauthorized"`) ||
		!strings.Contains(response.body, `"message":"Your session has expired. Please sign in again."`) {
		t.Fatalf("统一响应出口未使用固定文案：%s", response.body)
	}
}

func TestEngineDoesNotExposeDiagnosticRoutes(t *testing.T) {
	t.Parallel()

	_, address := newTestEngine(t, testDependencies(t))
	for _, path := range []string{"/healthz", "/readyz", "/metrics", "/debug/pprof/"} {
		response := perform(t, address, http.MethodGet, path, nil)
		if response.status != http.StatusNotFound {
			t.Fatalf("公开端口意外暴露诊断路径 %s：status=%d", path, response.status)
		}
	}
}

func TestEngineLogsRequestPathAndIDWithoutQuerySecrets(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name      string
		path      string
		requestID string
		status    int
		level     string
	}{
		{name: "成功请求", path: "/api/v1/home", status: http.StatusNoContent, level: "INFO"},
		{name: "根路径404", path: "/", status: http.StatusNotFound, level: "WARN"},
		{name: "未知路径404", path: "/v1/missing", status: http.StatusNotFound, level: "WARN"},
		{name: "上游请求标识", path: "/api/v1/home", requestID: "req_upstream", status: http.StatusNoContent, level: "INFO"},
		{name: "非法上游请求标识", path: "/api/v1/home", requestID: "Bearer private-value", status: http.StatusNoContent, level: "INFO"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			output := newSynchronizedBuffer()
			_, address := newTestEngine(t, Dependencies{
				Consumer: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					writer.WriteHeader(http.StatusNoContent)
				}),
				Admin: http.NotFoundHandler(),
				Styx:  newHTTPTestRuntimeWithLogger(t, output),
			})
			response := perform(t, address, http.MethodGet, testCase.path+"?token=query-secret", http.Header{
				"X-Request-ID":  {testCase.requestID},
				"Authorization": {"Bearer header-secret"},
			})
			if response.status != testCase.status {
				t.Fatalf("响应状态=%d，期望=%d", response.status, testCase.status)
			}
			requestID := response.header.Get("X-Request-ID")
			validUpstream := requestmeta.ValidRequestID(testCase.requestID)
			if requestID == "" || validUpstream && requestID != testCase.requestID {
				t.Fatal("响应必须生成请求标识或复用安全的上游标识")
			}
			if !validUpstream && (len(requestID) < 26 || strings.Trim(requestID, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") != "") {
				t.Fatalf("请求标识必须使用 Styx 的无前缀随机格式：%q", requestID)
			}
			line := output.Wait(t)
			for _, field := range []string{"method=GET", "path=" + testCase.path, "status=" + strconv.Itoa(testCase.status), "level=" + testCase.level, "request_id=" + requestID} {
				if !strings.Contains(" "+line, " "+field+" ") && !strings.HasSuffix(strings.TrimSpace(line), " "+field) {
					t.Fatalf("访问日志缺少或记错字段 %s：%s", field, line)
				}
			}
			if strings.Count(line, `msg="http access"`) != 1 {
				t.Fatalf("每次请求必须恰好输出一条访问日志：%s", line)
			}
			if strings.Contains(line, "query-secret") || strings.Contains(line, "header-secret") || strings.Contains(line, "private-value") {
				t.Fatalf("访问日志泄露查询参数或请求头：%s", line)
			}
			// 本地真实 HTTP 请求通常不足一毫秒，浮点耗时须仍保持大于零。
			var duration float64
			for _, field := range strings.Fields(line) {
				if value, ok := strings.CutPrefix(field, "duration_ms="); ok {
					var err error
					duration, err = strconv.ParseFloat(value, 64)
					if err != nil {
						t.Fatalf("耗时字段不是有效毫秒数：%v", err)
					}
				}
			}
			if duration <= 0 {
				t.Fatalf("访问日志丢失耗时精度：%s", line)
			}
		})
	}
}

func TestEngineSharesStyxRequestIDWithBusinessLogs(t *testing.T) {
	t.Parallel()
	for _, disabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(disabled), func(t *testing.T) {
			t.Parallel()
			for _, upstream := range []string{"", "edge-safe", "Bearer private-value"} {
				accessOutput := newSynchronizedBuffer()
				businessOutput := newSynchronizedBuffer()
				logger := slog.New(slog.NewJSONHandler(businessOutput, nil))
				_, address := newTestEngine(t, Dependencies{
					Consumer: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						logger.InfoContext(r.Context(), "request handled", apprequestmeta.LogFields(r.Context())...)
						w.WriteHeader(http.StatusNoContent)
					}),
					Admin: http.NotFoundHandler(),
					Styx:  newHTTPTestRuntimeWithLogger(t, accessOutput),
					Config: &configv1.HTTPServerConfig{
						Addr:       ":0",
						Middleware: &configv1.MiddlewareConfig{DisableLogging: disabled, DisableTracing: disabled},
					},
				})
				response := perform(t, address, http.MethodGet, "/api/v1/home", http.Header{requestmeta.HeaderName: {upstream}})
				id := response.header.Get(requestmeta.HeaderName)
				if response.status != http.StatusNoContent || !requestmeta.ValidRequestID(id) {
					t.Fatalf("Styx 未提供有效请求标识：status=%d id=%q", response.status, id)
				}
				if requestmeta.ValidRequestID(upstream) && id != upstream || !requestmeta.ValidRequestID(upstream) && id == upstream {
					t.Fatal("Styx 未按规则复用或替换上游标识")
				}
				var entry map[string]any
				if err := json.Unmarshal([]byte(businessOutput.Wait(t)), &entry); err != nil {
					t.Fatalf("业务日志不是有效 JSON：%v", err)
				}
				if entry["request_id"] != id || entry["level"] != "INFO" || strings.Contains(businessOutput.Wait(t), "private-value") {
					t.Fatalf("业务日志必须复用 Styx 请求标识且不得泄露非法请求头：%v", entry)
				}
				if !disabled && !strings.Contains(accessOutput.Wait(t), "request_id="+id) {
					t.Fatal("访问日志与业务日志、响应头未使用同一个 Styx 请求标识")
				}
			}
		})
	}
}

type testResponse struct {
	status int
	header http.Header
	body   string
}

type synchronizedBuffer struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	written chan struct{}
	once    sync.Once
}

func newSynchronizedBuffer() *synchronizedBuffer {
	return &synchronizedBuffer{written: make(chan struct{})}
}

func (buffer *synchronizedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	count, err := buffer.buffer.Write(value)
	buffer.once.Do(func() { close(buffer.written) })
	return count, err
}

func (buffer *synchronizedBuffer) Wait(t *testing.T) string {
	t.Helper()
	select {
	case <-buffer.written:
	case <-time.After(2 * time.Second):
		t.Fatal("等待访问日志超时")
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func newTestEngine(t *testing.T, deps Dependencies) (*kratoshttp.Server, string) {
	t.Helper()
	if deps.Styx == nil {
		deps.Styx = newHTTPTestRuntime(t)
	}
	if deps.Config == nil {
		deps.Config = &configv1.HTTPServerConfig{}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deps.Listener = listener
	engine, err := buildTestHTTPServer(deps)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	startEngine(t, engine, listener.Addr().String())
	return engine, "http://" + listener.Addr().String()
}

func buildTestHTTPServer(deps Dependencies) (*kratoshttp.Server, error) {
	if deps.Styx == nil {
		return nil, errMissingHandlers
	}
	var options []styxserver.HTTPOption
	if deps.Listener != nil {
		options = append(options, styxserver.WithHTTPListener(deps.Listener))
	}
	return newHTTPServer(
		&config.Server{Http: deps.Config},
		&service.Handlers{Consumer: deps.Consumer, Admin: deps.Admin},
		deps.Styx,
		options...,
	)
}

func testRouter(deps Dependencies) http.Handler {
	messages, err := apimessages.NewMessageBundle()
	if err != nil {
		panic(err)
	}
	return styxi18n.HTTPFilter(messages)(
		styxapierror.HTTPFilter()(newRouter(&service.Handlers{Consumer: deps.Consumer, Admin: deps.Admin})),
	)
}

func startEngine(t *testing.T, engine *kratoshttp.Server, address string) {
	t.Helper()
	runErrors := make(chan error, 1)
	go func() { runErrors <- engine.Start(context.Background()) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		connection, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			break
		}
		select {
		case runErr := <-runErrors:
			t.Fatalf("Styx/Kratos 启动失败：%v", runErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("等待 Styx/Kratos 监听超时")
		}
		time.Sleep(time.Millisecond)
	}
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if shutdownErr := engine.Stop(shutdownContext); shutdownErr != nil && !errors.Is(shutdownErr, context.Canceled) {
			t.Errorf("Styx/Kratos 停止失败：%v", shutdownErr)
		}
		select {
		case runErr := <-runErrors:
			if runErr != nil {
				t.Errorf("Styx/Kratos 运行失败：%v", runErr)
			}
		case <-time.After(2 * time.Second):
			t.Error("等待 Styx/Kratos 停止超时")
		}
	})
}

func perform(t *testing.T, address, method, path string, headers http.Header) testResponse {
	t.Helper()
	request, err := http.NewRequest(method, address+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := (&http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return testResponse{status: response.StatusCode, header: response.Header.Clone(), body: string(body)}
}

func testDependencies(t *testing.T) Dependencies {
	t.Helper()
	return Dependencies{
		Consumer: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/api/v1/home" {
				writer.WriteHeader(http.StatusNoContent)
				return
			}
			http.NotFound(writer, request)
		}),
		Admin:  http.NotFoundHandler(),
		Styx:   newHTTPTestRuntime(t),
		Config: &configv1.HTTPServerConfig{Addr: ":0"},
	}
}

func newHTTPTestRuntime(t *testing.T) *styxruntime.Runtime {
	t.Helper()
	return newHTTPTestRuntimeWithLogger(t, io.Discard)
}

func newHTTPTestRuntimeWithLogger(t *testing.T, output io.Writer) *styxruntime.Runtime {
	t.Helper()
	return newHTTPTestRuntimeWithSlog(t, slog.New(slog.NewTextHandler(output, nil)))
}

func newHTTPTestRuntimeWithSlog(t *testing.T, logger *slog.Logger) *styxruntime.Runtime {
	t.Helper()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	dependencies, err := styxobserve.NewDependencies(styxobserve.Config{
		Logger:         logger,
		TracerProvider: tracerProvider,
		MeterProvider:  metricnoop.NewMeterProvider(),
		Propagator:     propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity:     styxruntime.Identity{Name: "drama-api-test", Version: "test", Environment: "test"},
		Dependencies: dependencies,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Own("test tracer", tracerProvider.Shutdown); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	return runtime
}
