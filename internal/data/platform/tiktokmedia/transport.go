package tiktokmedia

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"drama-server/internal/biz/operations"
)

const maxResponseSize = 2 << 20

type tokenCache struct {
	mu        sync.Mutex
	value     string
	expiresAt time.Time
}

// accessToken 获取并缓存官方两小时 client_credentials Token。
func (c *Client) accessToken(ctx context.Context) (value string, callErr error) {
	diagnostic := platformCallDiagnostic{method: http.MethodPost, path: "/v2/oauth/token/"}
	defer func() { diagnostic.logFailure(ctx, callErr) }()
	if c == nil || c.httpClient == nil || c.clientKey == "" || c.clientSecret == "" {
		return "", platformError("platform_not_configured", false)
	}
	diagnostic.secrets = []string{c.clientSecret}
	c.tokens.mu.Lock()
	defer c.tokens.mu.Unlock()
	if c.tokens.value != "" && time.Now().UTC().Add(time.Minute).Before(c.tokens.expiresAt) {
		return c.tokens.value, nil
	}
	form := url.Values{
		"client_key": []string{c.clientKey}, "client_secret": []string{c.clientSecret},
		"grant_type": []string{"client_credentials"},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.baseURL, "/")+"/v2/oauth/token/", strings.NewReader(form.Encode()))
	if err != nil {
		diagnostic.cause = err
		return "", platformError("invalid_request_contract", false)
	}
	request.Header.Set("content-type", "application/x-www-form-urlencoded")
	response, err := c.httpClient.Do(request)
	if err != nil {
		diagnostic.cause = err
		return "", platformError("network_error", true)
	}
	defer diagnostic.closeBody(ctx, response.Body)
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize))
	diagnostic.readResponse(response, raw)
	if err != nil {
		diagnostic.cause = err
		return "", platformError("response_read_error", true)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", httpStatusError(response.StatusCode)
	}
	payload := struct {
		AccessToken string   `json:"access_token"`
		ExpiresIn   intValue `json:"expires_in"`
		Data        *struct {
			AccessToken string   `json:"access_token"`
			ExpiresIn   intValue `json:"expires_in"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		diagnostic.cause = err
		return "", platformError("invalid_json_response", true)
	}
	if payload.Data != nil {
		if payload.AccessToken == "" {
			payload.AccessToken = payload.Data.AccessToken
		}
		if payload.ExpiresIn == 0 {
			payload.ExpiresIn = payload.Data.ExpiresIn
		}
	}
	// Token 响应不完整时仍可能带有凭据，只用于定点脱敏，不能写入诊断字段。
	diagnostic.secrets = append(diagnostic.secrets, payload.AccessToken)
	if strings.TrimSpace(payload.AccessToken) == "" || payload.ExpiresIn <= 0 {
		return "", platformError("invalid_token_response", true)
	}
	c.tokens.value = strings.TrimSpace(payload.AccessToken)
	c.tokens.expiresAt = time.Now().UTC().Add(time.Duration(payload.ExpiresIn) * time.Second)
	return c.tokens.value, nil
}

// doJSON 统一添加 Bearer Token、限制响应体；官方错误只记录脱敏诊断，不向调用方暴露正文。
func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, input, output any) error {
	return c.doJSONWithHeaders(ctx, method, path, query, input, output, nil)
}

// doJSONWithHeaders 只接收服务端构造的附加请求头，例如持久化的转码幂等键。
func (c *Client) doJSONWithHeaders(ctx context.Context, method, path string, query url.Values, input, output any, headers http.Header) error {
	var diagnostic platformCallDiagnostic
	return c.doJSONWithDiagnostic(ctx, method, path, query, input, output, headers, &diagnostic)
}

// doJSONWithDiagnostic 将本次调用的脱敏上下文保留给响应业务校验；传输失败仍只在此记录一次。
func (c *Client) doJSONWithDiagnostic(ctx context.Context, method, path string, query url.Values, input, output any, headers http.Header, diagnostic *platformCallDiagnostic) (callErr error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	// 获取 Token 的失败已在其调用边界记录，业务请求只记录自身失败，避免同处重复日志。
	*diagnostic = platformCallDiagnostic{method: method, path: path, secrets: []string{c.clientSecret, token}}
	defer func() { diagnostic.logFailure(ctx, callErr) }()
	endpoint := strings.TrimRight(c.baseURL, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var body io.Reader
	if input != nil {
		encoded, marshalErr := json.Marshal(input)
		if marshalErr != nil {
			diagnostic.cause = marshalErr
			return platformError("invalid_request_contract", false)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		diagnostic.cause = err
		return platformError("invalid_request_contract", false)
	}
	request.Header.Set("authorization", "Bearer "+token)
	// 同时传入当前应用的公开 client-key，保持官方调用的应用归属明确。
	request.Header.Set("client-key", c.clientKey)
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	if input != nil {
		request.Header.Set("content-type", "application/json")
	}
	// 从发送业务请求开始标记可能的未知结果；获取 Token 失败不会进入这个范围。
	defer func() {
		if platformErr, ok := callErr.(*operations.PlatformCallError); ok && method != http.MethodGet && platformErr.Retryable && platformErr.Code != "http_429" {
			platformErr.OutcomeUnknown = true
		}
	}()
	response, err := c.httpClient.Do(request)
	if err != nil {
		diagnostic.cause = err
		return platformError("network_error", true)
	}
	defer diagnostic.closeBody(ctx, response.Body)
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize))
	diagnostic.readResponse(response, raw)
	if err != nil {
		diagnostic.cause = err
		return platformError("response_read_error", true)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return httpStatusError(response.StatusCode)
	}
	if err := validateAPIResponse(raw); err != nil {
		// Album Update 的 22003 是过期版本冲突；人工恢复只能核对，不能切换到新版本号再次更新。
		if path == "/v2/sg/shortdrama/album/update/" && diagnostic.code == "22003" {
			return platformError("album_version_conflict", false)
		}
		// 上架接口会明确拒绝重复上架；此响应已经确认目标状态，不能再依赖可能滞后的查询判为故障。
		// 仅接受该接口、上架动作及已观测的错误码和完整文案，其他 4001、下架及 HTTP 故障仍失败。
		if request, ok := input.(map[string]any); ok && method == http.MethodPost &&
			path == "/v2/sg/shortdrama/album/status/" && request["status"] == 1 &&
			diagnostic.code == "4001" && diagnostic.message == "album is already online" {
			slog.InfoContext(ctx, "TikTok album listing already confirmed",
				"method", method, "path", path, "error", err,
				"platform_code", diagnostic.code, "platform_message", diagnostic.clean(diagnostic.message),
				"platform_request_id", diagnostic.clean(diagnostic.requestID))
			return nil
		}
		// 申诉接口的 22003 表示该项目已申诉，业务层应查询已有结果，不能再次提交。
		if path == "/v2/sg/shortdrama/album/review/appeal/" && diagnostic.code == "22003" {
			return platformError("appeal_already_submitted", false)
		}
		// 仅批量转码的 23002 表示幂等键冲突；保留其他 API 的通用拒绝分类。
		if path == "/v2/sg/shortdrama/video/transcode/batch/" && diagnostic.code == "23002" {
			return platformError("transcode_idempotency_conflict", false)
		}
		return err
	}
	if output == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var wrapper struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper.Data) > 0 && string(wrapper.Data) != "null" {
		diagnostic.readVideoRegistrationResult(wrapper.Data, "data")
		diagnostic.readAlbumPayload("data")
		if err := json.Unmarshal(wrapper.Data, output); err != nil {
			diagnostic.cause = err
			return platformError("invalid_json_response", true)
		}
		return nil
	}
	diagnostic.readVideoRegistrationResult(raw, "root")
	diagnostic.readAlbumPayload("root")
	if err := json.Unmarshal(raw, output); err != nil {
		diagnostic.cause = err
		return platformError("invalid_json_response", true)
	}
	return nil
}

func validateAPIResponse(raw []byte) error {
	var payload struct {
		Error        json.RawMessage `json:"error"`
		BaseResponse struct {
			StatusCode      int `json:"status_code"`
			StatusCodeCamel int `json:"StatusCode"`
		} `json:"BaseResp"`
		BaseResponseSnake struct {
			StatusCode      int `json:"status_code"`
			StatusCodeCamel int `json:"StatusCode"`
		} `json:"base_resp"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return platformError("invalid_json_response", true)
	}
	for _, code := range []int{payload.BaseResponse.StatusCode, payload.BaseResponseSnake.StatusCode, payload.BaseResponse.StatusCodeCamel, payload.BaseResponseSnake.StatusCodeCamel} {
		if code != 0 {
			return platformRejection(strconv.Itoa(code))
		}
	}
	if len(payload.Error) == 0 || string(payload.Error) == "null" {
		return nil
	}
	var platform struct {
		Code any `json:"code"`
	}
	if err := json.Unmarshal(payload.Error, &platform); err != nil {
		return platformError("api_rejected", false)
	}
	code := strings.TrimSpace(strings.ToLower(toString(platform.Code)))
	if code != "" && code != "0" && code != "ok" {
		return platformRejection(code)
	}
	return nil
}

// platformRejection 将官方额度错误映射成稳定业务分类，其他拒绝仍保留通用分类。
func platformRejection(code string) error {
	if code == "22018" {
		return platformError("urgent_review_quota_exceeded", false)
	}
	return platformError("api_rejected", false)
}

func httpStatusError(status int) error {
	retryable := status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
	return platformError("http_"+strconv.Itoa(status), retryable)
}

func platformError(code string, retryable bool) error {
	return &operations.PlatformCallError{Code: code, Retryable: retryable}
}

func numericID(value string) (json.Number, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", platformError("invalid_numeric_id", false)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return "", platformError("invalid_numeric_id", false)
		}
	}
	return json.Number(value), nil
}

type stringID string

// UnmarshalJSON 同时接受官方 JSON number 和 string，并保留十进制原文。
func (value *stringID) UnmarshalJSON(raw []byte) error {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "\"") {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
		*value = stringID(strings.TrimSpace(text))
		return nil
	}
	number, err := numericID(trimmed)
	if err != nil {
		return err
	}
	*value = stringID(number.String())
	return nil
}

func (value stringID) String() string { return string(value) }

type intValue int

// UnmarshalJSON 接受 expires_in 的数字或数字字符串两种官方响应形态。
func (value *intValue) UnmarshalJSON(raw []byte) error {
	text := strings.Trim(strings.TrimSpace(string(raw)), "\"")
	parsed, err := strconv.Atoi(text)
	if err != nil {
		return err
	}
	*value = intValue(parsed)
	return nil
}

// normalizeEpisodeMapping 按官方实际返回的 seq:序号 或既有分集标识合并映射；未知键或冲突结果不能被完整旧映射掩盖。
func normalizeEpisodeMapping(episodes []operations.AlbumEpisodeDraft, raw map[string]stringID) (map[int]string, bool) {
	existing := make(map[string]int, len(episodes))
	result := make(map[int]string, len(episodes))
	expected := make(map[int]bool, len(episodes))
	for _, episode := range episodes {
		expected[episode.Sequence] = true
		if episode.PlatformEpisodeID != "" {
			existing[episode.PlatformEpisodeID] = episode.Sequence
			// Album 更新可能只为新增或发生变化的分集返回映射，未出现在响应中的既有分集沿用原官方标识。
			result[episode.Sequence] = episode.PlatformEpisodeID
		}
	}
	valid := true
	returned := make(map[int]string, len(raw))
	for key, value := range raw {
		sequence := existing[key]
		if sequence == 0 && strings.HasPrefix(key, "seq:") {
			sequence, _ = strconv.Atoi(strings.TrimPrefix(key, "seq:"))
		}
		if sequence <= 0 || !expected[sequence] {
			valid = false
			continue
		}
		if _, err := numericID(value.String()); err != nil {
			valid = false
			continue
		}
		if previous, exists := returned[sequence]; exists && previous != value.String() {
			valid = false
			continue
		}
		returned[sequence] = value.String()
		result[sequence] = value.String()
	}
	return result, valid
}

// validEpisodeMapping 拒绝重复平台标识、缺失分集和无关序号，不以集合长度代替身份校验。
func validEpisodeMapping(episodes []operations.AlbumEpisodeDraft, mapping map[int]string) bool {
	if len(episodes) == 0 || len(episodes) != len(mapping) {
		return false
	}
	seen := make(map[string]bool, len(episodes))
	for _, episode := range episodes {
		id := mapping[episode.Sequence]
		if id == "" || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func toString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}
