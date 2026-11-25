# Styx Tracing 简化实施计划

> 设计来源：对话讨论（Kafka/RocketMQ tracing context propagation 简化）
> 创建时间：2026-03-18
> 总任务数：5
> 预计批次：3

---

## 背景

当前 styx 的 Kafka 和 RocketMQ 组件中，tracing 实现存在以下问题：

1. 每个组件都有 `disableTracing` 开关 + 手动 `tracer.Start` 创建 span，代码冗余
2. RocketMQ Producer 返回原始 `rmq.Producer`，**缺少 trace context inject**，导致 consumer 侧 Extract 拿到空 context，链路断裂
3. 接入方需要关心 `disableTracing` 配置项

## 设计决策

- **只做 trace context propagation（inject/extract），不创建 span**
- 参考 go-zero kq 的做法：始终传播 context，零配置，接入方无感知
- 移除 Kafka/RocketMQ 配置中的 `disable_tracing` 字段
- RocketMQ Producer 需要包一层实现自动 inject（`rmq.Producer` 接口含未导出方法 `isClient()`，无法直接实现，必须定义 styx 自己的类型）

## 执行概览

| 批次 | 任务 | 可并行 | 状态 |
|------|------|--------|------|
| 1 | Task 1: Proto — 移除 Kafka/RocketMQ disable_tracing 字段 | 是 | 待执行 |
| 1 | Task 2: Kafka Producer — 简化为始终 inject | 是 | 待执行 |
| 1 | Task 3: Kafka Consumer — 简化为始终 extract | 是 | 待执行 |
| 2 | Task 4: RocketMQ Producer — 包装实现自动 inject | 否 | 待执行 |
| 2 | Task 5: RocketMQ Consumer — 简化为始终 extract | 是（与 Task 4 并行） | 待执行 |

---

## 任务详情

### Task 1: Proto — 移除 Kafka/RocketMQ disable_tracing 字段

**批次**：1
**依赖**：无

**目标**：从 proto 中移除 Kafka 和 RocketMQ 的 `disable_tracing` 字段

**涉及文件**：
- `api/config/v1/data.proto`
- `api/config/v1/data.pb.go`（重新生成）

**实现步骤**：

1. 在 `data.proto` 中将以下字段标记为 reserved（保留字段编号，防止兼容性问题）：
   - `KafkaProducerConfig.disable_tracing`（field 13）→ `reserved 13;`
   - `KafkaConsumerConfig.disable_tracing`（field 16）→ `reserved 16;`
   - `RocketMQConsumerConfig.disable_tracing`（field 11）→ `reserved 11;`
2. 运行 `make proto` 重新生成 pb.go

**注意**：
- Redis/MongoDB/ES 的 `disable_tracing` **不动**，它们控制的是初始化时是否注册 OTel instrumentation，语义不同
- Server/Client middleware 的 `disable_tracing` **不动**，它们控制的是是否注册中间件

**完成标准**：
- [ ] Proto 编译通过
- [ ] Kafka/RocketMQ 配置中不再有 `disable_tracing`

---

### Task 2: Kafka Producer — 简化为始终 inject

**批次**：1
**依赖**：无（不依赖 Task 1，代码改动独立）

**目标**：移除 `disableTracing` 字段和 span 创建，始终做 trace context inject

**涉及文件**：
- `kafka/producer.go`

**实现步骤**：

1. 从 `KafkaProducer` struct 中移除 `disableTracing` 字段
2. 修改 `NewKafkaProducer`：移除 `disableTracing: c.DisableTracing`
3. 修改 `Send` 方法：
   - 移除 `if !p.disableTracing` 判断
   - 移除 `tracer.Start` 和 `span.End`
   - 保留 `otel.GetTextMapPropagator().Inject(ctx, ...)`
4. 修改 `SendBatch` 方法：
   - 移除 `if !p.disableTracing` 判断（两处）
   - 移除 `tracer.Start` 和 `span.End`
   - 保留循环内 `otel.GetTextMapPropagator().Inject(ctx, ...)`

**改动后 Send 方法**：
```go
func (p *KafkaProducer) Send(ctx context.Context, topic, key string, value []byte) error {
    msg := kafkago.Message{
        Topic: topic,
        Key:   []byte(key),
        Value: value,
    }
    otel.GetTextMapPropagator().Inject(ctx, newKafkaHeaderCarrier(&msg.Headers))
    if err := p.writer.WriteMessages(ctx, msg); err != nil {
        return fmt.Errorf("kafka: send failed: %w", err)
    }
    return nil
}
```

**改动后 SendBatch 方法**：
```go
func (p *KafkaProducer) SendBatch(ctx context.Context, msgs ...Message) error {
    if len(msgs) == 0 {
        return nil
    }
    kafkaMsgs := make([]kafkago.Message, 0, len(msgs))
    for _, m := range msgs {
        km := kafkago.Message{
            Topic: m.Topic,
            Key:   m.Key,
            Value: m.Value,
        }
        otel.GetTextMapPropagator().Inject(ctx, newKafkaHeaderCarrier(&km.Headers))
        kafkaMsgs = append(kafkaMsgs, km)
    }
    if err := p.writer.WriteMessages(ctx, kafkaMsgs...); err != nil {
        return fmt.Errorf("kafka: batch send failed: %w", err)
    }
    return nil
}
```

**完成标准**：
- [ ] `KafkaProducer` struct 无 `disableTracing` 字段
- [ ] `Send` / `SendBatch` 始终 inject，无 span 创建
- [ ] 移除 `"go.opentelemetry.io/otel/trace"` import（如果不再使用）
- [ ] 编译通过

---

### Task 3: Kafka Consumer — 简化为始终 extract

**批次**：1
**依赖**：无

**目标**：移除 `disableTracing` 参数和 span 创建，始终做 trace context extract

**涉及文件**：
- `kafka/server.go`

**实现步骤**：

1. 修改 `Start` 方法：`consumeLoop` 调用移除 `entry.Config.DisableTracing` 参数
2. 修改 `consumeLoop` 函数签名：移除 `disableTracing bool` 参数
3. 修改 `processMessage` 函数签名：移除 `disableTracing bool` 参数
4. 修改 `processMessage` 方法体：
   - 移除 `if !disableTracing` 判断
   - 移除 `tracer.Start` 和 `span.End`
   - 保留 `otel.GetTextMapPropagator().Extract(ctx, ...)`

**改动后 processMessage**：
```go
func (s *KafkaServer) processMessage(name string, reader *kafkago.Reader, handler MessageHandler, msg kafkago.Message) {
    headers := msg.Headers
    ctx := otel.GetTextMapPropagator().Extract(s.ctx, newKafkaHeaderCarrier(&headers))
    if err := handler(ctx, msg.Key, msg.Value); err != nil {
        slog.Error("kafka: handler error", "name", name, "topic", msg.Topic,
            "partition", msg.Partition, "offset", msg.Offset, "error", err)
    }
    if err := reader.CommitMessages(s.ctx, msg); err != nil {
        if s.ctx.Err() != nil {
            return
        }
        slog.Error("kafka: commit failed", "name", name, "error", err)
    }
}
```

**完成标准**：
- [ ] `consumeLoop` 和 `processMessage` 无 `disableTracing` 参数
- [ ] `processMessage` 始终 extract，无 span 创建
- [ ] 移除 `"go.opentelemetry.io/otel/trace"` import（如果不再使用）
- [ ] 编译通过

---

### Task 4: RocketMQ Producer — 包装实现自动 inject

**批次**：2
**依赖**：无

**目标**：将 `NewProducer` 从返回 `rmq.Producer` 改为返回 styx 包装的 `*Producer`，自动 inject trace context

**涉及文件**：
- `rocketmq/rocketmq.go`（重命名为 `rocketmq/producer.go`）

**背景**：
- `rmq.Producer` 接口含未导出方法 `isClient()`，无法被外部类型实现
- 必须定义 styx 自己的 `Producer` struct，包装 `rmq.Producer`
- 这是一个**破坏性变更**，调用方需要将 `rmq.Producer` 改为 `rocketmq.Producer`

**实现步骤**：

1. 将 `rocketmq.go` 重命名为 `producer.go`（与 kafka 保持一致）
2. 定义 `Producer` struct：
```go
type Producer struct {
    name  string
    inner rmq.Producer
}
```
3. 实现 `Send` 方法（自动 inject）：
```go
func (p *Producer) Send(ctx context.Context, msg *rmq.Message) ([]*rmq.SendReceipt, error) {
    otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(msg.GetProperties()))
    return p.inner.Send(ctx, msg)
}
```
4. 实现 `SendAsync` 方法（自动 inject）：
```go
func (p *Producer) SendAsync(ctx context.Context, msg *rmq.Message, callback func(context.Context, []*rmq.SendReceipt, error)) {
    otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(msg.GetProperties()))
    p.inner.SendAsync(ctx, msg, callback)
}
```
5. 实现 `SendWithTransaction` 方法（自动 inject）：
```go
func (p *Producer) SendWithTransaction(ctx context.Context, msg *rmq.Message, tx rmq.Transaction) ([]*rmq.SendReceipt, error) {
    otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(msg.GetProperties()))
    return p.inner.SendWithTransaction(ctx, msg, tx)
}
```
6. 透传其他方法：`BeginTransaction()`, `GracefulStop()`
7. 修改 `NewProducer` 返回值为 `(*Producer, func(), error)`
8. 修正注释：移除 "RocketMQ 5.x SDK has built-in OTel tracing support"（SDK 实际未实现）

**改动后 NewProducer 签名**：
```go
func NewProducer(name string, c *configv1.RocketMQConfig) (*Producer, func(), error)
```

**调用方迁移**：
```go
// 之前
var producer rmq.Producer
producer, cleanup, err = rocketmq.NewProducer("default", cfg)
receipts, err := producer.Send(ctx, msg)

// 之后
var producer *rocketmq.Producer
producer, cleanup, err = rocketmq.NewProducer("default", cfg)
receipts, err := producer.Send(ctx, msg)  // 自动 inject trace context
```

**完成标准**：
- [ ] `Producer` struct 定义完成
- [ ] `Send` / `SendAsync` / `SendWithTransaction` 自动 inject
- [ ] `NewProducer` 返回 `*Producer`
- [ ] 编译通过

---

### Task 5: RocketMQ Consumer — 简化为始终 extract

**批次**：2
**依赖**：无

**目标**：移除 `disableTracing` 和 span 创建，始终做 trace context extract

**涉及文件**：
- `rocketmq/server.go`

**实现步骤**：

1. 移除包级别 `var tracer = otel.Tracer("styx/rocketmq")`
2. 修改 `createConsumer` 方法：
   - 移除 `disableTracing := c.DisableTracing`
   - 移除 `if !disableTracing` 判断
   - 移除 `tracer.Start` 和 `span.End`
   - 保留 `otel.GetTextMapPropagator().Extract(ctx, ...)`
3. 清理不再需要的 import：`"go.opentelemetry.io/otel/attribute"`、`"go.opentelemetry.io/otel/trace"`、`semconv`
4. 删除 `consumerSpanAttrs` 函数（不再需要）

**改动后 message listener**：
```go
opts = append(opts, rmq.WithPushMessageListener(&rmq.FuncMessageListener{
    Consume: func(msg *rmq.MessageView) rmq.ConsumerResult {
        carrier := propagation.MapCarrier(msg.GetProperties())
        ctx := otel.GetTextMapPropagator().Extract(s.ctx, carrier)
        if err := handler(ctx, msg); err != nil {
            slog.ErrorContext(ctx, "rocketmq: handler error",
                "name", entry.Name, "topic", topic,
                "message_id", msg.GetMessageId(), "error", err)
            return rmq.FAILURE
        }
        return rmq.SUCCESS
    },
}))
```

**完成标准**：
- [ ] consumer 始终 extract，无 span 创建
- [ ] 无 `disableTracing` 逻辑
- [ ] `consumerSpanAttrs` 函数已删除
- [ ] `tracer` 包级变量已删除
- [ ] 编译通过

---

## 清理工作（所有 Task 完成后）

1. 检查 `kafka/kafka.go` 中 `consumerSpanAttrs` 和 `producerSpanAttrs` 是否还被引用，如不再使用则删除
2. 检查 `kafka/kafka.go` 中 `var tracer` 是否还被引用，如不再使用则删除
3. 运行 `go build ./...` 确保全部编译通过
4. 打 tag `v0.1.19`
