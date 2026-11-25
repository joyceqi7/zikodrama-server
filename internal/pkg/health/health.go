// Package health 定义服务就绪检查在 data 与 service 之间共享的最小契约。
package health

import "context"

// Check 检查当前实例依赖是否就绪；返回 nil 表示可以继续接收请求。
type Check func(context.Context) error
