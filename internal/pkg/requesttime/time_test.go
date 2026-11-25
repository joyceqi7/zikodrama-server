package requesttime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRequestTimeKeepsFirstBoundary 验证跨日嵌套调用保留首次入口时间，内部时钟只读取一次。
func TestRequestTimeKeepsFirstBoundary(t *testing.T) {
	first := time.Date(2026, 10, 23, 23, 59, 59, 0, time.FixedZone("app", 8*3600))
	calls := 0
	clock := func() time.Time { calls++; return first.Add(time.Duration(calls-1) * time.Hour) }
	ctx := Ensure(context.Background(), clock)
	ctx = Ensure(ctx, clock)
	ctx = WithReceivedAt(ctx, first.Add(time.Hour))
	if got := At(ctx, clock); !got.Equal(first) || got.Location() != time.UTC || calls != 1 {
		t.Fatalf("请求边界时间未固定为 UTC: %v 读取次数=%d", got, calls)
	}
}

// TestRequestTimeMiddlewareRunsBeforeBusiness 验证 HTTP 入口在鉴权或请求体处理前提供可信时间，并保留上游已记录的时间。
func TestRequestTimeMiddlewareRunsBeforeBusiness(t *testing.T) {
	first := time.Date(2026, 10, 23, 15, 59, 59, 0, time.UTC)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/me/ad-shows", nil)
	request = request.WithContext(WithReceivedAt(request.Context(), first))
	Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := At(r.Context(), func() time.Time { t.Fatal("不应重新读取时钟"); return time.Time{} }); !got.Equal(first) {
			t.Fatalf("HTTP 嵌套入口覆盖了接收时间: %v", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(httptest.NewRecorder(), request)
}
