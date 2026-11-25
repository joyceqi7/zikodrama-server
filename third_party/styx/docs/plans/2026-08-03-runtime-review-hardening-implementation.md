# REQ-2026-017 Runtime 复审加固实施计划

**目标：** 修复复审发现的隐私、初始化截止时间、Runtime 启停竞态、依赖就绪和交付证据缺口。

**架构：** Styx Runtime 新增统一启动门禁和可继续等待的单次关闭适配器；各基础设施模块只组合这些原语。服务端只负责把 Redis 健康检查接入现有 readiness，并移除 Store 的第二关闭所有权。

**技术栈：** Go 1.25、Kratos、OpenTelemetry、Nacos SDK、RocketMQ SDK、GORM、go-redis、Protocol Buffers。

### 任务 1：Runtime 启动与关闭原语

**文件：** `runtime/runtime.go`、`runtime/close.go`、`runtime/runtime_test.go`、`runtime/close_test.go`

1. 写入“关闭后拒绝启动”“关闭等待在途启动”“调用方超时后继续等待同一 Shutdown”的失败测试。
2. 运行 `go test -count=1 ./runtime`，确认按预期失败。
3. 实现 `StartResource`、在途启动等待和 `BoundedShutdown`。
4. 再次运行 `go test -count=1 ./runtime`，预期通过。

### 任务 2：Styx 隐私与网络初始化

**文件：** `api/config/v1/data.proto`、`api/config/v1/data.pb.go`、`elasticsearch/`、`registry/nacos/`、`rocketmq/` 下相关实现与测试。

1. 先增加 Proto 字段移除、Logger 恢复/冲突、Context 超时清理的失败测试。
2. 分包运行测试确认失败原因。
3. 删除正文采集能力；实现 Nacos Logger 租约与受控异步初始化；RocketMQ Producer 增加 Context，Server Start 使用 Runtime 门禁。
4. 运行 `make generate` 和相关包测试。

### 任务 3：全部可启动资源接入 Runtime 门禁

**文件：** `safe/task.go`、`safe/cron.go`、`kafka/server.go`、`rocketmq/server.go` 及对应测试。

1. 增加 Runtime 关闭后不能启动的失败测试。
2. 使用 `StartResource` 包裹各 Start 状态转换。
3. 运行 `go test -count=1 ./safe ./kafka ./rocketmq` 和对应竞态测试。

### 任务 4：Telemetry 可继续等待关闭

**文件：** `otel/otel.go`，以及 tiptop-server 的 `internal/platform/observe/tracing.go`、`internal/platform/observe/styx.go` 和相关测试。

1. 用阻塞 Exporter 证明首次等待超时、释放后第二次等待成功且只关闭一次。
2. Styx Provider 和服务端 Provider 统一改用 `BoundedShutdown`。
3. 运行两个仓库的观测聚焦测试。

### 任务 5：Redis readiness 与单一资源所有权

**文件：** tiptop-server 的 `internal/adapters/redisstore/store.go`、`internal/adapters/mysqlstore/store.go`、`cmd/server/main.go` 及相关测试。

1. 增加 Redis 失联和 API readiness 失败测试。
2. 为 Redis Store 增加脱敏 `Ping`，接入 API readiness。
3. 删除 MySQL/Redis Store 的直接 Close 所有权，测试只关闭 Runtime。
4. 运行 `go test -count=1 ./cmd/server ./internal/adapters/mysqlstore ./internal/adapters/redisstore`。

### 任务 6：完整验证、交付记录与提交

**文件：** tiptop-infra 的 `requirements/REQ-2026-017-styx-hardening/` 下计划、测试报告、交接和状态记录。

1. 运行 Styx `make verify`、`make race`。
2. 运行 tiptop-server `make verify` 和受影响包竞态测试。
3. 运行 tiptop-infra `REQ=REQ-2026-017-styx-hardening REPO=tiptop-infra make verify`。
4. 使用 review 流程审查最终差异，修复阻断问题。
5. 分别提交三个当前需求分支；不推送、不创建或移动远程 Tag。
