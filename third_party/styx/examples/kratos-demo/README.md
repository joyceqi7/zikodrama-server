# Kratos Demo

该示例直接复用 Styx 生成的 `AppInfo`、`OTelConfig`、`HTTPServerConfig`、`GRPCServerConfig` 和 `DataConfig`，不再维护一份会漂移的手写字段副本。

运行：

```bash
go run ./examples/kratos-demo -conf examples/kratos-demo/configs/config.yaml
```

默认配置不连接外部存储，会真实创建并启动 HTTP `:8000` 与 gRPC `:9000` Server。示例服务尚未包含业务 Protobuf API，因此只演示组合根、运行依赖、生命周期和 Server 工厂接线。

关键接线：

```go
styx.MustRun(ctx, styx.Config{App: config.App, OTel: config.Otel},
    newServerBuilder(config),
)
```

如需数据库，在 `data.mysql` 或 `data.redis` 下增加配置；连接自动纳入同一个 `Runtime`。
builder 只装配 HTTP/gRPC Server；Kratos App 由 Styx 创建。`MustRun` 在 Kratos 停止后先
执行 Wire cleanup，再统一逆序关闭 Runtime 资源。
