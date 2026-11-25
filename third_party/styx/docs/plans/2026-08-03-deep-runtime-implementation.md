# Styx 深 Runtime 实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**目标：** 把 Styx 从分散工厂集合改造成拥有不可变身份、显式依赖、受控扩展和统一关闭语义的深 Runtime，并迁移唯一调用方。

**架构：** Runtime 作为公开 seam，持有服务身份、`observe.Dependencies`、注册发现能力和逆序资源栈。基础设施适配器继续返回真实第三方类型，但自动把关闭函数注册到 Runtime；Transport 只暴露不会覆盖治理链的受约束 Option。

**技术栈：** Go、Kratos、OpenTelemetry、GORM、go-redis、Nacos、Kafka、RocketMQ、Protocol Buffers。

---

### 任务一：实现 Runtime 生命周期和错误模型

**文件：**
- 创建：`runtime/runtime.go`
- 创建：`runtime/error.go`
- 创建：`runtime/runtime_test.go`
- 修改：`styx.go`

**步骤：**
1. 先编写身份快照、资源逆序关闭、错误聚合、Context 截止、重复关闭和关闭中拒绝注册的失败测试。
2. 实现不可变 Runtime、`Own`、`Shutdown`、Registrar/Discovery 和安全分类错误。
3. 把根初始化改为 `styx.New`，内建 OTel 与 Nacos 纳入 Runtime，不再返回 cleanup。
4. 运行 `go test ./runtime . -race`。

### 任务二：移除隐式全局状态并修复配置存在性

**文件：**
- 修改：`styx.go`
- 修改：`api/config/v1/registry.proto`
- 修改：`registry/nacos/nacos.go`
- 修改：`audit_repro_test.go`
- 修改：`registry/nacos/*_test.go`

**步骤：**
1. 编写默认初始化不修改全局值、显式安装冲突和幂等恢复测试。
2. 实现 `Runtime.InstallGlobals`，同一进程只允许一个安装者。
3. 用枚举表达 Nacos 启动缓存策略并补默认值、显式覆盖测试。
4. 运行 `make generate` 和相关聚焦测试。

### 任务三：收紧 Transport 扩展 seam

**文件：**
- 修改：`server/http.go`
- 创建：`server/options.go`
- 修改：`server/grpc.go`
- 修改：`client/http.go`
- 修改：`client/grpc.go`
- 修改：`server/*_test.go`
- 修改：`client/*_test.go`

**步骤：**
1. 编写 Listener、追加 Filter、Recovery 包裹顺序和公开签名编译测试。
2. 删除原生 Kratos Option 透传；HTTP 只保留 Styx 自有 Listener 与 Filter Option。
3. 服务名和可观测依赖统一从 Runtime 获取。
4. 运行 `go test ./server ./client ./middleware -race`。

### 任务四：迁移基础设施适配器到 Runtime 所有权

**文件：**
- 修改：`mysql`、`redis`、`mongodb`、`elasticsearch`、`kafka`、`rocketmq`、`registry/nacos`、`safe` 的生产源码和测试。
- 修改：`examples/kratos-demo`。

**步骤：**
1. 将公开构造函数的 `observe.Dependencies` 参数替换为 Runtime。
2. 构造成功后调用 `Runtime.Own` 注册有 Context、有错误返回的关闭函数；注册失败立即回滚。
3. 删除公开 cleanup 返回值，保留内部可注入 seam 供聚焦测试使用。
4. 运行各包聚焦测试和 `go test -race`。

### 任务五：迁移 tiptop-server 唯一调用方

**文件：**
- 修改：`tiptop-server/internal/platform/observe/styx.go`
- 修改：`tiptop-server/cmd/server/main.go`
- 修改：`tiptop-server/internal/adapters/mysqlstore/store.go`
- 修改：`tiptop-server/internal/adapters/redisstore/store.go`
- 修改：`tiptop-server/internal/transport/http/router.go`
- 修改：相关测试、`go.mod`、`go.sum`。

**步骤：**
1. 让服务端创建一个 Runtime，并把现有 Prometheus/Tracing shutdown 注册进去。
2. MySQL、Redis 和 HTTP 统一接收 Runtime，不再保存 Styx cleanup。
3. 关闭顺序改由 Runtime 负责，业务 Store 只保留领域层关闭接口。
4. 运行聚焦竞态测试、`make verify` 和 Worker 诊断冒烟。

### 任务六：文档、审查与发布

**文件：**
- 修改：Styx `README.md`、各模块 README、示例和审计文档。
- 修改：`tiptop-infra/requirements/REQ-2026-017-styx-hardening/`。

**步骤：**
1. 更新公开签名、生命周期、全局安装和迁移说明。
2. 执行 Styx `make verify`、`make race`、Staticcheck、Govulncheck 和敏感信息扫描。
3. 执行服务端标准验证、干净模块缓存和基础设施 45 项测试。
4. 审查差异，提交并发布新的 Styx `v1` 标签，再固定服务端远程依赖版本和精确提交。

