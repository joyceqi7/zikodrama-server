package tiktokmedia

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestAlbumListingAlreadyOnline 验证重复上架的明确状态只记一条确认日志，不传播为发布故障。
func TestAlbumListingAlreadyOnline(t *testing.T) {
	logs := capturePlatformDiagnostics(t)
	client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/sg/shortdrama/album/status/" || r.Method != http.MethodPost {
			t.Fatalf("上架请求使用了错误接口：%s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"error":{"code":4001,"message":"album is already online","log_id":"already-test-token-online"},"data":{"access_token":"private-response-token","media_url":"https://private.example/video"}}`)
	})
	if err := client.SetAlbumListed(context.Background(), "7637420375425239060", true); err != nil {
		t.Fatalf("官方已上架仍被当作故障：%v", err)
	}
	record := onePlatformDiagnostic(t, logs)
	for key, expected := range map[string]any{
		"level": "INFO", "msg": "TikTok album listing already confirmed",
		"method": http.MethodPost, "path": "/v2/sg/shortdrama/album/status/",
		"error": "TikTok platform call failed: api_rejected", "platform_code": "4001",
		"platform_message": "album is already online", "platform_request_id": "already-[REDACTED]-online",
	} {
		if record[key] != expected {
			t.Errorf("确认日志字段 %s：得到 %v，期望 %v", key, record[key], expected)
		}
	}
	assertDiagnosticSecretsAbsent(t, logs.String(), "test-token", "test-secret", "private-response-token", "private.example")
}

// TestAlbumListingRejectsOtherFailures 验证相同错误码、相同文案或其他操作不能扩大幂等成功范围。
func TestAlbumListingRejectsOtherFailures(t *testing.T) {
	for _, tc := range []struct {
		name, response, classification string
		listed                         bool
		selectVersion                  bool
		status                         int
	}{
		{name: "相同错误码但状态未满足", listed: true, status: http.StatusOK, classification: "api_rejected", response: `{"error":{"code":4001,"message":"album cannot be listed"}}`},
		{name: "相同文案但错误码不同", listed: true, status: http.StatusOK, classification: "api_rejected", response: `{"error":{"code":4002,"message":"album is already online"}}`},
		{name: "下架不能接受已上架", listed: false, status: http.StatusOK, classification: "api_rejected", response: `{"error":{"code":4001,"message":"album is already online"}}`},
		{name: "HTTP拒绝不能接受已上架", listed: true, status: http.StatusForbidden, classification: "http_403", response: `{"error":{"code":4001,"message":"album is already online"}}`},
		{name: "已上架不能证明版本选择成功", selectVersion: true, status: http.StatusOK, classification: "api_rejected", response: `{"error":{"code":4001,"message":"album is already online"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			client := contractClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.response)
			})
			var err error
			if tc.selectVersion {
				err = client.SetOnlineVersion(context.Background(), "7637420375425239060", 2)
			} else {
				err = client.SetAlbumListed(context.Background(), "7637420375425239060", tc.listed)
			}
			assertPlatformFailureUnchanged(t, err, tc.classification, false, false)
			record := onePlatformDiagnostic(t, logs)
			if record["level"] != "WARN" || record["msg"] != "TikTok API request failed" || record["platform_message"] == "" {
				t.Fatalf("真实拒绝缺少原错误诊断：%s", logs.String())
			}
		})
	}
}

// TestAlbumListingSuccessLogsNoFailure 验证正常上架与下架不产生失败或重复状态日志。
func TestAlbumListingSuccessLogsNoFailure(t *testing.T) {
	logs := capturePlatformDiagnostics(t)
	client := contractClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":{},"error":{"code":"ok"}}`)
	})
	for _, listed := range []bool{true, false} {
		if err := client.SetAlbumListed(context.Background(), "7637420375425239060", listed); err != nil {
			t.Fatalf("正常上下架失败：%v", err)
		}
	}
	if strings.TrimSpace(logs.String()) != "" {
		t.Fatalf("正常上下架产生故障日志：%s", logs.String())
	}
}
