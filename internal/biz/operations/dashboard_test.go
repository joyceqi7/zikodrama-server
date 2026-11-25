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
	"time"
)

type dashboardRepository struct {
	*memory.Store
	rows        []operations.DashboardDay
	err         error
	audienceErr error
	rankingErr  error
	rankingFrom time.Time
	rankingTo   time.Time
}

// DashboardDays 返回乱序日期或依赖故障，验证服务补零与错误传播。
func (r *dashboardRepository) DashboardDays(context.Context, time.Time, time.Time) ([]operations.DashboardDay, error) {
	return r.rows, r.err
}

func TestDashboardFillsMissingDays(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	repo := &dashboardRepository{Store: memory.NewStore(memory.Seed{}), rows: []operations.DashboardDay{{Date: "2026-09-03", NewUsers: 8}, {Date: "2026-09-01", RewardCompletions: 2}}}
	service := operations.NewReportingService(repo, nil)
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	result, err := service.Dashboard(context.Background(), from, from.AddDate(0, 0, 3))
	if err != nil || len(result.Days) != 3 || result.Days[0].RewardCompletions != 2 || result.Days[1].Date != "2026-09-02" || result.Days[1].NewUsers != 0 || result.Days[2].NewUsers != 8 {
		t.Fatalf("连续日期或补零错误：%+v %v", result, err)
	}
	if strings.Contains(logs.String(), "ERROR") {
		t.Fatal("成功查询不能打印错误日志")
	}
	repo.err = errors.New("dashboard storage unavailable")
	if _, err := service.Dashboard(context.Background(), from, from.AddDate(0, 0, 1)); !errors.Is(err, repo.err) {
		t.Fatal("查询故障不能伪装成空数据")
	}
	if !strings.Contains(logs.String(), "query analytics dashboard failed") || !strings.Contains(logs.String(), repo.err.Error()) {
		t.Fatalf("缺少可诊断日志：%s", logs.String())
	}
}

// DashboardAudience 返回可控故障，验证受众查询不会掩盖数据库错误。
func (r *dashboardRepository) DashboardAudience(context.Context, time.Time) (operations.DashboardAudience, error) {
	return operations.DashboardAudience{}, r.audienceErr
}

func TestDashboardAudienceLogging(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	expected := errors.New("audience read unavailable")
	repo := &dashboardRepository{Store: memory.NewStore(memory.Seed{}), audienceErr: expected}
	from := time.Now().UTC().Truncate(24 * time.Hour)
	if _, err := operations.NewReportingService(repo, nil).Dashboard(context.Background(), from, from.AddDate(0, 0, 1)); !errors.Is(err, expected) {
		t.Fatal("受众查询故障必须传播")
	}
	if !strings.Contains(logs.String(), "query dashboard audience failed") || !strings.Contains(logs.String(), expected.Error()) {
		t.Fatal("受众查询缺少错误详情")
	}
}

// CampaignRewardRanking 捕获排行查询范围并注入失败，验证业务层日志和历史范围。
func (r *dashboardRepository) CampaignRewardRanking(_ context.Context, from, to time.Time) ([]operations.CampaignRewardRank, error) {
	r.rankingFrom, r.rankingTo = from, to
	return []operations.CampaignRewardRank{{CampaignID: "123", RewardCompletions: 8, RewardUsers: 2}}, r.rankingErr
}

func TestDashboardCampaignRankingRangeAndFailure(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	repo := &dashboardRepository{Store: memory.NewStore(memory.Seed{})}
	service := operations.NewReportingService(repo, nil)
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	result, err := service.Dashboard(context.Background(), from, to)
	if err != nil || repo.rankingFrom != from || repo.rankingTo != to || len(result.CampaignRanking) != 1 {
		t.Fatalf("日期排行错误：%+v %v", result, err)
	}
	result, err = service.Dashboard(context.Background(), from, to, true)
	if err != nil || repo.rankingFrom.Year() != 1000 || repo.rankingTo.Before(to) || len(result.Days) != 1 {
		t.Fatalf("历史范围影响每日趋势：%+v %v", result, err)
	}
	repo.rankingErr = errors.New("ranking database unavailable")
	if _, err = service.Dashboard(context.Background(), from, to); !errors.Is(err, repo.rankingErr) || !strings.Contains(logs.String(), "query campaign reward ranking failed") || !strings.Contains(logs.String(), repo.rankingErr.Error()) {
		t.Fatal("排行失败须传播并打印实际错误")
	}
}
