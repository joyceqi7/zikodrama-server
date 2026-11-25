# Styx 应用边界收敛设计

## 问题

`v1.1.2` 的 Runtime 正确统一了身份、显式可观测依赖和资源关闭，但应用仍需自行复制
Runtime 初始化、全局依赖安装、Wire cleanup、Kratos Run 和超时 Shutdown。标准库第三方
HTTP API 也没有对应工厂，调用方只能自行组合 `otelhttp.Transport` 并向 Runtime 注册
Transport。结果是框架复杂度重新出现在业务仓库，并诱导 Runtime 继续向 biz/service 传播。

## 边界

- Runtime 继续作为 Styx 内部基础设施资源的唯一所有者；MySQL、Redis、HTTP Client、
  safe.Task 等工厂仍显式接收 Runtime，避免依赖隐式全局值。
- `Run`/`MustRun` 成为单服务进程入口，固定执行 Runtime 创建、全局 Logger/OTel 安装、
  Server 装配、Kratos App 创建与运行、Wire cleanup 和 Runtime 逆序关闭。业务项目不再
  创建或包装 Kratos App。
- `client.NewExternalHTTPClient` 覆盖 OAuth、云厂商开放 API 等标准库 HTTP 场景，显式
  使用 Runtime 的 TracerProvider 与 Propagator，并把独立 Transport 交给 Runtime 关闭；
  Trace 不记录 path/query/body，且不自动创建 HTTP 指标。
- Runtime 只能出现在应用组合根和直接调用 Styx 工厂的基础设施适配层；业务项目的
  biz/service 不得依赖 Runtime、Styx、Kratos 或具体 data 实现。
- safe 负责 panic recovery、Trace、取消与调度，但不创建任务指标；产品指标必须来自
  明确的业务数据，而不是框架后台任务。

## 生命周期顺序

1. `styx.New` 创建 Runtime 与内建 OTel/Registry 资源。
2. `Run` 安装进程级 Logger、TracerProvider、MeterProvider 和 Propagator。
3. Wire builder 创建 data、biz、service 并返回 server 列表；Styx 工厂创建的资源登记到 Runtime。
4. Styx 创建 Kratos App；Kratos 并发启动 Server，并在信号到达后停止 HTTP、gRPC 或 Worker Server。
5. Wire cleanup 释放非 Styx 应用资源。
6. Runtime 在截止时间内按逆序关闭所有 Styx 资源并恢复进程全局值。

任意初始化错误或 panic 都必须执行已经登记的 cleanup；Runtime Shutdown 可在调用方超时
后使用新 Context 继续等待尚未完成的受管关闭。
