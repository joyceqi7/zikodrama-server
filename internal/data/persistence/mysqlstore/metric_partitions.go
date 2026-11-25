package mysqlstore

import (
	"context"
	"regexp"
	"time"
)

// metricEventKey 是跨日幂等索引，只保留身份、事实键和首次归属日；与过期明细一起清理。
type metricEventKey struct {
	// ApplicationID 隔离应用。
	ApplicationID string `gorm:"primaryKey"`
	// UserID 隔离认证用户。
	UserID string `gorm:"primaryKey"`
	// EventID 是稳定的事实键。
	EventID string `gorm:"primaryKey"`
	// Kind 区分开始和完成。
	Kind string `gorm:"primaryKey"`
	// EventDate 保存首次接受的 UTC 日期，重试不更新。
	EventDate string
}

// TableName 返回精简去重索引表。
func (metricEventKey) TableName() string { return "metric_event_keys" }

// DashboardCounters 是月表使用的每日统计列，数据库字段映射只留在持久化层。
type DashboardCounters struct {
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
	TikTokPostbackAttempts int64 `json:"tiktokPostbackAttempts" gorm:"column:tiktok_postback_attempts;not null;default:0;comment:TikTok关键行为回传尝试次数"`
	// TikTokPostbackSuccesses 是官方 SDK 明确确认成功的回传次数。
	TikTokPostbackSuccesses int64 `json:"tiktokPostbackSuccesses" gorm:"column:tiktok_postback_successes;not null;default:0;comment:TikTok SDK确认回传成功次数"`
	// TikTokPostbackFailures 是能力缺失、调用异常或官方拒绝的回传次数。
	TikTokPostbackFailures int64 `json:"tiktokPostbackFailures" gorm:"column:tiktok_postback_failures;not null;default:0;comment:TikTok关键行为回传失败次数"`
	// VIPActivations 是 VIP 权益授予次数。
	VIPActivations int64 `json:"vipActivations" gorm:"column:vip_activations"`
}

// dashboardSnapshotModel 在月表内按应用、UTC 日期保存统计，原始明细过期后仍可读取。
type dashboardSnapshotModel struct {
	// ApplicationID 是应用内部主键。
	ApplicationID string `gorm:"primaryKey"`
	// DashboardCounters 包含每日账本和行为计数。
	DashboardCounters `gorm:"embedded"`
	// MonthlyActiveUsers 是截至当日的 30 日去重快照；缺少完整窗口时为空。
	MonthlyActiveUsers *int64
	// UpdatedAt 是这份汇总实际生成的时间。
	UpdatedAt time.Time
}

// TableName 返回汇总月表模板。
func (dashboardSnapshotModel) TableName() string { return "dashboard_daily_template" }

// metricSummaryState 记录应用刷新时间与首次采集时间，并串行化多实例汇总。
type metricSummaryState struct {
	// ApplicationID 是当前应用。
	ApplicationID string `gorm:"primaryKey"`
	// RefreshedAt 是最近一次成功完成汇总的时间。
	RefreshedAt *time.Time
	// FirstReceivedAt 保留首次采集时间，不随明细清理丢失。
	FirstReceivedAt *time.Time
	// RefreshGeneration 每次领取递增，阻止失去租约的旧任务发布结果。
	RefreshGeneration uint64
	// RefreshOwner 是当前刷新任务的随机身份，不作为业务或用户标识。
	RefreshOwner string
	// RefreshLeaseUntil 使用 MySQL 时钟判断刷新任务是否仍可续期及提交。
	RefreshLeaseUntil *time.Time
	// SnapshotAt 保留计算窗口基准，RefreshedAt 则记录计算完成时间。
	SnapshotAt *time.Time
}

// TableName 返回应用汇总状态表。
func (metricSummaryState) TableName() string { return "metric_summary_state" }

var metricDayPattern = regexp.MustCompile(`^metric_events_[0-9]{8}$`)
var metricMonthPattern = regexp.MustCompile(`^dashboard_daily_[0-9]{6}$`)

// metricCutoff 保留今天和此前 89 个 UTC 自然日。
func metricCutoff(now time.Time) time.Time {
	return now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -89)
}

// metricCutoffNow 返回当前保留窗口首日，便于只读查询与清理共享同一口径。
func metricCutoffNow() time.Time { return metricCutoff(time.Now().UTC()) }

// metricTomorrow 返回当前 UTC 日的排他性上界。
func metricTomorrow() time.Time            { return time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 1) }
func metricDayTable(at time.Time) string   { return "metric_events_" + at.UTC().Format("20060102") }
func metricMonthTable(at time.Time) string { return "dashboard_daily_" + at.UTC().Format("200601") }

// metricTables 仅发现当前数据库中格式和日期均合法的分表，拒绝把任意表名拼入查询。
func (s *Store) metricTables(ctx context.Context, monthly bool) ([]string, error) {
	var candidates []string
	prefix := "metric_events_"
	pattern := metricDayPattern
	layout := "20060102"
	if monthly {
		prefix = "dashboard_daily_"
		pattern = metricMonthPattern
		layout = "200601"
	}
	if err := s.data.DB().WithContext(ctx).Table("information_schema.tables").Where("table_schema=DATABASE() AND table_name LIKE ?", prefix+"%").Order("table_name").Pluck("table_name", &candidates).Error; err != nil {
		return nil, err
	}
	tables := []string{}
	for _, name := range candidates {
		if pattern.MatchString(name) {
			if _, err := time.Parse(layout, name[len(prefix):]); err == nil {
				tables = append(tables, name)
			}
		}
	}
	return tables, nil
}
