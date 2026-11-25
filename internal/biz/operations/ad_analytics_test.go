package operations_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"drama-server/internal/biz/operations"
)

type adAnalyticsRepository struct{ err error }

// ReadAdAnalytics 注入数据库故障，验证错误传播与实际原因日志。
func (r adAnalyticsRepository) ReadAdAnalytics(context.Context, operations.AdAnalyticsQuery) (operations.AdAnalytics, error) {
	return operations.AdAnalytics{}, r.err
}

func TestAdAnalyticsServiceLogging(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	failure := errors.New("ad metrics database disconnected")
	for _, expected := range []error{nil, failure} {
		logs.Reset()
		service := operations.NewAdAnalyticsService(adAnalyticsRepository{err: expected})
		_, err := service.ReadAdAnalytics(context.Background(), operations.AdAnalyticsQuery{})
		if !errors.Is(err, expected) {
			t.Fatalf("仓储错误未原样传播: %v", err)
		}
		if expected == nil && logs.Len() != 0 {
			t.Fatal("成功不应打印错误")
		}
		if expected != nil && (!strings.Contains(logs.String(), "read ad analytics failed") || !strings.Contains(logs.String(), failure.Error())) {
			t.Fatalf("错误日志缺少操作或实际原因: %s", logs.String())
		}
	}
	logs.Reset()
	_, err := operations.NewAdAnalyticsService(nil).ReadAdAnalytics(context.Background(), operations.AdAnalyticsQuery{})
	if !errors.Is(err, operations.ErrInvalidInput) || !strings.Contains(logs.String(), "read ad analytics unavailable") {
		t.Fatal("缺少读端口时应记录故障")
	}
}
