// Command server 启动 Drama 短剧业务服务。
package main

import (
	"context"
	"flag"

	"gitee.com/linpbin/styx"
	styxconfig "gitee.com/linpbin/styx/config"
	"github.com/go-kratos/kratos/v2/transport"

	config "drama-server/internal/conf"
)

var configPath = flag.String("conf", "configs/config.yaml", "配置文件路径")

// main 读取启动配置，再把日志策略和进程生命周期交给 Styx。
func main() {
	flag.Parse()
	bootstrap, err := loadBootstrap(*configPath)
	if err != nil {
		panic(err)
	}
	styx.MustRun(context.Background(), styx.Config{
		App: bootstrap.GetApp(), OTel: bootstrap.GetOtel(), Registry: bootstrap.GetRegistry(),
	}, newServerBuilder(bootstrap))
}

// loadBootstrap 只读取指定配置文件，不允许进程环境覆盖业务与日志参数。
func loadBootstrap(path string) (*config.Bootstrap, error) {
	var bootstrap config.Bootstrap
	if err := styxconfig.LoadFile(path, &bootstrap); err != nil {
		return nil, err
	}
	return &bootstrap, nil
}

// newServerBuilder 只绑定启动配置；具体业务依赖由 Wire 生成的唯一依赖图装配。
func newServerBuilder(bootstrap *config.Bootstrap) styx.ServerBuilder {
	return func(ctx context.Context, runtime *styx.Runtime) ([]transport.Server, func(), error) {
		return wireServers(ctx, bootstrap, runtime)
	}
}
