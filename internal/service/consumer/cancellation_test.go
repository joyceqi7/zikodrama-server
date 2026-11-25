package consumer

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/visitor"
	"gorm.io/gorm"
)

// TestRequestCancellationSurvivesGORMRollback 复现 GORM 的复合回滚错误，验证取消与真实依赖故障仍能区分。
func TestRequestCancellationSurvivesGORMRollback(t *testing.T) {
	db := &gorm.DB{Config: &gorm.Config{}}
	db.AddError(context.Canceled)
	db.AddError(sql.ErrTxDone)
	if errors.Is(db.Error, context.Canceled) {
		t.Fatal("用例未复现 GORM 丢失取消错误链")
	}
	failure := visitor.DependencyError(visitor.ErrServiceUnavailable, db.Error)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, test := range []struct {
		name   string
		ctx    context.Context
		status int
		reason string
	}{
		{"客户端取消", cancelled, http.StatusRequestTimeout, "request_cancelled"},
		{"请求超时", expired, http.StatusGatewayTimeout, "request_timeout"},
		{"真实依赖故障", context.Background(), http.StatusServiceUnavailable, "service_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/api/v1/bootstrap", nil).WithContext(test.ctx)
			response := httptest.NewRecorder()
			writeDomainError(response, request, failure)
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"reason":"`+test.reason+`"`) || strings.Contains(response.Body.String(), "transaction") {
				t.Fatalf("取消被误分类或泄露底层错误: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
