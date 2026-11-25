// Package main 演示业务项目只装配 Server，Kratos App 与进程生命周期由 Styx 创建。
package main

import (
	"context"
	"flag"

	"gitee.com/linpbin/styx"
	styxconfig "gitee.com/linpbin/styx/config"
	"gitee.com/linpbin/styx/examples/kratos-demo/internal/conf"
)

var configPath = flag.String("conf", "configs/config.yaml", "config path")

func main() {
	flag.Parse()
	var bootstrap conf.Bootstrap
	styxconfig.MustLoadFile(*configPath, &bootstrap)
	styx.MustRun(
		context.Background(),
		styx.Config{App: bootstrap.App, OTel: bootstrap.Otel},
		newServerBuilder(&bootstrap),
	)
}
