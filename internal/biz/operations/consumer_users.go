package operations

import (
	"context"
	"encoding/base64"
	"log/slog"
	"strings"
	"time"
)

// ConsumerUser 是运营列表可见的观众资料，不包含平台身份摘要或凭据。
type ConsumerUser struct {
	// ID 是 号段用户主键的十进制字符串。
	ID string `json:"id"`
	// ApplicationID 是用户所属应用的内部主键字符串。
	ApplicationID string `json:"applicationId"`
	// IdentityType 区分已绑定 TikTok 的用户与开发环境访客。
	IdentityType string `json:"identityType"`
	// CreatedAt 是首次创建时间，不代表最近登录时间。
	CreatedAt time.Time `json:"createdAt"`
}

// ConsumerUserQuery 接收入口校验后的游标与用户 ID 精确筛选。
type ConsumerUserQuery struct {
	// PageSize 每页 1 至 100 条。
	PageSize int
	// UserID 为空时查询全部，否则按规范十进制主键精确匹配。
	UserID string
	// AfterCreatedAt 是上一页最后一条记录的注册时间，零值表示首页。
	AfterCreatedAt time.Time
	// AfterID 是相同注册时间下的排他性用户主键边界。
	AfterID string
}

// ConsumerUserPage 返回游标分页结果，不执行跨分片总数统计。
type ConsumerUserPage struct {
	// Items 按创建时间及数值 ID 倒序排列，空列表使用空数组。
	Items []ConsumerUser `json:"items"`
	// HasMore 表示当前读取时还有后续记录。
	HasMore bool `json:"hasMore"`
	// NextCursor 绑定应用和筛选条件，仅在还有后续记录时返回。
	NextCursor string `json:"nextCursor"`
	// PageSize 回显每页数量。
	PageSize int `json:"pageSize"`
}

// ConsumerUserCursor 编码下一页边界并绑定应用和精确筛选；游标只用于定位，不授予数据访问权限。
func ConsumerUserCursor(applicationID, userID string, last ConsumerUser) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join([]string{applicationID, userID, last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID}, "|")))
}

// ConsumerUserRepository 提供应用范围的观众查询，不允许修改登录身份。
type ConsumerUserRepository interface {
	// ListConsumerUsers 聚合上下文应用的用户分片并全局排序分页；未找到返回空页，不返回身份摘要。
	ListConsumerUsers(context.Context, ConsumerUserQuery) (ConsumerUserPage, error)
}

// ConsumerUserService 提供后台只读观众查询。
type ConsumerUserService struct {
	repository ConsumerUserRepository
}

// NewConsumerUserService 注入观众查询端口，独立于管理员账号维护。
func NewConsumerUserService(repository ConsumerUserRepository) *ConsumerUserService {
	return &ConsumerUserService{repository: repository}
}

// ListConsumerUsers 查询可信应用上下文中的观众，入口负责字段校验，仓储错误保留原始原因。
func (s *ConsumerUserService) ListConsumerUsers(ctx context.Context, input ConsumerUserQuery) (ConsumerUserPage, error) {
	if s == nil || s.repository == nil {
		slog.ErrorContext(ctx, "list consumer users unavailable", "error", ErrInvalidInput)
		return ConsumerUserPage{}, ErrInvalidInput
	}
	result, err := s.repository.ListConsumerUsers(ctx, input)
	if err != nil {
		slog.ErrorContext(ctx, "list consumer users failed", "error", err)
	}
	return result, err
}
