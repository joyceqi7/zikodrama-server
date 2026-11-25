package tiktokmedia

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"drama-server/internal/biz/operations"
)

// RegisterVideo 使用官方已存在 VID 登记能力，不下载或二次上传视频。
func (c *Client) RegisterVideo(ctx context.Context, input operations.TikTokVideoInput) (operations.TikTokVideoRegistration, error) {
	diagnostic := platformCallDiagnostic{method: http.MethodPost, path: "/v2/sg/shortdrama/video"}
	if input.VID == "" || input.Title == "" || input.SpaceName == "" || input.BytePlusAccountID == "" {
		err := platformError("invalid_video_input", false)
		diagnostic.logFailure(ctx, err)
		return operations.TikTokVideoRegistration{}, err
	}
	request := map[string]string{"client_key": c.clientKey, "vid": input.VID, "title": input.Title,
		"space_name": input.SpaceName, "byteplus_account_id": input.BytePlusAccountID}
	if input.Region != "" {
		request["byteplus_region"] = input.Region
	}
	var result struct {
		// ResultType 是平台登记结果；指针用于区分缺失、空值与明确的失败枚举。
		ResultType *int `json:"result_type"`
		// JobID 是平台返回的异步登记任务标识。
		JobID string `json:"job_id"`
		// VID 是平台返回的视频标识，用于核对输入身份。
		VID string `json:"byteplus_vid"`
	}
	if err := c.doJSONWithDiagnostic(ctx, diagnostic.method, diagnostic.path, nil, request, &result, nil, &diagnostic); err != nil {
		return operations.TikTokVideoRegistration{}, err
	}
	if result.ResultType != nil && *result.ResultType == 3 {
		err := platformError("video_registration_rejected", false)
		diagnostic.logFailure(ctx, err)
		return operations.TikTokVideoRegistration{}, err
	}
	if result.VID != "" && result.VID != input.VID {
		err := platformError("video_identity_mismatch", false)
		diagnostic.logFailure(ctx, err)
		return operations.TikTokVideoRegistration{}, err
	}
	// 已存在 VID 的同步登记实测只返回 byteplus_vid。仅接受相同 VID 且结果字段确实缺失的形态；
	// 空值、非法枚举或缺少视频身份仍按未知结果处理，成功受理后依然必须查询确认。
	vidOnlyResponse := result.ResultType == nil && diagnostic.registration != nil &&
		diagnostic.registration.resultTypeState == "missing" && result.VID == input.VID
	if !vidOnlyResponse && (result.ResultType == nil || *result.ResultType < 1 || *result.ResultType > 3) {
		err := &operations.PlatformCallError{Code: "invalid_video_response", Retryable: true, OutcomeUnknown: true}
		diagnostic.logFailure(ctx, err)
		return operations.TikTokVideoRegistration{}, err
	}
	// 即便同步登记成功也必须再查询 VID，不能把 POST 受理当成送审就绪。
	return operations.TikTokVideoRegistration{JobID: strings.TrimSpace(result.JobID), VID: input.VID}, nil
}

// QueryVideo 查询登记事实；异步任务优先使用 job_id，已存在的视频可以按 vid 查询。
func (c *Client) QueryVideo(ctx context.Context, jobID, vid, accountID string) (operations.TikTokVideoObservation, error) {
	diagnostic := platformCallDiagnostic{method: http.MethodGet, path: "/v2/sg/shortdrama/video"}
	if accountID == "" || (jobID == "" && vid == "") {
		err := platformError("invalid_video_query", false)
		diagnostic.logFailure(ctx, err)
		return operations.TikTokVideoObservation{}, err
	}
	query := url.Values{"client_key": {c.clientKey}, "byteplus_account_id": {accountID}}
	if jobID != "" {
		query.Set("job_id", jobID)
	} else {
		query.Set("vid", vid)
	}
	var result struct {
		// Status 是官方查询状态，缺失或空值不能视作成功。
		Status *int `json:"upload_status"`
		// VID 是查询确认的视频标识，成功时必须存在。
		VID string `json:"vid"`
	}
	if err := c.doJSONWithDiagnostic(ctx, diagnostic.method, diagnostic.path, query, nil, &result, nil, &diagnostic); err != nil {
		return operations.TikTokVideoObservation{}, err
	}
	diagnostic.uploadStatus = result.Status
	if result.Status == nil || *result.Status < 1 || *result.Status > 3 || (*result.Status == 2 && strings.TrimSpace(result.VID) == "") {
		err := platformError("invalid_video_response", true)
		diagnostic.logFailure(ctx, err)
		return operations.TikTokVideoObservation{}, err
	}
	return operations.TikTokVideoObservation{Status: *result.Status, VID: strings.TrimSpace(result.VID)}, nil
}

// StartTranscode 使用 TikTok 默认模板，由官方在 BytePlus 完成转码和回调登记。
func (c *Client) StartTranscode(ctx context.Context, input operations.TikTokTranscodeInput) (string, error) {
	if input.IdempotencyKey == "" || strings.ContainsAny(input.IdempotencyKey, "\r\n") || input.SpaceName == "" || input.BytePlusAccountID == "" || len(input.VIDs) == 0 || len(input.VIDs) > 1000 {
		return "", platformError("invalid_transcode_input", false)
	}
	seen := map[string]bool{}
	for _, vid := range input.VIDs {
		if strings.TrimSpace(vid) == "" || seen[vid] {
			return "", platformError("invalid_transcode_input", false)
		}
		seen[vid] = true
	}
	request := map[string]any{"byteplus_account_id": input.BytePlusAccountID, "space_name": input.SpaceName,
		"vids": input.VIDs, "template_id": "default_0", "template_type": 0}
	var result struct {
		TaskID string `json:"task_id"`
	}
	err := c.doJSONWithHeaders(ctx, http.MethodPost, "/v2/sg/shortdrama/video/transcode/batch/", nil, request, &result,
		http.Header{"Idempotency-Key": {input.IdempotencyKey}})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(result.TaskID) == "" {
		return "", platformError("invalid_transcode_response", true)
	}
	return strings.TrimSpace(result.TaskID), nil
}

// QueryTranscode 验证任务身份和计数，保留逐视频结果供发布流程进行完整集合校验。
func (c *Client) QueryTranscode(ctx context.Context, taskID, accountID string) (operations.TikTokTranscodeObservation, error) {
	if taskID == "" || accountID == "" {
		return operations.TikTokTranscodeObservation{}, platformError("invalid_transcode_query", false)
	}
	var result struct {
		TaskID       string          `json:"task_id"`
		Status       string          `json:"task_status"`
		Total        int             `json:"total_count"`
		SuccessCount int             `json:"success_count"`
		FailedCount  int             `json:"failed_count"`
		PendingCount int             `json:"pending_count"`
		Success      []transcodeItem `json:"success_list"`
		Failed       []transcodeItem `json:"failed_list"`
		Pending      []transcodeItem `json:"pending_list"`
	}
	query := url.Values{"task_id": {taskID}, "byteplus_account_id": {accountID}}
	if err := c.doJSON(ctx, http.MethodGet, "/v2/sg/shortdrama/video/transcode/batch/", query, nil, &result); err != nil {
		return operations.TikTokTranscodeObservation{}, err
	}
	if result.TaskID != taskID || result.Total <= 0 || result.SuccessCount != len(result.Success) ||
		result.FailedCount != len(result.Failed) || result.PendingCount != len(result.Pending) ||
		result.SuccessCount+result.FailedCount+result.PendingCount != result.Total {
		return operations.TikTokTranscodeObservation{}, platformError("invalid_transcode_response", true)
	}
	return operations.TikTokTranscodeObservation{TaskID: result.TaskID, Status: result.Status, TotalCount: result.Total,
		SuccessVIDs: transcodeVIDs(result.Success), FailedVIDs: transcodeVIDs(result.Failed), PendingVIDs: transcodeVIDs(result.Pending)}, nil
}

// GenerateMinisLink 只返回官方生成的链接，任何平台失败都不得降级为自行拼接 URL。
func (c *Client) GenerateMinisLink(ctx context.Context, input operations.MinisLinkInput) (string, error) {
	path, err := url.Parse(input.Path)
	if err != nil || !strings.HasPrefix(input.Path, "/") || strings.HasPrefix(input.Path, "//") || path.IsAbs() || path.Host != "" {
		return "", platformError("invalid_minis_path", false)
	}
	request := map[string]string{"minis_path": input.Path}
	if input.Title != "" {
		request["title"] = input.Title
	}
	if input.ImageURL != "" {
		request["image_url"] = input.ImageURL
	}
	var result struct {
		Link string `json:"minis_link"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v2/minis/link/generate/", nil, request, &result); err != nil {
		return "", err
	}
	link, err := url.Parse(result.Link)
	if err != nil || link.Scheme != "https" || link.Host != "www.tiktok.com" || !strings.HasPrefix(link.Path, "/minis/") || link.User != nil {
		return "", platformError("invalid_minis_link_response", false)
	}
	return result.Link, nil
}

// transcodeItem 只提取官方视频标识，不保存媒体下载地址和原始异常。
type transcodeItem struct {
	VID string `json:"vid"`
}

// transcodeVIDs 将平台结果归一化为字符串标识集合。
func transcodeVIDs(items []transcodeItem) []string {
	vids := make([]string, 0, len(items))
	for _, item := range items {
		vids = append(vids, item.VID)
	}
	return vids
}
