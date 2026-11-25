package middleware

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"gitee.com/linpbin/styx/internal/logutil"
	"gitee.com/linpbin/styx/observe"
	"gitee.com/linpbin/styx/requestmeta"
)

const accessBodyLimit = 4 * 1024

// HTTPAccessLog 返回 HTTP 访问日志过滤器。
// 记录原始请求的转义路径、方法、客户端 IP、最终状态码、保留小数的毫秒耗时和安全请求标识，
// client_ip 优先采用网关覆盖写入且格式有效的 X-Real-IP，否则回退到 TCP 对端地址；
// 不采集 query 或请求头；请求和响应正文按原始字节最多保留前 4 KiB，不脱敏。
// 请求正文只包含业务实际读取的字节；完整合法 JSON 作为 JSON 值输出，截断正文作为前缀输出。
// 4xx 使用 WARN，5xx 使用 ERROR，其余响应使用 INFO；应放在 HTTPRecovery 外层。
// 单独使用时也装配 requestmeta.HTTPMiddleware；已有 Context 标识会被复用，业务处理器可直接读取。
func HTTPAccessLog(dependencies observe.Dependencies) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return requestmeta.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			// 在应用改写 URL 之前保存请求路径，404 也能定位到客户端访问的地址。
			method, path, clientIP := r.Method, r.URL.EscapedPath(), resolveClientIP(r)
			requestContentLength := r.ContentLength
			requestID := requestmeta.RequestID(r.Context())
			var requestBody bodyCapture
			var requestReader *capturingReadCloser
			if r.Body != nil && r.Body != http.NoBody {
				requestReader = &capturingReadCloser{ReadCloser: r.Body, capture: &requestBody}
				r.Body = requestReader
			}
			writer := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK, requestID: requestID}
			next.ServeHTTP(writer, r)

			// 应用可能有自己的请求标识中间件，优先使用最终响应中的安全标识，
			// 让访问日志与客户端收到的标识以及应用业务日志保持一致。
			if !writer.written {
				writer.captureRequestID()
			}
			attributes := []any{
				"method", method,
				"path", path,
				"client_ip", clientIP,
				"status", writer.statusCode,
				"duration_ms", float64(time.Since(start)) / float64(time.Millisecond),
				"request_id", writer.requestID,
			}
			if requestReader != nil {
				if requestBody.bytes > 0 {
					complete := !requestReader.readFailed && (requestReader.eof || requestContentLength >= 0 && requestBody.bytes == requestContentLength)
					attributes = appendBodyAttributes(attributes, "request", &requestBody, complete)
				}
			}
			attributes = appendBodyAttributes(attributes, "response", &writer.body, !writer.hijacked && !writer.writeFailed)
			switch {
			case writer.statusCode >= http.StatusInternalServerError:
				logutil.Error(dependencies.Logger(), r.Context(), "http access", attributes...)
			case writer.statusCode >= http.StatusBadRequest:
				logutil.Warn(dependencies.Logger(), r.Context(), "http access", attributes...)
			default:
				logutil.Info(dependencies.Logger(), r.Context(), "http access", attributes...)
			}
		}))
	}
}

// bodyCapture 只保存正文前 4 KiB，同时统计实际经过包装器的字节数。
type bodyCapture struct {
	data  []byte
	bytes int64
}

func (c *bodyCapture) add(data []byte) {
	c.bytes += int64(len(data))
	if remaining := accessBodyLimit - len(c.data); remaining > 0 {
		c.data = append(c.data, data[:min(len(data), remaining)]...)
	}
}

// capturingReadCloser 在业务读取时采集正文，不预读或改变读取结果。
type capturingReadCloser struct {
	io.ReadCloser
	capture    *bodyCapture
	eof        bool
	readFailed bool
}

func (r *capturingReadCloser) Read(data []byte) (int, error) {
	n, err := r.ReadCloser.Read(data)
	if n > 0 {
		r.capture.add(data[:n])
	}
	if err == io.EOF {
		r.eof = true
	} else if err != nil {
		r.readFailed = true
	}
	return n, err
}

// appendBodyAttributes 尽量以 JSON 值记录完整正文；未读完或超过上限的正文直接记录前缀。
func appendBodyAttributes(attributes []any, side string, body *bodyCapture, complete bool) []any {
	if body.bytes == 0 {
		return attributes
	}
	truncated := body.bytes > accessBodyLimit
	field := side + "_body"
	if !truncated && complete && json.Valid(body.data) {
		return append(attributes, field, json.RawMessage(body.data))
	}
	var value string
	if utf8.Valid(body.data) {
		value = string(body.data)
	} else {
		field += "_base64"
		value = base64.StdEncoding.EncodeToString(body.data)
	}
	return append(attributes, field, value)
}

// resolveClientIP 优先返回网关规范化注入的客户端地址，缺失或非法时回退到 TCP 对端。
func resolveClientIP(request *http.Request) string {
	realIP, err := netip.ParseAddr(strings.TrimSpace(request.Header.Get("X-Real-IP")))
	if err == nil {
		return realIP.Unmap().String()
	}
	peer, ok := parseRemoteIP(request.RemoteAddr)
	if !ok {
		return ""
	}
	return peer.String()
}

func parseRemoteIP(remoteAddress string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = remoteAddress
	}
	address, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

type statusResponseWriter struct {
	http.ResponseWriter
	statusCode  int
	written     bool
	requestID   string
	body        bodyCapture
	hijacked    bool
	writeFailed bool
}

func (w *statusResponseWriter) WriteHeader(code int) {
	if w.written {
		return
	}
	// 100/103 等临时响应不结束响应头，后续最终状态仍需传给客户端并写入日志。
	// 101 则已切换协议，按最终状态处理。
	w.ResponseWriter.WriteHeader(code)
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		return
	}
	w.statusCode = code
	w.written = true
	w.captureRequestID()
}

// captureRequestID 记录发送响应头时的关联标识，避免事后修改 Header 造成日志与客户端不一致。
func (w *statusResponseWriter) captureRequestID() {
	if responseID := w.Header().Get(requestmeta.HeaderName); requestmeta.ValidRequestID(responseID) {
		w.requestID = responseID
	}
}

func (w *statusResponseWriter) Write(body []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	if n > 0 {
		w.body.add(body[:n])
	}
	if err != nil || n != len(body) {
		w.writeFailed = true
	}
	return n, err
}

// Unwrap 允许 http.ResponseController 访问原始 ResponseWriter。
func (w *statusResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Flush 保留流式响应能力。
func (w *statusResponseWriter) Flush() {
	_ = w.FlushError()
}

// FlushError 在刷新前锁定隐式 200，并向 ResponseController 调用方保留刷新错误。
func (w *statusResponseWriter) FlushError() error {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err != nil {
		w.writeFailed = true
	}
	return err
}

// Hijack 保留 WebSocket 等连接接管能力。
func (w *statusResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	connection, readWriter, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.hijacked = true
		if !w.written {
			w.statusCode = http.StatusSwitchingProtocols
			w.written = true
		}
	}
	return connection, readWriter, err
}

// Push 保留 HTTP/2 Server Push 能力。
func (w *statusResponseWriter) Push(target string, options *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}
