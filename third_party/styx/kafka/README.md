# Kafka

```go
producer, err := kafka.NewKafkaProducer("events", producerConfig, runtime)
server, err := kafka.NewKafkaServer(runtime, entries...)
```

生产者基于 kafka-go，`flush_frequency` 映射到 `Writer.BatchTimeout`，确认级别使用 `KafkaRequiredAcks` 枚举，默认等待所有同步副本。压缩、SASL 和 TLS 配置遇到未知或不完整值会失败。

消费者映射 `rebalance_timeout`、`fetch_max_wait`、`fetch_min_bytes` 和 `fetch_max_bytes`。业务 Handler 成功后才提交位点；失败会重试同一消息，避免后续提交跨过失败消息。重试等待可被 Context 取消，生产者与 Server 均由 Runtime 关闭。
