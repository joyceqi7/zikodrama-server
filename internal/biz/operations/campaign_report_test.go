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

type campaignReportStore struct {
	*memory.Store
	cause error
}

// CampaignReport 注入报表故障，验证服务不会丢失存储错误。
func (s *campaignReportStore) CampaignReport(context.Context, operations.CampaignReportQuery) (*operations.CampaignReportPage, error) {
	return &operations.CampaignReportPage{}, s.cause
}

// TestCampaignReportLogsFailures 验证服务记录真实错误且不改变返回链。
func TestCampaignReportLogsFailures(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	repo := &campaignReportStore{Store: memory.NewStore(memory.Seed{}), cause: errors.New("report storage unavailable")}
	service := operations.NewReportingService(repo, nil)
	if _, err := service.CampaignReport(context.Background(), operations.CampaignReportQuery{}); !errors.Is(err, repo.cause) {
		t.Fatal("存储错误丢失")
	}
	if !strings.Contains(logs.String(), repo.cause.Error()) || !strings.Contains(logs.String(), "read campaign report failed") {
		t.Fatal("缺少实际故障日志")
	}
	logs.Reset()
	repo.cause = nil
	if _, err := service.CampaignReport(context.Background(), operations.CampaignReportQuery{}); err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Fatal("成功查询不应输出失败日志")
	}
	unavailable := operations.NewReportingService(memory.NewStore(memory.Seed{}), nil)
	if _, err := unavailable.CampaignReport(context.Background(), operations.CampaignReportQuery{}); !errors.Is(err, operations.ErrInvalidInput) || !strings.Contains(logs.String(), "read campaign report unavailable") {
		t.Fatal("未配置报表适配器时应保留错误及日志")
	}
}
