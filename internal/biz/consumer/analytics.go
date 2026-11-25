package consumer

import (
	"context"
	"log/slog"
	"time"
)

// MetricEvent 是运营计数信号；可选回传诊断只短期保存，不能用于权益判定或用户观看历史。
type MetricEvent struct {
	// ID 固定一次事实的去重键；同一事实重试不得重新生成。
	ID string `json:"id"`
	// Kind 是入口白名单允许的事实类型。
	Kind string `json:"kind"`
	// OccurredAt 是服务器首次接收该统计请求的 UTC 时刻；客户端观察时间不参与日期或窗口校验。
	OccurredAt time.Time `json:"occurredAt"`
	// Postback 仅用于官方事件排障，旧客户端无需传入。
	Postback *PostbackDetails `json:"postback,omitempty"`
	// Ad 是可选的广告调用诊断，旧 Mini 不需传入。
	Ad *AdMetricDetails `json:"ad,omitempty"`
}

// AnalyticsRepository 只接收可信应用、桶和用户身份下的运营信号。
type AnalyticsRepository interface {
	// RecordMetrics 原子保存一批信号，按当前用户、事件 ID 和类型幂等；不保存播放记录或授予权益。
	RecordMetrics(context.Context, string, []MetricEvent) error
}

// AnalyticsService 编排异步指标落库，与播放授权和奖励事务无关。
type AnalyticsService struct{ repository AnalyticsRepository }

// NewAnalyticsService 只注入指标写端口，不依赖消费者完整仓库。
func NewAnalyticsService(repository AnalyticsRepository) *AnalyticsService {
	return &AnalyticsService{repository: repository}
}

// Record 保存入口已校验的事实；实际存储故障保留日志并传播给重试客户端。
func (s *AnalyticsService) Record(ctx context.Context, userID string, events []MetricEvent) error {
	err := s.repository.RecordMetrics(ctx, userID, events)
	if err != nil {
		slog.ErrorContext(ctx, "record consumer metrics failed", "error", err)
	}
	return err
}

// PostbackDetails 保存提交 SDK 的脱敏白名单参数与调用结果，不代表 TikTok 服务端入库。
type PostbackDetails struct {
	// Scene 是有限业务入口分类，不包含页面 URL。
	Scene string `json:"scene"`
	// ClientVersion 是 Mini 构建修订。
	ClientVersion string `json:"clientVersion"`
	// Params 只允许官方事件业务字段，OpenID 必须替换成固定占位符。
	Params map[string]any `json:"params"`
	// Warnings 是上下文不足的固定分类。
	Warnings []string `json:"warnings"`
	// ElapsedMS 是调用至结果的单调时钟耗时，未调用时省略。
	ElapsedMS *float64 `json:"elapsedMs,omitempty"`
	// Reason 是失败、未知或跳过的固定原因。
	Reason string `json:"reason,omitempty"`
	// ErrorCode 是官方数值错误码。
	ErrorCode string `json:"errorCode,omitempty"`
	// SubErrorCode 是官方可选数值子码。
	SubErrorCode string `json:"subErrorCode,omitempty"`
	// ErrorMessage 是二次脱敏且截断的消息，不保留 SDK 原始对象。
	ErrorMessage string `json:"errorMessage,omitempty"`
}
