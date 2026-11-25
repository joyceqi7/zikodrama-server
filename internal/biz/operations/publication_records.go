package operations

import (
	"context"
	"log/slog"
	"time"
)

// PublicationRecordQuery 是入口校验后的共享审核或上线分页条件。
type PublicationRecordQuery struct {
	// Kind 只允许 reviews 或 releases。
	Kind string
	// Page 从一开始。
	Page int
	// PageSize 最大100条。
	PageSize int
	// DramaID 可精确筛选一部短剧。
	DramaID string
	// Status 筛选业务记录状态。
	Status string
}

// PublicationRecord 是独立审核或上线记录，ID等于不可复用的发布任务ID。
type PublicationRecord struct {
	// ID 关联冻结版本的任务ID，同时是审核记录和上线记录的关联键。
	ID string `json:"id"`
	// DramaID 是共享短剧ID。
	DramaID string `json:"dramaId"`
	// Title 保存提交时标题，避免草稿编辑改变历史名称。
	Title string `json:"title"`
	// Status 是本记录所属流程的状态，审核通过后不会变成上线中。
	Status string `json:"status"`
	// ActorID 是发起该流程的管理员ID，历史记录无法追溯时为空。
	ActorID string `json:"actorId"`
	// ActorName 是操作人的当前昵称或账号，历史用户不存在时为空。
	ActorName string `json:"actorName"`
	// CreatedAt 是首次送审或首次请求上线时间。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 是本记录最近变更时间。
	UpdatedAt time.Time `json:"updatedAt"`
	// Current 表示仍为该剧最新任务，历史记录只读。
	Current bool `json:"current"`
	// TaskStatus 是当前任务状态，仅在Current为真时用于操作门禁。
	TaskStatus string `json:"taskStatus"`
	// TaskStage 是当前任务阶段，不改变已冻结审核结果。
	TaskStage string `json:"taskStage"`
	// Publication 只包含既有管理API允许公开的版本与结果，不含内部快照或凭据。
	Publication TikTokPublication `json:"publication"`
}

// PublicationRecordPage 返回匹配总数与当前页，历史数据不随分页丢弃。
type PublicationRecordPage struct {
	// Items 是按发起时间倒序的记录。
	Items []PublicationRecord `json:"items"`
	// Total 是相同筛选条件的总数。
	Total int64 `json:"total"`
}

// PublicationRecordRepository 读取独立记录表，写入由发布任务事务统一维护。
type PublicationRecordRepository interface {
	// ListPublicationRecords 查询共享记录；query已在Handler校验，不按应用隔离。
	ListPublicationRecords(context.Context, PublicationRecordQuery) (PublicationRecordPage, error)
	// FindPublicationRecord 按记录ID及类别读取，未找到返回内容域ErrNotFound。
	FindPublicationRecord(context.Context, string, string) (PublicationRecord, error)
}

type publicationActorKey struct{}
type expectedPublicationKey struct{}

// WithPublicationActor 由可信管理会话设置人工操作者，供发布记录及短剧操作人持久化复用，Worker 不伪造操作者。
func WithPublicationActor(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, publicationActorKey{}, id)
}

// PublicationActor 从可信请求中读取发起人ID，后台任务返回空字符串。
func PublicationActor(ctx context.Context) string {
	value, _ := ctx.Value(publicationActorKey{}).(string)
	return value
}

// WithExpectedPublication 将历史页操作绑定到明确任务，防止误操作后来的版本。
func WithExpectedPublication(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, expectedPublicationKey{}, id)
}

// ListPublicationRecords 返回共享审核或上线记录，依赖失败保留诊断。
func (s *DramaService) ListPublicationRecords(ctx context.Context, q PublicationRecordQuery) (PublicationRecordPage, error) {
	if s.repositories.Content.Records == nil {
		slog.ErrorContext(ctx, "publication record repository unavailable", "error", ErrPublicationUnavailable)
		return PublicationRecordPage{}, ErrPublicationUnavailable
	}
	result, err := s.repositories.Content.Records.ListPublicationRecords(ctx, q)
	if err != nil {
		slog.ErrorContext(ctx, "list publication records failed", "error", err)
	}
	return result, err
}

// FindPublicationRecord 返回包含官方审核结果的指定历史记录。
func (s *DramaService) FindPublicationRecord(ctx context.Context, kind, id string) (PublicationRecord, error) {
	if s.repositories.Content.Records == nil {
		slog.ErrorContext(ctx, "publication record repository unavailable", "error", ErrPublicationUnavailable)
		return PublicationRecord{}, ErrPublicationUnavailable
	}
	result, err := s.repositories.Content.Records.FindPublicationRecord(ctx, kind, id)
	if err != nil {
		slog.ErrorContext(ctx, "find publication record failed", "error", err)
	}
	return result, err
}
