# 客户端广告统计

新增独立的“运营数据 → 广告统计”菜单，路由 `/channels/ad-analytics`。继续通过 `POST /api/v1/metrics` 批量采集，不改 `/api/v1/*`、`/api/v1/open/*` 路径，不增加广告控制、领奖或 TikTok 回传任务。

## 官方能力与边界

| 能力 | 本轮使用方式 |
| --- | --- |
| `createRewardedVideoAd` / `createInterstitialAd` | 记录创建调用及取得实例；取得对象不等于广告已加载 |
| `show()` Promise | 调用时记录展示请求，成功时记录客户端确认展示 |
| `onClose` | 记录关闭；只有激励 `isEnded === true` 才记录完播并允许原有领奖流程继续 |
| `onError` / Promise 拒绝 | 记录实际来源阶段和数值错误码；不凭同一码猜测库存或取消原因 |
| 单实例 `load/onLoad/offLoad` | 当前短剧接入不调用，仅使用官方短剧示例的直接 `show` 流程 |

普通关闭可能由素材或播放错误触发，不能当作曝光。激励完播可补证展示，但其耗时是直到完播的上界，不能当作开始展示耗时。

当前不把小游戏 SDK 的可选加载接口移植到短剧。上线前仍需在目标 TikTok 容器真机验证，不使用其他产品线的版本号推断短剧支持情况。

这里的“展示请求”指业务代码调用 `show`，可能不同于平台内部取广告请求；“确认展示”指 SDK 可观察到的展示事实，不是 TikTok 计费曝光、平台入库、收入或结算。`minis_ad_show` 的业务事件回传仍在原“回传日志”查看，回传成功不等于曝光。没有已验证的 Mini Drama 广告报表 API 或曝光 webhook，本轮不自动拉取官方报表。

依据（2026-10-21 核对）：

- [Mini Drama 广告 API](https://developers.tiktok.com/docs/en/minis-sdk-in-app-ads)
- [激励广告集成指南](https://developers.tiktok.com/docs/en/tiktok-minis-in-app-ads)
- [插屏广告集成指南](https://developers.tiktok.com/docs/en/tiktok-minis-in-app-ads-interstitial-ads)
- [Mini Drama 数据分析](https://developers.tiktok.com/docs/en/mini-drama-analytics)

## 阶段与归属

每次业务调用产生一个随机 ID，固定发起时的身份、广告类型、广告位、场景、构建、系统和 TikTok 版本；所有阶段复用 ID 和原始诊断观察时间 `occurredAt`；业务统计归属使用请求首次到达服务器的时间，手机时间不作为日期或拒绝窗口依据。切换身份后旧闭包不再发指标。网络重试使用原始阶段，不重新调用 SDK。

当前接入流程为 `create → 注册 onClose/onError → show`，不调用可选 `load`，也不生成独立加载事件。采集入口仅接受当前阶段；查询不将已有加载事件或加载阶段故障计入当前漏斗、错误分布及明细，原始记录不改写、不删除。

| kind | 语义 |
| --- | --- |
| `ad_trigger` | 进入广告业务调用 |
| `ad_skipped` | 能力不可用、本地互斥、页面不在前台或冷却期，未发起展示请求 |
| `ad_create` / `ad_created` | 调用创建 / 取得实例 |
| `ad_request` | 实际调用 `show` |
| `ad_shown` | `show` Promise 成功，或激励完播补证；同一次调用最多一次 |
| `ad_closed` / `ad_complete` | 原生关闭 / 激励明确完播 |
| `ad_cancelled` | 激励未完整观看，或有明确阶段依据的用户取消 |
| `ad_failure` | 创建、准备、展示或错误回调的明确错误 |
| `ad_show_unknown` / `ad_close_unknown` | 对应阶段本地等待超时或容器销毁时尚未确认结果，不计明确失败 |

展示超时恢复页面等待，但保留原生广告互斥；没有原生终止信号时不自动重拉广告。迟到展示或完播更新原调用。

创建阶段的 `30001` 与激励展示阶段的 `30001` 含义不同。无法确认来源的 `onError` 保留 `callback`；未确认展示时不据此认定用户退出。

## 采集合同

旧 Mini 的 `{id,kind,occurredAt}` 格式继续接受。新增 `ad_*` 指标必须带 `ad`，其他 kind 不得带 `ad`，不能同时带 `postback`。示例中的值是说明合同的占位值，不是生产广告配置：

```json
{
  "events": [{
    "id": "example-ad-call-123456",
    "kind": "ad_shown",
    "occurredAt": "2026-10-21T01:02:03Z",
    "ad": {
      "adType": "rewarded",
      "adUnitId": "slot-1",
      "scene": "entry_reward",
      "clientVersion": "git-short-revision",
      "os": "ios",
      "tiktokVersion": "46.1.0",
      "stage": "show",
      "elapsedMs": 800,
      "showElapsedMs": 650,
      "evidence": "show_promise"
    }
  }]
}
```

- `adType`、`adUnitId`、`scene`、`clientVersion`、`os`、`tiktokVersion`、`stage` 必传；ID 和广告位均用字符串。广告位限 1–128 个字母、数字、下划线或横线，构建限 1–64 个字母、数字、点、下划线或横线。
- `stage` 仅允许 `capability/gate/create/setup/show/callback/close/destroy`，不接受 `load` 阶段，也不接受 `ad_load_request/ad_loaded/ad_load_unknown` 事件；无效阶段或事件令整批请求返回 400，不写入同批其他事件。
- 场景为 `entry_reward`、`episode_unlock`、`vip_reward`、`home_interstitial`、`pause_interstitial`；必须与广告类型一致。系统只取官方公开的 `ios/android`，不可用为 `unknown`；TikTok 版本只保留有界数字点分版本，不可用为 `unknown`，不采集完整 UA。
- `elapsedMs/showElapsedMs` 可选，0–86400000 的有限数值；使用单调时钟。只有实际进入 `show` 后提供 `showElapsedMs`，`ad_shown` 要求它存在。
- `evidence` 只允许 `show_promise/completed_close`，`ad_shown` 必传。其他事件不以 evidence 推断展示。
- `reason` 是固定枚举：`unsupported/busy/not_visible/cooldown/exception/sdk_error/ad_active_conflict/user_cancel/not_completed/callback_timeout/container_destroyed`，其他情况省略。
- `errorCode/subErrorCode` 只接收数值错误码字符串。**不采集错误文本**，不保存 Token、OpenID、SDK 原始对象、完整 URL 或响应。
- 应用及用户仅由认证上下文确认；自填应用请求头不能改绑消费端身份。沿用原接口每批最多 50 条及事件字段校验；合法旧 `occurredAt` 继续接收，但不再按手机时间窗口拒绝，新统计按服务器请求接收时间保存。
- `adTimeline` 是可选的同级时序字段，只随已实际调用 `show` 的观测阶段上报。统计服务仍接收原有事实，时序只输出到结构化日志。

指标只用于观测，不得作为发奖、授权、归因或结算证据。原领奖幂等键、所有权和 `isEnded === true` 条件均保持不变。

## 统计、查询与权限

`GET /admin/v1/ad-analytics` 必须带当前应用上下文，需独立权限 `operations.ad_analytics.read`。前端同时检查 `menu.ad_analytics` 菜单授权和读取权限，路由不能绕过服务端鉴权。已有角色不会自动得到新增节点。

参数：必传 UTC `from/to`（包含结束日，限最近 90 日），可选精确匹配 `adType/adUnitId/scene/clientVersion/os/tiktokVersion/errorCode`、`pageSize`（1–100，默认 20）、`cursor`。游标绑定应用和全部筛选条件；改筛选从第一页开始。不提供任意页码或用户浏览历史查询。

结果包含 `summary/daily/errors/items/hasMore/nextCursor/generatedAt`：

- 同一应用、内部用户、广告调用 ID 合并各阶段。展示后失败可以同时计入“确认展示”和“展示后失败”，这些卡片不是互斥分区。
- 请求到展示率 = **同一调用既有请求又有展示的数量 / 展示请求数**。缺失请求阶段的孤立展示仍保留展示事实，不加入该比率分子。
- “展示前失败”仅含已请求但没有展示确认的明确错误；未进入 `show` 的错误计“创建 / 准备失败”。本地拦截不计展示请求。
- 未解决的展示、关闭超时分别统计；展示后即使关闭未知，也已经是展示。迟到明确结果消除对应未知状态，明细仍保留“曾超时”。
- P50/P95 使用最近秩法，仅取有对应请求且 **show Promise 成功**的样本，同时返回 `timingSamples`。不以失败或超时时间填充样本，也不纳入完播补证；迟到成功仍按实际确认耗时计入，展示后失败保留已有展示耗时。无样本显示“—”，不当作零毫秒。
- 去掉独立加载前后按 `clientVersion` 分别查询：旧流程只有前置加载成功后才调用 `show`，加载失败不进入请求分母；旧流程的 `showElapsedMs` 不含前置加载，新流程可能包含 SDK 内部取广告时间。两版的请求到展示率及 P50/P95 不直接横向比较，不用改写历史数据来对齐。
- 返回的 `summary/daily` 只含当前直接展示流程，不再包含 `loadRequests/loaded/loadUnknown`；后台保留展示请求、展示确认、激励完播、展示或关闭未知等结果。
- 日期按原始调用 UTC 日期归属，迟到结果不挪到接收日。一次查询内总计、趋势、分布及明细使用同一只读数据库快照。
- 调用明细的阶段全部有 `elapsedMs` 时，按调用耗时排序，同一耗时按采集阶段顺序展示；批量上报和网络补报不会把业务调用排到展示之后。任一阶段缺少耗时时，整次调用回退到接收时间，同一接收时间仍按阶段顺序排列，不伪造缺失的发生时间。接收时间原值、调用列表游标及聚合计数保持不变。
- 错误筛选先合并完整调用，再匹配码，避免丢失请求阶段。错误分布按阶段/错误码/子码/原因分组，最多返回前 100 项；明细按原始时间及字符串 ID 稳定游标分页。

广告事实侧表 `metric_ad_events` 与现有计数及去重键同事务写入，主键为应用、用户、调用 ID、kind。重复阶段不增加计数，侧表失败回滚整批事务。按原始调用日期保留 90 个 UTC 自然日，原指标维护入口按索引分批清理；查询强制限制 90 日。只保留内部主体 ID，没有观看进度或剧集身份。

移除独立加载流程仅收紧采集合同和报表聚合，不修改表结构，无需新增 SQL；首次启用广告统计仍需先执行既有 `20261021_001_ad_analytics.sql`。

采集沿用有限内存队列，页面退出、网络失败三次或队列满可能丢失部分阶段；这不是无损账本。旧 Mini 没有这些阶段，因此历史不能补齐，也不能用新增漏斗直接解释旧版平台请求。对比官方报表须对齐应用、日期/时区、广告位/类型、地区及数据成熟度；本轮没有地区采集，不能直接与“美国”单地区筛选比较。

## 发布、验证与回滚

1. 全部前序增量完成后，执行一次 [20261021_001_ad_analytics.sql](../sql/migrations/20261021_001_ad_analytics.sql)。只新增广告统计表及菜单/操作节点，不重跑冻结基线，不自动执行迁移。
2. 部署新版 Server 和指标维护进程，再部署 Admin。普通角色显式授予“广告统计”菜单和“查看广告统计”，刷新会话。
3. 最后上传新版 Mini。旧 Mini 和原领奖流程继续兼容；新版 Mini 不应先对接旧 Server，否则旧入口不认识新 kind/字段会拒绝整批。
4. 真机验收激励/插屏展示、未完播、明确错误、展示及关闭超时、迟到结果；检查版本维度、重复回调、账号隔离和平台数据口径。不要通过模拟广告替代真机结论。
5. 可回滚 Mini/Admin 并保留新增表；只要仍有新版 Mini 在运行，不能把 Server 回滚到不接受这些字段的版本。

本轮自动验证覆盖聚焦 SDK 回调测试、三项目完整检查、隔离 MySQL 旧库升级、阶段幂等、事务回滚、应用/权限/游标隔离、延迟结果、分位数及保留清理，并进行桌面/移动界面检查。尚未执行业务环境 SQL、部署或上传 Mini；真机原生广告效果仍需发包验收。

## 2026-11-02：11006 互斥与调用时序

`104111006 / 11006` 与 `11006 / 11006` 合并为 `ad_active_conflict`。错误分布仍区分来源阶段，两种主码在同一阶段合并；筛选任一主码都查询整个 11006 类别。数据库明细与时序保留原始主码、子码，不改写历史记录，不通过截取其他主码猜测含义。

原生锁和页面等待分开处理：

- 已确认展示后收到错误，结束业务 Promise 并记录失败，但保留原生锁、关闭监听与有界关闭等待。五秒退避到期不是关闭证明。
- `onError` 先于 show Promise 结束且不是明确 11006 拒绝时，暂保留锁；之后 show 拒绝才释放，show 成功则继续等待关闭。始终缺少结果时超时只恢复普通播放，保留原生互斥，须关闭并重开容器恢复。
- 尚未展示的 show 拒绝或 11006 只释放本次持有的锁并立即清理实例监听；不为诊断保留五分钟观察窗口，不制造关闭事实。
- 所有释放核对持有者。已经被明确拒绝的旧实例收到迟到回调直接忽略，不释放新锁、不更新时间间隔起点，也不由这类矛盾回调发奖；其余迟到 `isEnded === true` 继续按原主体和幂等奖励流程处理。
- 只有当前持有者的实际关闭更新原生结束时刻。不增加自动 show 重试，不加入小游戏独有 API。

## 2026-11-02：50008 容器可用性

本节在前述 11006 修复基础上补齐前后台门禁。`subErrorCode=50008` 或 `innerErrorCode=50008` 归为 `container_unavailable`；主码 `101150000` 是通用错误，单独出现时不归类。错误分布按阶段归并到 50008，详情保留三个独立原始码。错误码筛选支持主码、子码、内部码；输入 50008 查询明确容器不可用类别，输入 101150000 仍能查看相同主码下的其他故障。

Mini 在 SDK 初始化完成后安装共享生命周期监听，组件及广告实例卸载只退订自己的观察，不清除页面状态。创建前与 show 前同时检查网页、宿主、浏览器明确预渲染状态、可见性版本和当前页面/主体/Offer 意图。无首次宿主事件时保持 unknown，允许满足网页门禁的首次尝试；这不是原生容器已经就绪的证明。剧目查询与广告并行，只有事件回传等待资料。

未展示的 50008 可明确结束本次调用并释放自身原生锁；已展示广告的 50008 仍等待关闭。明确未展示的失败立即移除实例监听，迟到结果不抢占新锁或补造奖励。当前前台周期停止自动重试，固定时间到期不会恢复；真实前台转换、首次宿主前台确认或用户手动操作后按最新业务条件重新评估。重复 minis.show 不会恢复；新尝试取得原生展示证明后清除旧暂停，允许正常自动衔接；插屏不会排队到前台补播。

本地跳过 `not_visible/stale_intent/container_suspended` 仍归入 `ad_skipped`，不能计作 SDK 展示失败。原始主码、子码和内部码独立保留；仅新增 [20261102_002_ad_inner_error.sql](../sql/migrations/20261102_002_ad_inner_error.sql) 的内部码列，不新增诊断列。发布顺序为 SQL → Server/维护进程 → Admin → Mini，应用不自动迁移。

## 失败诊断只进入日志服务

失败接口仍只保存一次失败的固定现场。客户端每个 SDK 保留上一次实际 show 的调用键、释放原因和收到关闭的时刻；关闭后至下一次展示之间的生命周期由下方独立的时序日志承载。

独立接口 `POST /api/v1/ad-errors` 接收 `{ "events": [{ "id": "调用ID", "ad": {广告入口与错误码}, "failure": {固定失败现场} }] }`，只允许广告失败，没有通用日志级别、原始对象或成功事件类型。复用 VisitorToken 鉴权，应用和内部用户 ID 取自可信上下文。每批最多 5 条、请求体最多 8 KiB；拒绝未知字段、历史数组和超长消息。复用现有有界内存限流实现，广告使用独立实例避免挤占运营入口容量；配置文件的 `business.ad_error_requests_per_minute: 6` 同时限制单实例、同凭据的鉴权请求数和同应用、同用户的日志请求数。鉴权前按凭据摘要拒绝超限请求，避免它们继续读取主库凭据版本；获准请求仍完整鉴权，鉴权后按用户再次限流，更换有效凭据不能绕过用户额度。缺少凭据返回原有 401；超限返回 429 和 `Retry-After`，限流依赖故障返回 503，不绕过保护。此配额不是跨实例全局配额，也不能替代网关的总入口保护。

校验后直接输出 `client ad failed`，交给既有 Styx 文件日志和 LogCollector 采集，见 [日志文件与 TLS 采集](log-file-deployment.md)。不新增 MySQL JSON、表、索引、清理任务或 Redis 写入；获准请求的鉴权继续沿用已有身份读取，前置限流不缓存或信任客户端身份。Cookie 字段按完整值脱敏，未加引号时隐藏到行末，保留前面的操作描述及后续行的故障说明。`/metrics` 只接收既有统计合同，顶层 `adFailure` 已删除；统计入库和日志输出不互相等待、也不保证原子一致。未上线的快照迁移已移除，无需执行或回滚它。

`POST /api/v1/ad-errors` 的原始请求正文在 Styx 访问日志外隔离，再交回内层业务 Handler；不预读或复制正文，鉴权、限流、8 KiB 限制与字段校验保持原行为。访问日志仍保留状态、耗时与请求/链路关联，但不采集该接口的请求正文、截断前缀或 Base64 表示；成功和参数拒绝路径均适用。错误现场只由通过校验、脱敏的 `client ad failed` 输出，避免访问日志再次保存未经筛选的消息。

日志复用可信 `application_id/user_id` 和客户端 `event_id`，同时含阶段、入口、构建、系统、TikTok 版本和三个原始错误码。`failure` 是固定字段对象：

| 字段 | 解释 |
| --- | --- |
| `showDocument/showNative/showPrerender` | 实际 show 前的网页、宿主及浏览器预渲染状态；没有调用 show 则省略 |
| `errorDocument/errorNative/errorPrerender` | 失败当刻的同类证据，原生未知保持 unknown |
| `shown/userInitiated` | 本次已确认展示与手动触发标记 |
| `lockOwnerId` | 处理错误、释放锁之前的本地持有者 |
| `previousAttemptId/previousRelease` | 同 SDK 前一次实际 show 的调用键和本地释放原因；未释放为 pending |
| `previousCloseMs` | 失败当刻距前次收到 onClose 的时间，最多 24 小时；不能代替 show 前的关闭间隔；未收到则省略。关闭不证明完播，也不证明宿主内部已清理 |
| `error/innerError` | 实际 SDK 错误消息，客户端与服务端分别脱敏，每字段最多 192 个 UTF-8 字节；不上传原始对象 |

先在后台按错误类别、构建、系统和客户端版本找到调用，再在 TLS 中用应用、内部用户与调用 ID 检索 `client ad failed`。11006 看前次是 `closed` 还是仅 `show_rejected/active_conflict`，以及是否有 `previousCloseMs`；50008 对比 show 前与错误时可见状态。网页前台、canIUse 或原生 unknown 均不能证明原生容器就绪。前次关闭已收到但仍冲突、或状态始终可见仍报 50008 时，应带原始码与客户端版本交平台调查，不能猜测宿主内部根因。

只有每次广告调用的首个 `ad_failure` 可进入日志队列，重复回调不追加；正常调用、未完播、本地拦截和结果未知不额外发送。客户端策略在 `drama-mini/src/config/adErrorPolicy.ts`：2 秒合批、每批最多 5 条、待发队列最多 20 条、同页两次请求至少间隔 10 秒且只允许一个请求在途。队列满丢弃新增现场；切换主体或会话失效清空旧现场并作废旧回调，但不重置发送间隔。发送失败（含 401/429）直接丢弃该批，后续批次退避 60 秒，不重试、不恢复登录、不在退出时补发。错误统计沿用已有独立队列与幂等规则，日志限流不采样指标错误总数。

服务端按批内 event_id 去重；不增加跨请求持久化去重。日志为尽力保存，网络响应丢失、断网、队列满、限流、页面退出或日志采集故障均可能丢失现场，无法追补旧错误缺失字段。流量随实际错误数变化，通常一个独立错误对应一条延迟请求，密集错误可合批；不能仅凭 DAU 承诺容量。既有全量统计仍有数据库写入成本，本次未进行 10 万 DAU 容量压测、TikTok 真机验收、部署或业务库 SQL 执行。

## 连续广告时序

指标批次入库成功后，服务端为携带 `adTimeline` 的阶段输出 `client ad timeline`。日志使用认证后的 `application_id/user_id`、原调用 `event_id` 和 `kind`，并附校验过的广告维度和错误码。成功与失败共用采集字段和指标队列。批内重复只输出一次，跨请求重试时查询按这四个键去重。时序不写入 MySQL，后台广告统计 API 合同不变。

| 日志阶段 | 可关联的时序 |
| --- | --- |
| `ad_closed` | 实际收到原生 `onClose` 的 `timeline.atMs` 和可见状态 |
| `ad_request` | 调用 `show()` 前的时间、可见状态、是否手动触发，以及同指标主体、同 SDK 的 `timeline.request.previousAttemptId` |
| `ad_shown` / `ad_failure` / `ad_cancelled` / `ad_show_unknown` / `ad_close_unknown` | 已调用 `show()` 后观测到的结果与可见状态；按 `kind`、`ad.stage` 和 `ad.evidence` 区分明确展示、完播补证、错误、用户取消和未知。关闭时的未完播仍由原指标记录 |

有前次关闭证据时，`ad_request` 额外记录 `previousCloseAtMs`，以及关闭之后、调用之前观测到的 `document`、`minis.hide`、`minis.show`、`minis.destroy` 序列。只保留最近八个信号，`lifecycleTruncated=true` 表示序列有缺口。重复宿主信号保留在诊断序列中；同一毫秒内按数组顺序解读。切换指标主体后不上传旧主体的前次调用关联。

在 TLS 中先用 `msg:"client ad timeline"` 检索。用请求里的 `previousAttemptId` 找到上一条 `ad_closed`，再用本次 `event_id` 找结果。实际间隔取 `ad_request.timeline.atMs - previousCloseAtMs`。这些值来自同一 WebView 的单调时钟，跨批次或乱序送达时也能比较；WebView 重建后不可跨时钟比较。没有收到事件、记录丢失或序列截断时保留未知，`visible` 仍只代表可见信号，无法证明宿主广告环境已就绪。

发布时先更新 Server，再更新 Mini。旧 Server 的严格 JSON 校验无法接收新增的 `adTimeline` 字段。原指标的业务聚合、展示间隔及重试策略沿用现有逻辑。
