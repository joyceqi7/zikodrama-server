package consumer_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	domain "drama-server/internal/biz/consumer"
)

type launchReadFailureStore struct {
	domain.Store
	cause error
}

// FindDrama 返回指定目录故障，区分预期深链失效与依赖异常。
func (s launchReadFailureStore) FindDrama(context.Context, string) (*domain.Drama, error) {
	return nil, s.cause
}

// FindDramaDetail 与指定集查询返回相同故障，覆盖无分集的落地入口。
func (s launchReadFailureStore) FindDramaDetail(context.Context, string) (*domain.Drama, error) {
	return nil, s.cause
}

// TestLaunchContentFailureLogging 验证失效链接正常回退不误报 ERROR，数据库故障仍带实际错误返回。
func TestLaunchContentFailureLogging(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, episode := range []string{"", "301"} {
		for _, cause := range []error{domain.ErrNotFound, errors.New("launch catalogue database offline")} {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			service := domain.NewService(launchReadFailureStore{cause: cause}, nil)
			target, err := service.ResolveLaunchTarget(t.Context(), "201", episode)
			if cause == domain.ErrNotFound {
				if err != nil || target != nil || strings.Contains(logs.String(), `"level":"ERROR"`) {
					t.Fatal("失效链接错误阻塞启动或误报", err)
				}
			} else if !errors.Is(err, cause) || !strings.Contains(logs.String(), `"level":"ERROR"`) {
				t.Fatal("落地依赖故障被吞掉", err)
			}
			if !strings.Contains(logs.String(), "find drama failed") || !strings.Contains(logs.String(), cause.Error()) {
				t.Fatal("目录失败未保留操作与实际错误")
			}
		}
	}
}
