package operations

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// RecordAudit 接收审计中间件提供的操作结果，补齐缺省请求 ID 和创建时间后保存。
// 这是非请求体入口，仍检查操作者与操作名；不在此重做原业务操作或改变其响应。
func (s *ReportingService) RecordAudit(ctx context.Context, record AuditRecord) error {
	if s == nil || s.audits == nil || strings.TrimSpace(record.ActorID) == "" || strings.TrimSpace(record.Action) == "" {
		slog.WarnContext(ctx, "record audit rejected", "error", ErrInvalidInput)
		return ErrInvalidInput
	}
	if strings.TrimSpace(record.RequestID) == "" {
		requestID, err := NewAuditRequestID()
		if err != nil {
			slog.ErrorContext(ctx, "new audit request id failed", "error", err)
			return err
		}
		record.RequestID = requestID
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}

	resultErr := s.audits.SaveAuditRecord(ctx, record)
	if resultErr != nil {
		slog.ErrorContext(ctx, "save audit record failed", "error", resultErr)
	}
	return resultErr
}

// NewAuditRequestID 创建不可预测的审计请求标识。
func NewAuditRequestID() (string, error) {
	return secureID("audit")
}

// AuditRecords 按入口已校验的查询数量返回最近的管理端审计记录。
func (s *ReportingService) AuditRecords(ctx context.Context, limit int) ([]AuditRecord, error) {
	if s == nil || s.audits == nil {
		slog.WarnContext(ctx, "audit records rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}

	result1, resultErr := s.audits.ListAuditRecords(ctx, limit)
	if resultErr != nil {
		slog.ErrorContext(ctx, "list audit records failed", "error", resultErr)
	}
	return result1, resultErr
}
