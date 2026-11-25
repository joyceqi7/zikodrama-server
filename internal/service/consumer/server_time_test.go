package consumer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/visitor"
	"drama-server/internal/pkg/requesttime"
)

type serverTimeMetricRepository struct{ events []domain.MetricEvent }

// RecordMetrics 保留入口送来的事实快照，确认手机观察时间已经被服务器接收时间替换。
func (s *serverTimeMetricRepository) RecordMetrics(_ context.Context, _ string, events []domain.MetricEvent) error {
	s.events = append(s.events, events...)
	return nil
}

// TestMetricServerReceiptTime 验证诊断与统计不因手机时钟偏移被丢弃，且仍校验事实字段。
func TestMetricServerReceiptTime(t *testing.T) {
	repository := &serverTimeMetricRepository{}
	handler := &Handler{config: Config{Analytics: domain.NewAnalyticsService(repository)}}
	received := time.Date(2026, 10, 23, 15, 59, 59, 0, time.UTC)
	for _, clientTime := range []string{"2001-01-01T00:00:00Z", "2099-01-01T00:00:00Z"} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", strings.NewReader(fmt.Sprintf(`{"events":[{"id":"metric-server-time-key","kind":"active","occurredAt":%q}]}`, clientTime)))
		request = request.WithContext(requesttime.WithReceivedAt(request.Context(), received))
		response := httptest.NewRecorder()
		handler.collectMetrics(response, request, visitor.Context{VisitorID: "107"})
		if response.Code != http.StatusOK || !repository.events[len(repository.events)-1].OccurredAt.Equal(received) {
			t.Fatalf("统计没有使用首次接收时间: %d %s", response.Code, response.Body)
		}
	}
}
