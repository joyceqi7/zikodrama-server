package operations

import (
	"context"
	"log/slog"
	"time"
)

// DramaReportQuery 是入口已校验的日期、共享剧搜索、分页和白名单排序条件。
type DramaReportQuery struct {
	// From 是 UTC 开始日期（包含）。
	From time.Time
	// To 是 UTC 结束日期的次日（不包含）。
	To time.Time
	// Query 支持剧名包含查询或全数字剧 ID 精确查询。
	Query string
	// Page 是从 1 开始的页码。
	Page int
	// PageSize 是每页最多 100 条。
	PageSize int
	// Sort 支持 unlockCount、unlockUsers 和 averageUnlocks，保留旧播放字段的兼容读取。
	Sort string
	// Direction 是 asc 或 desc。
	Direction string
}

// DramaReportRow 是共享剧在选定小程序和日期范围内的核心指标，未采集指标保持空值。
type DramaReportRow struct {
	// DramaID 是共享剧内部标识。
	DramaID string `json:"dramaId"`
	// Title 是当前运营剧名。
	Title string `json:"title"`
	// PlayUsers 未具备剧目归因采集时为 null，不以解锁人数推测播放人数。
	PlayUsers *int64 `json:"playUsers"`
	// FirstEpisodeCompletionRate 缺少剧集归因时为 null；可用时以 0–1 表示。
	FirstEpisodeCompletionRate *float64 `json:"firstEpisodeCompletionRate"`
	// UnlockUsers 按应用和用户成对去重，仅统计逐集广告奖励成功结算。
	UnlockUsers int64 `json:"unlockUsers"`
	// UnlockCount 是逐集广告奖励的幂等成功结算次数，不是批量授予的集数。
	UnlockCount int64 `json:"unlockCount"`
	// AverageUnlocks 是结算次数除以领取人数，无领取用户时为 null。
	AverageUnlocks *float64 `json:"averageUnlocks"`
}

// DramaReportPage 返回真实数据库分页结果，不将前 50 名误作全部剧目。
type DramaReportPage struct {
	// AdShows 是同应用日期范围展示回调统计，不受名称搜索或分页影响。
	AdShows ReportAdShows `json:"adShows"`
	// Summary 是所有匹配剧目的范围汇总，与分页无关。
	Summary RewardReportSummary `json:"summary"`
	// Items 是当前页核心指标。
	Items []DramaReportRow `json:"items"`
	// Total 是符合剧名或 ID 筛选的共享剧数量，包含零解锁剧。
	Total int64 `json:"total"`
}

// DramaReportRepository 只读共享剧和奖励账本，不创建播放流水。
type DramaReportRepository interface {
	// DramaReport 按显式应用范围读取分页及核心指标。
	DramaReport(context.Context, DramaReportQuery) (*DramaReportPage, error)
}

// DramaReport 保持存储错误和统一 HTTP 映射，当前采集不支持的播放指标不补造。
func (s *ReportingService) DramaReport(ctx context.Context, query DramaReportQuery) (*DramaReportPage, error) {
	if s.dramaReports == nil {
		slog.ErrorContext(ctx, "read drama report unavailable", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	result, err := s.dramaReports.DramaReport(ctx, query)
	if err != nil {
		slog.ErrorContext(ctx, "read drama report failed", "error", err)
	}
	return result, err
}
