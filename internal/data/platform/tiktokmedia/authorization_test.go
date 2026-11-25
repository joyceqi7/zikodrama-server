package tiktokmedia

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"drama-server/internal/biz/operations"
)

// TestAuthorizationResultsAndDiagnostics 验证授权与撤销逐项确认、失败分类和诊断脱敏，不调用真实平台。
func TestAuthorizationResultsAndDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, response, wantCode string
		targets                  []string
		revoke                   bool
		fields                   []string
	}{
		{"实际字段授权成功", `{"data":{"client_key_result":[{"client_key":"target","auth_status":1}]}}`, "", []string{"target"}, false, nil},
		{"实际字段撤销成功", `{"data":{"client_key_result":[{"client_key":"target","auth_status":2}]}}`, "", []string{"target"}, true, nil},
		{"实际字段错误目标", `{"data":{"client_key_result":[{"client_key":"other","auth_status":1}]}}`, "authorization_result_incomplete", []string{"target"}, false, []string{"unexpected_target_count=1"}},
		{"实际字段状态缺失", `{"data":{"client_key_result":[{"client_key":"target"}]}}`, "authorization_rejected", []string{"target"}, false, []string{"rejected_target_count=1"}},
		{"实际字段非列表", `{"data":{"client_key_result":{"client_key":"target","auth_status":1}}}`, "invalid_json_response", []string{"target"}, false, nil},
		{"两字段冲突", `{"data":{"client_key_result":[{"client_key":"target","auth_status":2}],"client_key_result_list":[{"client_key":"target","auth_status":1}]}}`, "invalid_json_response", []string{"target"}, false, nil},
		{"授权成功", `{"data":{"client_key_result_list":[{"client_key":"target","auth_status":1}]}}`, "", []string{"target"}, false, nil},
		{"重复授权已满足最终状态", `{"data":{"client_key_result_list":[{"client_key":"target","auth_status":1,"error_code":22010}]}}`, "", []string{"target"}, false, nil},
		{"撤销成功", `{"client_key_result_list":[{"client_key":"target","auth_status":2}]}`, "", []string{"target"}, true, nil},
		{"缺少结果", `{"data":{}}`, "authorization_result_incomplete", []string{"target"}, false, []string{"response_result_count=0", "expected_target_count=1", "matched_target_count=0"}},
		{"结果空值", `{"data":{"client_key_result_list":null}}`, "authorization_result_incomplete", []string{"target"}, false, []string{"response_result_count=0"}},
		{"错误目标", `{"data":{"client_key_result_list":[{"client_key":"wrong","auth_status":1}]}}`, "authorization_result_incomplete", []string{"target"}, false, []string{"unexpected_target_count=1", "matched_target_count=0"}},
		{"重复目标不能掩盖遗漏", `{"data":{"client_key_result_list":[{"client_key":"target","auth_status":1},{"client_key":"target","auth_status":1}]}}`, "authorization_result_incomplete", []string{"target", "second"}, false, []string{"unexpected_target_count=1", "matched_target_count=1"}},
		{"逐项拒绝保留原因并脱敏", `{"data":{"client_key_result_list":[{"client_key":"target","auth_status":2,"error_code":22011,"error_message":"denied test-secret test-token https://media.example/private token=private-token"}]}}`, "authorization_rejected", []string{"target"}, false, []string{"platform_code=22011", "denied", "rejected_target_count=1", "level=WARN"}},
		{"大写状态码不能误判成功", `{"BaseResp":{"StatusCode":22012,"StatusMessage":"permission denied"},"data":{}}`, "api_rejected", []string{"target"}, false, []string{"platform_code=22012", "permission denied"}},
		{"小写状态码", `{"base_resp":{"status_code":22012,"status_message":"permission denied"},"data":{}}`, "api_rejected", []string{"target"}, false, []string{"platform_code=22012"}},
		{"结果格式错误", `{"data":{"client_key_result_list":"unexpected"}}`, "invalid_json_response", []string{"target"}, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(previous)
			client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v2/sg/shortdrama/album/authorize/" {
					t.Errorf("接口路径错误")
				}
				w.Header().Set("X-Tt-Logid", "authorization-request-id")
				_, _ = io.WriteString(w, tc.response)
			})
			var err error
			if tc.revoke {
				err = client.RevokeAlbum(context.Background(), "123", tc.targets)
			} else {
				err = client.AuthorizeAlbum(context.Background(), "123", tc.targets)
			}
			if tc.wantCode == "" {
				if err != nil || logs.Len() != 0 {
					t.Fatalf("成功不应报错：%v %s", err, logs.String())
				}
				return
			}
			var platform *operations.PlatformCallError
			if !errors.As(err, &platform) || platform.Code != tc.wantCode {
				t.Fatalf("分类错误：%v", err)
			}
			text := logs.String()
			if strings.Count(text, "TikTok API request failed") != 1 {
				t.Fatalf("同处应只记录一次：%s", text)
			}
			for _, field := range append(tc.fields, "platform_request_id=authorization-request-id") {
				if !strings.Contains(text, field) {
					t.Errorf("缺少诊断 %s：%s", field, text)
				}
			}
			for _, secret := range []string{"test-secret", "test-token", "private-token", "https://media.example/private", tc.response} {
				if strings.Contains(text, secret) {
					t.Fatal("日志泄漏敏感响应")
				}
			}
		})
	}
}

// TestAuthorizationRejectsInvalidTargetsBeforeRequest 验证无效目标不会触发外部授权或获取 Token。
func TestAuthorizationRejectsInvalidTargetsBeforeRequest(t *testing.T) {
	for _, targets := range [][]string{nil, {""}, {"  "}, {"same", "same"}} {
		client := NewClient("owner", "test-secret", &http.Client{Transport: authorizationNoRequest{t}})
		err := client.AuthorizeAlbum(context.Background(), "123", targets)
		var platform *operations.PlatformCallError
		if !errors.As(err, &platform) || platform.Code != "invalid_authorization_targets" {
			t.Fatalf("无效目标未被拒绝：%v", err)
		}
	}
}

// authorizationNoRequest 拒绝测试中的任何 HTTP 外部请求。
type authorizationNoRequest struct{ t *testing.T }

// RoundTrip 记录意外请求并返回本地错误，避免测试访问平台。
func (r authorizationNoRequest) RoundTrip(*http.Request) (*http.Response, error) {
	r.t.Error("无效目标仍发起请求")
	return nil, errors.New("unexpected request")
}
