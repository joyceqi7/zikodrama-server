//go:build wireinject
// +build wireinject

// Package main 声明示例应用的 Wire 依赖图；生成源与应用入口位于同一 Go package。
package main

import (
	"context"

	"github.com/go-kratos/kratos/v2/transport"
	"github.com/google/wire"

	configv1 "gitee.com/linpbin/styx/api/config/v1"

	"gitee.com/linpbin/styx/examples/kratos-demo/internal/biz"
	"gitee.com/linpbin/styx/examples/kratos-demo/internal/conf"
	"gitee.com/linpbin/styx/examples/kratos-demo/internal/data"
	"gitee.com/linpbin/styx/examples/kratos-demo/internal/server"
	"gitee.com/linpbin/styx/examples/kratos-demo/internal/service"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

// wireServers 使用同一个 Runtime 构建存储、业务用例和传输层 Server。
// Kratos App 由 Styx Run 创建，不属于业务项目的 Wire 依赖图。
func wireServers(context.Context, *conf.Server, *configv1.DataConfig, *styxruntime.Runtime) ([]transport.Server, func(), error) {
	panic(wire.Build(
		data.ProviderSet,
		biz.ProviderSet,
		service.ProviderSet,
		server.ProviderSet,
		newServers,
	))
}
