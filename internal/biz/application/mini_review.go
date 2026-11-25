package application

import (
	"context"
	"regexp"
	"slices"
)

// MiniReviewVersion 是手动登记的包版本及其独立审核开关。
type MiniReviewVersion struct {
	// Version 精确匹配 Mini 随包发送的三段版本号。
	Version string `json:"version"`
	// Enabled 只控制该版本，不影响应用的其他版本。
	Enabled bool `json:"enabled"`
}

// MiniReview 保存应用内多个审核版本及它们共用的首页清单。
type MiniReview struct {
	// Version 保留旧单版本接口的兼容摘要，新调用使用 Versions。
	Version string `json:"version"`
	// Enabled 是兼容摘要中 Version 的开关，不代表全部版本。
	Enabled bool `json:"enabled"`
	// Versions 为各包版本独立配置开关；历史配置从 Version、Enabled 自动读取。
	Versions []MiniReviewVersion `json:"versions,omitempty"`
	// DramaIDs 仅限制首页，不改变搜索、直达或播放授权。
	DramaIDs []string `json:"dramaIds,omitempty"`
	// Revision 防止旧表单覆盖并发修改，并隔离首页结果。
	Revision int64 `json:"revision"`
}

// ConfiguredVersions 兼容历史单版本 JSON，并复制结果以隔离调用方修改。
func (m MiniReview) ConfiguredVersions() []MiniReviewVersion {
	if m.Versions != nil {
		return append([]MiniReviewVersion{}, m.Versions...)
	}
	if m.Version != "" {
		return []MiniReviewVersion{{Version: m.Version, Enabled: m.Enabled}}
	}
	return []MiniReviewVersion{}
}

// AnyEnabled 保证任一版本开启时都保留共享清单与 IAA 逐集策略门禁。
func (m MiniReview) AnyEnabled() bool {
	return slices.ContainsFunc(m.ConfiguredVersions(), func(v MiniReviewVersion) bool { return v.Enabled })
}

// WithSettings 合并独立设置写入并保留应用白名单；旧单版本请求只更新对应版本。
func (m MiniReview) WithSettings(input MiniReview) MiniReview {
	versions := input.ConfiguredVersions()
	if input.Versions == nil {
		versions = m.ConfiguredVersions()
		version := input.Version
		if version == "" {
			version = m.Version
		}
		if version != "" {
			index := slices.IndexFunc(versions, func(v MiniReviewVersion) bool { return v.Version == version })
			row := MiniReviewVersion{Version: version, Enabled: input.Enabled}
			if index < 0 {
				versions = append(versions, row)
			} else {
				versions[index] = row
			}
		}
	}
	input.Versions = versions
	input.Version, input.Enabled = "", false
	if len(versions) > 0 {
		input.Version, input.Enabled = versions[0].Version, versions[0].Enabled
	}
	input.DramaIDs = slices.Clone(m.DramaIDs)
	return input
}

// ValidMiniVersion 限制为至多 32 字节、无前导零的三段业务版本号。
func ValidMiniVersion(version string) bool {
	return len(version) <= 32 && miniVersionPattern.MatchString(version)
}

var miniVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type miniVersionKey struct{}
type miniReviewKey struct{}

// WithMiniVersion 安装已由 HTTP 入口校验的包版本；版本不是身份凭据。
func WithMiniVersion(ctx context.Context, version string) context.Context {
	return context.WithValue(ctx, miniVersionKey{}, version)
}

// MiniVersionFromContext 返回请求包版本，旧客户端缺失时返回空值。
func MiniVersionFromContext(ctx context.Context) string {
	version, _ := ctx.Value(miniVersionKey{}).(string)
	return version
}

// MiniReviewState 是按可信应用与请求版本解析的有效审核结果。
type MiniReviewState struct {
	// Enabled 表示该版本首页需要过滤。
	Enabled bool `json:"enabled"`
	// EpisodeOnly 仅在纯 IAA 且审核开启时为真。
	EpisodeOnly bool `json:"episodeOnly"`
	// Revision 是本次应用审核配置修订。
	Revision int64 `json:"revision"`
	// DramaIDs 为服务端首页过滤使用，不通过公开配置返回。
	DramaIDs []string `json:"-"`
}

// WithMiniReview 只使用权威应用配置解析覆盖，复制清单防止共享修改。
func WithMiniReview(ctx context.Context, config MiniReview, mode MonetizationMode) context.Context {
	active := slices.ContainsFunc(config.ConfiguredVersions(), func(v MiniReviewVersion) bool {
		return v.Enabled && v.Version != "" && v.Version == MiniVersionFromContext(ctx)
	})
	return context.WithValue(ctx, miniReviewKey{}, MiniReviewState{Enabled: active,
		EpisodeOnly: active && mode == MonetizationModeIAA, Revision: config.Revision, DramaIDs: slices.Clone(config.DramaIDs)})
}

// MiniReviewFromContext 返回本次请求的有效覆盖，缺省保持原有行为。
func MiniReviewFromContext(ctx context.Context) MiniReviewState {
	state, _ := ctx.Value(miniReviewKey{}).(MiniReviewState)
	return state
}
