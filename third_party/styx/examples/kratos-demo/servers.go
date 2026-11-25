package main

import (
	"context"

	"github.com/go-kratos/kratos/v2/transport"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/go-kratos/kratos/v2/transport/http"

	"gitee.com/linpbin/styx"
	"gitee.com/linpbin/styx/examples/kratos-demo/internal/conf"
)

// newServerBuilder 绑定示例配置，并把 Wire 生成的 Server 图交给 Styx。
func newServerBuilder(bootstrap *conf.Bootstrap) styx.ServerBuilder {
	return func(ctx context.Context, runtime *styx.Runtime) ([]transport.Server, func(), error) {
		return wireServers(ctx, bootstrap.Server, bootstrap.Data, runtime)
	}
}

// newServers 把具体 HTTP/gRPC Server 投影为 Styx 可统一启动的传输层列表。
func newServers(grpcServer *grpc.Server, httpServer *http.Server) []transport.Server {
	return []transport.Server{grpcServer, httpServer}
}
