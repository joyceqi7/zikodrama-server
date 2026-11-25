package operations

import (
	"context"
	"crypto/sha256"
	"drama-server/internal/biz/consumer"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// TikTokPostbackLog 是已脱敏的 TikTok 关键行为回传明细，不包含真实 OpenID、凭据或 SDK 原始对象。
type TikTokPostbackLog struct {
	// UserID 是发起回传的内部用户主键，按字符串返回以保留精度。
	UserID string `json:"userId"`
	// EventID 唯一标识一次回传事实；底层 attempt 与最终结果在查询时合并。
	EventID string `json:"eventId"`
	// Event 是 TikTok 官方关键行为名。
	Event string `json:"event"`
	// Outcome 是事实的当前结果；允许等待、接受、失败、未知或未调用。
	Outcome string `json:"outcome"`
	// OccurredAt 是客户端观察到该事实的 UTC 时间。
	OccurredAt time.Time `json:"occurredAt"`
	// ReceivedAt 是服务端首次接收该事实的 UTC 时间。
	ReceivedAt time.Time `json:"receivedAt"`
	// Details 是七天内采集的脱敏 SDK 参数与结果，历史计数没有此字段。
	Details *consumer.PostbackDetails `json:"details,omitempty"`
}

// TikTokPostbackLogQuery 是入口校验后的应用内回传日志游标查询。
type TikTokPostbackLogQuery struct {
	// PageSize 每页 1 至 100 条。
	PageSize int
	// Event 为空时查询全部，否则限定一个官方事件名。
	Event string
	// Outcome 为空时查询全部，否则限定 attempt、success、failure、unknown 或 skipped。
	Outcome string
	// Scene 限定固定业务入口，空值不筛选。
	Scene string
	// ClientVersion 限定 Mini 构建修订。
	ClientVersion string
	// ErrorCode 限定官方数值错误码。
	ErrorCode string
	// From 与 Through 为可选 UTC 闭区间日期，最长九十天。
	From string
	// Through 为日期范围结束日。
	Through string
	// BeforeReceivedAt 是上一页最后一条记录的服务端接收时间。
	BeforeReceivedAt time.Time
	// BeforeUserID、BeforeEventID 与 BeforeKind 共同消除相同接收时间下的排序歧义；BeforeKind 使用合并后的最终结果。
	BeforeUserID  string
	BeforeEventID string
	BeforeKind    string
}

// TikTokPostbackLogPage 返回不执行总数统计的稳定游标页。
type TikTokPostbackLogPage struct {
	// Items 按服务端首次接收时间倒序排列，每个事实 ID 只出现一次，空页使用空数组。
	Items []TikTokPostbackLog `json:"items"`
	// HasMore 表示当前筛选条件下仍有后续记录。
	HasMore bool `json:"hasMore"`
	// NextCursor 绑定应用与筛选条件，仅在还有后续记录时返回。
	NextCursor string `json:"nextCursor"`
	// PageSize 回显每页数量。
	PageSize int `json:"pageSize"`
}

// TikTokPostbackLogCursor 编码下一页边界；游标只用于定位，不授予应用访问权限。
func TikTokPostbackLogCursor(applicationID string, query TikTokPostbackLogQuery, last TikTokPostbackLog) string {
	kind := postbackMetricKind(last.Event, last.Outcome)
	parts := []string{applicationID, query.Event, query.Outcome, last.ReceivedAt.UTC().Format(time.RFC3339Nano), last.UserID, last.EventID, kind}
	if binding := TikTokPostbackFilterBinding(query); binding != "" {
		parts = append(parts, binding)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, "|")))
}

// TikTokPostbackLogRepository 提供应用隔离的回传日志只读查询。
type TikTokPostbackLogRepository interface {
	// ListTikTokPostbackLogs 跨保留期日表查询回传明细，返回经白名单校验的短期诊断，不返回平台身份或完整 SDK 对象。
	ListTikTokPostbackLogs(context.Context, TikTokPostbackLogQuery) (TikTokPostbackLogPage, error)
}

// TikTokPostbackLogService 提供后台回传日志只读用例。
type TikTokPostbackLogService struct{ repository TikTokPostbackLogRepository }

// NewTikTokPostbackLogService 注入回传日志读取端口。
func NewTikTokPostbackLogService(repository TikTokPostbackLogRepository) *TikTokPostbackLogService {
	return &TikTokPostbackLogService{repository: repository}
}

// ListTikTokPostbackLogs 查询当前应用的脱敏回传明细，仓储失败保留实际原因并向上返回。
func (s *TikTokPostbackLogService) ListTikTokPostbackLogs(ctx context.Context, query TikTokPostbackLogQuery) (TikTokPostbackLogPage, error) {
	if s == nil || s.repository == nil {
		slog.ErrorContext(ctx, "list TikTok postback logs unavailable", "error", ErrInvalidInput)
		return TikTokPostbackLogPage{}, ErrInvalidInput
	}
	result, err := s.repository.ListTikTokPostbackLogs(ctx, query)
	if err != nil {
		slog.ErrorContext(ctx, "list TikTok postback logs failed", "error", err)
	}
	return result, err
}

// postbackMetricKind 把对外事件与结果映射为内部计数类型；调用方只传入口白名单值。
func postbackMetricKind(event, outcome string) string {
	prefix := map[string]string{"video_play_request": "tt_video_request", "ep_play": "tt_episode_play", "minis_ad_show": "tt_ad_show"}[event]
	if prefix == "" || outcome == "" {
		return ""
	}
	return prefix + "_" + outcome
}

// TikTokPostbackFilterBinding 把扩展筛选绑定到游标，旧版无扩展条件保留七段格式。
func TikTokPostbackFilterBinding(query TikTokPostbackLogQuery) string {
	if query.Scene == "" && query.ClientVersion == "" && query.ErrorCode == "" && query.From == "" && query.Through == "" {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join([]string{query.Scene, query.ClientVersion, query.ErrorCode, query.From, query.Through}, "|"))))
}
