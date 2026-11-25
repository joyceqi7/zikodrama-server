package operations

import (
	"context"
	"log/slog"
	"time"
)

// DashboardDay 是某一 UTC 日期的账本与已接收客户端信号统计。
type DashboardDay struct {
	// Date 是 UTC 自然日。
	Date string `json:"date"`
	// NewUsers 是首次建立应用内用户主体的数量。
	NewUsers int64 `json:"newUsers"`
	// RewardCompletions 是幂等落账的广告奖励次数。
	RewardCompletions int64 `json:"rewardCompletions"`
	// EpisodeUnlocks 是逐集权益授予集数，重复授予不是去重用户数。
	EpisodeUnlocks int64 `json:"episodeUnlocks"`
	// ActiveUsers 是当日有成功上报事实的去重用户数。
	ActiveUsers int64 `json:"activeUsers"`
	// Launches 是成功建立身份后的 WebView 启动次数。
	Launches int64 `json:"launches"`
	// PlayStarts 是实际播放开始次数，不含预加载和暂停恢复。
	PlayStarts int64 `json:"playStarts"`
	// PlayCompletions 是匹配开始事实的结束次数，按开始日期归属。
	PlayCompletions int64 `json:"playCompletions"`
	// RewardedShows 是激励广告展示成功回调数。
	RewardedShows int64 `json:"rewardedShows"`
	// AdCompletions 是匹配展示事实的完整观看数，按展示日期归属。
	AdCompletions int64 `json:"adCompletions"`
	// InterstitialShows 是插屏广告展示成功回调数，不参与激励完播率。
	InterstitialShows int64 `json:"interstitialShows"`
	// TikTokPostbackAttempts 是客户端调用官方关键行为回传的次数。
	TikTokPostbackAttempts int64 `json:"tiktokPostbackAttempts" gorm:"column:tiktok_postback_attempts"`
	// TikTokPostbackSuccesses 是官方 SDK 明确确认成功的回传次数。
	TikTokPostbackSuccesses int64 `json:"tiktokPostbackSuccesses" gorm:"column:tiktok_postback_successes"`
	// TikTokPostbackFailures 是能力缺失、调用异常或官方拒绝的回传次数。
	TikTokPostbackFailures int64 `json:"tiktokPostbackFailures" gorm:"column:tiktok_postback_failures"`
	// VIPActivations 是 VIP 权益授予次数。
	VIPActivations int64 `json:"vipActivations"`
}

// AnalyticsDashboard 是仪表盘时间范围及每日统计；未采集指标不返回虚构零值。
type AnalyticsDashboard struct {
	// TimeZone 是全部日期边界使用的时区。
	TimeZone string `json:"timeZone"`
	// GeneratedAt 是本次查询完成时间，不代表平台实时数据。
	GeneratedAt time.Time `json:"generatedAt"`
	// Audience 给出范围末日的去重活跃统计及采集起点。
	Audience *DashboardAudience `json:"audience"`
	// CampaignRanking 是按结算次数降序排列的投放链接前 50 名。
	CampaignRanking []CampaignRewardRank `json:"campaignRanking"`
	// Days 按日期递增，无记录的日期由服务补零。
	Days []DashboardDay `json:"days"`
}

// Dashboard 查询入口已校验的日期范围，补齐无记录日期并返回 UTC 账本和采集统计。
func (s *ReportingService) Dashboard(ctx context.Context, from, to time.Time, allHistory ...bool) (*AnalyticsDashboard, error) {
	rows, err := s.metrics.DashboardDays(ctx, from, to)
	if err != nil {
		slog.ErrorContext(ctx, "query analytics dashboard failed", "error", err)
		return nil, err
	}
	indexed := make(map[string]DashboardDay, len(rows))
	for _, row := range rows {
		indexed[row.Date] = row
	}
	result := &AnalyticsDashboard{TimeZone: "UTC", Days: []DashboardDay{}}
	for date := from; date.Before(to); date = date.AddDate(0, 0, 1) {
		key := date.Format("2006-01-02")
		row := indexed[key]
		row.Date = key
		result.Days = append(result.Days, row)
	}
	audience, err := s.metrics.DashboardAudience(ctx, to)
	if err != nil {
		slog.ErrorContext(ctx, "query dashboard audience failed", "error", err)
		return nil, err
	}
	result.Audience = &audience
	rankingFrom, rankingTo := from, to
	// 全部历史仅扩展投放账本排行，每日趋势仍使用入口校验的日期范围。
	if len(allHistory) > 0 && allHistory[0] {
		rankingFrom = time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC)
		rankingTo = time.Now().UTC()
	}
	ranking, err := s.metrics.CampaignRewardRanking(ctx, rankingFrom, rankingTo)
	if err != nil {
		slog.ErrorContext(ctx, "query campaign reward ranking failed", "error", err)
		return nil, err
	}
	result.CampaignRanking = ranking
	result.GeneratedAt = time.Now().UTC()
	return result, nil
}

// DashboardAudience 的 MAU 按范围末日向前 30 个 UTC 自然日去重，不累加 DAU。
type DashboardAudience struct {
	// MonthlyActiveUsers 是过去 30 日成功上报事实的去重用户数；缺少完整历史窗口时为空。
	MonthlyActiveUsers *int64 `json:"monthlyActiveUsers"`
	// FirstReceivedAt 是此应用首次成功采集时间；空值说明尚未收到信号。
	FirstReceivedAt *time.Time `json:"firstReceivedAt"`
}

// CampaignRewardRank 仅统计带可信投放归因的成功奖励结算，不代表广告曝光或完播。
type CampaignRewardRank struct {
	// CampaignID 是当前应用内的投放记录标识。
	CampaignID string `json:"campaignId"`
	// Name 是投放配置当前名称。
	Name string `json:"name"`
	// Code 是稳定的投放入口编码。
	Code string `json:"code"`
	// LandingURL 是已保存的官方投放链接。
	LandingURL string `json:"landingUrl"`
	// RewardCompletions 是查询时间内幂等落账的奖励次数。
	RewardCompletions int64 `json:"rewardCompletions"`
	// RewardUsers 是查询时间内至少成功结算一次的去重用户数。
	RewardUsers int64 `json:"rewardUsers"`
}
