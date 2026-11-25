// Package requesttime 固定一次业务请求的 UTC 接收时刻，避免处理跨日改变事实归属。
package requesttime

import (
	"context"
	"net/http"
	"time"
)

type receivedAtKey struct{}

// WithReceivedAt 为入口安装可信接收时间；嵌套入口保留最外层已记录的时刻。
func WithReceivedAt(ctx context.Context, receivedAt time.Time) context.Context {
	if _, ok := ctx.Value(receivedAtKey{}).(time.Time); ok {
		return ctx
	}
	return context.WithValue(ctx, receivedAtKey{}, receivedAt.UTC())
}

// Ensure 在内部非 HTTP 调用的入口读取一次可注入时钟；已有请求时间时不再次读取时钟。
func Ensure(ctx context.Context, clock func() time.Time) context.Context {
	if _, ok := ctx.Value(receivedAtKey{}).(time.Time); ok {
		return ctx
	}
	return WithReceivedAt(ctx, clock())
}

// At 返回本次入口固定的 UTC 时刻；调用方须先通过 Ensure 或 Middleware 建立请求边界。
func At(ctx context.Context, clock func() time.Time) time.Time {
	if receivedAt, ok := ctx.Value(receivedAtKey{}).(time.Time); ok {
		return receivedAt
	}
	return clock().UTC()
}

// Middleware 在读取请求体、鉴权和业务依赖之前记录一次服务器接收时间。
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(Ensure(r.Context(), time.Now)))
	})
}
