package operations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"drama-server/internal/biz/operations"
)

// TestUnconfirmedAlbumUpdateHTTPError 验证未确认版本返回稳定 409，并保留真实原因的预期状态日志。
func TestUnconfirmedAlbumUpdateHTTPError(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	failure := fmt.Errorf("TikTok album update failed (TikTok platform call failed: http_500): %w",
		&operations.PlatformCallError{Code: "album_update_unconfirmed", OutcomeUnknown: true})
	response := httptest.NewRecorder()
	writeOperationsError(response, httptest.NewRequest(http.MethodPost, "/admin/v1/dramas/310/publication/retry", nil), failure)
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusConflict || body.Reason != "album_update_unconfirmed" {
		t.Fatalf("核对待办仍返回通用 500：%d %s %v", response.Code, response.Body.String(), err)
	}
	if !strings.Contains(logs.String(), `"level":"WARN"`) || !strings.Contains(logs.String(), "operations request failed") || !strings.Contains(logs.String(), "http_500") {
		t.Fatal("核对待办缺少可定位的预期日志", logs.String())
	}
	if strings.Contains(response.Body.String(), "http_500") {
		t.Fatal("平台原始错误泄漏到用户响应", response.Body.String())
	}
}
