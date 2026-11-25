# MongoDB

```go
database, err := mongodb.NewMongoDB(ctx, "default", config, runtime)
```

Tracing 和指标绑定 Runtime 的显式 Provider。初始化 Ping 使用调用方 Context；Runtime 会注销 Observable Gauge 回调，并在有界 Context 内断开客户端。

最小连接数不能大于最大连接数，所有显式超时和空闲时间必须大于 0。支持 `primary`、`primaryPreferred`、`secondary`、`secondaryPreferred` 和 `nearest` 读取偏好。
