package operations

import (
	"context"
	"log/slog"
	"time"
)

// RewardCounts 表示成功奖励账本次数与应用内用户标识的去重人数。
type RewardCounts struct {
	// RewardCompletions 是幂等落账次数，重试不增加计数。
	RewardCompletions int64 `json:"rewardCompletions"`
	// RewardUsers 按应用及用户标识去重，不承诺跨应用真人去重。
	RewardUsers int64 `json:"rewardUsers"`
}

// DramaRewardRank 合并所有所选应用中同一共享剧的逐集奖励。
type DramaRewardRank struct {
	// DramaID 是共享剧标识。
	DramaID string `json:"dramaId"`
	// Title 是当前剧名，删除后显示历史标识。
	Title string `json:"title"`
	// RewardCounts 是该剧的逐集奖励汇总。
	RewardCounts
}

// CampaignRewardContribution 保留链接所属应用，不按落地剧合并。
type CampaignRewardContribution struct {
	// CampaignRewardRank 提供原链接信息及统计。
	CampaignRewardRank
	// ApplicationID 是链接所属应用的内部标识。
	ApplicationID string `json:"applicationId"`
	// ApplicationName 是所属应用展示名称。
	ApplicationName string `json:"applicationName"`
	// DramaTitle 是链接当前落地剧名称，与实际奖励发生剧不同。
	DramaTitle string `json:"dramaTitle"`
	// EpisodeRewards 是逐集奖励次数。
	EpisodeRewards int64 `json:"episodeRewards"`
	// VIPRewards 是 VIP 广告进度奖励次数，不是 VIP 权益激活次数。
	VIPRewards int64 `json:"vipRewards" gorm:"column:vip_rewards"`
}

// RewardAnalysis 包含同一结算时间范围的剧目、链接排行和无剧归属 VIP。
type RewardAnalysis struct {
	// Dramas 是逐集奖励前 50 名，无结算剧不列入。
	Dramas []DramaRewardRank `json:"dramas"`
	// Campaigns 是已归因链接前 50 名，包含停用链接。
	Campaigns []CampaignRewardContribution `json:"campaigns"`
	// UnassignedVIP 单独统计没有剧归属的 VIP 奖励。
	UnassignedVIP RewardCounts `json:"unassignedVip"`
}

// EpisodeRewardRow 按奖励目标集聚合，不表示播放或流失情况。
type EpisodeRewardRow struct {
	// EpisodeNumber 是集序；无法可靠恢复的历史集序为 0。
	EpisodeNumber int `json:"episodeNumber"`
	// RewardCounts 是目标集奖励汇总。
	RewardCounts
}

// RewardTrendDay 是某 UTC 日的奖励次数及当日去重人数。
type RewardTrendDay struct {
	// Date 是 UTC 日期。
	Date string `json:"date"`
	// RewardCounts 是当天奖励统计。
	RewardCounts
}

// RewardTrend 对范围总人数独立去重，不累加每日人数。
type RewardTrend struct {
	// Days 按日期连续补零。
	Days []RewardTrendDay `json:"days"`
	// Total 是整个范围的账本次数与独立去重人数。
	Total RewardCounts `json:"total"`
}

// RewardAnalysisRepository 是只读奖励报表端口，空应用上下文明确表示所有小程序。
type RewardAnalysisRepository interface {
	// RewardAnalysis 查询剧目、投放链接与无剧归属 VIP 奖励。
	RewardAnalysis(context.Context, time.Time, time.Time) (*RewardAnalysis, error)
	// RewardEpisodes 查询指定共享剧的逐集奖励目标集明细。
	RewardEpisodes(context.Context, string, time.Time, time.Time) ([]EpisodeRewardRow, error)
	// RewardTrend 查询指定剧或投放链接的每日及范围去重统计。
	RewardTrend(context.Context, string, string, time.Time, time.Time) (*RewardTrend, error)
}

// RewardAnalysis 查询调用方已确认应用范围的奖励贡献，故障不伪装成零。
func (s *ReportingService) RewardAnalysis(ctx context.Context, from, to time.Time) (*RewardAnalysis, error) {
	if s.rewards == nil {
		slog.ErrorContext(ctx, "reward analysis repository unavailable", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	result, err := s.rewards.RewardAnalysis(ctx, from, to)
	if err != nil {
		slog.ErrorContext(ctx, "query reward analysis failed", "error", err)
	}
	return result, err
}

// RewardEpisodes 返回入口已校验剧标识的奖励目标集明细。
func (s *ReportingService) RewardEpisodes(ctx context.Context, id string, from, to time.Time) ([]EpisodeRewardRow, error) {
	if s.rewards == nil {
		slog.ErrorContext(ctx, "reward episode repository unavailable", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	result, err := s.rewards.RewardEpisodes(ctx, id, from, to)
	if err != nil {
		slog.ErrorContext(ctx, "query reward episodes failed", "error", err, "drama_id", id)
	}
	return result, err
}

// RewardTrend 保留范围总人数的独立去重并补齐没有奖励的日期。
func (s *ReportingService) RewardTrend(ctx context.Context, dimension, id string, from, to time.Time) (*RewardTrend, error) {
	if s.rewards == nil {
		slog.ErrorContext(ctx, "reward trend repository unavailable", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	result, err := s.rewards.RewardTrend(ctx, dimension, id, from, to)
	if err != nil {
		slog.ErrorContext(ctx, "query reward trend failed", "error", err)
		return nil, err
	}
	indexed := map[string]RewardTrendDay{}
	for _, row := range result.Days {
		indexed[row.Date] = row
	}
	result.Days = []RewardTrendDay{}
	for at := from; at.Before(to); at = at.AddDate(0, 0, 1) {
		date := at.Format(time.DateOnly)
		row := indexed[date]
		row.Date = date
		result.Days = append(result.Days, row)
	}
	return result, nil
}

// RewardReportSummary 对全部筛选结果独立去重，不累加分页、逐日或各行人数。
type RewardReportSummary struct {
	// RewardCounts 是筛选范围的总次数和应用内去重人数。
	RewardCounts
	// AverageRewards 是总次数除以去重人数，无奖励用户时为空。
	AverageRewards *float64 `json:"averageRewards"`
	// EpisodeRewards 是成功剧集解锁奖励结算次数。
	EpisodeRewards int64 `json:"episodeRewards"`
	// VIPRewards 是成功 VIP 广告进度奖励次数，不是 VIP 领取次数。
	VIPRewards int64 `json:"vipRewards" gorm:"column:vip_rewards"`
}

// ReportAdShows 统计应用和日期范围内客户端展示成功回调，不按剧目或链接推测归因。
type ReportAdShows struct {
	// Total 是激励与插屏展示次数之和，不含官方回传尝试或奖励结算次数。
	Total int64 `json:"total"`
	// Rewarded 是幂等保存的激励广告展示成功次数。
	Rewarded int64 `json:"rewarded"`
	// Interstitial 是幂等保存的插屏广告展示成功次数。
	Interstitial int64 `json:"interstitial"`
	// AvailableFrom 是原始展示埋点保留窗口首个 UTC 日期。
	AvailableFrom string `json:"availableFrom"`
	// Complete 表示查询日期完全位于原始埋点保留窗口内，不代表客户端上报不会丢失。
	Complete bool `json:"complete"`
}
