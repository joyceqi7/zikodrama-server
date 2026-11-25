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

type consumerUserRepository struct{ err error }

// ListConsumerUsers 返回可控故障，验证业务层保留错误和记录诊断。
func (r consumerUserRepository) ListConsumerUsers(context.Context, operations.ConsumerUserQuery) (operations.ConsumerUserPage, error) {
	return operations.ConsumerUserPage{Items: []operations.ConsumerUser{}, PageSize: 20}, r.err
}

func TestConsumerUserListLogging(t *testing.T) {
	var output bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(old)
	failure := errors.New("consumer database disconnected")
	for _, err := range []error{nil, failure} {
		output.Reset()
		service := operations.NewConsumerUserService(consumerUserRepository{err: err})
		_, got := service.ListConsumerUsers(context.Background(), operations.ConsumerUserQuery{PageSize: 20})
		if !errors.Is(got, err) {
			t.Fatal("错误传播发生变化")
		}
		if err == nil && output.Len() != 0 {
			t.Fatal("成功查询不应输出错误")
		}
		if err != nil && (!strings.Contains(output.String(), "list consumer users failed") || !strings.Contains(output.String(), failure.Error()) || !strings.Contains(output.String(), `"level":"ERROR"`)) {
			t.Fatal("失败日志缺少操作或实际错误")
		}
	}
}
