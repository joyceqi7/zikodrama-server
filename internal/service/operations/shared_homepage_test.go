package operations

import (
	"context"
	"net/http/httptest"
	"testing"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

// TestSharedRecommendationContext 验证仅白名单管理接口可获得当前主体目录，不依赖客户端应用头。
func TestSharedRecommendationContext(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	h := &Handler{service: operations.NewService(memory.OperationsRepositories(store), nil)}
	for _, pattern := range []string{"GET /admin/v1/carousel", "PUT /admin/v1/carousel", "GET /admin/v1/carousel/targets", "GET /admin/v1/hot-dramas", "PUT /admin/v1/hot-dramas", "GET /admin/v1/hot-dramas/targets", "GET /admin/v1/category-rankings", "GET /admin/v1/category-rankings/{tagID}", "PUT /admin/v1/category-rankings/{tagID}", "GET /admin/v1/category-rankings/{tagID}/targets"} {
		for _, header := range []string{"", "101", "102", "deleted-application"} {
			r := httptest.NewRequest("GET", "/", nil).WithContext(application.WithContext(context.Background(), "stale"))
			r.Pattern = pattern
			r.Header.Set("x-application-id", header)
			r.Header.Set("x-subject-id", "1")
			scoped, ok := h.withApplicationContext(httptest.NewRecorder(), r)
			if !ok || application.IDFromContext(scoped.Context()) != "" || !homepage.IsManagementContext(scoped.Context()) || requiresApplicationContext(pattern) {
				t.Fatalf("主体入口仍依赖应用：%s", pattern)
			}
		}
	}
	for _, pattern := range []string{"GET /admin/v1/dashboard", "GET /admin/v1/campaigns", "GET /admin/v1/consumer-users", "GET /admin/v1/application-monetization"} {
		if !requiresApplicationContext(pattern) || isSharedRecommendationRoute(pattern) {
			t.Fatalf("应用级入口权限被扩大：%s", pattern)
		}
	}
	for _, pattern := range []string{"GET /api/v1/open/home/carousel", "GET /api/v1/open/home/hot", "GET /api/v1/open/home/rankings"} {
		if isSharedRecommendationRoute(pattern) || homepage.IsManagementContext(context.Background()) {
			t.Fatal("消费者获得全局可见性")
		}
	}
}
