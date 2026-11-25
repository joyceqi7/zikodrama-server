package operations

import (
	"context"
	"crypto/sha256"
	"drama-server/internal/biz/consumer"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// AdAnalyticsQuery 只查询认证应用最近九十天广告指标，全部条件由 Handler 校验。
type AdAnalyticsQuery struct {
	// From 是 UTC 起始日。
	From string `json:"from"`
	// To 是 UTC 结束日，包含当天。
	To string `json:"to"`
	// AdType 为空时查询激励和插屏。
	AdType string `json:"adType"`
	// AdUnitID 精确匹配广告位。
	AdUnitID string `json:"adUnitId"`
	// Scene 精确匹配固定业务入口。
	Scene string `json:"scene"`
	// ClientVersion 精确匹配 Mini 构建修订。
	ClientVersion string `json:"clientVersion"`
	// OS 精确匹配公开系统类型。
	OS string `json:"os"`
	// TikTokVersion 精确匹配公开客户端版本。
	TikTokVersion string `json:"tiktokVersion"`
	// ErrorCode 匹配调用中任一阶段的数值错误码。
	ErrorCode string `json:"errorCode"`
	// PageSize 限定每页一至一百条。
	PageSize int `json:"-"`
	// BeforeAt、BeforeUser、BeforeID 定位原始调用，迟到结果不改变排序。
	BeforeAt time.Time `json:"-"`
	// BeforeUser 保留字符串精度及稳定排序。
	BeforeUser string `json:"-"`
	// BeforeID 是上一页最后一个调用事实键。
	BeforeID string `json:"-"`
}

// AdSummary 按同一次调用合并各阶段，展示后失败不会抵消已展示。
type AdSummary struct {
	// Calls 是收到的调用事实数，不等于平台请求。
	Calls int64 `json:"calls"`
	// Requests 是实际调用 show 的去重次数。
	Requests int64 `json:"requests"`
	// Shown 是客户端确认展示的次数。
	Shown int64 `json:"shown"`
	// MatchedShown 只计已关联同一调用请求的展示，避免缺失阶段扭曲转化率。
	MatchedShown int64 `json:"matchedShown"`
	// Completed 是激励完播次数。
	Completed int64 `json:"completed"`
	// FailedBeforeShow 是已请求但没有展示确认的明确错误。
	FailedBeforeShow int64 `json:"failedBeforeShow"`
	// FailedAfterShow 保留展示后发生的错误。
	FailedAfterShow int64 `json:"failedAfterShow"`
	// CreateFailures 是未进入 show 的创建或准备错误。
	CreateFailures int64 `json:"createFailures"`
	// Cancelled 是激励未完播或明确用户取消。
	Cancelled int64 `json:"cancelled"`
	// Unknown 是展示请求仍未确认的超时，不计失败。
	Unknown int64 `json:"unknown"`
	// CloseUnknown 是已展示但关闭结果尚未确认的超时。
	CloseUnknown int64 `json:"closeUnknown"`
	// Pending 是仍未收到展示或明确结果的调用。
	Pending int64 `json:"pending"`
	// Skipped 是本地拦截，不能计入请求分母。
	Skipped int64 `json:"skipped"`
	// TimingSamples 只含关联请求且 show Promise 成功的耗时，不把完播补证当开始展示时间。
	TimingSamples int64 `json:"timingSamples"`
	// P50MS 是 show Promise 确认展示的最近秩中位耗时。
	P50MS *float64 `json:"p50Ms"`
	// P95MS 是已展示调用从 show 到确认的最近秩九十五分位耗时。
	P95MS *float64 `json:"p95Ms"`
}

// AdDay 保留请求发起日期，迟到结果更新原日期。
type AdDay struct {
	// Date 使用 UTC 自然日。
	Date string `json:"date"`
	// AdSummary 与总计使用相同关联口径。
	AdSummary
}

// AdErrorCount 按阶段和码聚合，避免把创建频控与展示取消混淆。
type AdErrorCount struct {
	// Stage 是实际失败阶段。
	Stage string `json:"stage"`
	// ErrorCode 是官方数值错误码。
	ErrorCode string `json:"errorCode"`
	// SubErrorCode 是可选官方子码。
	SubErrorCode string `json:"subErrorCode"`
	// Reason 是固定业务分类。
	Reason string `json:"reason"`
	// Count 是去重调用阶段数。
	Count int64 `json:"count"`
}

// AdEventRecord 返回白名单阶段，不返回原始 SDK 对象。
type AdEventRecord struct {
	// Kind 是不可变阶段类型。
	Kind string `json:"kind"`
	// AdMetricDetails 是有界的公开业务维度与数值诊断。
	consumer.AdMetricDetails
	// ReceivedAt 是服务端接收该阶段时间。
	ReceivedAt time.Time `json:"receivedAt"`
}

// AdAnalyticsItem 是同一次广告调用的合并事实。
type AdAnalyticsItem struct {
	// UserID 是内部主体字符串，不是 OpenID。
	UserID string `json:"userId"`
	// EventID 关联各阶段，不用于授予权益。
	EventID string `json:"eventId"`
	// OccurredAt 固定调用日期。
	OccurredAt time.Time `json:"occurredAt"`
	// AdType 区分激励与插屏。
	AdType string `json:"adType"`
	// AdUnitID 标识实际广告位。
	AdUnitID string `json:"adUnitId"`
	// Scene 标识入口。
	Scene string `json:"scene"`
	// ClientVersion 标识 Mini 构建。
	ClientVersion string `json:"clientVersion"`
	// Requested 表示实际进入 show。
	Requested int `json:"requested"`
	// Shown 表示客户端确认展示。
	Shown int `json:"shown"`
	// Completed 表示激励完播。
	Completed int `json:"completed"`
	// Failed 表示任一阶段明确失败，可与 Shown 同时成立。
	Failed int `json:"failed"`
	// Cancelled 表示激励未完播。
	Cancelled int `json:"cancelled"`
	// Unknown 表示曾超时，是否已解决由其他阶段判断。
	Unknown int `json:"unknown"`
	// Closed 表示收到原生关闭。
	Closed int `json:"closed"`
	// Skipped 表示本地拦截。
	Skipped int `json:"skipped"`
	// Events 是按接收顺序排列的阶段事实。
	Events []AdEventRecord `json:"events"`
}

// AdAnalytics 是同一筛选范围下的漏斗、趋势、错误和游标明细。
type AdAnalytics struct {
	// Summary 是该范围总计。
	Summary AdSummary `json:"summary"`
	// Daily 是有采集数据日期的趋势。
	Daily []AdDay `json:"daily"`
	// Errors 最多返回一百项错误分布。
	Errors []AdErrorCount `json:"errors"`
	// Items 是当前游标页。
	Items []AdAnalyticsItem `json:"items"`
	// HasMore 表示仍有后续调用。
	HasMore bool `json:"hasMore"`
	// NextCursor 绑定应用和所有筛选条件。
	NextCursor string `json:"nextCursor"`
	// GeneratedAt 是本次查询时间。
	GeneratedAt time.Time `json:"generatedAt"`
}

// AdAnalyticsRepository 复用指标事实查询，不控制广告或奖励。
type AdAnalyticsRepository interface {
	// ReadAdAnalytics 在认证应用内查询广告指标。
	ReadAdAnalytics(context.Context, AdAnalyticsQuery) (AdAnalytics, error)
}

// AdAnalyticsService 提供独立读取权限下的广告统计。
type AdAnalyticsService struct{ repository AdAnalyticsRepository }

// NewAdAnalyticsService 注入广告指标读端口。
func NewAdAnalyticsService(repository AdAnalyticsRepository) *AdAnalyticsService {
	return &AdAnalyticsService{repository: repository}
}

// ReadAdAnalytics 记录真实仓储故障，统一交由 HTTP 错误目录映射。
func (s *AdAnalyticsService) ReadAdAnalytics(ctx context.Context, q AdAnalyticsQuery) (AdAnalytics, error) {
	if s == nil || s.repository == nil {
		slog.ErrorContext(ctx, "read ad analytics unavailable", "error", ErrInvalidInput)
		return AdAnalytics{}, ErrInvalidInput
	}
	result, err := s.repository.ReadAdAnalytics(ctx, q)
	if err != nil {
		slog.ErrorContext(ctx, "read ad analytics failed", "error", err)
	}
	return result, err
}

// AdAnalyticsBinding 防止换应用或条件后复用游标，不包含权限凭据。
func AdAnalyticsBinding(app string, q AdAnalyticsQuery) string {
	b, _ := json.Marshal(q)
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(app+"|"), b...)))
}

// AdAnalyticsCursor 只编码定位值，应用权限仍由认证中间件决定。
func AdAnalyticsCursor(app string, q AdAnalyticsQuery, last AdAnalyticsItem) string {
	b, _ := json.Marshal([]string{AdAnalyticsBinding(app, q), last.OccurredAt.UTC().Format(time.RFC3339Nano), last.UserID, last.EventID})
	return base64.RawURLEncoding.EncodeToString(b)
}
