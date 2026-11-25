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

type rewardAnalysisRepository struct {
	*memory.Store
	err error
}

// RewardAnalysis 注入报表故障以校验业务日志。
func (r *rewardAnalysisRepository) RewardAnalysis(context.Context, time.Time, time.Time) (*operations.RewardAnalysis, error) {
	return &operations.RewardAnalysis{}, r.err
}

// RewardEpisodes 注入分集查询故障。
func (r *rewardAnalysisRepository) RewardEpisodes(context.Context, string, time.Time, time.Time) ([]operations.EpisodeRewardRow, error) {
	return []operations.EpisodeRewardRow{}, r.err
}

// RewardTrend 返回单日数据及独立范围人数，验证补零不会改写总人数。
func (r *rewardAnalysisRepository) RewardTrend(_ context.Context, _ string, _ string, from, _ time.Time) (*operations.RewardTrend, error) {
	return &operations.RewardTrend{Days: []operations.RewardTrendDay{{Date: from.Format(time.DateOnly), RewardCounts: operations.RewardCounts{RewardCompletions: 4, RewardUsers: 2}}}, Total: operations.RewardCounts{RewardCompletions: 4, RewardUsers: 2}}, r.err
}

func TestRewardAnalysisLoggingAndTrendGaps(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	repo := &rewardAnalysisRepository{Store: memory.NewStore(memory.Seed{})}
	service := operations.NewReportingService(repo, nil)
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 3)
	trend, err := service.RewardTrend(context.Background(), "drama", "201", from, to)
	if err != nil || len(trend.Days) != 3 || trend.Days[1].RewardCompletions != 0 || trend.Total.RewardUsers != 2 {
		t.Fatalf("趋势补零错误：%+v %v", trend, err)
	}
	repo.err = errors.New("reward report storage unavailable")
	if _, err = service.RewardAnalysis(context.Background(), from, to); !errors.Is(err, repo.err) {
		t.Fatal("汇总故障丢失")
	}
	if _, err = service.RewardEpisodes(context.Background(), "201", from, to); !errors.Is(err, repo.err) {
		t.Fatal("分集故障丢失")
	}
	if _, err = service.RewardTrend(context.Background(), "drama", "201", from, to); !errors.Is(err, repo.err) {
		t.Fatal("趋势故障丢失")
	}
	for _, message := range []string{"query reward analysis failed", "query reward episodes failed", "query reward trend failed", repo.err.Error()} {
		if !strings.Contains(logs.String(), message) {
			t.Fatalf("缺少错误日志 %s", message)
		}
	}
}
