# Redis

```go
client, err := redis.New(ctx, "cache", config, runtime)
client, err := redis.OpenURL(ctx, "cache", redisURL, runtime)
client, err := redis.NewWithOptions(ctx, "cache", options, runtime)
```

`New` 支持 standalone、sentinel 和 cluster；`OpenURL` 使用 go-redis 原生 URL 解析器，避免丢失协议、DB、用户名和连接参数；`NewWithOptions` 接收原生 `UniversalOptions`。

OTel 明确关闭 `db.statement`，慢命令日志只记录命令名、耗时和命令数量。TLS 客户端证书与私钥必须成对配置，最低 TLS 1.2。初始化 Ping 使用调用方 Context，连接由 Runtime 关闭。

单条 `EVALSHA` / `EVALSHA_RO` 返回 Redis `NOSCRIPT` 时不记录 ERROR，原始错误仍交给 go-redis `Script.Run` / `RunRO` 自动回退；锁竞争返回 `0` 同样不属于错误。回退执行、权限、网络等真实失败记录具体 `error` 与错误类型，不输出脚本、键名或参数值。流水线不自动执行上述脚本回退，因此流水线中的 `NOSCRIPT` 仍记录错误详情。
