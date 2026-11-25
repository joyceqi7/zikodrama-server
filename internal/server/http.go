// Package server 把短剧传输层挂载到 Styx 管理的 Kratos Server。
package server

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"strings"

	styxi18n "gitee.com/linpbin/styx/i18n"
	styxruntime "gitee.com/linpbin/styx/runtime"
	styxserver "gitee.com/linpbin/styx/server"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"

	config "drama-server/internal/conf"
	"drama-server/internal/pkg/apimessages"
	"drama-server/internal/pkg/requesttime"
	"drama-server/internal/service"
)

var errMissingHandlers = errors.New("缺少 HTTP 业务处理器")
var errMissingHTTPConfig = errors.New("缺少 Styx HTTP 配置")

type accessLogExcludedBodyKey struct{}

// NewHTTPServer 使用 Styx 创建受治理的 Kratos HTTP Server，再挂载业务路由。
// 请求标识、监听、超时、TLS、Recovery、Tracing、访问日志和关闭行为均由 Styx/Kratos 负责；
// 本包只保留 Drama 独有的 URL 到 Handler 映射。
func NewHTTPServer(
	serverConfig *config.Server,
	handlers *service.Handlers,
	runtime *styxruntime.Runtime,
) (*kratoshttp.Server, error) {
	return newHTTPServer(serverConfig, handlers, runtime)
}

func newHTTPServer(
	serverConfig *config.Server,
	handlers *service.Handlers,
	runtime *styxruntime.Runtime,
	options ...styxserver.HTTPOption,
) (*kratoshttp.Server, error) {
	if handlers == nil || handlers.Consumer == nil || handlers.Admin == nil {
		return nil, errMissingHandlers
	}
	if serverConfig == nil || serverConfig.GetHttp() == nil {
		return nil, errMissingHTTPConfig
	}
	messages, err := apimessages.NewMessageBundle()
	if err != nil {
		return nil, err
	}
	options = append([]styxserver.HTTPOption{
		styxserver.WithHTTPFilter(restoreAccessLogExcludedBody),
		styxserver.WithHTTPFilter(styxi18n.HTTPFilter(messages)),
	}, options...)
	if runtime != nil {
		runtime, err = playbackHTTPRuntime(runtime)
		if err != nil {
			return nil, err
		}
	}
	server, err := styxserver.NewHTTPServer(serverConfig.GetHttp(), runtime, options...)
	if err != nil {
		return nil, err
	}
	server.HandlePrefix("/", newRouter(handlers))
	server.Handler = requesttime.Middleware(hideRequestBodyFromAccessLog(server.Handler))
	return server, nil
}

// hideRequestBodyFromAccessLog 隔离广告错误现场、主体发布密钥和代传视频流，防止 Styx 访问日志采集未经校验、脱敏的正文。
// 不预读或复制正文，鉴权、限流和请求体大小限制仍由内层业务 Handler 执行。
func hideRequestBodyFromAccessLog(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		proxyUpload := strings.HasPrefix(r.URL.Path, "/admin/v1/episodes/") &&
			strings.Contains(r.URL.Path, "/media-uploads/") && strings.HasSuffix(r.URL.Path, "/proxy")
		subjectWrite := (r.Method == stdhttp.MethodPost || r.Method == stdhttp.MethodPut) && (r.URL.Path == "/admin/v1/subjects" || strings.HasPrefix(r.URL.Path, "/admin/v1/subjects/"))
		if !subjectWrite && (r.Method != stdhttp.MethodPost || r.URL.Path != "/api/v1/ad-errors" && !proxyUpload) {
			next.ServeHTTP(w, r)
			return
		}
		originalBody := r.Body
		request := r.WithContext(context.WithValue(r.Context(), accessLogExcludedBodyKey{}, originalBody))
		request.Body = stdhttp.NoBody
		next.ServeHTTP(w, request)
	})
}

// restoreAccessLogExcludedBody 在 Styx 访问日志内层交回原始正文，保留广告错误校验、脱敏及视频上传行为。
func restoreAccessLogExcludedBody(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if body, ok := r.Context().Value(accessLogExcludedBodyKey{}).(io.ReadCloser); ok {
			r.Body = body
		}
		next.ServeHTTP(w, r)
	})
}

func newRouter(handlers *service.Handlers) stdhttp.Handler {
	router := stdhttp.NewServeMux()
	// 公网消费者接口与管理接口使用彼此独立的根前缀；这里只挂载当前合同，
	// 不把根路径交给任一业务 Handler，也不为未上线的旧路径保留兼容入口。
	router.Handle("/admin/v1/", handlers.Admin)
	router.Handle("/api/v1/", handlers.Consumer)
	router.Handle("/callbacks/v1/", handlers.Admin)
	return router
}
