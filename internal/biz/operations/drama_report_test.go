package operations_test

import (
	"bytes"
	"context"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type dramaReportStore struct {
	*memory.Store
	cause error
}

// DramaReport 注入报表故障，验证服务不会丢失存储错误。
func (s *dramaReportStore) DramaReport(context.Context, operations.DramaReportQuery) (*operations.DramaReportPage, error) {
	return &operations.DramaReportPage{}, s.cause
}

// TestDramaReportLogsFailures 验证服务记录真实错误且不改变返回链。
func TestDramaReportLogsFailures(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	repo := &dramaReportStore{Store: memory.NewStore(memory.Seed{}), cause: errors.New("report storage unavailable")}
	service := operations.NewReportingService(repo, nil)
	if _, err := service.DramaReport(context.Background(), operations.DramaReportQuery{}); !errors.Is(err, repo.cause) {
		t.Fatal("存储错误丢失")
	}
	if !strings.Contains(logs.String(), repo.cause.Error()) || !strings.Contains(logs.String(), "read drama report failed") {
		t.Fatal("缺少实际故障日志")
	}
	repo.cause = nil
	if _, err := service.DramaReport(context.Background(), operations.DramaReportQuery{}); err != nil {
		t.Fatal(err)
	}
}
