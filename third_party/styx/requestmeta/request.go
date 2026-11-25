// Package requestmeta 统一管理 HTTP 请求关联标识及业务日志所需的链路字段。
package requestmeta

import (
	"context"
	"crypto/rand"
	"net/http"

	"go.opentelemetry.io/otel/trace"
)

// HeaderName 是上游传入和服务端返回请求关联标识的 HTTP 头。
const HeaderName = "X-Request-ID"

type requestIDContextKey struct{}

// HTTPMiddleware 复用安全的上游请求标识，缺失或非法时生成无前缀的随机标识。
// 标识同时写入 Context 和响应头；重复装配时复用已有 Context 标识，不再次生成。
// 不修改请求头，不依赖日志或链路追踪开关，也不记录不安全的上游原值。
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := RequestID(r.Context())
		if requestID == "" {
			requestID = r.Header.Get(HeaderName)
			if !ValidRequestID(requestID) {
				requestID = rand.Text()
			}
			r = r.WithContext(context.WithValue(r.Context(), requestIDContextKey{}, requestID))
		}
		w.Header().Set(HeaderName, requestID)
		next.ServeHTTP(w, r)
	})
}

// RequestID 返回 HTTPMiddleware 写入的请求标识；nil 或未经过中间件的 Context 返回空字符串。
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

// ValidRequestID 检查标识是否为 1 至 96 字节的 ASCII 字母、数字或 -_.:，供请求入口及响应日志共用。
// 不裁剪或改写输入，拒绝空白、控制字符和其他字符，避免把任意请求头内容写入日志。
func ValidRequestID(value string) bool {
	if value == "" || len(value) > 96 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.' || character == ':' {
			continue
		}
		return false
	}
	return true
}

// TraceID 返回 Context 中有效 OpenTelemetry Span 的 Trace ID；nil 或没有有效 Span 时返回空字符串。
func TraceID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

// LogFields 返回可传给结构化日志器的 request_id 和 trace_id 键值对；缺失的标识保留为空字符串。
func LogFields(ctx context.Context) []any {
	return []any{"request_id", RequestID(ctx), "trace_id", TraceID(ctx)}
}
