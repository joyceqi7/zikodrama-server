# Styx

Styx 是面向 Kratos 服务的 Go 运行时与基础设施适配库。当前版本要求 Go 1.25+；`Runtime` 统一持有不可变服务身份、Logger、TracerProvider、MeterProvider、Propagator、注册发现能力和资源生命周期。

## 设计约束

- 网络工厂必须接收调用方 `context.Context`，初始化超时和取消由应用控制。
- 默认日志不采集 HTTP query、RPC request/reply、SQL、Redis 参数、panic 值或堆栈；HTTP access 日志采集实际读取或写出的正文前 4 KiB，正文不脱敏。
- TLS、明文 gRPC、gRPC Reflection、SASL 和云凭据必须显式配置。
- 基础设施工厂接收同一个 `Runtime`，成功创建的资源自动纳入逆序关闭流程。
- `styx.New` 默认不修改 `slog.Default` 和 OpenTelemetry 全局 Provider；只有应用组合根可显式调用 `runtime.InstallGlobals()`。Nacos SDK 只提供进程级 Logger，创建 Registry 时由单个 Runtime 独占该受控例外，并在 Client 完成关闭后恢复原值。
- `config.LoadFile` / `config.MustLoadFile` 管理 Kratos 文件配置源的创建、加载、扫描和关闭，应用入口只保留自己的配置 schema。
- HTTP 仅开放追加 Filter 和注入 Listener；调用方不能替换 Styx 的恢复与治理链路。
- 消费者只有在业务处理成功后才提交 Kafka 位点。

## 应用组合根

```go
styx.MustRun(ctx, styx.Config{
    App: appConfig,
    OTel: otelConfig,
    Registry: nacosConfig,
}, newServerBuilder(bootstrap))
```

`Run`/`MustRun` 统一负责 Runtime 初始化、进程全局 Logger/OTel 安装、Wire cleanup、
Kratos App 创建与运行，以及带超时的逆序关闭。业务项目的 builder 只返回当前角色需要
启动的 `transport.Server` 列表，不能自行创建 Kratos App。只有需要在同一进程承载多个
Runtime 的工具或测试才使用 `WithoutProcessGlobals`。底层基础设施工厂仍接收显式
Runtime，不读取全局依赖。

只创建组件、不让 Styx 管理整个进程时，仍可直接使用 `styx.New` 和
`runtime.Shutdown`。

## 本地 Trace ID

只需要在日志中关联同一次请求时，配置 `otel.trace.enabled: true` 并留空 `otel.endpoint`。Styx 会在 HTTP/gRPC 入口生成有效的 Trace ID，继续接受上游 `traceparent`，并在带 Context 的结构化日志中写入 `trace_id` 和 `span_id`。此模式不记录或远程导出 Span，也不会连接 Collector；`sampler` 和 `sample_rate` 仅在配置 endpoint 进行远程追踪时生效。`otel.log.enabled` 独立控制日志远程导出。

## 本地日志文件

应用配置文件可在 Styx 定义的 `otel.log.file` 中使用 `styx.config.v1.LogFileConfig`，通过已有的 `styx.Config.OTel` 传入。`directory`、`prefix`、`rotation` 和 `retention_days` 必须明确有效；`directory` 支持绝对路径或相对于进程工作目录的路径，解析后统一转为绝对路径；`rotation` 支持 `day` 或 `hour`。Styx 为同一条 `slog` JSON 记录分别写 stdout 与文件，运行时持有文件句柄并在退出时关闭。文件按 UTC 日期或小时命名；过期文件在启动与切割时清理。目录需要进程写权限，文件写入错误会输出到 stderr，stdout 仍继续工作。

```go
styx.MustRun(ctx, styx.Config{
    App: appConfig,
    OTel: &configv1.OTelConfig{Log: &configv1.LogConfig{
        Console: true,
        File: &configv1.LogFileConfig{
            Directory: "/data/logs",
            Prefix: "order-service",
            Rotation: "hour",
            RetentionDays: 14,
        },
    }},
}, builder)
```

需要接入已有可观测系统时，可直接创建底层 Runtime：

```go
dependencies, err := observe.NewDependencies(observe.Config{
    Logger: logger,
    TracerProvider: tracerProvider,
    MeterProvider: meterProvider,
    Propagator: propagator,
})
runtime, err := styxruntime.New(styxruntime.Config{
    Identity: styxruntime.Identity{Name: "order-service", Version: "v1.0.0"},
    Dependencies: dependencies,
})
```

测试和完全关闭导出的程序可以把 `observe.NoopDependencies()` 注入底层 Runtime。

## 模块

| 模块 | 工厂或入口 |
|---|---|
| HTTP/gRPC Server | `server.NewHTTPServer`、`server.NewGRPCServer` |
| 请求关联标识 | `requestmeta.RequestID`、`requestmeta.TraceID`、`requestmeta.LogFields` |
| HTTP/gRPC Client | `client.NewHTTPClient`、`client.NewGRPCClient`、`client.NewExternalHTTPClient` |
| 多语言 | `i18n.NewBundle`、`i18n.Middleware`、`i18n.HTTPFilter` |
| API 错误 | `apierror.Must`、`apierror.WriteHTTP` |
| MySQL | `mysql.NewMySQL` |
| Redis | `redis.New`、`redis.OpenURL`、`redis.NewWithOptions` |
| MongoDB | `mongodb.NewMongoDB` |
| Elasticsearch | `elasticsearch.NewElasticsearch` |
| Kafka | `kafka.NewKafkaProducer`、`kafka.NewKafkaServer` |
| RocketMQ | `rocketmq.NewProducer`、`rocketmq.NewRocketMQServer` |
| Nacos | `nacos.NewRegistry` |
| 后台任务 | `safe.NewRunner` |

## 验证

```bash
make generate
make verify
make race
```

`make generate-check` 会在临时目录重新生成 Proto 并与仓库文件比较，不会污染工作区。
