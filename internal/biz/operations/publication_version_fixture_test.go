package operations_test

import (
	"context"
	"drama-server/internal/biz/operations"
	"testing"
)

// submitPreparedPublication 显式模拟运营确认待上线，再同步指定版本；不改变生产发布入口的状态门禁。
func submitPreparedPublication(t *testing.T, service *operations.Service, ctx context.Context, dramaID string, input operations.TikTokPublicationInput) (*operations.TikTokPublication, error) {
	t.Helper()
	if _, err := service.PrepareDrama(ctx, dramaID); err != nil {
		return nil, err
	}
	p, err := service.SubmitTikTokPublication(ctx, dramaID, input)
	// 媒资流程测试继续观察已保存的处理事实；同步接口的错误传播由专门的请求测试验证。
	if err != nil && p != nil {
		return p, nil
	}
	return p, err
}
