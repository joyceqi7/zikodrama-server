# Styx 运行时治理加固设计

## 目标与边界

本次改造把 Styx 从“若干带全局副作用的工厂函数”收敛为可治理的基础设施模块。允许破坏现有 Go 接口，不保留兼容包装；当前唯一交付调用方 `tiptop-server` 同步迁移。对外 HTTP、数据库结构和短剧业务规则不变。

## 运行依赖模块

新增 `observe` 模块，接口只包含四项运行依赖：Logger、TracerProvider、MeterProvider 和 Propagator。构造函数校验依赖完整性，并提供显式 no-op 构造器供不启用 Telemetry 的应用和测试使用。server、client、middleware、存储、消息、Nacos 和 safe 都接收该依赖，不再在构造时读取全局默认值。

根 `styx.Init` 仍可作为应用引导入口：它创建 OTel Provider 与 Logger、安装 Kratos/第三方所需的全局 Provider，并把相同依赖保存在 `Styx` 中供 Wire 注入。直接集成方也可用自己创建的 Provider 构造 `observe.Dependencies`。清理时按逆序关闭并恢复引导前的全局值，方便测试和同进程工具重复初始化。

## 网络资源接口

所有网络工厂的第一个参数为 `context.Context`，随后是实例名、配置和 `observe.Dependencies`，继续返回原生 SDK 类型、Wire cleanup 和错误。MySQL 关闭 GORM 自动 Ping 后调用 `PingContext`；Redis 支持直接解析 URL 为 go-redis 原生完整配置；MongoDB 和 Elasticsearch 使用调用方 Context 进行首次 Ping。指标注册句柄、HTTP Transport 和失败后已创建的客户端都纳入 cleanup。

RocketMQ 与 Nacos SDK 的部分构造调用本身不支持 Context，Styx 会在调用前后检查取消、完整释放已经返回的对象，并在文档中保留这一 SDK 限制，不通过无法回收的后台 goroutine伪造超时。

## 安全日志与错误流

HTTP/gRPC 日志只记录 method/operation、status/code、duration、实例名和稳定错误分类。原始 HTTP 路径、查询字符串、请求/响应正文、SQL、Redis 参数、Panic 值和下游错误正文一律不记录；稳定路由由 Kratos operation 表示。MySQL 和 Redis Span 只记录数据库类型与操作名，关闭原始 statement。Recovery 由 Styx 自行实现，避免 Kratos 原生 Recovery 在自定义处理器之前打印请求。

错误返回给调用方时保留可判定的根因和 Context 状态；日志只输出安全分类。配置错误包含字段名和约束，但不回显配置值。连接地址只在不包含 userinfo 的情况下记录。

## Server、Client 与服务发现

Server/Client 构造阶段统一校验非空地址、正时长、Endpoint/Discovery 互斥和 TLS 明文选择。gRPC Reflection 只在显式开启时注册；`insecure=true` 才允许 gRPC 明文连接。HTTP ResponseWriter 包装器保留标准扩展能力。

Nacos 使用原始服务名注册，依靠 Endpoint scheme 让 Kratos 选择 HTTP 或 gRPC；GetService 与 Watch 使用相同 group/cluster 条件和同一转换函数。批量注册失败会回滚本次已经注册的 Endpoint，Watcher Stop 幂等且优先取消本地 Context。

## 消息与后台任务

Kafka 配置只保留 kafka-go 能真实支持的字段并逐项校验。Handler 成功后才提交 offset；拉取退避响应 Context 取消；Start/Stop 建立幂等状态和有截止时间的等待。RocketMQ 对必填字段与成对凭据做同样校验，并修复部分启动失败的释放路径。

`safe` 改为由显式依赖创建 Runner。Task 使用互斥状态机，校验正间隔和非空函数；Start、Stop 可重复调用，Stop 接收 Context 并取消当前执行。Cron 和一次性任务复用相同的安全 Recovery、Logger 与 Telemetry，删除直接 stderr 输出和动态高基数指标标签。

## TipTop 接入

`tiptop-server` 在任何 Styx 模块之前安装统一 `slog`，用现有 Prometheus Registry 创建 OTel MeterProvider，再构造 Styx 运行依赖。MySQL/Redis 适配器直接把启动 Context 和运行依赖传给 Styx；Redis URL 不再经过不完整的字段复制。

诊断模块组合存储与 Worker 就绪检查。API 模式检查 MySQL 与 Redis，Worker 模式检查 MySQL 和 Worker 启动状态，all 模式检查全部依赖。两种运行模式都启动独立诊断端口并暴露 `/healthz`、`/readyz` 和 `/metrics`；生产 Compose 为 Worker 启用 `/readyz` 健康检查。

## 验证策略

先保留当前失败的审计复现测试，再为 Context 截止、SQL/Redis 脱敏、配置拒绝、消息不提交、Nacos 转换、Task 状态机和资源注销补充回归测试。每批变更运行聚焦测试，最终执行 Styx `go test -race ./...`、`go vet ./...`、`go mod tidy -diff`，以及 `tiptop-server make verify`、干净模块缓存解析和 `tiptop-infra make verify`。

真实生产冒烟只验证 API/Worker 诊断和启动关闭；本需求不需要 TikTok、IAA 或 VePlayer 真机检查。
