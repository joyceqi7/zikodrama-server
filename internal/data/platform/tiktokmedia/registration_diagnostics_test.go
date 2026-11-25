package tiktokmedia

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"drama-server/internal/biz/operations"
)

// TestRegistrationResponseDiagnostics 从登记入口验证业务校验失败的字段形态、平台上下文和错误传播。
func TestRegistrationResponseDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name, response, payload, state, code, level, message, requestID string
		value                                                           int
		retryable                                                       bool
	}{
		{name: "明确拒绝保留平台详情", response: `{"data":{"result_type":3},"error":{"code":"ok","message":"Video registration denied","log_id":900719925474099312345}}`, payload: "data", state: "integer", value: 3, code: "video_registration_rejected", level: "WARN", message: "Video registration denied", requestID: "900719925474099312345"},
		{name: "顶层拒绝保留响应头标识", response: `{"result_type":3}`, payload: "root", state: "integer", value: 3, code: "video_registration_rejected", level: "WARN"},
		{name: "字段缺失不伪造零值", response: `{"data":{"job_id":"private-job","message":"Registration result unavailable"}}`, payload: "data", state: "missing", code: "invalid_video_response", level: "ERROR", retryable: true, message: "Registration result unavailable"},
		{name: "空值与缺失可区分", response: `{"data":{"result_type":null}}`, payload: "data", state: "null", code: "invalid_video_response", level: "ERROR", retryable: true},
		{name: "明确零值保留实际值", response: `{"data":{"result_type":0}}`, payload: "data", state: "integer", code: "invalid_video_response", level: "ERROR", retryable: true},
		{name: "未知枚举保留实际值", response: `{"data":{"result_type":9}}`, payload: "data", state: "integer", value: 9, code: "invalid_video_response", level: "ERROR", retryable: true},
		{name: "按实际data载荷诊断", response: `{"result_type":2,"data":{}}`, payload: "data", state: "missing", code: "invalid_video_response", level: "ERROR", retryable: true},
		{name: "空data沿用顶层解析", response: `{"result_type":3,"data":null}`, payload: "root", state: "integer", value: 3, code: "video_registration_rejected", level: "WARN"},
		{name: "大小写匹配与业务解析一致", response: `{"DATA":{"RESULT_TYPE":3}}`, payload: "data", state: "integer", value: 3, code: "video_registration_rejected", level: "WARN"},
		{name: "非法字符串不输出原值", response: `{"data":{"result_type":"private-result"}}`, payload: "data", state: "invalid", code: "invalid_json_response", level: "ERROR", retryable: true},
		{name: "非法对象不输出内容", response: `{"data":{"result_type":{"account":"private-result"}}}`, payload: "data", state: "invalid", code: "invalid_json_response", level: "ERROR", retryable: true},
		{name: "视频身份不符保留诊断", response: `{"data":{"result_type":2,"byteplus_vid":"other-private-vid"}}`, payload: "data", state: "integer", value: 2, code: "video_identity_mismatch", level: "ERROR"},
		{name: "仅VID响应必须匹配身份", response: `{"data":{"byteplus_vid":"other-private-vid"}}`, payload: "data", state: "missing", code: "video_identity_mismatch", level: "ERROR"},
		{name: "相同VID不能覆盖空结果", response: `{"data":{"result_type":null,"byteplus_vid":"private-vid"}}`, payload: "data", state: "null", code: "invalid_video_response", level: "ERROR", retryable: true},
		{name: "相同VID不能覆盖非法枚举", response: `{"data":{"result_type":0,"byteplus_vid":"private-vid"}}`, payload: "data", state: "integer", code: "invalid_video_response", level: "ERROR", retryable: true},
		{name: "相同VID不能覆盖明确拒绝", response: `{"data":{"result_type":3,"byteplus_vid":"private-vid"}}`, payload: "data", state: "integer", value: 3, code: "video_registration_rejected", level: "WARN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			calls := 0
			client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("X-Tt-Logid", "header-log")
				_, _ = io.WriteString(w, test.response)
			})
			registered, err := client.RegisterVideo(context.Background(), diagnosticVideoInput())
			assertPlatformFailureUnchanged(t, err, test.code, test.retryable, test.retryable)
			if calls != 1 || registered != (operations.TikTokVideoRegistration{}) {
				t.Fatal("响应异常不得增加请求次数或返回成功结果")
			}
			record := onePlatformDiagnostic(t, logs)
			requestID := test.requestID
			if requestID == "" {
				requestID = "header-log"
			}
			for key, expected := range map[string]any{
				"msg": "TikTok API request failed", "method": "POST", "path": "/v2/sg/shortdrama/video", "http_status": float64(200),
				"error_code": test.code, "level": test.level, "platform_request_id": requestID,
				"response_payload": test.payload, "result_type_present": test.state != "missing", "result_type_state": test.state,
			} {
				if record[key] != expected {
					t.Errorf("日志字段 %s：得到 %v，期望 %v", key, record[key], expected)
				}
			}
			if test.state == "integer" {
				if record["result_type"] != float64(test.value) {
					t.Errorf("实际枚举值未保留：%v", record["result_type"])
				}
			} else if _, exists := record["result_type"]; exists {
				t.Error("缺失、空值或非法字段不应被打印为整数")
			}
			if test.message != "" && record["platform_message"] != test.message {
				t.Errorf("平台说明未保留：%v", record["platform_message"])
			}
			if test.code == "invalid_json_response" {
				if !strings.Contains(record["error"].(string), "cannot unmarshal") {
					t.Error("解析失败未保留实际错误")
				}
			} else if record["error"] != err.Error() {
				t.Error("业务校验失败未记录实际返回错误")
			}
			assertDiagnosticSecretsAbsent(t, logs.String()+err.Error(), "private-result", "private-job", "private-vid", "private-account", "private-title")
		})
	}
}

// TestRegistrationResponseShapeDiagnostics 验证实际缺字段场景能定位两层协议形态，且不输出字段值或未知字段名。
func TestRegistrationResponseShapeDiagnostics(t *testing.T) {
	logs := capturePlatformDiagnostics(t)
	client := contractClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"result_type":2,"data":{"job_id":"private-job","byteplus_vid":"","result":{"result_type":2},"private-field":"private-value"},"private-account":{"token":"private-token"}}`)
	})
	_, err := client.RegisterVideo(context.Background(), diagnosticVideoInput())
	assertPlatformFailureUnchanged(t, err, "invalid_video_response", true, true)
	record := onePlatformDiagnostic(t, logs)
	if !reflect.DeepEqual(record["root_field_types"], map[string]any{"result_type": "number", "data": "object", "unknown_field_count": "1"}) ||
		!reflect.DeepEqual(record["data_field_types"], map[string]any{"job_id": "string", "byteplus_vid": "string", "result": "object", "unknown_field_count": "1"}) {
		t.Errorf("未保留两层白名单字段形态：%s", logs.String())
	}
	assertDiagnosticSecretsAbsent(t, logs.String(), "private-job", "private-vid", "private-field", "private-value", "private-account", "private-token")
}

// TestVIDOnlyRegistrationResponse 验证实测同步登记形态，只接受与输入相同的 VID 且不误报日志。
func TestVIDOnlyRegistrationResponse(t *testing.T) {
	for _, response := range []string{`{"data":{"byteplus_vid":"private-vid"}}`, `{"byteplus_vid":"private-vid"}`} {
		t.Run("仅返回VID", func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			client := contractClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, response) })
			result, err := client.RegisterVideo(context.Background(), diagnosticVideoInput())
			if err != nil || result.VID != "private-vid" || result.JobID != "" || logs.Len() != 0 {
				t.Fatalf("同步登记返回值或日志错误：%+v %v %s", result, err, logs.String())
			}
		})
	}
}

// TestRegistrationValidationDiagnosticRedaction 验证传输成功后的业务错误仍使用同一次调用的凭据脱敏集合。
func TestRegistrationValidationDiagnosticRedaction(t *testing.T) {
	for _, response := range []string{
		`{"data":{"result_type":3,"access_token":"response-token","message":"Video unavailable; test-secret; test-token; response-token; https://media.test/private.mp4?signature=private-sign; password=private-password"},"request_id":"request-test-secret","account":{"password":"private-account-password"}}`,
		`{"DATA":{"result_type":3,"access_token":"response-token","message":"Video unavailable; test-secret; test-token; response-token; https://media.test/private.mp4?signature=private-sign; password=private-password"},"request_id":"request-test-secret"}`,
		`{"data":{"result_type":3,"access_token":"response-token"},"error":{"code":"ok","message":"Video unavailable; test-secret; test-token; response-token; https://media.test/private.mp4?signature=private-sign; password=private-password","log_id":"request-test-secret"}}`,
	} {
		t.Run("业务拒绝脱敏", func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			client := contractClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, response) })
			_, err := client.RegisterVideo(context.Background(), diagnosticVideoInput())
			assertPlatformFailureUnchanged(t, err, "video_registration_rejected", false, false)
			record := onePlatformDiagnostic(t, logs)
			if !strings.Contains(record["platform_message"].(string), "Video unavailable") || record["platform_request_id"] != "request-[REDACTED]" {
				t.Error("脱敏丢失具体原因或请求标识")
			}
			assertDiagnosticSecretsAbsent(t, logs.String()+err.Error(), "test-secret", "test-token", "response-token", "media.test", "private-sign", "private-password", "private-account-password")
		})
	}
}

// TestInvalidRegistrationInputDiagnostics 验证入口拒绝有日志，且不会发送外部请求或输出参数。
func TestInvalidRegistrationInputDiagnostics(t *testing.T) {
	for _, field := range []string{"vid", "title", "space", "account"} {
		t.Run(field, func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			input := diagnosticVideoInput()
			switch field {
			case "vid":
				input.VID = ""
			case "title":
				input.Title = ""
			case "space":
				input.SpaceName = ""
			case "account":
				input.BytePlusAccountID = ""
			}
			client := NewClient("owner", "test-secret", &http.Client{Transport: diagnosticRoundTripper(func(*http.Request) (*http.Response, error) {
				t.Fatal("入口拒绝不应发送外部请求")
				return nil, nil
			})})
			_, err := client.RegisterVideo(context.Background(), input)
			assertPlatformFailureUnchanged(t, err, "invalid_video_input", false, false)
			record := onePlatformDiagnostic(t, logs)
			if record["error"] != err.Error() || record["level"] != "WARN" || record["path"] != "/v2/sg/shortdrama/video" {
				t.Error("入口拒绝日志缺少实际错误或操作信息")
			}
			if _, exists := record["http_status"]; exists {
				t.Error("尚未请求平台时不应伪造响应信息")
			}
			assertDiagnosticSecretsAbsent(t, logs.String(), "private-vid", "private-title", "private-space", "private-account", "test-secret")
		})
	}
}

// TestSuccessfulRegistrationVariantsDoNotLogFailure 验证同步、异步和顶层响应保持成功且不误报日志。
func TestSuccessfulRegistrationVariantsDoNotLogFailure(t *testing.T) {
	for _, response := range []string{
		`{"data":{"result_type":1,"byteplus_vid":"private-vid","job_id":" job-1 "}}`,
		`{"data":{"result_type":2,"job_id":" job-1 "}}`,
		`{"result_type":1,"byteplus_vid":"private-vid","job_id":" job-1 "}`,
		`{"result_type":2,"job_id":" job-1 "}`,
	} {
		t.Run("登记成功", func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			client := contractClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, response) })
			result, err := client.RegisterVideo(context.Background(), diagnosticVideoInput())
			if err != nil || result.JobID != "job-1" || result.VID != "private-vid" || logs.Len() != 0 {
				t.Fatalf("成功返回值或日志行为发生变化：%+v %v %s", result, err, logs.String())
			}
		})
	}
}
