package tiktokmedia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"drama-server/internal/biz/operations"
)

// platformCallDiagnostic 只在单次调用内保存诊断白名单和脱敏凭据，不保存请求或响应全文。
type platformCallDiagnostic struct {
	method, path             string
	httpStatus               int
	code, message, requestID string
	cause                    error
	secrets                  []string
	registration             *videoRegistrationDiagnostic
	authorization            *authorizationDiagnostic
	rootFieldTypes           map[string]string
	dataFieldTypes           map[string]string
	uploadStatus             *int
	authorizationShape       []responseShapeField
	albumPayload             string
}

// authorizationDiagnostic 记录授权结果的计数和操作类型，不包含 Client Key 或响应全文。
type authorizationDiagnostic struct {
	expectedCount, resultCount, matchedCount, unexpectedCount, rejectedCount, operation int
}

// videoRegistrationDiagnostic 只记录解析位置和枚举字段形态，不保留任务、视频或响应全文。
type videoRegistrationDiagnostic struct {
	payload, resultTypeState string
	resultType               int
}

// readVideoRegistrationResult 使用业务解码实际选中的载荷，区分字段缺失、空值、整数及非法类型。
func (d *platformCallDiagnostic) readVideoRegistrationResult(raw []byte, payload string) {
	if d.method != http.MethodPost || d.path != "/v2/sg/shortdrama/video" {
		return
	}
	object := diagnosticObject(raw)
	result := &videoRegistrationDiagnostic{payload: payload, resultTypeState: "missing"}
	d.registration = result
	// 只识别 result_type 并兼容大小写形式，避免输出其他响应字段。
	value, present := object["result_type"]
	if !present {
		for key, candidate := range object {
			if strings.EqualFold(key, "result_type") {
				value, present = candidate, true
				break
			}
		}
	}
	if present {
		var number *int
		switch err := json.Unmarshal(value, &number); {
		case err != nil:
			result.resultTypeState = "invalid"
		case number == nil:
			result.resultTypeState = "null"
		default:
			result.resultTypeState = "integer"
			result.resultType = *number
		}
	}
	// 业务拒绝的说明可能位于 data；仅在外层没有说明时提取同名白名单字段。
	// 使用实际选中的载荷补齐脱敏集合，兼容外层 Data 字段的大小写形式。
	for _, key := range []string{"access_token", "refresh_token", "client_secret"} {
		d.secrets = append(d.secrets, diagnosticScalar(object, key))
	}
	if d.message == "" {
		d.message = diagnosticScalar(object, "message", "error_description")
	}
}

// readResponse 提取官方错误及请求标识；标识符使用 JSON 原文，避免大整数精度丢失。
func (d *platformCallDiagnostic) readResponse(response *http.Response, raw []byte) {
	d.httpStatus = response.StatusCode
	d.requestID = response.Header.Get("X-Tt-Logid")
	if d.requestID == "" {
		d.requestID = response.Header.Get("X-Request-Id")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		d.cause = err
		return
	}
	if d.path == "/v2/sg/shortdrama/video" || d.path == "/v2/sg/shortdrama/album/authorize/" ||
		d.path == "/v2/sg/shortdrama/album/update/" || d.path == "/v2/sg/shortdrama/album/query/" {
		d.rootFieldTypes = diagnosticFieldTypes(root)
		var wrapper struct {
			// Data 与业务解码使用同一标签，兼容大小写形式。
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &wrapper) == nil {
			d.dataFieldTypes = diagnosticFieldTypes(diagnosticObject(wrapper.Data))
		}
	}
	if id := diagnosticScalar(root, "log_id", "request_id"); id != "" {
		d.requestID = id
	}
	// 失败响应偶尔也携带 Token；只把它们加入脱敏集合，不作为日志内容。
	data := diagnosticObject(root["data"])
	for _, object := range []map[string]json.RawMessage{root, data} {
		for _, key := range []string{"access_token", "refresh_token", "client_secret"} {
			d.secrets = append(d.secrets, diagnosticScalar(object, key))
		}
	}
	if d.path == "/v2/sg/shortdrama/album/authorize/" {
		d.authorizationShape = d.responseShape(raw)
	}
	// BaseResp 的失败优先级与现有分类一致，兼容两种命名方式而不改变成功判定。
	for _, key := range []string{"BaseResp", "base_resp"} {
		object := diagnosticObject(root[key])
		code := diagnosticScalar(object, "status_code", "StatusCode")
		if code != "" && code != "0" {
			d.code = code
			d.message = diagnosticScalar(object, "status_message", "StatusMessage", "status_msg", "message")
			if id := diagnosticScalar(object, "log_id", "request_id", "LogId"); id != "" {
				d.requestID = id
			}
			return
		}
	}
	platform := diagnosticObject(root["error"])
	d.code = diagnosticScalar(platform, "code")
	d.message = diagnosticScalar(platform, "message", "description")
	if id := diagnosticScalar(platform, "log_id", "request_id"); id != "" {
		d.requestID = id
	}
	if d.code == "" {
		// OAuth 使用字符串 error 和 error_description；不输出其他响应字段。
		d.code = diagnosticScalar(root, "error", "code")
	}
	if d.message == "" {
		d.message = diagnosticScalar(root, "error_description", "message")
	}
}

// logFailure 在适配层记录一次诊断；分类、重试和向业务层返回的错误保持原样。
func (d *platformCallDiagnostic) logFailure(ctx context.Context, callErr error) {
	if callErr == nil {
		return
	}
	level := slog.LevelError
	var platformErr *operations.PlatformCallError
	if errors.As(callErr, &platformErr) && (platformErr.Code == "api_rejected" || platformErr.Code == "urgent_review_quota_exceeded" || platformErr.Code == "http_429" ||
		platformErr.Code == "appeal_already_submitted" || platformErr.Code == "authorization_rejected" || platformErr.Code == "invalid_authorization_targets" ||
		platformErr.Code == "invalid_video_input" || platformErr.Code == "invalid_video_query" ||
		(platformErr.Code == "video_registration_rejected" && d.registration != nil && d.registration.resultTypeState == "integer" && d.registration.resultType == 3) ||
		(d.httpStatus >= 400 && d.httpStatus < 500)) {
		level = slog.LevelWarn
	}
	if errors.Is(d.cause, context.Canceled) {
		level = slog.LevelInfo
	}
	cause := callErr
	if d.cause != nil {
		cause = d.cause
	}
	fields := []any{"method", d.method, "path", d.path, "error", d.clean(cause.Error())}
	if platformErr != nil {
		fields = append(fields, "error_code", platformErr.Code)
	}
	if d.httpStatus != 0 {
		fields = append(fields, "http_status", d.httpStatus)
	}
	if result := d.registration; result != nil {
		fields = append(fields, "response_payload", result.payload, "result_type_present", result.resultTypeState != "missing", "result_type_state", result.resultTypeState)
		if result.resultTypeState == "integer" {
			fields = append(fields, "result_type", result.resultType)
		}
	}
	if d.rootFieldTypes != nil {
		fields = append(fields, "root_field_types", d.rootFieldTypes, "data_field_types", d.dataFieldTypes)
	}
	if d.albumPayload != "" {
		fields = append(fields, "response_payload", d.albumPayload)
	}
	if len(d.authorizationShape) > 0 {
		fields = append(fields, "authorization_response_shape", d.authorizationShape)
	}
	if a := d.authorization; a != nil {
		fields = append(fields, "authorization_operation", a.operation, "expected_target_count", a.expectedCount, "response_result_count", a.resultCount, "matched_target_count", a.matchedCount, "unexpected_target_count", a.unexpectedCount, "rejected_target_count", a.rejectedCount)
	}
	if d.uploadStatus != nil {
		fields = append(fields, "upload_status", *d.uploadStatus)
	}
	for _, field := range []struct{ key, value string }{
		{"platform_code", d.code}, {"platform_message", d.message}, {"platform_request_id", d.requestID},
	} {
		if field.value != "" {
			fields = append(fields, field.key, d.clean(field.value))
		}
	}
	slog.Log(ctx, level, "TikTok API request failed", fields...)
}

// diagnosticFieldTypes 仅输出已知协议字段的 JSON 类型；其他字段只计数，名称和值都不进入日志。
func diagnosticFieldTypes(object map[string]json.RawMessage) map[string]string {
	fields := map[string]string{}
	known := 0
	for _, key := range []string{"client_key_result", "client_key_result_list", "data", "result_type", "job_id", "byteplus_vid", "vid", "upload_status", "error", "code", "message", "description", "error_code", "error_message", "BaseResp", "base_resp", "status_code", "status_message", "result", "video", "video_info", "version", "episode_id_map", "album_version_list"} {
		value, exists := object[key]
		// 与结构体 JSON 解码的字段大小写匹配一致；只输出白名单中的规范名称。
		if !exists {
			for actual, candidate := range object {
				if strings.EqualFold(actual, key) {
					value, exists = candidate, true
					break
				}
			}
		}
		if !exists {
			continue
		}
		known++
		value = bytes.TrimSpace(value)
		kind := "invalid"
		if len(value) > 0 {
			switch value[0] {
			case '{':
				kind = "object"
			case '[':
				kind = "array"
			case '"':
				kind = "string"
			case 'n':
				kind = "null"
			case 't', 'f':
				kind = "boolean"
			default:
				kind = "number"
			}
		}
		fields[key] = kind
	}
	if unknown := len(object) - known; unknown > 0 {
		fields["unknown_field_count"] = strconv.Itoa(unknown)
	}
	return fields
}

// closeBody 记录关闭响应流的实际错误，不改变已经取得的业务结果或重试语义。
func (d *platformCallDiagnostic) closeBody(ctx context.Context, body io.Closer) {
	if err := body.Close(); err != nil {
		slog.WarnContext(ctx, "close TikTok API response failed", "method", d.method, "path", d.path, "error", d.clean(err.Error()))
	}
}

// diagnosticObject 仅用对象查找白名单字段，非对象的错误形态由标量提取处理。
func diagnosticObject(raw json.RawMessage) map[string]json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return nil
	}
	return object
}

// diagnosticScalar 只接受字符串和 JSON 数字，拒绝把嵌套账号、请求或响应对象写入日志。
func diagnosticScalar(object map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw := object[key]
		var value string
		if json.Unmarshal(raw, &value) == nil && value != "" {
			return value
		}
		var number json.Number
		if json.Unmarshal(raw, &number) == nil && number.String() != "" {
			return number.String()
		}
	}
	return ""
}

var (
	diagnosticURL        = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*://|https?%3a%2f%2f)[^\s<>"']+`)
	diagnosticBearer     = regexp.MustCompile(`(?i)\bBearer\s+[^\s,;"']+`)
	diagnosticCookie     = regexp.MustCompile(`(?i)(\b(?:set-cookie|cookie)["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\r\n]*)`)
	diagnosticCredential = regexp.MustCompile(`(?i)(\b(?:access[_-]?token|refresh[_-]?token|client[_-]?secret|authorization|password(?:[_-]?(?:hash|digest))?|token|secret|signature|sign|api[_-]?key|access[_-]?key(?:[_-]?id)?|secret[_-]?key|connection[_-]?string|dsn)["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;]+)`)
)

// clean 定点移除当前凭据、媒体地址和显式敏感字段，保留错误其余内容；脱敏后再限制长度。
func (d *platformCallDiagnostic) clean(value string) string {
	for _, secret := range d.secrets {
		if secret == "" {
			continue
		}
		encoded, _ := json.Marshal(secret)
		for _, variant := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), string(encoded[1 : len(encoded)-1])} {
			value = strings.ReplaceAll(value, variant, "[REDACTED]")
		}
	}
	value = diagnosticURL.ReplaceAllString(value, "[REDACTED_URL]")
	value = diagnosticBearer.ReplaceAllString(value, "Bearer [REDACTED]")
	value = diagnosticCookie.ReplaceAllString(value, "${1}[REDACTED]")
	value = diagnosticCredential.ReplaceAllString(value, "${1}[REDACTED]")
	const maxDiagnosticRunes = 2048
	runes := []rune(value)
	if len(runes) > maxDiagnosticRunes {
		value = string(runes[:maxDiagnosticRunes]) + "…"
	}
	return value
}
