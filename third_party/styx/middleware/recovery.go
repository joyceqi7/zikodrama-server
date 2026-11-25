package middleware

import (
	"context"
	"net/http"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/middleware"
	"go.opentelemetry.io/otel/metric"

	"gitee.com/linpbin/styx/internal/logutil"
	"gitee.com/linpbin/styx/observe"
)

// Recovery 返回不泄露 panic 值、请求正文和堆栈的恢复中间件。
func Recovery(serviceName string, dependencies observe.Dependencies) middleware.Middleware {
	counter, counterError := dependencies.MeterProvider().Meter("gitee.com/linpbin/styx/middleware").Int64Counter(
		"styx.server.panics",
		metric.WithDescription("服务端捕获的 panic 数量"),
		metric.WithUnit("{panic}"),
	)
	if counterError != nil {
		dependencies.Logger().Warn("create panic counter failed", "error", counterError)
	}

	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, request any) (reply any, err error) {
			defer func() {
				if recover() == nil {
					return
				}
				if counterError == nil {
					counter.Add(ctx, 1)
				}
				logutil.Error(dependencies.Logger(), ctx, "panic recovered", "service", serviceName)
				err = errors.InternalServer("PANIC", "internal server error")
			}()
			return handler(ctx, request)
		}
	}
}

// HTTPRecovery 覆盖原始 HTTP Handler，避免 panic 值和堆栈落入 net/http 默认日志。
func HTTPRecovery(serviceName string, dependencies observe.Dependencies) func(http.Handler) http.Handler {
	counter, counterError := dependencies.MeterProvider().Meter("gitee.com/linpbin/styx/middleware").Int64Counter(
		"styx.http.server.panics",
		metric.WithDescription("HTTP 服务端捕获的 panic 数量"),
		metric.WithUnit("{panic}"),
	)
	if counterError != nil {
		dependencies.Logger().Warn("create HTTP panic counter failed", "error", counterError)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			defer func() {
				if recover() == nil {
					return
				}
				if counterError == nil {
					counter.Add(request.Context(), 1)
				}
				logutil.Error(dependencies.Logger(), request.Context(), "HTTP panic recovered", "service", serviceName)
				http.Error(writer, "internal server error", http.StatusInternalServerError)
			}()
			next.ServeHTTP(writer, request)
		})
	}
}
