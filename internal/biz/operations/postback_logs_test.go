package operations_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"drama-server/internal/biz/operations"
)

type postbackLogRepository struct{ err error }

// ListTikTokPostbackLogs 返回固定空页或注入错误，用于验证业务层错误传播与日志。
func (r postbackLogRepository) ListTikTokPostbackLogs(context.Context, operations.TikTokPostbackLogQuery) (operations.TikTokPostbackLogPage, error) {
	return operations.TikTokPostbackLogPage{Items: []operations.TikTokPostbackLog{}, PageSize: 20}, r.err
}

// TestTikTokPostbackLogServicePreservesRepositoryFailure 验证查询失败记录实际原因且保持错误传播。
func TestTikTokPostbackLogServicePreservesRepositoryFailure(t *testing.T) {
	expected := errors.New("postback storage unavailable")
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	service := operations.NewTikTokPostbackLogService(postbackLogRepository{err: expected})
	_, err := service.ListTikTokPostbackLogs(context.Background(), operations.TikTokPostbackLogQuery{PageSize: 20})
	if !errors.Is(err, expected) || !strings.Contains(logs.String(), "list TikTok postback logs failed") || !strings.Contains(logs.String(), expected.Error()) {
		t.Fatalf("错误传播或日志不完整: err=%v logs=%s", err, logs.String())
	}
}
