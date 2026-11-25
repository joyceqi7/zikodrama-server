# TikTok IAA 回传与诊断

适用分支：三个项目的 `feat/back`，基于 2026-10-20 最新 main。复用 `/api/v1/metrics` 与 `/admin/v1/tiktok-postback-logs`，不改 `/api/v1/*`、`/api/v1/open/*` 路径，不新增业务任务或回传重试服务。

## 事件及真实语义

| 官方事件 | 触发事实 | SDK 参数 |
| --- | --- | --- |
| `video_play_request` | 用户进入当前剧集，取得可信元数据后，一次切集只发一次；预加载、权益刷新、恢复授权不重复发送 | `minis_drama_name`、`minis_drama_id`、`episode_number`、`if_last_ep` |
| `ep_play` | 播放器实际开始播放，一次请求最多一次；暂停恢复不重复 | 同上 |
| `minis_ad_show` | 激励或插屏展示成功；激励完播领奖仍要求官方 `isEnded === true` | 以上上下文及 `if_first_time`、`minis_name`、`minis_id`、`open_id`、`ad_type` |

`minis_drama_id` 是内部业务剧目 ID，不是 TK Album ID；`minis_id` 是 ClientKey，不是数字 TikTok AppID；集号按字符串传入。播放事件的 `if_last_ep` 用总集数判断，不能因为授权响应只有当前一集就填 1。

广告的 `episode_number` 仅使用实际播放的上一集，不把待解锁的目标集当作上一集。入剧广告先复用/加载可信目录摘要，最多额外等待 1.5 秒；无历史播放时省略上一集并记录警告。`if_last_ep`、`if_first_time` 无法确定时为 null。没有剧目上下文的首页广告继续发送已知应用信息并记录警告，不虚构剧目、集号或播放请求。广告发起时固定身份及上下文；切换账号后不把迟到事件归给新主体。

官方材料未明确无剧目场景的字符串字段填法，也未定义 `if_first_time` 的跨会话范围。本实现不把它解释成用户终身首次，沿用当前 Mini 运行会话内“本剧首次广告”。这两点需向平台确认后再收紧合同，后台展示当前语义。

外层 `reportEvent` 的 `eventName`、`params` 必传，`callback` 可选；集成指南列出业务字段及类型，不能据此把每个嵌套字段都声明为官方硬性必填。SDK 自动附加时间戳、消息 ID 及桥接封装，诊断快照只表示调用时提交的 `params`，不冒充完整网络报文。

## 结果与幂等

| outcome | 解释 | 是否计 SDK 尝试/失败 |
| --- | --- | --- |
| attempt | 已发起 SDK 调用，等待回调 | 尝试 +1 |
| success | SDK 回调 `isSuccess === true` | 成功 +1；不证明 TK 服务端入库、归因或计费曝光 |
| failure | 调用抛错或 SDK 明确拒绝 | 失败 +1 |
| unknown | 调用后 10 秒仍无回调 | 不计失败；允许迟到结果补充 |
| skipped | 能力或身份不可用，未调用 SDK | 不计尝试或失败 |

所有阶段复用同一事实 ID 和原始 `occurredAt`。超时不自动重发官方事件；迟到回调在有界窗口内更新结果，最多再保留 5 分钟。切换身份取消旧监听闭包。SDK 连续/重复回调只取一次终态。SDK 抛错发生在成功回调之后时不能覆盖成功。

API 网络重试仅重发原指标批次，保留原始 ID、时间和参数，绝不再次调用 SDK；原有有限队列、三次失败上限及主体隔离继续有效。进程/页面退出可能丢失未确认诊断，诊断不是财务或权益账本。

列表按同一应用、用户、事实 ID 和官方事件合并，终态优先级 success > failure > unknown > skipped > attempt。历史客户端的三阶段格式继续接受，不回填不存在的参数，不重算历史能力缺失的旧口径。

## 采集合同

旧客户端保持 `{id,kind,occurredAt}`。新增可选 `postback`，仅允许 `tt_*` 回传指标携带：

```json
{
  "events": [{
    "id": "example-postback-123456",
    "kind": "tt_ad_show_unknown",
    "occurredAt": "2026-10-20T01:02:03Z",
    "postback": {
      "scene": "entry_reward",
      "clientVersion": "git-short-revision",
      "params": {
        "minis_drama_name": "Example",
        "minis_drama_id": "9007199254740993",
        "if_last_ep": null,
        "if_first_time": 1,
        "minis_name": "Example Mini",
        "minis_id": "client-key",
        "open_id": "[REDACTED]",
        "ad_type": "RewardedVideoAd"
      },
      "warnings": ["missing_previous_episode", "unknown_last_episode"],
      "elapsedMs": 10000,
      "reason": "callback_timeout"
    }
  }]
}
```

- `scene`：playback、entry_reward、episode_unlock、vip_reward、home_interstitial、pause_interstitial。
- `clientVersion`：构建时注入 Git 短修订，不可用时 unknown，1–64 个字母、数字、点、横线或下划线。
- `params`：上表字段白名单，保留缺失、null 和空字符串的区别。字符串最多 256 字符；标志仅 0、1、null。真实 OpenID 只交 SDK，采集端替换为固定占位符，服务端拒收未脱敏 OpenID 及额外参数。
- `warnings`：missing_drama_context、missing_previous_episode、unknown_last_episode。是上下文警告，不等同于平台拒收。
- `elapsedMs`：可选，0–86400000 的有限数值，使用单调时钟。
- `reason`：可选，sdk_rejected、exception、unsupported、identity_changed、identity_missing、callback_timeout、invalid_params。
- `errorCode`、`subErrorCode`：可选数值错误码字符串（可有负号，最多十位）。
- `errorMessage`：只提取消息，客户端脱敏后服务端再次清除 URL、凭据并截断 512 字符。禁止原始 SDK 对象、Token、Cookie、签名及媒体地址。

应用和用户由认证上下文确认；所有业务标识保留字符串精度。新增 kind 是 tt_video_request/tt_episode_play/tt_ad_show 的 unknown、skipped 后缀，原有 kind 保持不变。

## 存储与查询

日表和去重索引继续保存 90 个 UTC 自然日的计数。新增 `metric_postback_details` 按应用、用户、事实 ID、阶段唯一，与计数同事务写入；写入失败回滚去重键。诊断按首次接收时间保留 7 天，网络重试不延长。`MaintainMetrics` 分批清除超期明细，查询也强制排除过期诊断，即使清理尚未运行仍不返回。

不建设用户观看历史，不将这些参数用于授权、权益、结算或归因判断。保留既有独立读取权限 `operations.postback_log.read`，不新增菜单、不扩大现有角色。

`GET /admin/v1/tiktok-postback-logs`：保留 event、outcome、pageSize、cursor；新增可选 scene、clientVersion、errorCode、from、to，文本为精确匹配。from/to 同时传入 UTC 日期，结束日包含当天，范围限最近 90 日。扩展筛选绑定游标，切换条件后从第一页开始。跨应用复用游标会被拒绝；旧无扩展筛选的七段游标继续有效。

后台默认查询最近七个 UTC 自然日，重置筛选也回到七天；仍可手动选择最近九十天。指定日期时直接读取对应日表，并通过 `metric_event_keys` 和日表主键补齐这些事实位于范围外的阶段，最后合并结果并筛选日期、状态及游标。这样既减少其他日期的明细扫描，也保留次日收到成功回调、成功优先和最早发生时间的原有口径。旧调用方不传日期时仍查询完整保留期，不改变接口合同。

仪表盘首次刷新和每日首次刷新校正完整保留窗口；同日后续刷新只重算昨日、今日及被迟到完播影响的历史开始日。历史回补保留原账本计数和 MAU，当前 MAU 只扫描必要的三十日活跃窗口。沿用应用状态行锁、事务快照和一分钟刷新间隔，不新增任务、字段或迁移 SQL。

每行返回可选 `details`；只有当前结果阶段且七天内的明细才返回。旧版或过期时省略，页面明确显示“旧版未采集或明细已过期”。场景、构建及错误码过滤只匹配存在明细的记录。详情显示三个事件的参数、缺失警告、固定原因、错误码和耗时，脱敏 JSON 支持选择复制。仪表盘有独立权限时提供明细入口。

本地未采集国家维度，不应将这里的全地区统计与 TK 仅美国的筛选直接比较。需对齐应用、UTC/平台时区、地区、广告类型、日期和数据成熟度；SDK 接受量与官方计费曝光不是同一口径。

## 发布与回滚

1. 在已有全部前序增量的库执行 `sql/migrations/20261020_001_postback_details.sql`。不重跑冻结基线，不自动迁移。
2. 发布配套 Server，再发布 Admin，最后上传新版 Mini。旧 Mini 可一直运行；新 Mini 不能提前投放到不认识新 kind/字段的旧 Server，否则整批会被旧入口拒收。
3. 更新指标维护进程，确认每日清理运行。检查旧客户端计数、三事件真实设备回调、参数警告和权限隔离。
4. 如需回滚 Mini/Admin，可回到旧包；只要新 Mini 仍在使用，不应把 Server 回滚到不接受新字段的版本。新增表可保留，不删除已有计数或去重键。

本轮仅本地实现与隔离数据库验证，没有在测试或生产业务库执行 SQL，也没有发布 Mini。

查询优化的隔离验证使用 MySQL 8.4、45 日日表及 54 万条阶段事实：最近七日列表三次平均约从 638ms 降到 198ms；同批数据的仪表盘完整刷新约 593ms，同日增量约 301ms，返回计数及 MAU 一致。这是本机对照数据，不代表线上耗时承诺。跨日迟到成功、历史开始日完播回补和稳定分页均有专项回归；本次优化不新增迁移 SQL。

## 依据

- [官方 IAA 中间事件指南](https://bytedance.sg.larkoffice.com/docx/U0BLdQBBNozdWfxRzWjljPdxgkf)，含作者对业务剧目 ID 与 ClientKey 的澄清。
- [官方 reportEvent API](https://developers.tiktok.com/docs/en/minis-sdk-share)，外层参数及回调合同。
- 2026-10-20 读取的当前官方 Drama SDK：reportEvent 的成功回调发生于 SDK 提交本地日志后，不是服务端入库 ACK；上线前仍需平台报表和真机交叉验证。
