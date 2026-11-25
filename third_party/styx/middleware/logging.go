// Package middleware 提供 Styx 的 Kratos 中间件。
package middleware

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"

	"gitee.com/linpbin/styx/internal/logutil"
	"gitee.com/linpbin/styx/observe"
)

// Logging 返回服务端请求日志中间件，不采集请求、响应或错误正文。
func Logging(dependencies observe.Dependencies) middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, request any) (any, error) {
			var kind, operation string
			if info, ok := transport.FromServerContext(ctx); ok {
				kind = info.Kind().String()
				operation = info.Operation()
			}

			start := time.Now()
			reply, err := handler(ctx, request)
			attributes := []any{
				"kind", kind,
				"operation", operation,
				"duration_ms", time.Since(start).Milliseconds(),
			}
			if err != nil {
				serviceError := errors.FromError(err)
				attributes = append(attributes, "code", serviceError.Code, "reason", serviceError.Reason)
				logutil.Error(dependencies.Logger(), ctx, "request failed", attributes...)
			} else {
				logutil.Info(dependencies.Logger(), ctx, "request completed", attributes...)
			}
			return reply, err
		}
	}
}

// ClientLogging 返回下游调用失败日志中间件，不采集请求或错误正文。
func ClientLogging(dependencies observe.Dependencies) middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, request any) (any, error) {
			var kind, operation string
			if info, ok := transport.FromClientContext(ctx); ok {
				kind = info.Kind().String()
				operation = info.Operation()
			}

			start := time.Now()
			reply, err := handler(ctx, request)
			if err != nil {
				serviceError := errors.FromError(err)
				logutil.Error(dependencies.Logger(), ctx, "downstream call failed",
					"kind", kind,
					"operation", operation,
					"code", serviceError.Code,
					"reason", serviceError.Reason,
					"duration_ms", time.Since(start).Milliseconds(),
				)
			}
			return reply, err
		}
	}
}
