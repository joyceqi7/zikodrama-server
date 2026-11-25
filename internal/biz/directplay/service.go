package directplay

import (
	"context"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"errors"
	"log/slog"
)

// Service 同步冻结并发布当前已保存内容，事务提交后失效可回源的目录缓存。
type Service struct {
	repo       Repository
	invalidate func(context.Context)
}

// NewService 组装独立发布用例。
func NewService(repo Repository, invalidate func(context.Context)) *Service {
	return &Service{repo, invalidate}
}

// Workbench 读取独立状态与共享草稿。
func (s *Service) Workbench(ctx context.Context, id string) (Workbench, error) {
	w, err := s.repo.Workbench(ctx, id)
	if err != nil {
		slog.ErrorContext(ctx, "read direct publication workbench failed", "drama_id", id, "error", err)
	}
	return w, err
}

// List 列出独立发布工作台。
func (s *Service) List(ctx context.Context) ([]Workbench, error) {
	rows, err := s.repo.List(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list direct publications failed", "error", err)
	}
	return rows, err
}

// Operation 查询持久化的上下架结果，不启动后台处理。
func (s *Service) Operation(ctx context.Context, id string) (Operation, error) {
	op, err := s.repo.Operation(ctx, id)
	if err != nil {
		if errors.Is(err, consumer.ErrNotFound) {
			slog.WarnContext(ctx, "read direct operation rejected", "error", err)
		} else {
			slog.ErrorContext(ctx, "read direct operation failed", "operation_id", id, "error", err)
		}
	}
	return op, err
}

// Submit 同步发布当前已保存内容或下架；仅事务成功后失效缓存，失败保留原版本与状态。
func (s *Service) Submit(ctx context.Context, id string, c Command, actor string) (Operation, error) {
	op, err := s.repo.Accept(ctx, id, c, actor)
	if err != nil {
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrInvalid) || errors.Is(err, consumer.ErrNotFound) || errors.Is(err, homepage.ErrDramaRecommended) {
			slog.WarnContext(ctx, "direct publication submit rejected", "drama_id", id, "error", err)
		} else {
			slog.ErrorContext(ctx, "direct publication submit failed", "drama_id", id, "error", err)
		}
	}
	if err == nil && s.invalidate != nil {
		s.invalidate(ctx)
	}
	return op, err
}
