# 运营用例模块

`Service` 只承担组合根职责，将端口分配给五个独立业务服务。HTTP Handler 显式调用对应模块；各模块也可通过自己的构造函数独立装配和测试。

| 模块 | 负责内容 | 依赖 |
| --- | --- | --- |
| `DramaService` | 短剧、分集、媒资、发布、分发授权及 Worker | 内容端口、剧的媒资任务、应用身份、官方适配器 |
| `ApplicationService` | 应用身份及配置 | 应用注册表、广告位引用检查、拥有方配置查询 |
| `MonetizationService` | 解锁策略、消费设置、应用绑定 | 策略仓储、应用广告位查询 |
| `CampaignService` | 投放活动、线上目标、Minis Link | 活动仓储、应用身份、内容查询、官方客户端工厂 |
| `ReportingService` | 仪表盘统计与操作审计 | 指标查询、审计仓储 |

媒资、发布与授权是短剧生命周期的一部分，不建立与短剧并列的独立业务主体。Worker 只持有 `DramaService`，不再访问整个运营服务。跨模块约束通过明确端口检查，不将全部 `Repositories` 注入每个模块。

外部平台客户端和持久化实现仍位于适配器层；HTTP 参数校验及错误映射仍位于 `internal/service/operations`。模块拆分不改变数据库 Schema、API 合同或任务状态机。

验证：`go test ./internal/biz/operations ./internal/service/operations`，随后执行 `make verify`。
