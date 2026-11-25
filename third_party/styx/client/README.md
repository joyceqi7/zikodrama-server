# Client

HTTP 和 gRPC Client 工厂要求调用方 Context 与统一 Runtime；直连地址和 Runtime 中的服务发现能力二选一。

```go
httpClient, err := client.NewHTTPClient(ctx, httpConfig, runtime)
grpcConn, err := client.NewGRPCClient(ctx, grpcConfig, runtime)
```

OAuth、云厂商开放 API 等需要直接操作标准库 `http.Request`/`http.Response` 的场景，
使用原生 HTTP Client 工厂：

```go
httpClient, err := client.NewExternalHTTPClient(ctx, "tiktok-oauth", 8*time.Second, runtime)
```

该工厂使用独立 Transport，显式绑定 Runtime 的 TracerProvider 与 Propagator，并由
Runtime 在退出时关闭空闲连接。Trace 只记录 HTTP method、status 和固定错误分类，
不记录 path/query/body，也不自动创建 HTTP 指标。

约束：

- `endpoint` 与 `discovery` 不能同时配置。
- timeout 显式配置时必须大于 0。
- gRPC 默认不允许明文；无 TLS 时必须配置 `insecure: true`。
- `tls` 与 `insecure: true` 不能同时配置。
- 下游失败日志不记录请求或错误正文。
- Nacos 跨 group 仅在 discovery 为 Styx Nacos Registry 时支持。

连接成功后自动纳入 Runtime 的逆序关闭流程。
