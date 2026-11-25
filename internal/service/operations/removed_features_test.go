package operations

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemovedSubtitleAndTranslationRoutesReturnNotFound(t *testing.T) {
	handler := NewHandler(nil, nil, Config{})
	for _, route := range []struct{ method, path string }{
		{"GET", "/admin/v1/episodes/example/subtitles"}, {"POST", "/admin/v1/episodes/example/subtitles"},
		{"PUT", "/admin/v1/episodes/example/subtitles/track"}, {"GET", "/admin/v1/dramas/example/subtitles/automation"},
		{"POST", "/admin/v1/dramas/example/subtitles/generate"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("已撤销接口仍可用：%s %s -> %d", route.method, route.path, response.Code)
		}
	}
}
