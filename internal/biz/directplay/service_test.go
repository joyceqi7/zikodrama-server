package directplay

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
)

// failingAcceptRepository 在事务提交边界注入故障，其他仓储方法不参与本用例。
type failingAcceptRepository struct {
	Repository
	err error
}

// Accept 返回明确的事务结果，不模拟部分成功或修改输入。
func (r failingAcceptRepository) Accept(_ context.Context, _ string, c Command, _ string) (Operation, error) {
	return Operation{Command: c, Status: "succeeded"}, r.err
}

// TestDirectSubmissionLogsAndInvalidates 验证实际故障有诊断、业务拒绝不误报且失败不失效线上缓存。
func TestDirectSubmissionLogsAndInvalidates(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	for _, tc := range []struct {
		name, level string
		err         error
	}{
		{name: "提交成功不误报"},
		{name: "修订冲突", level: "WARN", err: ErrConflict},
		{name: "版本不满足门禁", level: "WARN", err: ErrInvalid},
		{name: "短剧未找到", level: "WARN", err: consumer.ErrNotFound},
		{name: "仍在轮播或热播", level: "WARN", err: homepage.ErrDramaRecommended},
		{name: "实际事务故障", level: "ERROR", err: errors.New("UPDATE direct_publications: transaction aborted")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			invalidated := 0
			s := NewService(failingAcceptRepository{err: tc.err}, func(context.Context) { invalidated++ })
			command := Command{RequestID: "private-request", Kind: "publish"}
			result, err := s.Submit(context.Background(), "201", command, "private-actor")
			if !errors.Is(err, tc.err) || result.Command != command {
				t.Fatalf("错误或原始操作结果改变：%v", err)
			}
			if tc.err == nil {
				if output.Len() != 0 || invalidated != 1 {
					t.Fatal("成功路径误报或未失效缓存")
				}
				return
			}
			log := output.String()
			if invalidated != 0 || !strings.Contains(log, `"level":"`+tc.level+`"`) || !strings.Contains(log, "direct publication submit") || !strings.Contains(log, tc.err.Error()) {
				t.Fatalf("失败传播、日志或缓存行为错误：%s", log)
			}
			if strings.Count(log, "\n") != 1 || strings.Contains(log, "private-request") || strings.Contains(log, "private-actor") || strings.Contains(log, `"command"`) {
				t.Fatal("重复日志或未经筛选的输入泄漏")
			}
		})
	}
}

// operationResultRepository 只替换持久化结果读取，验证主体隔离拒绝与实际故障的日志级别。
type operationResultRepository struct {
	Repository
	err error
}

// Operation 返回预设的存储结果，不读取或输出其他主体的数据。
func (r operationResultRepository) Operation(context.Context, string) (Operation, error) {
	return Operation{}, r.err
}

// TestDirectOperationLogs 验证跨主体未找到按业务拒绝记录，存储故障保留实际错误，成功不误报。
func TestDirectOperationLogs(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	for _, tc := range []struct {
		name, level string
		err         error
	}{{name: "成功"}, {name: "其他主体不可读", level: "WARN", err: consumer.ErrNotFound}, {name: "存储故障", level: "ERROR", err: errors.New("SELECT direct_publication_operations: connection reset")}} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			_, err := NewService(operationResultRepository{err: tc.err}, nil).Operation(context.Background(), "123")
			if !errors.Is(err, tc.err) {
				t.Fatal("读取错误传播改变")
			}
			if tc.err == nil {
				if output.Len() != 0 {
					t.Fatal("成功路径误报日志")
				}
				return
			}
			if !strings.Contains(output.String(), `"level":"`+tc.level+`"`) || !strings.Contains(output.String(), tc.err.Error()) || strings.Count(output.String(), "\n") != 1 {
				t.Fatal("读取失败日志缺少操作、级别或实际错误")
			}
		})
	}
}
