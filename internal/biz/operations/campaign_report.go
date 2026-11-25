package operations

import (
	"context"
	"log/slog"
	"time"
)

// CampaignReportQuery 是入口已校验的链接搜索、日期、分页和核心指标排序条件。
type CampaignReportQuery struct {
	// From 是 UTC 开始日期（包含）。
	From time.Time
	// To 是结束日期的次日（不包含）。
	To time.Time
	// Query 按投放名称或编码包含查询。
	Query string
	// Page 是从 1 开始的页码。
	Page int
	// PageSize 是每页最多 100 条。
	PageSize int
	// Sort 是 rewardCompletions、rewardUsers 或 averageRewards。
	Sort string
	// Direction 是 asc 或 desc。
	Direction string
}

// CampaignReportRow 保留每条链接的小程序和落地剧归属，不因同剧而合并。
type CampaignReportRow struct {
	// CampaignRewardContribution 包含链接身份、去重人数及逐集和 VIP 奖励拆分。
	CampaignRewardContribution
	// AverageRewards 是成功奖励结算次数除以领取人数，零人数时为 null。
	AverageRewards *float64 `json:"averageRewards"`
	// Status 标明投放配置是否停用，历史奖励仍计入。
	Status string `json:"status"`
}

// CampaignReportPage 是包含零奖励链接的真实分页结果。
type CampaignReportPage struct {
	// AdShows 是同应用日期范围展示回调统计，不受名称搜索或分页影响。
	AdShows ReportAdShows `json:"adShows"`
	// Summary 是全部匹配链接的独立去重汇总，包含停用链接。
	Summary RewardReportSummary `json:"summary"`
	// Unattributed 是同应用日期范围内缺少有效链接归因的奖励，不受链接名称搜索影响。
	Unattributed RewardReportSummary `json:"unattributed"`
	// Items 是当前页的独立链接指标。
	Items []CampaignReportRow `json:"items"`
	// Total 是所选应用及搜索条件匹配的链接数。
	Total int64 `json:"total"`
}

// CampaignReportRepository 仅查询成功奖励账本，不把奖励伪装成点击、播放或 ROI。
type CampaignReportRepository interface {
	// CampaignReport 在空应用上下文下显式汇总所有小程序的独立链接。
	CampaignReport(context.Context, CampaignReportQuery) (*CampaignReportPage, error)
}

// CampaignReport 返回分页核心指标，保留存储错误及统一友好提示映射。
func (s *ReportingService) CampaignReport(ctx context.Context, input CampaignReportQuery) (*CampaignReportPage, error) {
	if s.campaignReports == nil {
		slog.ErrorContext(ctx, "read campaign report unavailable", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	result, err := s.campaignReports.CampaignReport(ctx, input)
	if err != nil {
		slog.ErrorContext(ctx, "read campaign report failed", "error", err)
	}
	return result, err
}
