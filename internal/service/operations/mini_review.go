package operations

import (
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/operations"
	"drama-server/internal/pkg/identifier"
	"net/http"
	"strings"
)

// reviewListScope 只由固定路由安装主体渠道管理范围，不接受客户端应用或播放渠道覆盖。
func reviewListScope(provider string, next http.HandlerFunc) authenticatedHandler {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := application.WithPlayback(application.WithContext(r.Context(), ""), provider)
		next(w, r.WithContext(homepage.WithManagementContext(ctx)))
	}
}

// reviewSettings 只读取当前可信应用的审核开关菜单资料。
func (h *Handler) reviewSettings(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.ApplicationService.ReviewSettings(r.Context())
	writeResult(w, r, http.StatusOK, result, err)
}

// saveReviewSettings 拒绝清单和应用身份字段，版本与开关独立按修订保存。
func (h *Handler) saveReviewSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		// Versions 是当前应用的版本列表，与旧版单版本字段互斥。
		Versions []struct {
			// Version 必须与随包版本完全一致。
			Version string `json:"version"`
			// Enabled 要求显式选择该版本的审核状态。
			Enabled *bool `json:"enabled"`
		} `json:"versions"`
		// Version 必须与 Mini 包的三段版本完全匹配，关闭时允许为空。
		Version string `json:"version"`
		// Enabled 必须显式选择，防止缺失字段意外关闭审核。
		Enabled *bool `json:"enabled"`
		// Revision 是页面读取时的审核修订，首次为零。
		Revision *int64 `json:"revision"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Version = strings.TrimSpace(input.Version)
	if input.Revision == nil || *input.Revision < 0 || *input.Revision >= 9007199254740991 {
		rejectInvalidInput(w, r, "review settings input rejected", operations.ErrInvalidInput)
		return
	}
	review := application.MiniReview{Version: input.Version, Revision: *input.Revision}
	if input.Versions != nil {
		if input.Enabled != nil || input.Version != "" {
			rejectInvalidInput(w, r, "mixed review settings input rejected", operations.ErrInvalidInput)
			return
		}
		review.Versions = []application.MiniReviewVersion{}
		for _, row := range input.Versions {
			if row.Enabled == nil {
				rejectInvalidInput(w, r, "review version status missing", operations.ErrInvalidInput)
				return
			}
			review.Versions = append(review.Versions, application.MiniReviewVersion{Version: row.Version, Enabled: *row.Enabled})
		}
	} else {
		if input.Enabled == nil {
			rejectInvalidInput(w, r, "review settings status missing", operations.ErrInvalidInput)
			return
		}
		review.Enabled = *input.Enabled
	}
	if !normalizeMiniReviewVersions(&review) {
		rejectInvalidInput(w, r, "review versions rejected", operations.ErrInvalidInput)
		return
	}
	result, err := h.service.ApplicationService.SaveReviewSettings(r.Context(), review)
	writeResult(w, r, http.StatusOK, result, err)
}

// normalizeMiniReviewVersions 统一独立菜单与旧应用接口的版本格式及应用内唯一性。
func normalizeMiniReviewVersions(review *application.MiniReview) bool {
	review.Version = strings.TrimSpace(review.Version)
	if (review.Version != "" && !application.ValidMiniVersion(review.Version)) || (review.Versions == nil && review.Enabled && review.Version == "") || len(review.Versions) > 100 {
		return false
	}
	seen := map[string]bool{}
	for i := range review.Versions {
		row := &review.Versions[i]
		row.Version = strings.TrimSpace(row.Version)
		if !application.ValidMiniVersion(row.Version) || seen[row.Version] {
			return false
		}
		seen[row.Version] = true
	}
	return true
}

// reviewWhitelist 通过独立查看权限读取已选应用的审核内容清单。
func (h *Handler) reviewWhitelist(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.ApplicationService.ReviewWhitelist(r.Context())
	writeResult(w, r, http.StatusOK, result, err)
}

// saveReviewWhitelist 仅接收白名单与修订，不允许此菜单修改审核开关或应用身份。
func (h *Handler) saveReviewWhitelist(w http.ResponseWriter, r *http.Request) {
	var input struct {
		// Revision 是读取清单时得到的配置修订。
		Revision int64 `json:"revision"`
		// DramaIDs 是当前应用的首页内容引用。
		DramaIDs []string `json:"dramaIds"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Revision < 0 || input.Revision >= 9007199254740991 || len(input.DramaIDs) > 100 {
		rejectInvalidInput(w, r, "review whitelist input rejected", operations.ErrInvalidInput)
		return
	}
	seen := map[string]bool{}
	for i, id := range input.DramaIDs {
		id = strings.TrimSpace(id)
		if !identifier.ValidDatabaseID(id) || seen[id] {
			rejectInvalidInput(w, r, "review whitelist target rejected", operations.ErrInvalidInput)
			return
		}
		seen[id] = true
		input.DramaIDs[i] = id
	}
	result, err := h.service.ApplicationService.SaveReviewWhitelist(r.Context(), operations.ReviewWhitelist{Revision: input.Revision, DramaIDs: input.DramaIDs})
	writeResult(w, r, http.StatusOK, result, err)
}
