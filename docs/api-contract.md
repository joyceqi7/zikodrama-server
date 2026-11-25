# 消费者接口合同

## VePlayer 双通道扩展 2026年10月

所有 `/api/v1/*`、`/api/v1/open/*` 路径与包装不变，不新增版本头。应用 provider 可在新版 Mini 上线后通过运营接口按修订修改，历史无 profile 应用为官方；公开请求从 tiktokAppId、认证请求从可信 Visitor 应用解析通道。直连关闭、应用停用、配置损坏或未知 provider 会拒绝请求，不自动回退。

官方 bootstrap 的 playbackSdk 仍只有 `{ "provider": "tiktok_minis", "channel": "byteplus" }`，官方播放和奖励的 Album/Episode/VID 字段不变。直连 bootstrap 增加：

```json
{"playbackSdk":{"provider":"byteplus_web","channel":"byteplus","appId":"123456","capabilities":{"mediaPreload":false}}}
```

直连 playable 的 playback 包含 channel、vid、playAuthToken、expiresAt、posterUrl、title、episodeNumber、durationSeconds，省略 albumId 和平台 episodeId；顶层 episode.id 与请求 episodeId 均为内部 ID。expiresAt 为 UTC RFC3339，表示 Token 到期，不代表兑换媒体地址到期。

只有服务端通过目录与权益检查才签发 Token；单集、最多五集批量解析和领奖结果共用这一约束。直连 access_required 不返回 playback 或 preload，公开目录不返回 VID、凭据或个人权益。基础版不使用高级跨视频预加载，原片无需强制指定 Definition/Format。

奖励已提交而签发失败时，保留 granted/already_granted 结果及已解锁事实，省略 playback；Mini 重新解析当前集，不重看广告、不新建 claimId。SDK 失败时每次进入播放路由最多自动重新验权一次，后续由用户重试。凭据仅留内存，不写本地进度、收藏或日志。

完整三类客户端验收及外部接入边界见 [实施与上线手册](veplayer-release-runbook.md)。本文其他官方字段说明仅适用于 tiktok_minis 分支。

商业化规则与权益的当前合同见 [解锁策略与权益架构](unlock-strategies.md)。共享规则使用类型化 JSON，小程序只保存策略绑定和专属资源映射；原广告字段属于兼容投影。

消费者 H5 只调用 `/api/v1/**`。服务端不提供旧 `/v1/**` 路径或管理端路径的兼容别名。

## 公开目录（IAA 启动）

`GET /api/v1/open/catalog?tiktokAppId=<TikTok官方AppID>` 不要求 VisitorToken，不执行 OAuth 换码或创建访客。`/api/v1/open/*` 用于客户端在 SDK 与登录完成前发起的公开请求。`tiktokAppId` 必填，先裁剪空白，再校验 `^[A-Za-z0-9_-]{1,255}$`。接口重新解析当前有效应用，复用正式目录的线上版本与应用授权过滤。支持 `limit`（默认 20，最大 50）、`cursor` 及 discovery 的筛选条件；返回 `tiktokAppId`、`dramas`、整个已发布目录的分类 `tags` 和可选 `nextCursor`。数据库只查询当前页摘要，点击短剧后通过详情接口读取分集。

返回 `{ "code": 0, "message": "OK", "data": { "tiktokAppId": "0009007199254740993123", "dramas": [] } }`；`dramas` 为与首页相同的短剧摘要数组，不含播放描述、用户标识、访问凭据、广告 Offer 或个人权益。H5 用它在登录期间或失败后展示目录；播放与奖励等受保护接口保持原有鉴权要求。管理端不消费此接口。开发阶段统一使用官方 AppID，不保留 `appCode` 请求、响应或构建配置兼容入口。

## 统一响应结构

首页轮播、热门排行和播放页进入计数的接口及 Redis 约定见 [首页轮播与热门短剧](homepage.md)。More Drama 继续调用上述公开目录分页接口。

成功响应使用：

```json
{ "code": 0, "message": "OK", "data": {} }
```

错误响应使用：

```json
{ "code": 400, "reason": "machine_code", "message": "面向用户的错误说明", "data": null }
```

所有标识在 JSON 中均使用字符串。内容目录只暴露产品标识；服务提供方标识和 VID 仅允许出现在已授权的播放响应与已认证的运营接口中。

## 固定文案

消费者界面和 API 错误提示固定为英语；内容直接返回已保存的标题、简介和商业化文案。接口不协商 `Accept-Language`，不返回语言配置或翻译映射。TikTok 标签优先使用运营维护的英文展示名，未填写时回退官方名称；消费者继续接收字符串标签数组，筛选与搜索使用相同展示名。稳定错误码、认证、权益和播放授权合同不变。

## 静默登录与 Visitor

`POST /api/v1/bootstrap`

请求字段：必填字符串 `tiktokAppId`（规范化与格式同公开目录），以及 `loginCode`、可选 `visitorToken` 与 `launchQuery`。`staging` 和 `production` 必须使用本次 TikTok 静默登录返回的 `loginCode`，不降级为匿名。登录码缺失或无效属于认证错误；平台故障、服务端登录配置缺失、数据库故障和超时必须分别分类，不能统一返回 401。`development`/`test` 才允许匿名启动。静默登录不申请头像、昵称、手机号或通讯录授权；本次错误分类修复不调整上述环境准入策略。

Visitor 是业务主体，TikTok 身份按应用范围映射。当前登录账号与旧 `visitorToken` 不同时，使用当前账号自己的 Visitor，不把前一账号的权益或待领取奖励合并进来。服务端只持久化应用范围的 OpenID 摘要，不保存 OpenID 原文或平台 Access/Refresh Token。启动成功可返回当前用户的 `tiktokOpenId`，仅用于该用户的官方事件回传；H5 只在内存保留，不写日志。

`tiktokAppId` 是 TikTok 官方 AppID，来自 Mini 包内 `VITE_TIKTOK_APP_ID`，与 Client Key 不同。`applicationId` 是数据库自增主键的十进制字符串，`tiktokAppId` 原样保留官方 AppID（包括前导零），两者不互换。服务端按官方 ID 精确读取唯一启用应用，再以内部主键隔离用户、权益和投放；数据库唯一约束阻止重复注册。管理端应用对象额外返回 `id`，应用路由、策略绑定和 `X-Application-ID` 使用这个内部主键。消费者 `visitorId` 由 MySQL 永久预留号段、Redis 共享进度的发号器生成；短剧、分集、发布任务、媒资任务、投放和策略 ID 仍由数据库生成。所有标识均以字符串返回，不接收自定义的新实体 ID。启动参数不能覆盖它，也没有进程级默认应用回退。`launchQuery.campaignCode` 只能标识该应用内仍有效且目标内容可用的 TikTok 投放；无效值忽略。响应返回 `applicationId`、`tiktokAppId`、`visitorId`、新的 `visitorToken`、`identityState`、公开品牌 `appName`/`logoUrl`、应用级 `complianceBaseUrl`、变现、VIP、投放、落地目标和 `playbackSdk`。合规根地址可包含应用路径，接口不拼接 `lang` 查询参数。

官方应用的 `playbackSdk` 仍直接构造，仅包含 `provider=tiktok_minis`、`channel=byteplus`，不经过外部 Provider；H5 调用 `TTMinis.getPlayer("byteplus")`，media 缺失不影响这两个固定字段。直连应用使用下文扩展配置；任何分支均不下发 AK/SK、上传凭据、DirectUrl 或长期播放地址。

投放活动选择共享短剧或剧集后，所配置目标优先于原始启动参数中的内容标识。投放归因写入加密的 VisitorToken，并在后续已校验调用中恢复；奖励结算会记录该归因，但它不会改变奖励判定权。

下列接口都要求 `Authorization: Bearer <visitorToken>`。凭据绑定 Visitor、应用与身份状态；预发和生产拒绝匿名身份凭据。接口不接受客户端自行指定的 Visitor、应用或 Campaign。服务端不持久化 VisitorToken，也没有消费者 Session 表。

`OpenID` 是官方确认的用户标识，不是客户端自报即可采信的身份凭证。服务端用应用范围内的 OpenID 映射稳定 `visitorId`，再签发可校验的 `visitorToken` 供自有 API 使用；不能只传 OpenID 或本地随机标识领取权益。这不是额外的账号注册、资料授权或交互登录，身份与凭据不保存在 Redis，也不新增 Session 表。仅首次创建用户需要申请 UID；Redis 发号缓存不可用时该创建返回失败供客户端重试，已有用户换码登录无需申请 UID。

内部消费者主体与 TikTok 身份统一保存在 `users`：`id` 继续对应 API 的 `visitorId`，奖励和权益表的 `visitor_id` 关联该用户，关联关系由业务逻辑维护；`application_id + open_id_hash` 唯一。生产登录直接按该摘要查找或创建用户，不创建匿名中间记录。匿名开发用户的 `open_id_hash` 为 NULL，独立于正式账号，不提供权益迁移或账号合并。

当前 VisitorToken 的加密载荷包含签发时间、绝对过期时间和 MySQL 用户认证版本。有效期由服务端配置文件 `business.visitor_token_ttl_seconds` 指定，当前各环境配置为 `86400` 秒（24 小时）；普通观看和 API 请求不延长到期时间。配置修改后重启实例，仅影响新签发的凭据，已有凭据仍按签发时记录的到期时间校验。它与 TikTok 的 Access/Refresh Token 是不同凭据；平台登录响应中的令牌不会持久化，其有效期不用于判断 VisitorToken。凭据过期、版本撤销、缺失、损坏或对应 Visitor 不存在时返回 `401 unauthorized`；Mini 通过官方静默登录换发同一用户凭据，并使用原 ClaimID 重试一次未确认请求。应用禁用和依赖故障不伪装成凭据过期；静默登录恢复到不同账号时不重放旧账号领奖。

消费者启动接口不执行 IP、安装标识或请求频次限流；客户端不再采集或提交 `installationId`。反向代理后的观众不会共享某个代理地址的启动配额。管理端登录/写操作保护与每日广告奖励业务上限保留；后者是运营配置的权益规则，不是消费者入口限流。这些消费者入口限流规则不依赖 Redis；首页配置与排行按独立合同使用 Redis。

## 错误分类与客户端恢复

客户端同时依据 HTTP 状态和稳定 `reason` 判断，不解析提示文案，不把所有失败当成身份失效。

| HTTP | `reason` | 含义与处理 |
| --- | --- | --- |
| 400 | `invalid_request` | 参数有误，修改输入；不登录、不盲目重试。 |
| 401 | `unauthorized` | 自有 API 凭据无效。每个原请求最多静默恢复并重试一次，并发或迟到的同代 401 共享恢复结果。 |
| 401 | `bootstrap_failed` | 本次登录码缺失或明确无效。展示启动失败提示，不在 bootstrap 内递归登录。 |
| 403 | `application_unavailable` | 应用不存在或停用。提示当前小程序暂不可用，不重新登录。 |
| 403 | `application_forbidden` | 已校验凭据的应用范围不匹配。不重放，不重新登录。 |
| 404 | `not_found` | 内容或资源不存在，展示不可用状态。 |
| 408 | `request_cancelled` | 服务端请求上下文被取消；连接可能已断开，客户端未必能收到此响应。不能视作结算明确失败。 |
| 409 | `invalid_reward_claim` | 明确的奖励业务拒绝，不重放同一无效领取。 |
| 429 | `rate_limited` | 已达到当日广告奖励业务上限；不重新登录。客户端仍可识别外部网关的 `too_many_requests`，本服务消费者入口不产生该错误。 |
| 503 | `service_unavailable` | 身份持久化、奖励策略或广告位查询等依赖暂不可用；保留当前身份和未确认领取，稍后重试。 |
| 503 | `identity_unavailable` | 平台登录服务、网络或服务端登录配置异常；不是用户凭据失效。 |
| 503 | `playback_unavailable` | 播放服务暂不可用；不通过重新登录修复。 |
| 504 | `request_timeout` | 服务端处理或依赖请求超时；结果可能未知。 |
| 500 | `internal_error` | 未分类的内部故障；友好兜底，不输出内部异常。 |

OAuth 只有明确的 `invalid_grant` 被识别为登录码无效；`invalid_client`、缺少服务端配置、上游限流、5xx、坏 JSON 或身份字段缺失都属于 `identity_unavailable`。不能单凭上游 HTTP 401 推断观众应当重新登录。取消与超时优先于普通依赖故障分类。

H5 自身还区分 `network_error`、`request_timeout`、`request_cancelled` 和 `invalid_response`。HTTP 状态为 0 代表未确认响应，并不是业务拒绝。响应体读取也受超时控制；成功 HTTP 状态但缺失/错误的结算数据仍是结果未知，不能清除待领取记录。所有提示经过友好文案封装，日志仅记录稳定分类与 request/trace，不记录 Token、OpenID、SQL 或原始平台错误。

TikTok SDK 静默登录的失败回调、抛错及空登录码映射为 `identity_unavailable`，超时映射为 `request_timeout`，缺少官方登录能力映射为 `identity_unsupported`。这些故障在客户端终止，不发送缺失 `loginCode` 的启动请求伪装成 401。身份安装观察者失败时业务请求保持封闭，页面展示现有启动错误页；用户点击“重试”才完整重建会话，并发恢复合并为一次。普通服务端 503 不触发全局重新登录。

恢复前后必须是同一 `applicationId + visitorId` 才能重放；Token 字符串更新不代表换人。若主体变化，客户端产生 `identity_changed`，中止旧请求和旧待领取队列的后续发送，保留旧主体的记录，不将旧奖励或旧响应应用到新主体。这是异常隔离，不是日常切换账号的产品流程。

广告开始时固定主体、领取请求与待领取存储分区。断网、超时、取消、限流、5xx 或结算结果无法确认时保留原 `claimId`；只有确认成功或明确不可重试的业务拒绝才移除。后续恢复沿用原 `claimId` 做幂等领取，不能生成新标识重复计奖。此客户端隔离不等于平台提供了服务端 IAA 完成证明，也不把现有 Offer 改造成绑定观众的广告展示凭证。

本地 Storage 不可写时，待领取记录仍保留在当前页面的按主体隔离内存队列；存储恢复后合并增删变化，不复活已结算记录。只有实际持久化成功才具备跨刷新/重开页面的恢复能力，内存兜底不能作此承诺。直接领取或后台补领逐条确认成功后，运行时应用返回的 VIP 状态并立即通知当前主体的权益变化，不再额外静默登录或全量刷新启动配置；播放页清除受影响的预取许可并重新请求当前剧集授权，一次奖励解锁多集时按整剧刷新。迟到的其他主体事件不会影响当前页面。首次启动、显式配置刷新和明确 401 恢复仍完整校验身份，不绕过身份安装屏障。

## 内容目录

- `GET /api/v1/home`：返回当前页短剧摘要 `dramas` 和 `nextCursor`，分页参数与 discovery 相同。
- `GET /api/v1/discovery`：按可选 `query`、精确 `tag` 和 `status=ongoing|completed` 筛选已发布摘要。`limit` 范围为 1 至 50，默认 20；继续查询时将不透明 `nextCursor` 作为 `cursor` 返回。
- `GET /api/v1/dramas/{dramaId}`：返回短剧元数据和有序剧集。每个剧集包含 `accessMode=free|rewarded`。

运营编辑目录全局共享，消费者目录按应用固定通道隔离。官方只返回当前应用拥有或已授权的 `Published` 快照；同主体的有效直连应用共用该主体直连通道当前上架内容，不需要逐剧应用关系。不同主体的目录、更多、搜索、详情和播放均隔离。主体由服务端从可信应用解析，Mini 不新增主体参数。未上线或仅有草稿的内容不可见。草稿或失败任务不改变旧线上版本。

发现搜索会在本地化标题、说明和标签中匹配每个以空白分隔的关键词。响应包含 `items`、可选 `nextCursor` 和稳定目录 `tags`。无效筛选条件、数量限制或游标返回 `400 invalid_request`；标识仍为 JSON 字符串。

消费者 `tags` 仍为本地化 `string[]`，但每个值都来自短剧的 TikTok `tagIds`。名称解析顺序为：请求语言区域及其基础语言、英语、中文、不可变的 TikTok 官方名称。消费者接口不暴露标签标识和翻译字典。

剧集目录对象只包含 `id`、`dramaId`、`number`、`title`、`description`、`posterUrl`、`durationSeconds` 和 `accessMode`。

每个短剧摘要都显式包含 `firstEpisodeId` 和 `firstEpisodePosterUrl`（第一集首帧图，未生成或无剧集时为空字符串）；客户端可在进入第一集、真实视频首帧就绪前展示该图，续播其他集不可复用该图。客户端不得根据短剧标识推导剧集标识。

## 播放

`POST /api/v1/play/resolve`

请求字段仅包含内部 `dramaId`、`episodeId`，不含 clientVersion、provider 或平台媒资标识。服务端校验应用对应的线上快照、通道媒资条件与免费集/逐集解锁/VIP 权益。官方返回原 channel、albumId、episodeId、vid；直连返回下文 VID 授权扩展，不调用旧 play_token 接口，不返回媒体 URL。

响应状态为以下值之一：

- `playable`：包含 `channel=byteplus`、官方 `albumId`、`episodeId`、BytePlus `vid`、海报、标题、剧集编号和时长。不返回自研播放会话或水印字段。H5 不得把这些信息转换为自研播放地址，不持久化播放描述符；日志不得包含用户标识或访问凭据。
- `access_required`：包含剧集元数据、奖励 `offer` 和仅供官方 Player 预加载的 `preload`，不包含授权起播的 `playback`。`preload` 的字段结构与 `playback` 相同，允许广告前准备锁定集媒体；它不是逐用户播放凭据，客户端仍须在领奖确认并取得 `playable` 后才可切集起播。这是体验优先的软门禁，不能防止绕过客户端直接请求平台媒资。`kind` 为 `rewarded_episode` 或 `rewarded_vip`。`adUnitId` 是交给 TikTok SDK 的公开广告单元标识，不是服务端密钥。
- 逐集广告 `offer.promptOnIncomplete` 可选，缺省为 `false`；开启时仅在广告未完整观看后显示不可关闭的续看弹窗。`offer.incompleteAdCountdownSeconds` 控制实际重开等待，缺省为 3 秒；`offer.incompleteAdDisplayCountdownSeconds` 控制弹窗显示数字的起点，缺省与实际等待相同，数字在实际等待时间内等比例倒数到 0。两项秒数均为 1–300，到实际等待结束时自动重新拉起广告。显示秒数不改变 `isEnded === true` 才可领奖的条件。
- 旧小程序会忽略新增的 `incompleteAdDisplayCountdownSeconds`，继续按实际等待秒数显示；新小程序收到旧服务端缺省该字段的 Offer 时也按实际等待秒数显示。
- `unavailable`：只包含说明消息。

`POST /api/v1/play/resolve-batch` 接收同一 `dramaId` 下按顺序排列的 `episodeIds`，一次最多五集，并按输入顺序返回相同的播放决策数组。客户端用它准备当前集附近的连续播放窗口；每一项仍独立执行完整权益判断。`playable` 的 `playback` 与 `access_required` 的 `preload` 均可进入官方媒体预加载，只有前者允许起播。

返回 `playable` 只需要完成应用可见性、线上媒资和权益校验，不写逐次播放账本、观看进度、收藏或历史。观看进度、收藏和历史只在 H5 本地按应用及 Visitor 隔离保存，仅记录产品标识、时间和进度，不记录官方播放描述符或凭据。同浏览器重载可恢复；换设备或清除本地数据后不恢复，不作为权益判断依据。

## 激励广告

- `POST /api/v1/reward-grants`：TikTok 报告广告完整结束后直接领取奖励。请求包含客户端生成且重试时保持不变的 `claimId`、服务端下发的 `offerId`、`completedAdUnitId`、`isEnded=true`；逐集奖励还包含 `dramaId` 和 `episodeId`。逐集奖励成功时响应同时携带领奖目标集的 `playback`，该结果来自同一次结算已确认的权益和媒资快照，与当前页面目标匹配时 H5 无需再次调用 `/play/resolve`。后台开启连续提示且结算后仍有可解锁剧集时返回 `nextAd`，否则为 `null`。
- 结算状态回包（2026-10-24）：入剧新增可选 `entryAdStatus`，结构与入剧进度查询相同，新服务端按 `granted`／`already_granted` 保存和恢复所有有效入剧事实，Mini 保留旧 `entry_satisfied` 兼容；VIP 继续返回 `vipState`；逐集解锁新增可选 `drama`，结构与短剧详情相同，提供完整 `episodes[].accessMode`，并保留原 `playback`、`nextAd`。新版 Mini 直接消费回包，不在本地加一或推算解锁集数；旧响应缺少状态时才沿用原查询路径。附加状态读取失败不能推翻已提交结算。原请求、旧字段与路径保持；首次落账为 201，原键恢复为 200，无新增请求头或 SQL。
- `GET /api/v1/me/entry-ad`：返回当前应用、当前 Visitor 在应用时区自然日内的入剧广告进度。`enabled=false` 表示不拦截；启用时返回 `completedCount`、`requiredCount`、`progressDate` 和下一当地午夜的 `validUntil`。规则启用时提供 `kind=rewarded_entry` 的 `offer`，由 Mini 根据服务端次数判断是否仍需展示。Mini 在挂载播放器前读取此接口；服务端 `/play/resolve` 保持原有权益判断，不承担入剧门槛。入剧广告通过上述 `reward-grants` 逐条结算，领奖请求不携带剧集目标，响应的 `rewardType=entry_progress` 不发放分集或 VIP 权益。当天达到 N 条后不再追加新的入剧奖励事实；已入库 `claimId` 只恢复原结算，跨天回包返回新一天的当前进度，不把旧事实重复计入。

只有 `isEnded === true` 才能授予奖励。服务端重新解析 Offer，按应用与精确规则编码读取最新配置并校验当前奖励类型和广告单元，不因旧修订号拒绝新领取；原规则删除、停用或广告单元不匹配时仍拒绝。校验成功后，将当前用户分片中的 `reward_grants_XX` 结算流水、权益授予与投影或 VIP 进度放在同一数据库事务提交。内容广告按完整规则授予单集、批量、整剧或组合权益；VIP 广告只推进当日进度，达标后须由用户另行点击领取，才授予截至应用本地次日零点的目录访问权。同一 Visitor 重复提交已经成功的 `claimId` 时返回 `already_granted`，只恢复实际结算，不按新策略重复发奖或累计。无效领取返回 HTTP 409。

启动响应包含 `monetization`、`vipState`，并在会员权益仍可获得时包含 `vipOffer`。投放归因只决定落地内容；是否在播放前强弹 VIP 由 Mini 根据 `vip.requireBeforePlayback`（缺省关闭）决定。同时启用入剧广告时，先满足入剧广告，再执行 VIP。广告 VIP 规则可另设 `entryAdCountdownSeconds`（0–300 秒整数，只允许开启入剧强弹时配置），随 `monetization.vip` 与 `vipOffer.vip` 下发；缺省或 0 立即拉起，大于 0 时每次自动拉起前倒计时，后台暂停，提前关闭或未达标时重计，离页取消。普通首页自愿领取不使用倒计时。VIP 广告达到独立的 `requiredAdCount` 阈值后仍不自动生效，用户须调用 `POST /api/v1/vip/claim` 手动领取；重复领取返回当前有效状态。逐集广告不会累计 VIP 次数。旧 Mini 忽略新增配置，旧字段 `forceWatchCount` 和 `delaySeconds` 为兼容保留但不触发门禁；倒计时和播放前的策略顺序均由 Mini 控制，服务端只保存配置、结算与发放权益。消费者不会接收或选择配置修订号、内部广告位键或奖励规则。

逐集解锁和 VIP 目录权益的授予事实追加到 `entitlement_grants_XX`，撤销事实追加到 `entitlement_revocations_XX`；播放鉴权只读取按 application、drama、episode 目标展开的 `user_entitlement_state_XX`。每个剧集权益保留授予来源、配置修订及有效期，多集奖励共享对应 ClaimID；奖励、授予流水、权益投影和 VIP 进度在同一用户分片事务提交。这五类内部记录的 ID 由各物理表自增生成，不占用消费者用户 UID 号段；ID 只在所属物理表内唯一，查询和 `grant_id` 等关联须保留用户分片上下文。消费者 `visitorId` 的十进制字符串合同保持不变；号段与共享进度属于服务端实现。

播放授权只读取已授予的权益，不补发 VIP、不锁用户行。运营降低 VIP 广告门槛后，启动加载、进度刷新和已结算广告的重试仅显示新的可领取状态；用户仍须显式调用 `POST /api/v1/vip/claim`。领取不增加广告次数，也不改写原 ClaimID 的结算事实。

客户端直接使用确认的 `vipState.active`、`adCount`、`requiredCount`、`validUntil` 与 `progressDate`，不构造 `planId`、`planVersion` 或恒定 `version=1`，也不维护伪套餐兼容判断。奖励响应和播放授权响应中的 VIP 状态更新当前界面；领奖前的迟到播放响应不能覆盖更晚的结算。客户端按服务端 `validUntil` 撤去到期的 VIP 展示并清除旧日进度，定时到期或 WebView 回到前台均会检查；播放页随后清除预取许可并重新验权，首页恢复广告入口。到期展示不授予权益，播放和奖励继续由服务端判定，下一次响应提供新自然日进度；不为此额外静默登录。

`isEnded` 来自 TikTok 激励广告的 `onClose` 回调。服务端不创建或保存广告展示尝试，也没有尝试 token、过期时间或尝试清理配置。`claimId` 只解决响应丢失后的幂等重试，`reward_grants` 只记录最终成功结算。TikTok 目前没有公开服务端到服务端的 IAA 完成证明，因此服务端不能把该客户端回调视为广告收入证明。

## 消费者权益

- `GET /api/v1/me/unlocks`

解锁记录由服务端有效权益推导。收藏短剧、观看历史和播放位置属于 H5 本地状态，不提供消费者接口。

翻译字典仅供运营端使用，不会包含在消费者响应中。接口字段变更必须同步更新 TikTok Minis 客户端仓库和本合同。

消费者 API 只接收下文限定的运营计数信号，不提供通用客户端埋点接口，也不维护 `consumer_events` 表。H5 经 TikTok 官方 `reportEvent` 直接回传 `video_play_request`、`ep_play` 和 `minis_ad_show`，故障不影响播放或结算。广告事件需要前一实际播放剧集及本次登录身份；无剧集上下文的首页 VIP 广告目前不回传该事件，不能伪造剧集参数。奖励、解锁和会员激活从 MySQL 权威业务表统计，客户端事件不作为权益或收入证明。

目录与详情的 Redis 缓存规则见 [C 端短剧缓存](consumer-catalog-cache.md)。缓存不包含个人权益或播放授权结果，B 端提交剧变更后统一失效。

## 运营指标采集

`POST /api/v1/metrics` 使用现有 VisitorToken，不接受正文指定用户或应用。请求为 `{events:[{id,kind,occurredAt}]}`，单批 1–50 条，ID 是 16–64 位字母数字、下划线或连字符；`occurredAt` 为带时区时间，最多过去 24 小时、未来 5 分钟。入口先规范化字符串再校验，未知字段拒绝。

`kind` 允许基础运营计数 `active`、`launch`、`play_start`、`play_complete`、`rewarded_show`、`rewarded_complete`、`interstitial_show`，以及脱敏播放器质量事实 `player_first_frame`、`player_switch`、`player_stall`、`player_autoplay_blocked`、`player_error`。同一次播放或广告的开始和完成使用相同 ID，不传剧目、进度或媒体 URL。成功使用现有包装返回 `{accepted:true}`。应用与用户由凭据恢复，按 UTC 事实日期路由到 `metric_events_YYYYMMDD`；独立 `metric_event_keys` 按应用、用户、ID、类型跨日去重，重复提交不覆盖首次时间。失败保留原键重试，不触发静默登录，不影响播放授权和奖励事务。

验证：`TestMetricCollectionIntegration` 使用隔离 MySQL，覆盖认证采集到管理仪表盘读回、去重、时间/批量边界、跨日 DAU、30 日 MAU 和应用隔离；TikTok 换码使用明确测试替身，未调用真实平台。

## 新版 Mini 独立启动与状态接口

以 Mini `bc209b5df4b53412d47efde5e20d22afe29ca3ce` 为旧版边界，旧接口继续保留。新版新增 session、公开 app-config、launch/resolve、独立 VIP 状态与 Offer、入剧展示确认回包。具体请求、职责及部署顺序见 [Mini 接口职责拆分](consumer-interface-split.md)。新版不再调用 bootstrap 或 me/ad-experience 聚合接口。
