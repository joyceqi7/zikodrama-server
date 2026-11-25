package consumer

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeRejectsTrailingJSONValue(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(`{"tiktokAppId":"5004"}{"extra":true}`))
	response := httptest.NewRecorder()
	var input struct {
		TikTokAppID string `json:"tiktokAppId"`
	}
	if decode(response, request, &input) {
		t.Fatal("拼接的第二个 JSON 值必须被拒绝")
	}
	if response.Code != 400 {
		t.Fatalf("尾随 JSON 状态码=%d body=%s", response.Code, response.Body.String())
	}
}
