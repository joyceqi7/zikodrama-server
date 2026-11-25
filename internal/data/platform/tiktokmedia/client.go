// Package tiktokmedia 封装 TikTok Media Asset Management 官方 Open API。
package tiktokmedia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"drama-server/internal/biz/operations"
)

const defaultBaseURL = "https://open.tiktokapis.com"

// Client 使用一个 TikTok Minis 应用的 client_credentials 调用官方媒资接口。
type Client struct {
	clientKey    string
	clientSecret string
	baseURL      string
	httpClient   *http.Client
	tokens       tokenCache
}

// NewClient 创建按应用隔离的官方媒资客户端，复用应用 Access Token。
func NewClient(clientKey, clientSecret string, httpClient *http.Client) *Client {
	return &Client{
		clientKey: strings.TrimSpace(clientKey), clientSecret: strings.TrimSpace(clientSecret),
		baseURL: defaultBaseURL, httpClient: httpClient,
	}
}

// CreateImage 通过官方 Image API 把公开封面转换为 open_pic_id。
func (c *Client) CreateImage(ctx context.Context, imageURL string) (string, error) {
	var result struct {
		OpenPictureID stringID `json:"open_pic_id"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v2/sg/shortdrama/image", nil,
		map[string]string{"image_url": strings.TrimSpace(imageURL)}, &result); err != nil {
		return "", err
	}
	if result.OpenPictureID.String() == "" {
		return "", platformError("invalid_platform_response", false)
	}
	return result.OpenPictureID.String(), nil
}

// CreateAlbum 调用官方空 Album 创建接口并保留数字标识原文。
func (c *Client) CreateAlbum(ctx context.Context) (operations.CreateAlbumResult, error) {
	var result struct {
		AlbumID stringID `json:"album_id"`
		Version int      `json:"version"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v2/sg/shortdrama/album/create/", nil, struct{}{}, &result); err != nil {
		return operations.CreateAlbumResult{}, err
	}
	// 官方最小成功响应只有 album_id；首次 Update 时不发送不存在的版本号。
	if result.AlbumID.String() == "" {
		return operations.CreateAlbumResult{}, platformError("album_create_rejected", false)
	}
	return operations.CreateAlbumResult{AlbumID: result.AlbumID.String(), Version: result.Version}, nil
}

// UpdateAlbum 写入完整剧目版本并规范化官方 episode_id_map；返回结果不完整时只查询并核实已写入内容，不重复更新。
func (c *Client) UpdateAlbum(ctx context.Context, draft operations.AlbumDraft) (operations.UpdateAlbumResult, error) {
	language := strings.TrimSpace(draft.Language)
	if language == "" {
		return operations.UpdateAlbumResult{}, platformError("invalid_album_language", false)
	}
	albumID, err := numericID(draft.AlbumID)
	if err != nil {
		return operations.UpdateAlbumResult{}, err
	}
	tagIDs := make([]json.Number, 0, len(draft.TagIDs))
	for _, tagID := range draft.TagIDs {
		number, numberErr := numericID(tagID)
		if numberErr != nil {
			return operations.UpdateAlbumResult{}, platformError("invalid_tag_id", false)
		}
		tagIDs = append(tagIDs, number)
	}
	episodes := make([]albumEpisodeRequest, 0, len(draft.Episodes))
	for _, episode := range draft.Episodes {
		episodes = append(episodes, albumEpisodeRequest{
			EpisodeID: strings.TrimSpace(episode.PlatformEpisodeID), Title: strings.TrimSpace(episode.Title),
			Sequence: episode.Sequence, CoverList: []string{strings.TrimSpace(episode.CoverID)},
			BytePlusVID: strings.TrimSpace(episode.BytePlusVID),
		})
	}
	request := albumUpdateRequest{
		ClientKey: c.clientKey, AlbumID: albumID, OperationType: 1, Version: draft.Version,
		AlbumInfo: albumInfoRequest{
			Language: language, Title: strings.TrimSpace(draft.Title), SequenceCount: len(draft.Episodes),
			CoverList: []string{strings.TrimSpace(draft.CoverID)}, Year: draft.Year, AlbumStatus: draft.AlbumStatus,
			Description: strings.TrimSpace(draft.Description), DramaType: draft.DramaType, TagList: tagIDs,
		},
		EpisodeInfoList: episodes,
	}
	var result struct {
		Version      int                 `json:"version"`
		EpisodeIDMap map[string]stringID `json:"episode_id_map"`
	}
	var diagnostic platformCallDiagnostic
	if err := c.doJSONWithDiagnostic(ctx, http.MethodPost, "/v2/sg/shortdrama/album/update/", nil, request, &result, nil, &diagnostic); err != nil {
		var platformErr *operations.PlatformCallError
		if errors.As(err, &platformErr) && platformErr.OutcomeUnknown {
			resolved, resolveErr := c.ResolveAlbumUpdate(ctx, draft)
			if resolveErr != nil {
				return operations.UpdateAlbumResult{}, fmt.Errorf("TikTok album update failed (%v): %w", err, resolveErr)
			}
			return resolved, nil
		}
		return operations.UpdateAlbumResult{}, err
	}
	mapping, recognized := normalizeEpisodeMapping(draft.Episodes, result.EpisodeIDMap)
	if result.Version <= draft.Version || !recognized || !validEpisodeMapping(draft.Episodes, mapping) {
		// 白名单摘要保留异常的具体位置；映射键和值及完整响应不进入日志。
		slog.WarnContext(ctx, "TikTok album update response incomplete", "error", platformError("invalid_episode_mapping", false),
			"album_id", draft.AlbumID, "base_version", draft.Version, "response_version", result.Version,
			"expected_episode_count", len(draft.Episodes), "response_mapping_count", len(result.EpisodeIDMap),
			"resolved_mapping_count", len(mapping), "http_status", diagnostic.httpStatus,
			"episode_mapping_key_counts", episodeMappingKeyCounts(draft.Episodes, result.EpisodeIDMap),
			"response_payload", diagnostic.albumPayload, "root_field_types", diagnostic.rootFieldTypes, "data_field_types", diagnostic.dataFieldTypes,
			"platform_request_id", diagnostic.clean(diagnostic.requestID))
		return c.ResolveAlbumUpdate(ctx, draft)
	}
	return operations.UpdateAlbumResult{Version: result.Version, EpisodeIDsBySequence: mapping}, nil
}

// SubmitReview 提交当前 Album 版本，并把 review_id 原文作为字符串返回。
func (c *Client) SubmitReview(ctx context.Context, albumID string, version, priorityScore int) (string, error) {
	number, err := numericID(albumID)
	if err != nil {
		return "", err
	}
	var result struct {
		ReviewID stringID `json:"review_id"`
	}
	request := map[string]any{"album_id": number, "version": version, "priority_score": priorityScore}
	if err := c.doJSON(ctx, http.MethodPost, "/v2/sg/shortdrama/album/review/submit/", nil, request, &result); err != nil {
		return "", err
	}
	if result.ReviewID.String() == "" {
		return "", platformError("review_submit_rejected", false)
	}
	return result.ReviewID.String(), nil
}

// QueryAlbum 通过官方 Album Query 获取审核、上下架和线上版本状态。
func (c *Client) QueryAlbum(ctx context.Context, albumID string, version int) (operations.AlbumObservation, error) {
	if _, err := numericID(albumID); err != nil {
		return operations.AlbumObservation{}, err
	}
	query := url.Values{
		"client_key": []string{c.clientKey}, "album_id": []string{albumID},
		"version": []string{strconv.Itoa(version)},
	}
	var result albumQueryResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v2/sg/shortdrama/album/query/", query, nil, &result); err != nil {
		return operations.AlbumObservation{}, err
	}
	for _, candidate := range result.Versions {
		if candidate.Version == version {
			items := map[string]int{}
			if candidate.AlbumInfo.ReviewResult != nil {
				items[albumID] = candidate.AlbumInfo.ReviewResult.Status
			}
			for _, episode := range candidate.Episodes {
				if episode.ReviewResult != nil {
					items[string(episode.EpisodeID)] = episode.ReviewResult.Status
				}
			}
			return operations.AlbumObservation{
				ReviewItems: items, ReviewStatus: candidate.ReviewStatus, PublishStatus: candidate.PublishStatus,
				OnlineVersion: candidate.OnlineVersion, ReviewFailReasons: candidate.ReviewFailReasons,
			}, nil
		}
	}
	return operations.AlbumObservation{}, platformError("album_version_not_found", true)
}

// AuthorizeAlbum 批量授权目标 Client Key，并逐项检查官方结果。
func (c *Client) AuthorizeAlbum(ctx context.Context, albumID string, clientKeys []string) error {
	return c.setAlbumAuthorization(ctx, albumID, clientKeys, 1)
}

// RevokeAlbum 调用官方移除授权能力，逐项确认已移除状态。
func (c *Client) RevokeAlbum(ctx context.Context, albumID string, clientKeys []string) error {
	return c.setAlbumAuthorization(ctx, albumID, clientKeys, 2)
}

// setAlbumAuthorization 在发送前校验目标，按官方逐项结果确认授权；失败记录脱敏诊断以区分缺项和平台拒绝。
func (c *Client) setAlbumAuthorization(ctx context.Context, albumID string, clientKeys []string, operation int) (callErr error) {
	var diagnostic platformCallDiagnostic
	// 传输失败由统一调用层记录；这里只记录发送前校验和业务结果校验失败。
	logResult := true
	defer func() {
		if logResult {
			diagnostic.logFailure(ctx, callErr)
		}
	}()
	diagnostic.method, diagnostic.path = http.MethodPost, "/v2/sg/shortdrama/album/authorize/"
	expected := make(map[string]bool, len(clientKeys))
	if len(clientKeys) == 0 {
		return platformError("invalid_authorization_targets", false)
	}
	for _, key := range clientKeys {
		if strings.TrimSpace(key) == "" || expected[key] {
			return platformError("invalid_authorization_targets", false)
		}
		expected[key] = true
	}
	number, err := numericID(albumID)
	if err != nil {
		return err
	}
	request := map[string]any{"album_id": number, "operate_type": operation, "target_client_key_list": clientKeys}
	var result authorizationResponse
	if err := c.doJSONWithDiagnostic(ctx, http.MethodPost, "/v2/sg/shortdrama/album/authorize/", nil, request, &result, nil, &diagnostic); err != nil {
		logResult = false
		return err
	}
	detail := &authorizationDiagnostic{expectedCount: len(clientKeys), resultCount: len(result.Items), operation: operation}
	diagnostic.authorization = detail
	for _, item := range result.Items {
		if !expected[item.ClientKey] {
			detail.unexpectedCount++
			continue
		}
		delete(expected, item.ClientKey)
		detail.matchedCount++
		if item.AuthStatus != operation {
			detail.rejectedCount++
			// 仅保留首个拒绝项的官方错误，不输出目标 Client Key 或完整对象。
			if detail.rejectedCount == 1 {
				diagnostic.code = strconv.Itoa(item.ErrorCode)
				diagnostic.message = item.ErrorMessage
			}
		}
	}
	if len(result.Items) != len(clientKeys) || len(expected) != 0 || detail.unexpectedCount != 0 {
		return platformError("authorization_result_incomplete", true)
	}
	if detail.rejectedCount != 0 {
		return platformError("authorization_rejected", false)
	}
	return nil
}

// SetOnlineVersion 把已审核版本设为官方线上版本。
func (c *Client) SetOnlineVersion(ctx context.Context, albumID string, version int) error {
	number, err := numericID(albumID)
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodPost, "/v2/sg/shortdrama/album/online_version/", nil,
		map[string]any{"album_id": number, "version": version}, &struct{}{})
}

// SetAlbumListed 通过官方状态接口上架或下架剧目；明确的已上架响应视为上架成功，其他拒绝仍返回错误。
// 本方法只确认 Album 上下架状态，不选择或核对线上版本。
func (c *Client) SetAlbumListed(ctx context.Context, albumID string, listed bool) error {
	number, err := numericID(albumID)
	if err != nil {
		return err
	}
	status := 2
	if listed {
		status = 1
	}
	return c.doJSON(ctx, http.MethodPost, "/v2/sg/shortdrama/album/status/", nil,
		map[string]any{"client_key": c.clientKey, "album_id": number, "status": status}, &struct{}{})
}

type albumInfoRequest struct {
	Language      string        `json:"language"`
	Title         string        `json:"title"`
	SequenceCount int           `json:"seq_num"`
	CoverList     []string      `json:"cover_list"`
	Year          int           `json:"year"`
	AlbumStatus   int           `json:"album_status"`
	Description   string        `json:"desp"`
	DramaType     int           `json:"drama_type"`
	TagList       []json.Number `json:"tag_list"`
}

type albumEpisodeRequest struct {
	EpisodeID   string   `json:"episode_id,omitempty"`
	Title       string   `json:"title"`
	Sequence    int      `json:"seq"`
	CoverList   []string `json:"cover_list"`
	BytePlusVID string   `json:"byteplus_vid"`
}

type albumUpdateRequest struct {
	ClientKey       string                `json:"client_key"`
	AlbumID         json.Number           `json:"album_id"`
	OperationType   int                   `json:"operation_type"`
	Version         int                   `json:"version,omitempty"`
	AlbumInfo       albumInfoRequest      `json:"album_info"`
	EpisodeInfoList []albumEpisodeRequest `json:"episode_info_list"`
}

type albumQueryResponse struct {
	Versions []struct {
		AlbumInfo struct {
			ReviewResult *struct {
				Status int `json:"overall_review_status"`
			} `json:"review_result"`
		} `json:"album_info"`
		Episodes []struct {
			EpisodeID    json.Number `json:"episode_id"`
			ReviewResult *struct {
				Status int `json:"overall_review_status"`
			} `json:"review_result"`
		} `json:"episode_info_list"`

		Version           int      `json:"version"`
		OnlineVersion     int      `json:"online_version"`
		PublishStatus     int      `json:"publish_status"`
		ReviewStatus      int      `json:"review_status"`
		ReviewFailReasons []string `json:"review_fail_reasons"`
	} `json:"album_version_list"`
}

// authorizationResult 保存单个目标的官方授权结果；Client Key 只用于匹配，不写入诊断。
type authorizationResult struct {
	// ClientKey 是官方返回的目标应用标识，用于逐项核对。
	ClientKey string `json:"client_key"`
	// AuthStatus 为 1 时已授权，为 2 时已移除；缺失不能视为成功。
	AuthStatus int `json:"auth_status"`
	// ErrorCode 是单个目标的官方处理错误码。
	ErrorCode int `json:"error_code"`
	// ErrorMessage 仅用于脱敏诊断，不直接返回运营端。
	ErrorMessage string `json:"error_message"`
}

// authorizationResponse 同时支持官方文档与测试环境实际返回的结果字段。
type authorizationResponse struct {
	// Items 是两种字段名解析得到的目标结果列表。
	Items []authorizationResult
}

// UnmarshalJSON 兼容 client_key_result 与 client_key_result_list；不推断缺失状态或忽略冲突结果。
func (r *authorizationResponse) UnmarshalJSON(raw []byte) error {
	var payload struct {
		Actual     json.RawMessage `json:"client_key_result"`
		Documented json.RawMessage `json:"client_key_result_list"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	r.Items = nil
	var actual, documented []authorizationResult
	if len(payload.Actual) > 0 {
		if err := json.Unmarshal(payload.Actual, &actual); err != nil {
			return err
		}
	}
	if len(payload.Documented) > 0 {
		if err := json.Unmarshal(payload.Documented, &documented); err != nil {
			return err
		}
	}
	if len(payload.Actual) > 0 && len(payload.Documented) > 0 {
		// 两种名称同时出现时必须一致，避免选择其中一份掩盖失败。
		if len(actual) != len(documented) {
			return errors.New("conflicting authorization result fields")
		}
		for i := range actual {
			if actual[i] != documented[i] {
				return errors.New("conflicting authorization result fields")
			}
		}
	}
	if len(payload.Actual) > 0 {
		r.Items = actual
	} else {
		r.Items = documented
	}
	return nil
}
