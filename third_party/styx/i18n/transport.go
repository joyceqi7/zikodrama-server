package i18n

import (
	"context"
	"net/http"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
)

// Middleware 为 Kratos HTTP/gRPC Handler 安装请求级 Localizer。
func Middleware(bundle *Bundle) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, request any) (any, error) {
			if bundle != nil {
				header := ""
				if transporter, ok := transport.FromServerContext(ctx); ok {
					header = transporter.RequestHeader().Get("accept-language")
				}
				ctx = bundle.WithAcceptLanguage(ctx, header)
			}
			return next(ctx, request)
		}
	}
}

// HTTPFilter 为包括 raw net/http Handler 在内的所有 HTTP 路由安装 Localizer。
func HTTPFilter(bundle *Bundle) kratoshttp.FilterFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if bundle == nil {
				next.ServeHTTP(response, request)
				return
			}
			response.Header().Add("Vary", "Accept-Language")
			ctx := bundle.WithAcceptLanguage(request.Context(), request.Header.Get("Accept-Language"))
			next.ServeHTTP(response, request.WithContext(ctx))
		})
	}
}
