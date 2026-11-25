// Package subject 管理短剧和官方发布身份的主体归属，VOD 空间及管理员权限保持全局管理。
package subject

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"drama-server/internal/biz/application"
)

// ErrNotFound 表示主体不存在。
var ErrNotFound = errors.New("subject not found")

// ErrInvalid 表示入口主体字段不满足当前合同。
var ErrInvalid = errors.New("invalid subject input")

// ErrConflict 表示主体名称冲突或试图更换已有资产的发布身份。
var ErrConflict = errors.New("subject conflict")

// ErrNotConfigured 表示选定主体尚未配齐 TikTok 发布方凭据。
var ErrNotConfigured = errors.New("subject publisher not configured")

// Subject 是发布主体；发布方配置缺失不会阻断所属应用的直连播放。
type Subject struct {
	// ID 是数据库分配的主体主键，接口中始终为字符串。
	ID string `json:"id"`
	// Name 是后台显示的主体名称。
	Name string `json:"name"`
	// Remark 说明主体用途。
	Remark string `json:"remark"`
	// OwnerID 保留历史官方资产的拥有方内部标识，不是 applications.id。
	OwnerID string `json:"-"`
	// TikTokAppID 是发布方在开发者平台登记的 AppID。
	TikTokAppID string `json:"tiktokAppId"`
	// ClientKey 是发布方 Client Key，不替代应用自己的登录身份。
	ClientKey string `json:"clientKey"`
	// ClientSecret 仅供服务端调用平台，禁止序列化或记录日志。
	ClientSecret string `json:"-"`
	// ClientSecretConfigured 表示发布密钥已保存，不返回密钥原文。
	ClientSecretConfigured bool `json:"clientSecretConfigured"`
	// CreatedAt 是创建时间。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 是最后修改时间。
	UpdatedAt time.Time `json:"updatedAt"`
}

// Input 是主体维护命令；空发布方可用于尚未接入官方发布的直连应用。
type Input struct {
	// Name 是必填主体名称。
	Name string `json:"name"`
	// Remark 是可选说明。
	Remark string `json:"remark"`
	// VODSpaceName 仅兼容旧后台提交的字段，保存时忽略，空间统一由全局媒资配置决定。
	VODSpaceName string `json:"vodSpaceName"`
	// TikTokAppID 是发布方官方标识，设置后必须与 ClientKey 成对提供。
	TikTokAppID string `json:"tiktokAppId"`
	// ClientKey 是发布方公开客户端标识。
	ClientKey string `json:"clientKey"`
	// ClientSecret 是本次替换的密钥，更新空字符串保留现值。
	ClientSecret string `json:"clientSecret"`
}

// Repository 将主体资料及敏感发布方配置保存在 MySQL。
type Repository interface {
	// ListSubjects 返回主体目录；Secret 仅供服务内部使用，不允许直接输出原始对象。
	ListSubjects(context.Context) ([]Subject, error)
	// FindSubject 按内部主键查询，未找到返回 ErrNotFound。
	FindSubject(context.Context, string) (Subject, error)
	// CreateSubject 插入新主体并回填主键，名称或发布方身份重复返回 ErrConflict。
	CreateSubject(context.Context, *Subject) error
	// UpdateSubject 在事务内保留空密钥与稳定 OwnerID；已有资产时禁止更换发布方身份。
	UpdateSubject(context.Context, Subject) error
}

// Service 提供主体管理和发布方解析；不参与直连消费门禁。
type Service struct{ repo Repository }

// NewService 注入主体权威仓储。
func NewService(repo Repository) *Service { return &Service{repo: repo} }

// List 返回脱敏目录，失败记录实际仓储错误。
func (s *Service) List(ctx context.Context) ([]Subject, error) {
	rows, err := s.repo.ListSubjects(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list subjects failed", "error", err)
		return nil, err
	}
	for i := range rows {
		rows[i] = redact(rows[i])
	}
	return rows, nil
}

// Save 创建或更新主体；HTTP 字段校验由入口完成，密钥保留与已有身份约束在此处理。
func (s *Service) Save(ctx context.Context, id string, input Input) (Subject, error) {
	row := Subject{ID: id, Name: input.Name, Remark: input.Remark, TikTokAppID: input.TikTokAppID, ClientKey: input.ClientKey, ClientSecret: input.ClientSecret}
	hasSecret := row.ClientSecret != ""
	if id != "" {
		old, err := s.repo.FindSubject(ctx, id)
		if err != nil {
			logFailure(ctx, "find subject for update failed", err)
			return Subject{}, err
		}
		row.OwnerID, row.CreatedAt = old.OwnerID, old.CreatedAt
		if row.ClientKey != "" && (row.ClientKey != old.ClientKey || row.TikTokAppID != old.TikTokAppID) && row.ClientSecret == "" {
			slog.WarnContext(ctx, "new subject publisher secret required", "error", ErrNotConfigured)
			return Subject{}, ErrNotConfigured
		}
		hasSecret = hasSecret || old.ClientSecret != ""
	}
	if row.ClientKey != "" && !hasSecret {
		slog.WarnContext(ctx, "subject publisher rejected", "error", ErrNotConfigured)
		return Subject{}, ErrNotConfigured
	}
	var err error
	if id == "" {
		err = s.repo.CreateSubject(ctx, &row)
	} else {
		err = s.repo.UpdateSubject(ctx, row)
	}
	if err != nil {
		logFailure(ctx, "save subject failed", err)
		return Subject{}, err
	}
	row, err = s.repo.FindSubject(ctx, row.ID)
	if err != nil {
		slog.ErrorContext(ctx, "read saved subject failed", "error", err)
		return Subject{}, err
	}
	return redact(row), nil
}

// OwnerID 返回当前明确选定主体的稳定拥有方标识，未选主体时拒绝发布。
func (s *Service) OwnerID(ctx context.Context) (string, error) {
	row, err := s.repo.FindSubject(ctx, application.SubjectIDFromContext(ctx))
	if err != nil {
		logFailure(ctx, "resolve subject publisher failed", err)
		return "", err
	}
	if row.ClientKey == "" || row.ClientSecret == "" || row.TikTokAppID == "" {
		slog.WarnContext(ctx, "subject publisher unavailable", "error", ErrNotConfigured)
		return "", ErrNotConfigured
	}
	return row.OwnerID, nil
}

// Owner 返回当前主体的发布方，必须与记录固化的 OwnerID 一致，不能借用其他主体凭据。
func (s *Service) Owner(ctx context.Context, ownerID string) (application.Application, error) {
	row, err := s.repo.FindSubject(ctx, application.SubjectIDFromContext(ctx))
	if err != nil {
		logFailure(ctx, "load subject publisher failed", err)
		return application.Application{}, err
	}
	if row.OwnerID != ownerID || row.TikTokAppID == "" || row.ClientKey == "" || row.ClientSecret == "" {
		slog.WarnContext(ctx, "subject publisher identity rejected", "error", ErrNotConfigured)
		return application.Application{}, ErrNotConfigured
	}
	return application.Application{SubjectID: row.ID, TikTokAppID: row.TikTokAppID, ClientKey: row.ClientKey, ClientSecret: row.ClientSecret, Status: application.StatusActive}, nil
}

func redact(row Subject) Subject {
	row.ClientSecretConfigured = row.ClientSecret != ""
	row.ClientSecret = ""
	return row
}

// Find 返回指定主体的脱敏资料，用于确认管理请求中的主体仍存在；未找到返回 ErrNotFound。
func (s *Service) Find(ctx context.Context, id string) (Subject, error) {
	row, err := s.repo.FindSubject(ctx, id)
	if err != nil {
		logFailure(ctx, "find subject failed", err)
		return Subject{}, err
	}
	return redact(row), nil
}

func logFailure(ctx context.Context, operation string, err error) {
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) || errors.Is(err, ErrNotConfigured) {
		slog.WarnContext(ctx, operation, "error", err)
		return
	}
	slog.ErrorContext(ctx, operation, "error", err)
}
