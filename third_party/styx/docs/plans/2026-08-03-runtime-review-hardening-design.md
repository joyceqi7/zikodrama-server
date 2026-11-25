# REQ-2026-017 Runtime 复审加固设计

## 目标

本轮只修复复审确认的基础设施缺口，不改变 TipTop 的 HTTP 合同、短剧、广告、权益或部署拓扑。Styx 继续作为复杂度所有者：网络初始化必须能在调用方截止时间内返回；所有可启动资源必须与 Runtime 的关闭状态原子协调；Telemetry 关闭只能启动一次，但调用方超时后可以继续等待同一关闭过程；任何配置都不能把业务正文写入日志或 Span。

## 方案选择

采用 Runtime 启动门禁，而不是让每个 Task、Cron 和消息 Server 各自复制关闭状态。`Runtime.StartResource` 在统一锁内登记正在启动的资源，`Shutdown` 先切换为 closing，再等待已登记启动完成，随后逆序关闭；关闭开始后的新启动直接失败。相比“启动时才注册资源”，该方案保留构造失败回滚和确定的逆序顺序；相比仅在 `Start` 前读取状态，它同时消除检查与关闭之间的竞态。

无 Context 的 RocketMQ、Nacos SDK 调用放入单次受控 goroutine：调用方只等待自己的 Context，迟到的成功结果立即清理。Nacos SDK Logger 增加进程级单所有者租约，保存并在客户端关闭后恢复原 Logger，凭据只保存在当前租约中。Elasticsearch 删除 `capture_search_body` 配置字段并保留 Proto 编号，保证业务正文不可被重新开启。

服务端 Redis Store 只暴露业务能力和 `Ping`，不再拥有第二个 Close 入口；Runtime 是 MySQL、Redis 的唯一关闭所有者。API readiness 组合 MySQL、Redis 和 Worker 状态。Styx `v1.1.0` 已发布且保持不变，本轮代码作为后续 `v1.1.1` 候选，不在未授权时推送或移动 Tag。

## 验证

回归测试覆盖 Elasticsearch 配置合同、Nacos Logger 恢复与并发所有者、Nacos/RocketMQ 超时返回、Runtime 启动与关闭竞态、Telemetry 关闭继续等待、Redis 运行期失联 readiness。完成后依次运行聚焦测试、竞态测试、两个 Go 仓库的 `make verify` 以及 `tiptop-infra make verify`。
