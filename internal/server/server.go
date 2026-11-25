// Package server 创建并挂载由 Styx 管理的 Kratos Server。
package server

import (
	"github.com/go-kratos/kratos/v2/transport"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/google/wire"
)

// ProviderSet 声明公开 HTTP Server 与后台任务 Server。
var ProviderSet = wire.NewSet(NewHTTPServer, NewWorkerServer, NewServers)

// NewServers 将具体 Server 投影成 Styx/Kratos 统一管理的传输层列表。
// Server 的创建仍由各自构造函数负责，本函数不包含启动顺序或业务判断。
func NewServers(httpServer *kratoshttp.Server, workerServer *WorkerServer) []transport.Server {
	return []transport.Server{httpServer, workerServer}
}
