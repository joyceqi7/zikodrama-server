package consumer_test

import (
	"bytes"
	"context"
	domain "drama-server/internal/biz/consumer"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type metricRepository struct{ err error }

// RecordMetrics 返回可控故障，验证业务边界不吞错且成功时无误报。
func (r metricRepository) RecordMetrics(context.Context, string, []domain.MetricEvent) error {
	return r.err
}

func TestMetricCollectionLogging(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	expected := errors.New("analytics storage offline")
	service := domain.NewAnalyticsService(metricRepository{expected})
	if err := service.Record(context.Background(), "123", nil); !errors.Is(err, expected) {
		t.Fatal("必须传播存储故障")
	}
	if !strings.Contains(output.String(), "record consumer metrics failed") || !strings.Contains(output.String(), expected.Error()) {
		t.Fatal("缺少具体操作或原始错误")
	}
	output.Reset()
	if err := domain.NewAnalyticsService(metricRepository{}).Record(context.Background(), "123", nil); err != nil || output.Len() != 0 {
		t.Fatal("成功路径不能误报错误")
	}
}
