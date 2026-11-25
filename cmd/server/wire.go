//go:build wireinject

package main

import (
	"context"

	"gitee.com/linpbin/styx"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/google/wire"

	"drama-server/internal/biz"
	conf "drama-server/internal/conf"
	"drama-server/internal/data"
	"drama-server/internal/server"
	"drama-server/internal/service"
)

// wireServers 声明服务唯一的编译期依赖图。
// Kratos App 与 Runtime 生命周期由 Styx 管理，业务项目只装配业务、数据和 Server。
func wireServers(context.Context, *conf.Bootstrap, *styx.Runtime) ([]transport.Server, func(), error) {
	panic(wire.Build(
		wire.FieldsOf(new(*conf.Bootstrap), "Server", "Data"),
		conf.NewConfig,
		data.ProviderSet,
		biz.ProviderSet,
		service.ProviderSet,
		server.ProviderSet,
	))
}
