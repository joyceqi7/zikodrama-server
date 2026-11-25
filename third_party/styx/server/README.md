# Server

HTTP 和 gRPC Server 工厂统一接收 Runtime，并绑定其中的服务身份、TracerProvider、Propagator、Logger 和 MeterProvider。

```go
httpServer, err := server.NewHTTPServer(httpConfig, runtime)
grpcServer, err := server.NewGRPCServer(grpcConfig, runtime)
```

默认行为：

- 请求标识由 `requestmeta.HTTPMiddleware` 统一生成或复用安全的上游 `X-Request-ID`，同时写入 Context 和响应头。业务代码通过 `requestmeta.RequestID(ctx)` 读取；关闭访问日志或链路追踪后仍生效，应用无需再次装配或生成。
- Recovery 始终开启，但不记录 panic 值、请求正文或堆栈。
- 访问日志记录 `method`、原始转义 `path`、`client_ip`、最终 `status`、保留小数的 `duration_ms` 和 `request_id`；不记录查询参数、其他请求头或正文，路径中的标识符不会脱敏。4xx 使用 WARN，5xx 使用 ERROR，其余状态使用 INFO。
- `client_ip` 优先采用 Nginx 或网关覆盖写入且格式有效的 `X-Real-IP`，否则回退到 TCP 对端地址；业务服务不配置代理信任或解析 `X-Forwarded-For`。部署层必须覆盖该请求头，并限制客户端绕过网关直连服务端口。
- timeout 显式配置时必须大于 0。
- registry endpoint 必须包含 scheme 和 host。
- gRPC Reflection 默认关闭，仅 `enable_reflection: true` 时开启。
- TLS 证书和私钥必须成对配置，最低 TLS 1.2。

HTTP 只允许通过 `server.WithHTTPListener` 注入 Listener，或通过 `server.WithHTTPFilter` 在内建恢复 Filter 内层追加业务 Filter。gRPC 不开放原生 Kratos Option，避免调用方替换治理链路。
