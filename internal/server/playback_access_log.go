package server

import (
	"context"
	"log/slog"
	"net/url"
	"strings"

	styxobserve "gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

// 播放授权响应包含观众短时 Token，请求包含未经筛选的投放参数，只保留路径、状态和耗时等日志字段。
type playbackAccessLog struct{ slog.Handler }

// Handle 保留标准访问日志关联字段，排除播放鉴权请求及响应的所有正文形式。
func (h playbackAccessLog) Handle(ctx context.Context, record slog.Record) error {
	if record.Message != "http access" {
		return h.Handler.Handle(ctx, record)
	}
	private := false
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key != "path" {
			return true
		}
		path, err := url.PathUnescape(attr.Value.String())
		if err != nil {
			return false
		}
		private = bodyExcludedFromAccessLog(path)
		return false
	})
	if !private {
		return h.Handler.Handle(ctx, record)
	}
	filtered := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		if !strings.HasPrefix(attr.Key, "request_body") && !strings.HasPrefix(attr.Key, "response_body") {
			filtered.AddAttrs(attr)
		}
		return true
	})
	return h.Handler.Handle(ctx, filtered)
}

// bodyExcludedFromAccessLog 列出正文可能含凭据的路径：播放授权、观众登录与启动（平台授权码、VisitorToken）、
// 全部管理接口（管理员密码、JWT、BytePlus 密钥、TikTok 密钥）和平台回调（验签材料）。这些路径只记录关联字段。
func bodyExcludedFromAccessLog(path string) bool {
	switch path {
	case "/api/v1/play/entry", "/api/v1/play/resolve", "/api/v1/play/resolve-batch",
		"/api/v1/session", "/api/v1/bootstrap":
		return true
	}
	return strings.HasPrefix(path, "/admin/") || strings.HasPrefix(path, "/callbacks/")
}

// WithAttrs 保留底层结构化字段和播放脱敏规则。
func (h playbackAccessLog) WithAttrs(attrs []slog.Attr) slog.Handler {
	return playbackAccessLog{h.Handler.WithAttrs(attrs)}
}

// WithGroup 保留底层日志分组和播放脱敏规则。
func (h playbackAccessLog) WithGroup(name string) slog.Handler {
	return playbackAccessLog{h.Handler.WithGroup(name)}
}

// playbackHTTPRuntime 仅为 HTTP 链路装配脱敏日志器，沿用原 Runtime 的服务标识与可观测依赖。
func playbackHTTPRuntime(runtime *styxruntime.Runtime) (*styxruntime.Runtime, error) {
	deps := runtime.Dependencies()
	protected, err := styxobserve.NewDependencies(styxobserve.Config{
		Logger: slog.New(playbackAccessLog{deps.Logger().Handler()}), TracerProvider: deps.TracerProvider(),
		MeterProvider: deps.MeterProvider(), Propagator: deps.Propagator(),
	})
	if err != nil {
		return nil, err
	}
	return styxruntime.New(styxruntime.Config{Identity: runtime.Identity(), Dependencies: protected})
}
