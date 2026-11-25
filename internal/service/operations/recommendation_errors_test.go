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

	"drama-server/internal/biz/homepage"
)

// TestRecommendationOfflineErrorContract 验证两渠道共用稳定冲突码，业务拒绝记录警告且不泄漏内部配置。
func TestRecommendationOfflineErrorContract(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	response := httptest.NewRecorder()
	writeOperationsError(response, httptest.NewRequest(http.MethodPost, "/admin/v1/direct/dramas/201/publication/operations", nil), fmt.Errorf("%w: section=hot", homepage.ErrDramaRecommended))
	var result envelope
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusConflict || result.Reason != "publication_in_recommendations" || strings.Contains(response.Body.String(), "section=hot") {
		t.Fatal("推荐引用冲突合同或脱敏错误")
	}
	if !strings.Contains(logs.String(), `"level":"WARN"`) || !strings.Contains(logs.String(), "section=hot") {
		t.Fatal("推荐门禁缺少实际警告日志")
	}
}
