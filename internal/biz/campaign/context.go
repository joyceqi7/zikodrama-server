package campaign

import (
	"context"
	"strings"
)

// attributionContextKey 隔离可信投放归因的上下文键，避免客户端同名字段覆盖业务归因。
type attributionContextKey struct{}

// Attribution 是服务端完成投放链接解析后写入请求上下文的可信归因。
type Attribution struct {
	// ID 是当前消费者启动关联的内部投放标识。
	ID string `json:"id"`
	// Code 是投放链接的稳定启动编码。
	Code string `json:"code"`
	// DramaID 是可信投放解析得到的内部落地短剧标识。
	DramaID string `json:"dramaId,omitempty"`
	// EpisodeID 是可信投放解析得到的内部落地剧集标识。
	EpisodeID string `json:"episodeId,omitempty"`
	// ExperimentKey 是后续归因或 A/B 分组使用的稳定键。
	ExperimentKey string `json:"experimentKey,omitempty"`
}

// WithAttribution 把已经校验的投放标识写入业务上下文。
func WithAttribution(ctx context.Context, campaignID string) context.Context {
	attribution := Attribution{ID: strings.TrimSpace(campaignID)}
	return context.WithValue(ctx, attributionContextKey{}, attribution)
}

// AttributionFromContext 返回上游已经验证的投放归因；缺失时返回零值。
func AttributionFromContext(ctx context.Context) Attribution {
	if ctx != nil {
		if attribution, ok := ctx.Value(attributionContextKey{}).(Attribution); ok {
			return attribution
		}
	}
	return Attribution{}
}
