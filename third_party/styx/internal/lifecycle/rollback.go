// Package lifecycle 提供基础设施初始化失败时的内部回滚工具。
package lifecycle

import (
	"context"
	"errors"
	"time"

	styxruntime "gitee.com/linpbin/styx/runtime"
)

const rollbackTimeout = 5 * time.Second

// Rollback 使用独立有界 Context 回收尚未转交 Runtime 的资源，并保留安全错误分类。
func Rollback(name string, shutdown styxruntime.ShutdownFunc, cause error) error {
	if shutdown == nil {
		return cause
	}
	ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		return errors.Join(cause, styxruntime.NewError(name, "rollback", styxruntime.KindLifecycle, errors.Is(err, context.DeadlineExceeded), err))
	}
	return cause
}
