# 解锁策略与权益架构


新建策略默认免费试看 1 集，每次激励广告解锁 1 集；已有策略的集数保持保存值。IAA 与混合应用可在解锁策略顶部配置 `interstitialEnabled`，省略时沿用历史开启行为，显式 `false` 停止普通插屏，不影响激励广告和已领取权益。此开关保存在原策略 JSON，不改表。服务端在公开应用配置与旧 Bootstrap 中省略关闭应用的 `interstitialAdUnitId`，现有 Mini 的首页及暂停入口因此不再拉起插屏，无需更新 Mini；开关在下次启动读取配置时生效。应用登记的插屏 code 继续保留，重新开启可复用。

商业化配置以右上角当前小程序为归属，新增策略直接保存为该应用的独立策略 JSON 和商品映射，不再让运营手工绑定小程序。开发阶段直接维护最终 Schema，不迁移旧配置，也不在首次保存时复制共享策略。规则与权益均使用策略模式。RBAC JWT、C 端身份与 VisitorToken 保持各自现有职责；此次不实现真实支付、订单、订阅回调或平台 Token 持久化。

## 领域边界

- `UnlockStrategy`：名称、修订和 `StrategyDocument`。当前后台按应用独立维护，已有记录必须具有当前应用归属。
- `ApplicationStrategy`：当前应用、变现模式、应用修订、策略和商品映射的一致快照。`X-Application-ID` 是唯一归属输入，创建无需选择或绑定应用。
- `StrategyBinding`：持久化层沿用既有应用关联列与商品映射，广告位自动读取小程序配置；后台不再暴露手工绑定。用户、广告累计和权益始终按应用隔离。
- `UnlockRule`：`type + config JSON + grants[]`。规则数组顺序决定获取入口优先级，单条规则可启停。
- `RewardGrant`：已确认的广告业务事件，保存规则、具体权益和配置修订快照。ClaimID 幂等，事实与权益在同一事务保存。
- `Entitlement`：已发放权益的类型、JSON、来源项、有效区间和撤销时间。同一内容可以存在多个独立来源。

`internal/biz/monetization/strategy.go` 中的 `RuleStrategy` 负责规则校验、能力开关和 Offer 构造，`StrategyRegistry` 按类型分发。`Project` 是现有广告 Bootstrap/VIP 展示合同的投影，不构成第二份权威配置。

`internal/biz/monetization/entitlement.go` 中的 `EntitlementStrategy` 负责模板校验、内容快照生成、存储数据校验和访问判断。播放授权统一读取当前应用、用户的有效权益，再通过类型处理器求并集。

广告领取的领域检查分为 `episodeRewardStrategy` 和 `vipRewardStrategy`，通过领取策略注册表选择。幂等恢复、快照生成及事务结算由主流程统一编排。

## 应用模式与配置范围

| 应用模式 | 可配置获取方式 |
| --- | --- |
| IAA | 入剧广告、逐集/整剧广告、广告 VIP |
| IAP | 单次购买、订阅 VIP |
| IAA+IAP | 广告和购买、订阅 |

限制针对获取方式而非权益类型，VIP 可通过广告或内购获得。新建使用当前应用模式生成模板；完整文档必须符合模式，不能借停用绕过保存校验。改变应用模式前必须先调整不兼容规则；模式变化递增应用修订，旧编辑不能覆盖。当前、旧共享、旧绑定和旧消费设置写入均检查模式。IAP 不要求广告位，也不提供启动插屏；消费读取直接使用保存的配置，不做历史规则降级。

## 当前支持范围

| 获取方式 | 可执行状态 | 权益 |
| --- | --- | --- |
| `rewarded_ad` | 已实现 | 逐集/整剧广告解锁；自然日累计广告 VIP |
| `entry_rewarded_ad` | 已实现 | 应用自然日内累计 N 条广告后允许 Mini 挂载播放器；不发放内容权益 |
| `one_time_purchase` | 可校验和保存，暂不下发购买 Offer | 剧集、整剧、定时 VIP、组合权益模板 |
| `subscription` | 可校验和保存，暂不下发购买 Offer | 对应付费周期 VIP，也可配置其他权益模板 |

VIP 是可独立配置的待遇，获取方式可分别使用广告、单次购买或订阅。

入剧广告规则使用 `config: {"requiredAdCount": N, "scope": "user", "countBy": "completed"}` 和空 `grants`，N 为 1–100。范围可选每人每日共享或每人每剧每日独立，计数可选成功展示或完整观看。Mini 根据配置触发广告、自动重拉及达标放行；服务端保存和查询事实，不增加播放解析门禁。通用展示表 `ad_shows_XX` 仅保留主体、事件、场景、广告位、可选剧目／剧集及时间，不使用 `context_json` 或保存 N。新场景（例如无剧目标的首页）可复用同一上报／查询接口。入剧尚未上线，无兼容回退；VIP 兼容保留。详细合同见 [入剧广告与通用展示事实](entry-ad-contract.md)。

当前广告 VIP 的发放模板使用 `validity: "day"`，只能组合自然日 VIP 权益；广告解锁规则必须一次完成，支持组合剧集和整剧权益。更复杂的广告任务链需要增加对应规则策略，不通过任意 JSON 脚本执行。

广告 VIP 可设置 `requireBeforePlayback: true`，由新版 Mini 在播放前要求完整观看并手动领取 VIP；同时启用入剧广告时，先满足入剧广告，再执行 VIP。该开关缺省关闭。强弹可另设 `entryAdCountdownSeconds`（0–300 秒整数），缺省或 0 秒保持立即自动拉起；大于 0 时，每次自动拉起前在现有 VIP 弹窗倒计时，提前关闭广告或完播后仍未达标时重新开始整轮倒计时。后台暂停，回到前台继续，离页取消；用户可点击观看提前开始。普通首页自愿领取入口不使用该倒计时。达标后停止拉起，仍等待用户手动领取。配置只保存并随 Bootstrap 与 VIP Offer 下发，服务端不参与倒计时或播放门禁；旧 Mini 忽略新字段，旧 `delaySeconds` 保持兼容且不启用门禁。

策略基础设置的 `entryAdDelaySeconds` 用于延后首次执行入剧和强制 VIP 门禁，0 秒立即执行，4–300 秒需至少配置一集免费。进入首集且门禁尚未达标时，Mini 先正常播放到配置位置，首集更短时自然播完，再按先入剧广告后强制 VIP 的顺序执行。门禁放行后复用同一播放器续播；直达其他集直接执行门禁。该字段随消费者策略下发，只控制客户端时序。

逐集广告规则可配置 `promptOnIncomplete`，缺省为 `false`。开启后，客户端收到 TikTok 广告未完整结束的结果时显示不可关闭的续看弹窗，默认等待 3 秒后自动重新展示广告，也可提前点击重看。`incompleteAdCountdownSeconds` 配置实际等待时间，`incompleteAdDisplayCountdownSeconds` 可单独配置弹窗显示数字的起点，均为 1–300 秒；显示数字在实际等待时间内等比例倒数到 0，例如显示 5 秒、实际等待 3 秒时，每约 0.6 秒减一。未配置显示值时与实际等待秒数相同。切到后台时暂停计时；未完整观看不发起领奖，切集或离页会取消待重开的广告。三个字段随当前规则的 `offer` 下发，广告 VIP 不使用此开关。

Mini 新旧版本可并存：旧 Mini 忽略新增的 `offer.incompleteAdDisplayCountdownSeconds`，仍使用 `offer.incompleteAdCountdownSeconds` 每秒倒数，且按该时间重看；新 Mini 在显示字段缺失时回退到实际秒数。配置显示 5 秒、实际 3 秒时，旧 Mini 显示 3→0，新 Mini 在同样的 3 秒内显示 5→0。

发布顺序为先更新服务端，再更新后台和小程序。旧 Mini 忽略新增的显示倒计时字段，按已有的实际等待字段运行；新后台在开关关闭时省略配置字段，可继续保存旧服务端可识别的规则。开启开关后，旧服务端无法识别持久化规则中的新字段，回滚服务端前须先用新版本关闭开关并保存，确认规则 JSON 已移除相关字段。

H5 通过 `offers[]` 接收有序入口，并由 `src/domain/offerStrategies.ts` 按 `method` 分发。当前只注册广告实现，未知获取方式和未接通支付入口不会被误当广告执行。`offer` / `vipOffer` 保留为既有广告合同的投影。

## 混合变现策略 JSON 示例

```json
{
  "schemaVersion": 1,
  "enabled": true,
  "freeEpisodeCount": 5,
  "rules": [
    {
      "code": "ad_unlock",
      "type": "rewarded_ad",
      "enabled": true,
      "config": { "requiredAdCount": 1, "dailyRewardLimit": 0 },
      "grants": [
        { "type": "episode_access", "config": { "selection": "next_locked_episodes", "count": 2, "validity": "unlimited" } }
      ]
    },
    {
      "code": "ad_vip",
      "type": "rewarded_ad",
      "enabled": true,
      "config": {
        "requiredAdCount": 3,
        "dailyRewardLimit": 0,
        "forceWatchCount": 0,
        "delaySeconds": 0,
        "title": "",
        "content": "",
        "failureMessage": ""
      },
      "grants": [
        { "type": "vip", "config": { "scope": "application", "adFree": true, "validity": "day" } }
      ]
    },
    {
      "code": "drama_purchase",
      "type": "one_time_purchase",
      "enabled": false,
      "config": { "productCode": "drama_access" },
      "grants": [
        { "type": "drama_access", "config": { "selection": "current_drama", "includeFutureEpisodes": true, "validity": "unlimited" } },
        { "type": "vip", "config": { "scope": "application", "adFree": true, "validity": "duration", "durationSeconds": 86400 } }
      ]
    },
    {
      "code": "vip_subscription",
      "type": "subscription",
      "enabled": false,
      "config": { "productCode": "vip_monthly" },
      "grants": [
        { "type": "vip", "config": { "scope": "application", "adFree": true, "validity": "billing_period" } }
      ]
    }
  ]
}
```

`schemaVersion` 是结构解释版本，策略 `revision` 是并发编辑修订，两者独立。未知版本、类型、字段、重复规则编码、错误阈值均被拒绝。

权益有效期支持 `unlimited`（没有计划截止时间）、`day`（应用当地次日午夜）、`duration`（明确秒数）、`billing_period`（可信支付事件提供的起止时刻）。单次购买不能使用 `billing_period`；广告同样不能使用未确认的付费周期。

`episode_access` 写入实际剧集 ID 集合；`drama_access` 保存短剧 ID、已覆盖集数快照及是否包含后续更新；`vip` 保存应用范围和免广告待遇。JSON 中不保存客户端媒体地址或平台凭据。

## 管理 API

读取复用现有 `applications.read` 权限，写入复用 `applications.update` 权限，均经过现有 RBAC JWT 和审计中间件。

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET | `/admin/v1/application-strategy` | 读取当前应用的模式、修订、策略与商品映射 |
| PUT | `/admin/v1/application-strategy` | 按当前应用新建或编辑，提交 `name/revision/document/rules` |
| GET | `/admin/v1/unlock-strategies` | 全局策略目录及受影响应用 |
| POST | `/admin/v1/unlock-strategies` | 新建，提交 `name/revision:0/document` |
| PUT | `/admin/v1/unlock-strategies/{strategyID}` | 编辑，提交读取时的 `revision` |
| DELETE | `/admin/v1/unlock-strategies/{strategyID}?revision=N` | 删除指定修订的未绑定策略 |
| GET | `/admin/v1/application-unlock-strategy` | 当前应用绑定，必须带 `X-Application-ID` |
| PUT | `/admin/v1/application-unlock-strategy` | 保存绑定和资源映射，必须带 `X-Application-ID` |

以下绑定请求仅用于旧调用方兼容，新后台不调用：

```json
{
  "applicationId": "app-a",
  "strategyId": "strategy-id",
  "revision": 2,
  "rules": {
    "drama_purchase": { "productId": "app-a-drama-sku" },
    "vip_subscription": { "productId": "app-a-subscription-sku" }
  }
}
```

服务端以可信应用上下文覆盖请求体 `applicationId`。绑定时无需选择或提交广告位；仅有广告规则时 `rules` 可为空对象。旧请求中的 `adUnitId` 兼容接收但不再保存，历史映射也不作为运行时来源。所有激励广告规则自动使用应用的激励候选集合，每次下发单个广告位。平台商品当前仅保存映射，正式启用支付前还需通过支付适配器核对商品能力和价格。

绑定更新、策略更新和删除使用事务及修订检查。修改共享策略时，在同一事务检查所有绑定应用的广告资源，并递增它们的 Offer 修订。已绑定策略不能删除。启用策略时，所有已绑定应用必须配齐 1–20 个激励视频广告位和一个插屏广告位；绑定已启用策略同样检查完整性。停用策略允许先绑定尚未配齐的小程序。新增广告规则直接使用应用配置，无需新增映射。应用广告位实际变化时原子递增 Offer 修订；新领取始终按同一规则的最新配置执行，不比较 Offer 修订号，但实际完成的广告位必须属于当前规则允许的激励集合，不重新计算 UID 分配结果。已完成的奖励仍按原有事实幂等恢复。

旧 `/application-monetization` 接口保留为独占广告配置适配入口，仍写入共享策略表中的 JSON；新后台使用上述新接口。它不能修改多个应用复用的策略。应用表中不再保存免费集数、广告阈值、VIP 文案等固定业务列。

UID 分配、旧 Mini 合同及新版失败退避详见 [激励广告位分配](rewarded-ad-allocation.md)。

## 权益与一致性

- 唯一来源项为 `(application_id, visitor_id, source_type, source_id, source_item)`。同一事件可以发多个权益，不同来源不会互相覆盖。
- 有效区间是 `[valid_from, valid_until)`；未来周期不会提前生效；撤销只作用于对应来源。
- 退款撤销整剧购买后，同一用户原有的广告剧集权益继续生效；重放支付事件不会复活已撤销来源。
- 修改或解绑策略只影响新的获取入口。已发放权益仍通过原快照判断，内容下架或失去应用授权时仍遵循目录可见性门禁。
- 广告进度按应用、用户、策略与规则编码、自然日隔离；同规则修订保持已有完成次数，换策略或规则编码开始独立进度。
- 广告 ClaimID 优先恢复已结算结果。MySQL 用户行锁保护同一用户的奖励、规则日限额及 VIP 进度。批量集合在同一事务内按最新权益重算，权益和奖励只落 MySQL，不依赖 Redis 锁。新领取使用最新规则，已成功 ClaimID 不重算实际奖励。
- 不建设观看进度、收藏、历史的服务端存储，也不新增广告尝试或播放账本。

## 后续接入支付与新玩法

1. 新获取方式实现并注册 `RuleStrategy`，提供参数校验和真实 Offer 构造；接通所需平台资源后才能打开 `Available()`。
2. H5 注册对应 `OfferStrategy`。订单创建、支付发起、取消和结果查询属于独立支付流程；广告领取接口不能用于支付履约。
3. 订单创建时固定应用、稳定消费者身份、商品、价格和完整规则条款快照。平台服务端事件验签和状态核对完成后，以具体订单或付费周期事件生成 `GrantContext`。
4. 调用统一 `BuildEntitlements`；订单状态、事件去重和权益履约必须共享事务，或使用可靠业务事件处理。未来 SQL 支付适配器可在订单事务中复用 `writeEntitlementsTx`，不采用两个无关联的提交。
5. 订阅独立保存支付状态、自动续费状态和已确认周期；取消自动续费保留已付费周期权益。重复、乱序、退款先于成功的事件需要由订单/订阅状态机处理，不能只依赖权益撤销接口。
6. 新权益类型实现并注册 `EntitlementStrategy`，提供模板校验、快照生成、数据校验和访问判断。新增类型通常不需要扩展权益表字段。
7. 金币余额或按次消费需要额外的钱包流水/扣减模型，再接入统一规则。JSON 扩展不等于任意玩法无需业务代码。

## 数据库与验证

项目当前采用部署前最终 Schema，不执行运行时自动迁移。本次更新 `sql/init.sql`：新增 `unlock_strategies`，应用保存绑定与 JSON 资源映射，`entitlements` 改为类型 JSON 来源账本，奖励记录增加规则和权益快照，广告进度增加规则隔离键。已有旧开发库需按项目流程更新结构；不要在存有待保留数据的库上直接重建。

验证包括规则注册表扩展、组合权益、跨应用隔离、订阅时间边界、共享策略并发修订、旧 Offer 按最新同规则结算及失效规则拒绝、重复事件、退款来源独立，以及真实 MySQL 的广告事务回滚。支付测试仅模拟已确认业务事件，不代表真实 TikTok 支付验收。

后台编辑器使用基础设置、规则卡片与发放权益表单，支持启停、顺序调整、多项权益和有效期选择。新规则自动生成稳定编码，排序及参数编辑不会改变已有编码；为已绑定策略新增广告规则时默认停用，确认规则后可启用，广告位自动使用小程序配置。运营界面不提供原始 JSON 输入，API 合同及服务端最终校验保持不变。
