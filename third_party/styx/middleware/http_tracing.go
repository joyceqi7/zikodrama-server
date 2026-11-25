package middleware

import (
	"net/http"

	"go.opentelemetry.io/otel/propagation"

	"gitee.com/linpbin/styx/observe"
)

// HTTPTracing 返回使用显式 Provider 和 Propagator 的 HTTP 链路过滤器。
func HTTPTracing(dependencies observe.Dependencies) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := dependencies.Propagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			tracer := dependencies.TracerProvider().Tracer("gitee.com/linpbin/styx/middleware/http")
			// 原始路径可能包含高基数标识或凭据，原始 Handler 统一使用稳定 Span 名。
			ctx, span := tracer.Start(ctx, "HTTP "+r.Method)
			defer span.End()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
