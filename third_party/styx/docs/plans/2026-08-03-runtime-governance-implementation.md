# Styx 运行时治理加固实施计划

**目标：** 修复 Styx 已确认的安全、生命周期、配置和可观测性缺陷，并让 `tiptop-server` 以同一运行依赖接入新版本。

**架构：** 新增显式 `observe.Dependencies`，所有框架模块通过该接口获得 Logger、TracerProvider、MeterProvider 和 Propagator；网络初始化接收调用方 Context。安全日志默认不采集正文或参数，调用方负责组合运行模式所需的健康 Probe。

**技术栈：** Go 1.25+、Kratos v2.9、OpenTelemetry、GORM、go-redis、MongoDB Driver、Elasticsearch v8、kafka-go、RocketMQ v5、Nacos v2、Prometheus。

### 任务 1：建立显式运行依赖与安全日志基础

**文件：**

- Create: `observe/dependencies.go`
- Modify: `internal/logutil/caller.go`
- Modify: `log.go`
- Modify: `styx.go`
- Test: `observe/dependencies_test.go`
- Test: `log_test.go`
- Test: `audit_repro_test.go`

**步骤：**

1. 为 `observe.Dependencies` 编写缺少 Logger/Provider/Propagator 时失败、显式 no-op 时通过的测试。
2. 运行 `go test ./observe ./ -run 'Test(Dependencies|AuditConsole)'`，确认新测试或审计测试失败。
3. 实现不可变依赖对象及访问器；将 `logutil` 改为显式 Logger；用构造 Option 传入 Console Writer，删除无效全局输出变量。
4. 调整 `styx.Init` 接收 Context、失败关闭并向调用方暴露同一 Dependencies；只创建已启用的 OTel Provider。
5. 运行聚焦测试并提交 `refactor(REQ-2026-017): 建立显式运行依赖`。

### 任务 2：收敛 HTTP/gRPC 安全日志和 Server/Client 配置

**文件：**

- Modify: `middleware/access_log.go`
- Modify: `middleware/logging.go`
- Modify: `middleware/recovery.go`
- Modify: `middleware/http_tracing.go`
- Modify: `server/http.go`
- Modify: `server/grpc.go`
- Modify: `client/http.go`
- Modify: `client/grpc.go`
- Modify: `api/config/v1/client.proto`
- Modify: `api/config/v1/server.proto`
- Test: `middleware/audit_repro_test.go`
- Test: `middleware/*_test.go`
- Test: `server/*_test.go`
- Test: `client/*_test.go`

**步骤：**

1. 增加不记录 query/body/reply/panic 值、保留 Flusher、拒绝 Endpoint/Discovery 冲突和不明确 gRPC 明文、Reflection 显式开启的测试。
2. 运行 `go test ./middleware ./server ./client`，确认审计测试和新增合同测试失败。
3. 删除正文捕获；实现安全 Recovery 与 ResponseWriter `Unwrap/Flush`；所有日志使用显式 Logger。
4. 实现配置校验、Reflection 和 `insecure` 语义，重新生成 Proto。
5. 运行聚焦测试并提交 `fix(REQ-2026-017): 收敛传输安全默认值`。

### 任务 3：修复 OTel 默认值、生命周期和 Provider 绑定

**文件：**

- Modify: `otel/otel.go`
- Modify: `otel/trace.go`
- Modify: `otel/metric.go`
- Modify: `otel/log.go`
- Modify: `otel/resource.go`
- Modify: `otel/sampler.go`
- Modify: `api/config/v1/otel.proto`
- Test: `otel/audit_repro_test.go`
- Test: `otel/*_test.go`

**步骤：**

1. 增加无效 protocol/compression/sampler/duration 拒绝、关闭能力不建 Provider、Context 取消和 Shutdown 幂等测试。
2. 运行 `go test ./otel -run Test`，确认失败。
3. 实现集中配置校验、默认 parent-based always-on、仅按开关创建 Provider、Context-aware exporter 和 `errors.Join` Shutdown。
4. 运行 `go test -race ./otel` 并提交 `fix(REQ-2026-017): 修复OTel运行语义`。

### 任务 4：修复 MySQL、Redis、MongoDB 与 Elasticsearch

**文件：**

- Modify: `mysql/mysql.go`
- Modify: `redis/redis.go`
- Modify: `redis/hook.go`
- Modify: `mongodb/mongodb.go`
- Modify: `mongodb/metrics.go`
- Modify: `elasticsearch/elasticsearch.go`
- Modify: `internal/tlsutil/tls.go`
- Test: `mysql/mysql_test.go`
- Test: `redis/audit_repro_test.go`
- Test: `redis/*_test.go`
- Test: `mongodb/*_test.go`
- Test: `elasticsearch/*_test.go`
- Test: `internal/tlsutil/tls_test.go`

**步骤：**

1. 增加 Context 截止、SQL/Redis 脱敏、完整 Redis URL 参数、指标注销、TLS 成对证书和 Transport 关闭测试。
2. 运行相关包测试并确认失败。
3. MySQL 关闭自动 Ping 并使用 `PingContext`；日志/Span 只保留操作名，保存并注销 DB 指标 Registration。
4. Redis 增加 `OpenURL` 和原生 UniversalOptions 入口，关闭 raw statement，日志仅记录命令名。
5. MongoDB/Elasticsearch 使用调用方 Context、显式 Provider 和完整 cleanup；统一 TLS 最低版本与证书校验。
6. 运行 `go test -race ./mysql ./redis ./mongodb ./elasticsearch ./internal/tlsutil`，提交 `fix(REQ-2026-017): 治理存储客户端生命周期`。

### 任务 5：修复 Kafka 与 RocketMQ 消息语义

**文件：**

- Modify: `kafka/kafka.go`
- Modify: `kafka/producer.go`
- Modify: `kafka/server.go`
- Modify: `rocketmq/producer.go`
- Modify: `rocketmq/server.go`
- Modify: `api/config/v1/data.proto`
- Test: `kafka/*_test.go`
- Test: `rocketmq/*_test.go`

**步骤：**

1. 增加 Handler 失败不提交、退避可取消、重复 Start/Stop、配置字段真实生效、凭据成对和部分启动释放测试。
2. 运行 `go test ./kafka ./rocketmq`，确认失败。
3. 实现 Kafka 至少一次错误语义、Context-aware 循环和状态机；删除或替换 kafka-go 不支持的配置字段。
4. 实现 RocketMQ 校验和失败回滚，所有日志与传播使用显式依赖。
5. 运行竞态测试并提交 `fix(REQ-2026-017): 修复消息运行语义`。

### 任务 6：修复 Nacos 注册发现一致性

**文件：**

- Modify: `registry/nacos/nacos.go`
- Modify: `registry/nacos/options.go`
- Modify: `registry/nacos/registry.go`
- Modify: `registry/nacos/watcher.go`
- Modify: `registry/nacos/logger.go`
- Test: `registry/nacos/*_test.go`

**步骤：**

1. 增加原始服务名、group/cluster 一致、IPv6 Endpoint、批量注册回滚、Watcher 错误和幂等 Stop 测试。
2. 运行 `go test ./registry/nacos`，确认失败。
3. 统一实例转换与查询条件，注册失败回滚，Watcher 优先取消本地 Context，SDK logger 使用显式 Logger。
4. 运行竞态测试并提交 `fix(REQ-2026-017): 统一Nacos注册发现语义`。

### 任务 7：重建 safe 生命周期

**文件：**

- Modify: `safe/goroutine.go`
- Modify: `safe/task.go`
- Modify: `safe/cron.go`
- Modify: `safe/README.md`
- Test: `safe/audit_repro_test.go`
- Test: `safe/*_test.go`

**步骤：**

1. 增加 Runner 显式依赖、Task 非正间隔、重复 Start/Stop、停止取消当前任务和超时返回测试。
2. 运行 `go test -race ./safe`，确认失败。
3. 实现 Runner、Task 状态机和 Context-aware Stop；删除 stderr 重复输出和动态指标标签。
4. 更新测试与 README，运行竞态测试并提交 `fix(REQ-2026-017): 重建后台任务生命周期`。

### 任务 8：修正文档、示例与仓库验证入口

**文件：**

- Create: `Makefile`
- Modify: `README.md`
- Modify: `server/README.md`
- Modify: `client/README.md`
- Modify: `mysql/README.md`
- Modify: `redis/README.md`
- Modify: `mongodb/README.md`
- Modify: `elasticsearch/README.md`
- Modify: `kafka/README.md`
- Modify: `rocketmq/README.md`
- Modify: `registry/nacos/README.md`
- Modify: `middleware/README.md`
- Modify: `examples/kratos-demo/**`

**步骤：**

1. 增加 `make generate`、`make verify`、`make build`，并检查生成后 Git 无差异。
2. 让示例直接复用 Styx Proto、创建真实 Server/数据依赖并通过编译。
3. 删除不存在的字段/函数说明，统一 Go 1.25、原生 SDK 类型和新接口。
4. 执行 `make verify`，提交 `docs(REQ-2026-017): 同步Styx合同与示例`。

### 任务 9：迁移 tiptop-server 并恢复 Worker 诊断

**文件：**

- Modify: `tiptop-server/go.mod`
- Modify: `tiptop-server/cmd/server/main.go`
- Modify: `tiptop-server/cmd/server/logger.go`
- Modify: `tiptop-server/internal/adapters/mysqlstore/store.go`
- Modify: `tiptop-server/internal/adapters/redisstore/store.go`
- Modify: `tiptop-server/internal/platform/diagnostic/server.go`
- Modify: `tiptop-server/internal/platform/observe/*`
- Test: 对应 `*_test.go`
- Modify: `tiptop-infra/compose.production.yml`
- Test: `tiptop-infra/tests/*`

**步骤：**

1. 发布已验证的 Styx Tag，并在干净模块缓存确认可解析。
2. 为统一 Logger/MeterProvider、Redis URL 参数、Probe 聚合和 Worker 诊断写失败测试。
3. 升级依赖且不使用 `replace`；构造 Styx Dependencies，API/Worker 都启动诊断服务。
4. 为生产 Worker 启用 `/readyz` 健康检查。
5. 运行 `tiptop-server make verify`、`go test -race`、`tiptop-infra make verify`。
6. 更新 `REQ-2026-017-styx-hardening` 的测试报告、状态和交接，提交各仓库变更。

### 任务 10：最终审查与发布证据

**文件：**

- Modify: Styx 审计/计划文档
- Modify: `tiptop-infra/requirements/REQ-2026-017-styx-hardening/*`

**步骤：**

1. 对三个仓库执行 diff 审查，重点检查凭据、Context、资源释放、消息提交和范围蔓延。
2. 运行 Styx `make verify`、`go test -race ./...`、`go mod tidy -diff`。
3. 运行 `tiptop-server make verify`、`make build` 和干净模块缓存下载。
4. 在完整五仓库布局运行 `tiptop-infra make verify`，记录生产冒烟待办。
5. 固定提交 SHA、Tag、跳过检查和回滚方式，进入 `in_review`。
