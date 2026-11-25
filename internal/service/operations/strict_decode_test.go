package operations

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeRejectsTrailingJSONValue(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest("PUT", "/admin/v1/applications/5004", strings.NewReader(`{"name":"App"}{"status":"disabled"}`))
	response := httptest.NewRecorder()
	var input struct {
		Name string `json:"name"`
	}
	if decode(response, request, &input) {
		t.Fatal("拼接的第二个 JSON 值必须被拒绝")
	}
	if response.Code != 400 {
		t.Fatalf("尾随 JSON 状态码=%d body=%s", response.Code, response.Body.String())
	}
}
