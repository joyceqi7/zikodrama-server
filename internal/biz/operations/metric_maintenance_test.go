package operations_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

type failingMetricMaintenance struct {
	*memory.Store
	err error
}

// MaintainMetrics 返回可控故障，检查维护失败记录操作与实际错误且继续向上传播。
func (r *failingMetricMaintenance) MaintainMetrics(context.Context, time.Time) error { return r.err }

func TestMetricMaintenanceLogsFailure(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	expected := errors.New("metric partition storage unavailable")
	repo := &failingMetricMaintenance{Store: memory.NewStore(memory.Seed{}), err: expected}
	service := operations.NewReportingService(repo, nil)
	if err := service.MaintainMetrics(context.Background(), time.Now()); !errors.Is(err, expected) {
		t.Fatal("维护错误未传播")
	}
	if !strings.Contains(output.String(), "maintain dashboard partitions failed") || !strings.Contains(output.String(), expected.Error()) {
		t.Fatal("维护失败日志缺少操作或实际错误")
	}
	repo.err = nil
	output.Reset()
	if err := service.MaintainMetrics(context.Background(), time.Now()); err != nil || output.Len() != 0 {
		t.Fatal("成功维护误报错误")
	}
}
