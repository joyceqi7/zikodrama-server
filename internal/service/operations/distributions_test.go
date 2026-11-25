package operations

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAuthorizeApplicationDramasInput 验证空选择、重复或无效标识在 Handler 入口被拒绝，避免误授权整库。
func TestAuthorizeApplicationDramasInput(t *testing.T) {
	for _, input := range []string{
		`{"dramaIds":[]}`,
		`{"dramaIds":["201","201"]}`,
		`{"dramaIds":["invalid"]}`,
		`{"dramaIds":null}`,
	} {
		r := httptest.NewRequest(http.MethodPost, "/admin/v1/applications/42/drama-authorizations", strings.NewReader(input))
		r.SetPathValue("applicationID", "42")
		w := httptest.NewRecorder()
		(&Handler{}).authorizeApplicationDramas(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("输入 %s 未被拒绝: %d", input, w.Code)
		}
	}
}

// TestListApplicationDramaAuthorizationsInput 验证分页和视图参数在 Handler 入口拒绝无效值。
func TestListApplicationDramaAuthorizationsInput(t *testing.T) {
	for _, suffix := range []string{"?view=unknown", "?view=available&page=0", "?view=tracked&pageSize=101", "?view=available&page=invalid"} {
		r := httptest.NewRequest(http.MethodGet, "/admin/v1/applications/42/drama-authorizations"+suffix, nil)
		r.SetPathValue("applicationID", "42")
		w := httptest.NewRecorder()
		(&Handler{}).listApplicationDramaAuthorizations(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("输入 %s 未被拒绝: %d", suffix, w.Code)
		}
	}
}
