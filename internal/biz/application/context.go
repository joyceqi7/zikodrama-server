package application

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// contextKey 隔离可信应用标识的上下文键，防止与其他包的字符串键冲突。
type contextKey struct{}

type subjectContextKey struct{}

// WithSubject 写入已解析的运营主体，官方和直连线上内容均按此范围隔离。
func WithSubject(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, subjectContextKey{}, id)
}

// SubjectIDFromContext 返回明确的发布主体；缺失时不回退默认主体。
func SubjectIDFromContext(ctx context.Context) string {
	if ctx != nil {
		if id, ok := ctx.Value(subjectContextKey{}).(string); ok {
			return id
		}
	}
	return ""
}

// timeZoneContextKey 隔离应用业务时区的上下文键，与应用标识分别存放。
type timeZoneContextKey struct{}

// WithContext 写入已经由应用解析器或 VisitorToken 校验的应用数据库自增主键。
func WithContext(ctx context.Context, applicationID string) context.Context {
	return context.WithValue(ctx, contextKey{}, strings.TrimSpace(applicationID))
}

// IDFromContext 返回可信应用标识；空字符串表示调用链缺少应用边界。
func IDFromContext(ctx context.Context) string {
	if ctx != nil {
		if applicationID, ok := ctx.Value(contextKey{}).(string); ok && strings.TrimSpace(applicationID) != "" {
			return strings.TrimSpace(applicationID)
		}
	}
	return ""
}

// WithTimeZone 同时写入可信应用标识和该应用配置的 IANA 时区。
func WithTimeZone(ctx context.Context, applicationID, timeZone string) context.Context {
	ctx = WithContext(ctx, applicationID)
	timeZone = strings.TrimSpace(timeZone)
	if timeZone == "" {
		timeZone = "UTC"
	}
	return context.WithValue(ctx, timeZoneContextKey{}, timeZone)
}

// TimeZoneFromContext 返回当前应用时区；缺失时使用 UTC。
func TimeZoneFromContext(ctx context.Context) string {
	if ctx != nil {
		if timeZone, ok := ctx.Value(timeZoneContextKey{}).(string); ok && strings.TrimSpace(timeZone) != "" {
			return strings.TrimSpace(timeZone)
		}
	}
	return "UTC"
}

// LocationFromContext 将当前应用时区解析为 time.Location；无效值安全回退 UTC。
func LocationFromContext(ctx context.Context) *time.Location {
	location, err := time.LoadLocation(TimeZoneFromContext(ctx))
	if err != nil {
		slog.ErrorContext(ctx, "load location failed", "error", err)
		return time.UTC
	}
	return location
}

// playbackContextKey 隔离已由服务端确定的播放配置。
type playbackContextKey struct{}

// PlaybackProfile 是可信应用的播放边界。
type PlaybackProfile struct {
	// Provider 只保存服务端已确定的应用播放模式，直连参数统一读取全局默认配置。
	Provider string
}

// WithPlayback 写入已解析的应用播放模式，客户端不能覆写或指定直连配置。
func WithPlayback(ctx context.Context, provider string) context.Context {
	return context.WithValue(ctx, playbackContextKey{}, PlaybackProfile{Provider: provider})
}

// PlaybackFromContext 缺省仍为官方，保持旧应用行为。
func PlaybackFromContext(ctx context.Context) PlaybackProfile {
	if p, ok := ctx.Value(playbackContextKey{}).(PlaybackProfile); ok && p.Provider != "" {
		return p
	}
	return PlaybackProfile{Provider: "tiktok_minis"}
}

// IsDirect 判断可信应用是否使用独立 VePlayer。
func IsDirect(ctx context.Context) bool { return PlaybackFromContext(ctx).Provider == "byteplus_web" }

// ContextForApplication 同时安装可信应用标识、时区与当前播放模式。
func ContextForApplication(ctx context.Context, a Application) context.Context {
	return WithMiniReview(WithSubject(WithPlayback(WithTimeZone(ctx, a.ID, a.TimeZone), a.PlaybackProvider), a.SubjectID), a.MiniReview, a.MonetizationMode)
}
