# Styx 运行时治理审计

## 审计范围

本次审计覆盖 `api/config/v1`、根初始化与日志、server、client、middleware、OTel、MySQL、Redis、MongoDB、Elasticsearch、Kafka、RocketMQ、Nacos、safe、示例和全部现有测试，共约 9,000 行 Go/Proto 源码。审计基线为 `9145983d65c3a0320d6fcdd14360d22acf353d61`。

已执行：

- `go test ./...`：通过。
- `go test -race ./...`：通过，macOS 链接器有 `LC_DYSYMTAB` 警告但退出码为 0。
- `go vet ./...`：通过。
- `go mod tidy -diff`：无差异。
- `go test -cover ./...`：server、client、middleware、OTel、MongoDB、Elasticsearch 和 TLS 工具覆盖率为 0%；MySQL 4.5%，根包 8.7%。
- `staticcheck`、`govulncheck`：基线未执行，修复完成后已纳入最终验证。

## 总体结论

Styx 当前不是单点缺陷，而是四个共同根因叠加：

1. **隐式全局运行时**：模块直接读取全局 `slog`、TracerProvider、MeterProvider 和 Propagator，初始化顺序错误时会永久绑定 no-op provider，调用方也无法验证模块实际使用的依赖。
2. **声明、实现和文档分离**：Proto 存在未使用字段，README 仍描述已经不存在的函数、类型和配置，默认值说明与 Proto 零值冲突。
3. **资源生命周期接口过浅**：网络工厂自行创建 `context.Background()`，初始化和关闭不受调用方截止时间控制；部分注册句柄和底层 Transport 未释放。
4. **可观测性默认采集正文**：访问日志、gRPC 请求/响应、SQL、Redis 参数和 panic 请求会进入日志或 Span，框架默认行为不满足凭据和业务数据最小化原则。

## 已确认问题

### P1：安全与数据泄露

1. `HTTPAccessLog` 记录完整查询字符串、JSON/Form 请求正文和 JSON 响应正文，并在读取前没有限制请求体大小。Token、密码和业务数据会进入日志，同时存在额外内存放大风险。
2. gRPC `Logging` 与 `ClientLogging` 记录完整请求、响应和下游错误消息。Kratos 原生 Recovery 还会在 Styx 自定义处理器执行前记录 panic 值和完整请求。
3. MySQL 自定义 logger 使用 GORM 已插值 SQL，同时把 SQL 写入日志和 Span；数据库错误文本可能包含唯一键值等业务数据。
4. Redis 慢命令 hook 记录前五个完整参数；`redisotel` 默认也启用原始 `db.statement`。AUTH 之外的 SET、脚本参数、Token 和缓存值均可能泄露。
5. `safe.recoverPanic` 将 panic 值和堆栈同时写入结构化日志与 stderr，绕过应用日志策略并重复暴露信息。

### P1：启动、消息与生命周期正确性

6. MySQL `gorm.Open` 默认执行无 Context 的自动 Ping，之后又执行 `sql.DB.Ping()`；调用方无法用启动截止时间终止黑洞连接。
7. MongoDB、Elasticsearch 和 Redis 初始化自行使用 `context.Background()`。Elasticsearch 新建的 `http.Transport` 缺少连接、TLS 握手和空闲连接治理，Ping 可长期阻塞。
8. Kafka 消费者在业务 Handler 返回错误后仍提交消息，服务重启后也不会重放，实际形成错误路径消息丢失。
9. `safe.Task` 没有状态机：启动前 Stop 永久阻塞，重复 Stop 会关闭已关闭 channel，重复 Start 会产生多执行循环，非正间隔会在后台 goroutine 中触发未恢复 panic。
10. Worker-only 模式在 `tiptop-server` 创建诊断服务之前提前分支，生产 Compose 又禁用 Worker 健康检查，因此无法判断 Worker 就绪或采集其指标。

### P1：服务发现与 Telemetry 语义

11. Nacos 注册使用 `serviceName.scheme`，文档和客户端发现使用原始 `serviceName`，可能无法发现已注册实例；`GetService` 不按集群过滤而 Watcher 按集群过滤，同一服务的初始结果和变更结果不一致。
12. 默认 ratio sampler 的注释声明采样率为 1，实际 Proto 零值传入后为 0，启用 Trace 但未填比例时不会采集任何 Span。
13. OTel 日志关闭时仍创建 LoggerProvider 和 OTLP 导出器；配置了 OTLP 但初始化失败时根 `Init` 只告警后继续，生产配置错误无法在启动阶段暴露。
14. Recovery、safe 和 MongoDB 指标在包级 `sync.Once` 或构造阶段读取全局 MeterProvider；如果应用稍后安装真实 Provider，仪表永久绑定 no-op Provider。

### P2：接口、配置和释放

15. `SetConsoleOutput` 只修改变量，JSON handler 始终写入 `os.Stdout`，公开配置接口无效。
16. HTTP 访问日志的 ResponseWriter 包装器未保留 `http.Flusher` 等能力，会破坏 SSE、流式响应和升级协议。
17. gRPC Server 的 `enable_reflection` 从未读取；gRPC Client 的 `insecure` 从未读取。Endpoint 与 Discovery 同时配置时没有报错而是静默选择 Endpoint。
18. Kafka 的 `flush_frequency`、`rebalance_timeout`、`max_processing_time`、`fetch_default_bytes` 未生效；非法压缩、SASL 机制、确认级别和 offset 会静默降级。
19. Redis/TLS/连接池配置缺少交叉校验；单机模式多个地址会静默忽略，证书与私钥只配置一项时会被静默跳过。
20. MySQL 连接池指标注册返回的 Registration 未保存和注销；MongoDB observable callback 也没有注销。Elasticsearch cleanup 不关闭空闲连接。
21. RocketMQ 凭据只配置一半时静默忽略；Producer/Consumer 启动失败时新建对象没有完整停止，可能泄漏资源。
22. Nacos 多 Endpoint 注册中途失败不会回滚已经注册的实例；Watcher 回调忽略 SDK 错误，Stop 不是幂等且先执行可能阻塞的 Unsubscribe 后才取消 Context。

### P2：文档和验证漂移

23. 根 README 仍把 Kafka 写成 Sarama，并列出不存在的 `NewKafkaConsumerGroup`；MongoDB、Elasticsearch、RocketMQ、Nacos README 包含不存在的字段、函数或返回类型。
24. 示例的 `conf.proto` 与手写 `conf.go` 字段不一致，Wire stub 不创建 HTTP/gRPC Server，无法作为真实可运行参考。
25. README 声明 Go 1.21+，`go.mod` 实际要求 Go 1.24；仓库没有统一的 `make generate/verify`，Proto 生成和发布无法重复执行。
26. 大多数生产模块没有行为测试，现有测试主要验证辅助函数，无法阻止生命周期、安全默认值和文档合同回归。

## 最小复现证据

`audit_repro_test.go` 及 middleware、Redis、OTel、safe 下的同名测试在基线稳定失败，分别证明：

- `SetConsoleOutput` 不生效；
- 查询参数中的敏感值进入访问日志；
- ResponseWriter 丢失 `http.Flusher`；
- Redis 命令值进入慢命令日志；
- 默认 ratio sampler 零采样；
- 日志关闭仍创建 LoggerProvider；
- Task 启动前 Stop 永久阻塞。

其余问题由生产源码到依赖源码的数据流证实，例如 GORM `gorm.go:236-238` 明确调用无 Context 的 `Ping()`，`redisotel` 的默认配置明确设置 `dbStmtEnabled: true`。

## 修复原则

- 在拥有复杂度的 Styx 模块内修复，不要求业务服务重复补偿。
- 所有网络初始化使用调用方 Context；第三方 SDK 不支持 Context 的限制必须显式记录。
- Logger、TracerProvider、MeterProvider、Propagator 作为一个显式依赖传入模块；禁止在模块构造时隐式读取未初始化的全局值。
- 默认日志只保留操作类型、稳定资源名、耗时、结果和安全错误分类；正文和参数必须显式排除。
- 配置字段要么实现并测试，要么删除；禁止静默忽略和“文档默认值”。
- 保留 Wire 的 `(T, func(), error)` cleanup 形式，但初始化失败路径和 cleanup 必须完整释放资源。

## 修复结果

上述 26 项基线问题均已处理，且未保留旧 Go 接口兼容层。主要结果如下：

1. 新增 `observe.Dependencies`，server、client、middleware、存储、消息、Nacos 和 safe 统一使用显式 Logger、TracerProvider、MeterProvider 与 Propagator；零值依赖在构造阶段失败。
2. 根 `Init` 只在全部模块成功后安装进程全局值，清理时恢复原值；OTel 只创建已启用的 Provider，配置、默认采样、重试、压缩、协议和关闭语义均有校验与回归测试。
3. HTTP/gRPC、MySQL、Redis、panic 和异步任务日志不再记录原始路径、query、正文、SQL、命令参数、下游错误正文或 panic 值；日志时间统一为 UTC RFC3339Nano。
4. MySQL 关闭 GORM 自动 Ping 并使用 `PingContext`；Redis URL 直接交给 go-redis 原生解析；MongoDB、Elasticsearch 使用调用方 Context；指标注册、连接和 Transport 均纳入幂等 cleanup。
5. Kafka 只在 Handler 成功后提交位点，停止和重试响应 Context；RocketMQ 修复凭据校验、启动回滚、异步空回调和受截止时间控制的停止。
6. Nacos 统一服务名、group、cluster、TLS/IPv6 endpoint、小数权重、批量注册回滚、Watcher 回调合并与幂等停止；SDK 日志按所有已配置凭据脱敏。
7. safe 重建 Runner、Task、TaskGroup 和 Cron 生命周期；重复启动、停止超时、停止中重启、panic 恢复和回滚均有明确状态。
8. Server/Client 对空白值、互斥配置、TLS scheme、显式端口、消息大小、连接池和熔断参数做严格校验；gRPC Reflection 默认关闭，明文连接必须显式声明。
9. 示例改为直接复用 Styx 生成配置并创建真实组件；README 与公开签名同步；新增 `make generate`、`make verify`、`make race`。
10. `tiptop-server` 已迁移唯一调用方：启动失败会执行资源清理，MySQL/Redis 复用相同运行依赖，API/Worker 都有独立诊断端口，Worker 状态进入 `/readyz`，指标包含正确服务 Resource。

修复过程中额外发现并处理：OTel 依赖版本漂移、Go 1.25.5 标准库安全补丁缺失、gRPC/x/text/pgx/ch-go 可达漏洞、Nacos 同步回调可能阻塞、Cron 停止中可重启、服务 endpoint 与 TLS scheme 不一致，以及 Worker 指标显示 `unknown_service`。

## 最终验证

- `make verify`：通过，包含 Proto 生成一致性、格式、`go vet ./...`、`go test ./...` 和 `go build ./...`。
- `make race`：通过；macOS 链接器输出 `LC_DYSYMTAB` 警告，但所有包退出码为 0。
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`：通过。
- `go run golang.org/x/vuln/cmd/govulncheck@latest -show=traces ./...`：可达漏洞 0；另有 4 个导入包和 1 个模块中的不可达报告。
- `go mod tidy -diff` 与 `git diff --check`：无差异、无格式错误。
- 变更差异敏感信息扫描：命中仅为脱敏回归测试中的伪造凭据，没有真实密钥。
