package tiktokmedia

import (
	"context"
	"io"
	"net/http"
	"testing"
)

// TestVideoQueryInvalidResponseDiagnostics 验证恢复查询的异常响应有实际错误、字段形态和请求标识，且保持只读重试语义。
func TestVideoQueryInvalidResponseDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name, response string
		status         any
	}{
		{"缺失状态", `{"data":{}}`, nil},
		{"状态为空", `{"data":{"upload_status":null}}`, nil},
		{"未知状态", `{"data":{"upload_status":9}}`, float64(9)},
		{"成功但缺少VID", `{"data":{"upload_status":2}}`, float64(2)},
	} {
		t.Run(test.name, func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			client := contractClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Tt-Logid", "query-request")
				_, _ = io.WriteString(w, test.response)
			})
			_, err := client.QueryVideo(context.Background(), "", "private-vid", "private-account")
			assertPlatformFailureUnchanged(t, err, "invalid_video_response", true, false)
			record := onePlatformDiagnostic(t, logs)
			if record["method"] != "GET" || record["error"] != err.Error() || record["platform_request_id"] != "query-request" || record["level"] != "ERROR" || record["upload_status"] != test.status || record["data_field_types"] == nil {
				t.Errorf("查询异常诊断不完整：%s", logs.String())
			}
			assertDiagnosticSecretsAbsent(t, logs.String(), "private-vid", "private-account", "test-token", "test-secret")
		})
	}
}

// TestVideoQueryInvalidInputDiagnostics 验证参数拒绝在请求前记录实际错误，不依赖已配置客户端。
func TestVideoQueryInvalidInputDiagnostics(t *testing.T) {
	for _, input := range [][3]string{{"", "", "account"}, {"job", "vid", ""}} {
		t.Run("查询入口拒绝", func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			var client *Client
			_, err := client.QueryVideo(context.Background(), input[0], input[1], input[2])
			assertPlatformFailureUnchanged(t, err, "invalid_video_query", false, false)
			record := onePlatformDiagnostic(t, logs)
			if record["error"] != err.Error() || record["method"] != "GET" || record["level"] != "WARN" {
				t.Errorf("入口拒绝日志错误：%s", logs.String())
			}
		})
	}
}
