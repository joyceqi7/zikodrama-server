# Styx 深 Runtime 设计

## 背景

`v1.0.6` 已修复安全默认值、初始化上下文、资源泄漏和并发状态问题，但公开接口仍以多个基础设施工厂为主。调用方必须重复传递服务身份和 `observe.Dependencies`，并自行保存无上下文、无错误返回的 cleanup；HTTP/gRPC 还允许原生 Kratos Option 覆盖框架治理配置。顶层 `Styx` 因此没有形成足够深的模块。

## 方案比较

### 方案一：只修 cleanup

把 `func()` 改为 `func(context.Context) error`，保留所有工厂参数。改动最小，但调用方仍负责依赖传递、关闭排序和治理 Option，复杂度继续分散。

### 方案二：Runtime 统一身份、依赖和资源生命周期（采用）

新增不可变 `Runtime`。它持有服务身份、显式可观测依赖、Registrar、Discovery 和逆序资源栈。公开基础设施工厂接收 Runtime，自动注册关闭函数，不再向调用方返回 cleanup。HTTP 只开放 Listener 与追加 Filter 两个受约束扩展 seam；gRPC 和客户端不再接受可覆盖治理规则的原生 Option。

该方案把跨模块重复复杂度收进 Runtime，同时继续返回 GORM、go-redis、Kratos 等真实类型，不再制造通用数据库或网络抽象。

### 方案三：顶层 Runtime 为所有基础设施提供方法

例如 `runtime.MySQL`、`runtime.Redis`、`runtime.HTTPServer`。调用最短，但根包需要导入全部第三方 SDK，公开接口巨大，新增适配器也必须修改根包，不符合小接口原则。

## 目标接口

```go
runtime, err := styx.New(ctx, styx.Config{
    App:      app,
    OTel:     otelConfig,
    Registry: nacosConfig,
})
if err != nil {
    return err
}
defer runtime.Shutdown(shutdownContext)

db, err := mysql.New(ctx, "primary", mysqlConfig, runtime)
cache, err := redis.OpenURL(ctx, "cache", redisURL, runtime)
server, err := server.NewHTTPServer(httpConfig, runtime,
    server.WithHTTPListener(listener),
    server.WithHTTPFilter(applicationFilter),
)
```

`Runtime` 初始化时复制 `AppInfo`，后续调用方修改原对象不会改变服务身份。内建 OTel 和 Nacos 均注册到资源栈；关闭按创建逆序执行，接收调用方 Context，聚合并返回安全分类错误。

## 全局状态

`styx.New` 默认不修改 `slog` 或 OpenTelemetry 全局 Provider。Kratos tracing、HTTP tracing、数据库和缓存适配器全部从 Runtime 取得显式 Provider。

确需兼容只读取全局 Provider 的第三方库时，调用方必须显式调用 `runtime.InstallGlobals()`。同一进程同时只允许一个安装者；重复安装返回冲突错误。Runtime 关闭时只恢复自己安装的快照，避免交错初始化恢复到已关闭 Provider。

## 治理扩展 seam

- HTTP：允许预绑定 Listener 和追加应用 Filter。Styx 始终构建唯一 FilterChain，Recovery 包裹应用 Filter，调用方不能替换治理链。
- gRPC：移除原生 `grpc.ServerOption` 入口；消息大小、TLS、反射等均由受校验配置表达。
- HTTP/gRPC Client：移除原生 ClientOption 入口；新增需求先进入配置合同和验证，不允许绕过 tracing、metadata、日志和熔断顺序。

## 配置与错误

- 修复 Nacos `not_load_cache_at_start` 无法表达默认 `true` 的问题，改为显式枚举缓存启动策略，避免 Proto3 bool 存在性歧义。
- Runtime 错误至少包含模块、操作、类型和是否可重试；`Error()` 只输出安全摘要，底层错误通过 `Unwrap()` 保留给受控诊断。
- 配置在进入 Runtime 后立即复制或归一化，不保留调用方可变指针。

## 验证

- Runtime 公共接口测试：身份快照、逆序关闭、关闭超时、错误聚合、重复关闭、全局安装冲突和恢复。
- Transport 合同测试：应用 Filter 无法替换 Recovery，预绑定 Listener 正常，原生 Option 不再出现在公开接口。
- 适配器测试：工厂成功后自动纳入 Runtime，初始化失败不得注册资源。
- 唯一调用方迁移后执行 `make verify`、聚焦竞态测试、Worker 冒烟和干净模块缓存解析。

