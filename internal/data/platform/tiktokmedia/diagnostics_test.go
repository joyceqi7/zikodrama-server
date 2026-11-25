package tiktokmedia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"drama-server/internal/biz/operations"
)

// TestVideoFailureDiagnostics 从真实视频适配器经过本地 HTTP 服务，验证日志详情和既有错误语义。
func TestVideoFailureDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name, response, code, message, requestID, classification, level string
		status                                                          int
		query, retryable, unknown                                       bool
	}{
		{name: "登记被拒保留官方错误", response: `{"error":{"code":"permission_denied","message":"BytePlus account is not bound","log_id":"official-post-1"}}`, code: "permission_denied", message: "BytePlus account is not bound", requestID: "official-post-1", classification: "api_rejected", level: "WARN", status: 200},
		{name: "查询被拒保留数字请求标识", query: true, response: `{"error":{"code":40001,"message":"VID does not belong to this space","log_id":900719925474099312345}}`, code: "40001", message: "VID does not belong to this space", requestID: "900719925474099312345", classification: "api_rejected", level: "WARN", status: 200},
		{name: "BaseResp优先保留具体错误", response: `{"BaseResp":{"status_code":1002,"status_message":"Space is unavailable"},"error":{"code":"ok"},"log_id":"base-log"}`, code: "1002", message: "Space is unavailable", requestID: "base-log", classification: "api_rejected", level: "WARN", status: 200},
		{name: "兼容base_resp错误", response: `{"base_resp":{"status_code":1003,"status_message":"VID not found","request_id":"snake-log"}}`, code: "1003", message: "VID not found", requestID: "snake-log", classification: "api_rejected", level: "WARN", status: 200},
		{name: "HTTP拒绝仍提取错误正文白名单", response: `{"error":{"code":"scope_not_authorized","message":"Video scope is missing"}}`, code: "scope_not_authorized", message: "Video scope is missing", requestID: "header-log", classification: "http_403", level: "WARN", status: 403},
		{name: "限流保持可重试且结果已知", response: `{"error":{"code":"rate_limit_exceeded","message":"Too many requests"}}`, code: "rate_limit_exceeded", message: "Too many requests", requestID: "header-log", classification: "http_429", level: "WARN", status: 429, retryable: true},
		{name: "服务故障保留未知写入结果", response: `{"error":{"code":"internal_error","message":"Upstream unavailable"}}`, code: "internal_error", message: "Upstream unavailable", requestID: "header-log", classification: "http_503", level: "ERROR", status: 503, retryable: true, unknown: true},
		{name: "非法JSON不打印响应全文", response: `<html>private-response-body</html>`, requestID: "header-log", classification: "invalid_json_response", level: "ERROR", status: 200, retryable: true, unknown: true},
		{name: "数据类型错误保留解析详情", response: `{"data":{"result_type":"private-result","job_id":"job-1"},"error":{"code":"ok"}}`, code: "ok", requestID: "header-log", classification: "invalid_json_response", level: "ERROR", status: 200, retryable: true, unknown: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Tt-Logid", "header-log")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.response)
			})
			var err error
			method := http.MethodPost
			if test.query {
				method = http.MethodGet
				_, err = client.QueryVideo(context.Background(), "private-job", "private-vid", "private-account")
			} else {
				_, err = client.RegisterVideo(context.Background(), diagnosticVideoInput())
			}
			assertPlatformFailureUnchanged(t, err, test.classification, test.retryable, test.unknown)
			record := onePlatformDiagnostic(t, logs)
			for key, expected := range map[string]any{
				"msg": "TikTok API request failed", "method": method, "path": "/v2/sg/shortdrama/video",
				"http_status": float64(test.status), "error_code": test.classification, "level": test.level,
				"platform_request_id": test.requestID,
			} {
				if record[key] != expected {
					t.Errorf("日志字段 %s：得到 %v，期望 %v", key, record[key], expected)
				}
			}
			for key, expected := range map[string]string{"platform_code": test.code, "platform_message": test.message} {
				if expected == "" {
					if _, exists := record[key]; exists {
						t.Errorf("不应凭空补充官方字段 %s", key)
					}
				} else if record[key] != expected {
					t.Errorf("官方字段 %s：得到 %v，期望 %s", key, record[key], expected)
				}
			}
			if test.classification == "invalid_json_response" && !strings.Contains(record["error"].(string), "invalid character") && !strings.Contains(record["error"].(string), "cannot unmarshal") {
				t.Error("解析失败未保留实际错误详情")
			}
			assertDiagnosticSecretsAbsent(t, logs.String()+err.Error(), "private-response-body", "private-result", "private-job", "private-vid", "private-account", "private-title")
		})
	}
}

// TestPlatformDiagnosticRedaction 验证白名单、显式敏感字段和官方错误各字段中的凭据均被脱敏。
func TestPlatformDiagnosticRedaction(t *testing.T) {
	logs := capturePlatformDiagnostics(t)
	client := contractClient(t, func(w http.ResponseWriter, _ *http.Request) {
		payload := map[string]any{
			"error": map[string]any{
				"code":    "denied-test-token",
				"message": "account binding denied; test-secret; test-token; unused-response-token; https://media.example.test/private.mp4?signature=media-sign; password=private-password; password_digest=private-digest; signature=private-sign; access_token=other-token; Authorization: Bearer other-bearer; secret_key='other-secret'; Cookie: session=cookie-secret; second=second-cookie",
				"log_id":  "official-test-secret-request",
				"account": map[string]string{"password": "account-password"},
			},
			"data":    map[string]string{"access_token": "unused-response-token", "media_url": "https://media.example.test/other.mp4"},
			"request": map[string]string{"title": "private-title", "client_secret": "unrelated-secret"},
		}
		w.Header().Set("Set-Cookie", "header-cookie-secret")
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Error(err)
		}
	})
	_, err := client.RegisterVideo(context.Background(), diagnosticVideoInput())
	assertPlatformFailureUnchanged(t, err, "api_rejected", false, false)
	record := onePlatformDiagnostic(t, logs)
	if !strings.Contains(record["platform_message"].(string), "account binding denied") || record["platform_code"] != "denied-[REDACTED]" || record["platform_request_id"] != "official-[REDACTED]-request" {
		t.Error("脱敏不应抹掉可诊断原因或整个标识字段")
	}
	assertDiagnosticSecretsAbsent(t, logs.String()+err.Error(), "test-token", "test-secret", "media.example.test", "media-sign", "private-password", "private-digest", "private-sign", "other-token", "other-bearer", "other-secret", "cookie-secret", "second-cookie", "account-password", "unused-response-token", "private-title", "unrelated-secret")

	diagnostic := platformCallDiagnostic{secrets: []string{"secret+/value"}}
	cleaned := diagnostic.clean(`request denied secret+/value secret%2B%2Fvalue secret+%2Fvalue; api_key="key with spaces"; dsn='connection details'; https%3A%2F%2Fmedia.test%2Fvideo`)
	assertDiagnosticSecretsAbsent(t, cleaned, "secret+/value", "secret%2B%2Fvalue", "secret+%2Fvalue", "key with spaces", "connection details", "media.test")
	if !strings.Contains(cleaned, "request denied") {
		t.Error("不应只保留错误类型而丢失实际错误")
	}
	cleaned = diagnostic.clean(`permission denied; password="escaped\"password tail"; Cookie: "session=escaped\"cookie tail"; binding missing`)
	assertDiagnosticSecretsAbsent(t, cleaned, "escaped", "password tail", "cookie tail")
	if !strings.Contains(cleaned, "binding missing") {
		t.Error("带转义的敏感值之后仍应保留故障原因")
	}
}

// TestTokenFailureDiagnostics 验证取 Token 失败仍可定位官方原因，且业务接口不会重复打印或发送。
func TestTokenFailureDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name, response, classification string
		status                         int
		retryable                      bool
	}{
		{"OAuth拒绝", `{"error":"invalid_client","error_description":"client authentication failed","log_id":"oauth-log"}`, "http_401", 401, false},
		{"HTTP成功但Token缺失", `{"error":"invalid_client","error_description":"client authentication failed","log_id":"oauth-log"}`, "invalid_token_response", 200, true},
		{"Token响应格式损坏", `{`, "invalid_json_response", 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v2/oauth/token/" {
					t.Error("取Token失败后不应发送登记请求")
				}
				w.Header().Set("X-Request-Id", "oauth-header")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.response)
			}))
			t.Cleanup(server.Close)
			client := NewClient("owner", "private-secret", server.Client())
			client.baseURL = server.URL
			_, err := client.RegisterVideo(context.Background(), diagnosticVideoInput())
			assertPlatformFailureUnchanged(t, err, test.classification, test.retryable, false)
			record := onePlatformDiagnostic(t, logs)
			if record["path"] != "/v2/oauth/token/" || record["method"] != "POST" || calls != 1 {
				t.Error("Token失败未定位到唯一调用边界")
			}
			if test.classification != "invalid_json_response" && (record["platform_code"] != "invalid_client" || record["platform_message"] != "client authentication failed" || record["platform_request_id"] != "oauth-log") {
				t.Error("OAuth错误详情缺失")
			}
			assertDiagnosticSecretsAbsent(t, logs.String()+err.Error(), "private-secret")
		})
	}
}

// TestSuccessfulVideoCallsDoNotLogFailure 验证登记、确认和Token缓存成功路径保持安静。
func TestSuccessfulVideoCallsDoNotLogFailure(t *testing.T) {
	logs := capturePlatformDiagnostics(t)
	client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `{"data":{"result_type":2,"job_id":"job-1"},"error":{"code":"ok","message":""}}`)
		} else {
			_, _ = io.WriteString(w, `{"data":{"upload_status":2,"vid":"private-vid"},"BaseResp":{"status_code":0}}`)
		}
	})
	registered, err := client.RegisterVideo(context.Background(), diagnosticVideoInput())
	if err != nil || registered.JobID != "job-1" {
		t.Fatalf("登记成功结果发生变化：%+v %v", registered, err)
	}
	observed, err := client.QueryVideo(context.Background(), registered.JobID, registered.VID, "private-account")
	if err != nil || observed.Status != 2 || observed.VID != "private-vid" {
		t.Fatalf("确认成功结果发生变化：%+v %v", observed, err)
	}
	if logs.Len() != 0 {
		t.Fatalf("成功路径不应输出故障日志：%s", logs.String())
	}
}

// TestTransportFailureDiagnostics 对网络、读取和关闭故障注入实际错误，检查敏感信息及返回语义。
func TestTransportFailureDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name, code, level, detail string
		token, query, closeOnly   bool
		networkErr, readErr       error
	}{
		{name: "登记网络故障", code: "network_error", level: "ERROR", detail: "connection reset", networkErr: errors.New("connection reset for https://media.test/private?token=other-token with test-secret")},
		{name: "Token网络故障", token: true, code: "network_error", level: "ERROR", detail: "connection reset", networkErr: errors.New("connection reset with test-secret")},
		{name: "查询取消", query: true, code: "network_error", level: "INFO", detail: "context canceled", networkErr: context.Canceled},
		{name: "登记读取故障", code: "response_read_error", level: "ERROR", detail: "response stream interrupted", readErr: errors.New("response stream interrupted; token=other-token")},
		{name: "Token读取故障", token: true, code: "response_read_error", level: "ERROR", detail: "response stream interrupted", readErr: errors.New("response stream interrupted; test-secret")},
		{name: "关闭故障保留登记成功", closeOnly: true, level: "WARN", detail: "response close failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			client := NewClient("owner", "test-secret", &http.Client{Transport: diagnosticRoundTripper(func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/v2/oauth/token/" && !test.token {
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"access_token":"test-token","expires_in":7200}`))}, nil
				}
				if test.networkErr != nil {
					return nil, test.networkErr
				}
				body := &diagnosticResponseBody{reader: strings.NewReader(`{"data":{"result_type":2,"job_id":"job-1"}}`), readErr: test.readErr}
				if test.closeOnly {
					body.closeErr = errors.New("response close failed; test-token")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"X-Tt-Logid": {"transport-log"}}, Body: body}, nil
			})})
			var err error
			if test.query {
				_, err = client.QueryVideo(context.Background(), "job-1", "private-vid", "private-account")
			} else {
				_, err = client.RegisterVideo(context.Background(), diagnosticVideoInput())
			}
			if test.closeOnly {
				if err != nil {
					t.Fatalf("关闭响应流不应改变业务结果：%v", err)
				}
			} else {
				assertPlatformFailureUnchanged(t, err, test.code, true, !test.token && !test.query)
			}
			record := onePlatformDiagnostic(t, logs)
			if record["level"] != test.level || !strings.Contains(record["error"].(string), test.detail) {
				t.Error("实际故障原因或日志级别不正确")
			}
			if test.closeOnly && record["msg"] != "close TikTok API response failed" {
				t.Error("关闭故障操作描述不正确")
			}
			assertDiagnosticSecretsAbsent(t, logs.String(), "test-secret", "test-token", "other-token", "media.test")
		})
	}
}

// diagnosticRoundTripper 在传输边界注入故障，禁止向真实平台发送请求。
type diagnosticRoundTripper func(*http.Request) (*http.Response, error)

// RoundTrip 返回测试指定的HTTP响应或网络错误。
func (f diagnosticRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// diagnosticResponseBody 分别注入读取和关闭错误，以确认日志不改变既有返回行为。
type diagnosticResponseBody struct {
	reader            io.Reader
	readErr, closeErr error
}

// Read 返回注入的流错误；没有故障时读取给定内容。
func (b *diagnosticResponseBody) Read(p []byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	return b.reader.Read(p)
}

// Close 返回注入的关闭错误，不修改读取结果。
func (b *diagnosticResponseBody) Close() error { return b.closeErr }

// capturePlatformDiagnostics 替换并恢复全局logger；调用方禁止并行运行。
func capturePlatformDiagnostics(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &logs
}

func diagnosticVideoInput() operations.TikTokVideoInput {
	return operations.TikTokVideoInput{VID: "private-vid", Title: "private-title", SpaceName: "private-space", BytePlusAccountID: "private-account"}
}

func onePlatformDiagnostic(t *testing.T, logs *bytes.Buffer) map[string]any {
	t.Helper()
	var record map[string]any
	decoder := json.NewDecoder(strings.NewReader(logs.String()))
	if err := decoder.Decode(&record); err != nil {
		t.Fatalf("未取得结构化诊断日志：%v", err)
	}
	var extra map[string]any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("同一调用应只记录一次诊断：%s", logs.String())
	}
	return record
}

func assertPlatformFailureUnchanged(t *testing.T, err error, code string, retryable, unknown bool) {
	t.Helper()
	var platformErr *operations.PlatformCallError
	if !errors.As(err, &platformErr) || platformErr.Code != code || platformErr.Retryable != retryable || platformErr.OutcomeUnknown != unknown {
		t.Fatalf("原错误分类、重试或未知结果语义发生变化：%+v", err)
	}
}

func assertDiagnosticSecretsAbsent(t *testing.T, value string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(value, secret) {
			t.Errorf("日志或返回值泄漏测试敏感值 %q", secret)
		}
	}
}
