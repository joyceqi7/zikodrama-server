package consumer_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
	httpapi "drama-server/internal/service/consumer"
	"drama-server/internal/testsupport/memory"
)

// TestViewingStateHasNoServerRoutes 验证观看进度、收藏和历史不产生消费者端服务端读写接口。
// 广告奖励、VIP 和剧集权益仍是服务端权威业务，不属于客户端观看状态。
func TestViewingStateHasNoServerRoutes(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	handler := newConsumerHandler(domain.NewService(store, time.Now), newTestVisitorService(t, store), httpapi.Config{})
	for _, path := range []string{"/api/v1/me/progress", "/api/v1/me/history", "/api/v1/me/follows", "/api/v1/me/favorites", "/api/v1/playback-sessions", "/api/v1/playback/progress"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("客户端观看状态不应出现服务端路由：%s %s 返回 %d", method, path, response.Code)
			}
		}
	}
}
