package tiktokmedia

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"drama-server/internal/biz/operations"
)

// albumRecoveryVersion 只解码核对冻结版本需要的官方字段，不保存媒体地址或原始响应。
type albumRecoveryVersion struct {
	// AlbumID 保存查询结果的剧目归属。
	AlbumID stringID `json:"album_id"`
	// Version 是本条内容版本。
	Version int `json:"version"`
	// CurrentVersion 是平台声明的最新版本，用于拒绝过期查询结果。
	CurrentVersion int `json:"current_version"`
	// OnlineVersion 是当前实际上线的版本；已有旧版本在线不妨碍恢复本次未送审的新版本。
	OnlineVersion int `json:"online_version"`
	// ReviewStatus 仅明确未送审的版本可以继续送审。
	ReviewStatus *int `json:"review_status"`
	// PublishStatus 是 Album 整体上下架状态，不能据此判断当前查询版本是否已经上线。
	PublishStatus *int `json:"publish_status"`
	// AlbumInfo 保存本次完整剧目资料的核对字段。
	AlbumInfo struct {
		// Language 是原声语言。
		Language string `json:"language"`
		// Title 是剧名。
		Title string `json:"title"`
		// SequenceCount 是剧目声明的分集数。
		SequenceCount int `json:"seq_num"`
		// CoverList 是官方封面标识列表。
		CoverList []string `json:"cover_list"`
		// Year 是发行年份。
		Year int `json:"year"`
		// AlbumStatus 是内容连载状态。
		AlbumStatus int `json:"album_status"`
		// Description 是简介。
		Description string `json:"desp"`
		// DramaType 是官方短剧类型。
		DramaType int `json:"drama_type"`
		// TagList 保留标签标识原文。
		TagList []stringID `json:"tag_list"`
	} `json:"album_info"`
	// Episodes 保存用于按序号和 VID 核对的分集资料。
	Episodes []struct {
		// EpisodeID 保留官方大整数标识原文。
		EpisodeID stringID `json:"episode_id"`
		// Title 是分集标题。
		Title string `json:"title"`
		// Sequence 是从 1 开始的分集序号。
		Sequence int `json:"seq"`
		// CoverList 是分集官方封面标识列表。
		CoverList []string `json:"cover_list"`
		// VID 是该分集的 BytePlus 原视频标识。
		VID string `json:"byteplus_vid"`
	} `json:"episode_info_list"`
}

// ResolveAlbumUpdate 以完整剧目资料、分集序号和 VID 核实更新结果；新版本尚不可见时返回可重查的未知结果，歧义或内容冲突仍暂停，绝不重发 Album Update。
func (c *Client) ResolveAlbumUpdate(ctx context.Context, draft operations.AlbumDraft) (operations.UpdateAlbumResult, error) {
	versions, diagnostic, err := c.queryAlbumUpdate(ctx, draft)
	if err != nil {
		return operations.UpdateAlbumResult{}, err
	}
	return resolveAlbumUpdate(ctx, draft, versions, diagnostic)
}

// queryAlbumUpdate 共用只读查询和输入门禁，保留查询失败原因，不把一次不可见视为写入失败。
func (c *Client) queryAlbumUpdate(ctx context.Context, draft operations.AlbumDraft) ([]albumRecoveryVersion, platformCallDiagnostic, error) {
	if _, err := numericID(draft.AlbumID); err != nil {
		slog.WarnContext(ctx, "confirm TikTok album update rejected", "error", err)
		return nil, platformCallDiagnostic{}, err
	}
	if c.clientKey != draft.ClientKey || draft.Version < 0 || len(draft.Episodes) == 0 {
		err := &operations.PlatformCallError{Code: "invalid_album_recovery_input", OutcomeUnknown: true}
		slog.WarnContext(ctx, "confirm TikTok album update rejected", "error", err)
		return nil, platformCallDiagnostic{}, err
	}
	var response struct {
		// Versions 为同一剧目的官方版本列表。
		Versions []albumRecoveryVersion `json:"album_version_list"`
	}
	query := url.Values{"client_key": {c.clientKey}, "album_id": {draft.AlbumID}}
	var diagnostic platformCallDiagnostic
	if err := c.doJSONWithDiagnostic(ctx, http.MethodGet, "/v2/sg/shortdrama/album/query/", query, nil, &response, nil, &diagnostic); err != nil {
		// 查询失败不能证明原写入失败，必须保留原外部写检查点。
		var failure *operations.PlatformCallError
		retryable := errors.As(err, &failure) && failure.Retryable
		return nil, diagnostic, fmt.Errorf("query TikTok album update failed (%v): %w", err,
			&operations.PlatformCallError{Code: "album_update_unconfirmed", OutcomeUnknown: true, Retryable: retryable})
	}
	return response.Versions, diagnostic, nil
}

// resolveAlbumUpdate 仅依据同一次查询的完整内容确认新版本，不写平台或业务数据库。
func resolveAlbumUpdate(ctx context.Context, draft operations.AlbumDraft, versions []albumRecoveryVersion, diagnostic platformCallDiagnostic) (operations.UpdateAlbumResult, error) {
	latest := draft.Version
	for _, version := range versions {
		if version.Version > latest {
			latest = version.Version
		}
		if version.CurrentVersion > latest {
			latest = version.CurrentVersion
		}
	}
	var result operations.UpdateAlbumResult
	matches := 0
	latestVersionReturned := false
	mismatches := make(map[string]int)
	for _, version := range versions {
		if version.Version != latest || version.Version <= draft.Version {
			continue
		}
		latestVersionReturned = true
		mapping, reason := version.matchesDraft(draft)
		if reason != "" {
			mismatches[reason]++
			continue
		}
		matches++
		result = operations.UpdateAlbumResult{Version: version.Version, EpisodeIDsBySequence: mapping}
	}
	if matches != 1 {
		// current_version 可能先于新版本详情可见，不能仅凭版本号前进就判定内容冲突。
		// 尚未返回最新版本时允许继续只读核对；已返回但内容不符或重复歧义时仍需人工处理。
		if !latestVersionReturned {
			mismatches["latest_version_not_visible"] = 1
		}
		err := &operations.PlatformCallError{Code: "album_update_unconfirmed", OutcomeUnknown: true, Retryable: !latestVersionReturned}
		slog.WarnContext(ctx, "confirm TikTok album update failed", "error", err, "album_id", draft.AlbumID,
			"base_version", draft.Version, "latest_version", latest, "version_count", len(versions),
			"matching_version_count", matches, "platform_request_id", diagnostic.clean(diagnostic.requestID), "http_status", diagnostic.httpStatus,
			"version_mismatch_reasons", mismatches,
			"response_payload", diagnostic.albumPayload, "root_field_types", diagnostic.rootFieldTypes, "data_field_types", diagnostic.dataFieldTypes)
		return operations.UpdateAlbumResult{}, err
	}
	return result, nil
}

// matchesDraft 逐项比较本次写入的完整内容；失败仅返回固定字段类别，成功时原因为空，不输出实际值。
func (v albumRecoveryVersion) matchesDraft(draft operations.AlbumDraft) (map[int]string, string) {
	info := v.AlbumInfo
	for _, check := range []struct {
		reason   string
		mismatch bool
	}{
		{"album_id", v.AlbumID.String() != draft.AlbumID},
		{"client_key", draft.ClientKey == ""},
		{"review_status", v.ReviewStatus == nil || *v.ReviewStatus != 0},
		{"online_version", v.OnlineVersion == v.Version},
		{"title", info.Title != strings.TrimSpace(draft.Title)},
		{"language", info.Language != strings.TrimSpace(draft.Language)},
		{"description", info.Description != strings.TrimSpace(draft.Description)},
		{"year", info.Year != draft.Year},
		{"album_status", info.AlbumStatus != draft.AlbumStatus},
		{"drama_type", info.DramaType != draft.DramaType},
		{"seq_num", info.SequenceCount != len(draft.Episodes)},
		{"cover_list", !singleAlbumCover(info.CoverList, draft.CoverID)},
		{"tag_list", !sameAlbumTags(info.TagList, draft.TagIDs)},
	} {
		if check.mismatch {
			return nil, check.reason
		}
	}
	// 版本查询必须逐集确认冻结内容。旧映射只能提供标识，不能证明缺失分集的 VID、标题或封面已写入。
	if len(v.Episodes) != len(draft.Episodes) {
		return nil, "episode_count"
	}
	expected := make(map[int]operations.AlbumEpisodeDraft, len(draft.Episodes))
	mapping := make(map[int]string, len(draft.Episodes))
	for _, episode := range draft.Episodes {
		if episode.Sequence <= 0 || expected[episode.Sequence].Sequence != 0 {
			return nil, "draft_sequence"
		}
		expected[episode.Sequence] = episode
	}
	returned := make(map[int]bool, len(v.Episodes))
	for _, episode := range v.Episodes {
		want, exists := expected[episode.Sequence]
		if !exists || returned[episode.Sequence] {
			return nil, "episode_sequence"
		}
		if episode.VID != strings.TrimSpace(want.BytePlusVID) {
			return nil, "episode_vid"
		}
		if episode.Title != strings.TrimSpace(want.Title) {
			return nil, "episode_title"
		}
		if !singleAlbumCover(episode.CoverList, want.CoverID) {
			return nil, "episode_cover"
		}
		if _, err := numericID(episode.EpisodeID.String()); err != nil {
			return nil, "episode_id"
		}
		returned[episode.Sequence] = true
		mapping[episode.Sequence] = episode.EpisodeID.String()
	}
	if !validEpisodeMapping(draft.Episodes, mapping) {
		return nil, "episode_mapping"
	}
	return mapping, ""
}

func singleAlbumCover(actual []string, expected string) bool {
	return len(actual) == 1 && actual[0] == strings.TrimSpace(expected) && actual[0] != ""
}

func sameAlbumTags(actual []stringID, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	counts := make(map[string]int, len(expected))
	for _, tag := range expected {
		counts[strings.TrimSpace(tag)]++
	}
	for _, tag := range actual {
		if counts[tag.String()] == 0 {
			return false
		}
		counts[tag.String()]--
	}
	return true
}
