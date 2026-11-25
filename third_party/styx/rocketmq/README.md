# RocketMQ

```go
producer, err := rocketmq.NewProducer(ctx, "events", config, runtime)
server, err := rocketmq.NewRocketMQServer(runtime, entries...)
```

Producer 和 Consumer 都使用显式 Propagator 传播 Trace Context。`access_key` 与 `secret_key` 必须成对配置；Producer 必须配置 endpoint 和 topics；Consumer 必须配置 endpoint、consumer_group 和 topic。

启动失败会停止已经创建或启动的客户端；Server 的 Start/Stop 有明确状态并支持幂等 Stop。Handler 失败返回 `FAILURE`，日志不记录错误正文；生产者与 Server 均由 Runtime 关闭。
