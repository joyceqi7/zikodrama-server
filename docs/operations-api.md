# 运营接口合同

## 主体与应用归属（2026-11-05）

后台顶部以主体和应用联动选择。新管理端使用 `X-Subject-ID` 指定官方发布、审核/上线记录及官方列表摘要的主体；未选或主体不存在时官方流程拒绝请求。应用级接口继续使用 `X-Application-ID` 或路径中的应用，服务端从应用记录解析主体，两个头冲突时拒绝。`/admin/v1/direct/*` 和两渠道轮播、热播、分类接口也必须明确选择主体；直连不要求该主体配置 TikTok 发布凭据。RBAC 仍为全局权限，不按主体授予。

| 接口 | 合同 |
| --- | --- |
| GET /admin/v1/subjects/options | 已登录；仅 id/name，供无应用配置读取权限的账号选择 |
| GET /admin/v1/applications/options | 已登录；仅 id/subjectId/name/tiktokAppId/status，不返回登录或发布配置 |
| GET /admin/v1/subjects | subjects.read；脱敏主体目录 |
| POST /admin/v1/subjects | subjects.create；name 必填，remark 可选；发布方全空允许，填写时 tiktokAppId/clientKey/clientSecret 必须配齐 |
| PUT /admin/v1/subjects/{id} | subjects.update；整体保存资料，当前发布身份的空 Secret 保留；已有官方资产禁止换 AppID/Client Key |

主体维护字段：`name`（128 字）、`remark`（500 字）、`tiktokAppId`（1–64 位数字字符串）、`clientKey`（255 字节）、`clientSecret`（255 字节）。密钥不裁剪、不回显，响应只给 `clientSecretConfigured`。更换身份必须提供新密钥。新增应用必填 `subjectId`，既有应用不允许通过普通编辑跨主体移动；其他字段保持原合同。见 [发布主体与迁移](subjects.md)。

## 内容管理与渠道独立发布 2026年10月

短剧管理统一维护共享草稿和两渠道摘要，提供直连单条/批量上线、更新、恢复原版本、下线及官方发布入口。保存资料和媒资仍回到 draft，但发布不再依赖 ready。界面删除内容状态与确认就绪按钮；历史 `/admin/v1/dramas/{id}/ready`、`/admin/v1/dramas/ready` 和 `contentStatus` 查询继续兼容原行为。

`GET /admin/v1/dramas` 新增可选 `directPublication` 摘要，包含既有修订 revision、status、version 和 pendingChanges；`directStatus=unpublished|published|offline` 为可选过滤。官方继续独立返回 publication、officialRelease、onlineVersion。无公共内容版本和 `/dramas/{id}/versions`。删除仍受 deleteBlocked 及服务端引用检查保护。

直连首次上线/更新在事务内校验当前已保存内容并冻结直连快照，相同内容复用版本，变化才递增版本。下线不读取草稿，`relist` 恢复最后所选版本；两者都不产生新版本。同主体直连应用共用上架目录，不同主体独立维护版本和上下架。

TK 同步接受可选 `{transcode:boolean}`：新版后台默认 false，仍登记和查询新视频，但跳过转码；true 时执行多清晰度流程，旧调用空对象沿用原策略。选项保存在既有 workflow_json，不新增数据库字段。同步使用当前完整草稿生成官方快照，沿用 platformVersion；送审和上线为独立人工动作，编辑不影响旧线上版本，驳回后的同内容禁止重新送审。

直连使用既有 `/admin/v1/direct/*` 管理接口；消费者 `/api/v1/*` 和 `/api/v1/open/*` 路径及旧官方合同保持不变。移除菜单不删除发布操作权限，关闭直连仍返回依赖未配置错误。

| 方法与路径 | 权限与返回 |
| --- | --- |
| GET /admin/v1/direct/dramas | operations.direct_publication.read；工作台摘要数组，后台分页展示，支持 query、status、pendingChanges、dramaId；status=pending 合并 unpublished/offline，published 表示已上线。 |
| GET /admin/v1/direct/dramas/{id}/publication | operations.direct_publication.read；draft、sourceDigest、publication、直连 versions、summary。 |
| POST /admin/v1/direct/dramas/{id}/publication/operations | 按 kind 精确检查 operations.direct_publication.publish/offline；200 返回同事务完成的持久化操作，不进入队列。 |
| GET /admin/v1/direct/publication-operations/{id} | operations.direct_publication.read；查询同步事务的持久化操作结果。 |
| GET/PUT /admin/v1/direct/carousel、/hot-dramas | operations.direct_carousel.read/update、operations.direct_hot.read/update；沿用 revision/items 合同。 |
| GET /admin/v1/direct/carousel/targets、/hot-dramas/targets | 对应 read；只返回直连当前发布摘要，包含版本与上架应用 metadata。 |
| GET /admin/v1/direct/category-rankings、/{tagID}、/{tagID}/targets | operations.direct_ranking.read；分类及自动成员，包含版本与应用摘要。 |
| PUT /admin/v1/direct/category-rankings/{tagID} | operations.direct_ranking.update；revision、items，只调整顺序与显隐。 |
| GET /admin/v1/home-preview?applicationId= | content.drama.read；官方应用预览，另逐栏目检查原 read。 |

直连操作在短剧管理展示：未上线为“上线”，已有未发布修改为“发布更新”，下线后为“重新上线 Vn”。批量上线只选未上线/已下线剧目，逐条提交当前保存内容，独立校验和幂等；单条失败不阻断后续，失败项保留并提示友好原因，离开页面后不继续提交剩余项。

投放链接绑定实际选中的 Mini，服务端按该应用权威通道检查线上快照、第一集及官方授权；生成链接不改变所选应用或任一渠道内容。

发布命令为 `{requestId,expectedRevision,kind}`。requestId 8～128 字符，只允许字母、数字及 ._:-；同键同输入重试返回同一结果，不同输入 409。kind=publish 校验并冻结当前草稿，kind=offline 下线，kind=relist 恢复原直连版本；relist 沿用 publish 权限。直连和官方下线都检查各自轮播与热播，引用存在时返回 publication_in_recommendations，后台仅 toast 提示先移除。

消费者合同保持 `status=ongoing|completed`：运营内容状态 `unpublished`/`updating` 投影为 `ongoing`，不代表官方上下线。运营可随时编辑短剧并保存为草稿，包括发布、审核和下架期间；消费者继续读取渠道当前线上版本。只有明确发布成功才切换，全部已生成版本和官方任务快照持续保护分集身份及媒资引用。任一渠道仍上架或 TK 有活动任务时不允许删除短剧。

明确的 `transcode_failed` 或 `video_registration_rejected` 不改变编辑状态。原重试接口直接使用失败任务自身的冻结内容，不查询直连发布版本，新发布 ID 生成新的转码幂等键并保留原拥有方，不能混入修改后的草稿。若需要更换素材，必须修改草稿并显式同步当前内容。同步完成后仍需明确提交审核。其他临时错误继续原阶段；`external_write_uncertain` 仍禁止盲目重试。

Album Update 的未知结果通过原人工重试接口恢复，不创建新任务或 Album。已有平台版本时，先查询并完整核对已写入的新版本；只有官方仍停在原版本、原版本及分集身份完整可见且不处于审核/申诉中时，才携带原版本号再次更新冻结内容。平台返回版本冲突时只再查询，不能改用新版本号重放。首次同步的版本 0、查询缺项、内容冲突及 Worker 均只查询。仍无法确认时返回 HTTP 409 / `album_update_unconfirmed`，保留冻结内容和外部写入检查点，日志保留原始平台故障；完成后停在待送审，不自动送审或上线。

查询核对要求最新版本返回全部分集，逐集比对序号、VID、标题和封面，并读取官方分集 ID。不能以冻结内容中的旧映射补齐查询缺项；即使版本号和声明的集数已推进，缺一集也保持未确认，等待详情完整可见后再核对。该规则不改变更新成功响应仅返回新增 ID 映射的处理，两种响应各自遵循官方合同。

视频登记仅将 `result_type=3` 视为明确拒绝。已存在 VID 的同步登记允许只返回 `byteplus_vid`，必须与请求 VID 完全一致且 `result_type` 确实缺失，随后仍由官方查询确认就绪。其他缺字段、空值或未知枚举返回 `invalid_video_response`；登记请求结果未知时，保留原任务和内容冻结状态，将该视频的检查点转为按原 VID 查询。自动及人工重试都继续查询，只有官方确认成功且 VID 一致才进入可选转码或 Album 更新，查询明确失败才允许更换素材重新送审。成功的媒资交互会清除连续技术失败次数，避免跨分集累计和扩大退避。原始响应和媒体地址不写入日志，诊断仅保留两层白名单字段形态、结果枚举和脱敏的平台错误及请求标识。

转码进度按批次返回 `completedVids`、`startedAt`、`lastProgressAt`、`lastCheckedAt` 和 `monitoringStartedAt`。仅新增成功分集推进 `lastProgressAt`，成功轮询只推进 `lastCheckedAt`。旧记录缺少时间时从首次观察开始计时，不假定历史等待时长。默认连续 30 分钟无新增完成分集进入 `failed / transcode_stalled`，这表示本系统暂停查询，不代表官方转码失败。人工“检查并继续”重置观察窗口，继续查询原 TaskID，不重建转码、不解锁冻结内容。字段保存在既有 `workflow_json`，无需 Schema 升级；旧管理端可忽略新增字段，原重试接口仍可使用。官方审核查询和确认上线保持人工操作。

## 请求校验分层

应用、活动、短剧及分集、消费设置、发布和媒资请求均先在 Handler 规范化并校验字段，再调用业务 Service。封面文件在入口检查声明大小、扩展名及内容，校验后保留可读文件流。路径标识保持字符串格式。Service 继续检查权限、唯一性、数据存在性、归属、广告位关联、发布状态及事务约束。

VOD 回调先验证原始请求签名，再解析事件字段；业务层使用同一原始字节生成去重指纹并持久化事件。未知已验签事件仍正常应答，无法解析的可选 `CallbackArgs` 记录实际错误后沿用 Provider 标识匹配。

统一媒资拥有方和多应用显式分发使用 MySQL `distribution` 配置及 `drama_distributions` 关系表；应用只生成候选关系，运营选择后才授权。详情见 drama-distribution.md。

管理员客户端在到期前五分钟调用 `POST /admin/v1/session/refresh` 自动续期，返回与登录相同的结构。续期只接受仍有效且认证版本匹配的 JWT，每次签发有效期两小时；已过期、禁用或撤销的凭据返回 401，客户端清除会话并显示登录页。

## 仪表盘

`GET /admin/v1/dashboard?from=YYYY-MM-DD&to=YYYY-MM-DD` 需要 `operations.dashboard.read` 与 `X-Application-ID`。日期为 UTC 闭区间，必填，最多 90 天，不允许未来日期。返回 `{timeZone,generatedAt,days:[{date,newUsers,rewardCompletions,episodeUnlocks,vipActivations}]}`；日期连续递增，无账本记录时为零。新增用户按首次建立应用主体计数，奖励结算按幂等流水计次，解锁按逐集权益授予集数，VIP 按授予次数。账本按应用跨 16 个物理分片汇总，客户端事实按 UTC 日分表，仪表盘读取 `dashboard_daily_YYYYMM` 月表中的每日统计。后台每分钟刷新一次，首次查询会补齐快照，页面显示查询时间，不能标记为 TikTok 实时数据。

后台在 `directory.operations`（可由运营自定义显示为“渠道管理”）下提供独立仪表盘页面，使用 `/channels/dashboard` 路径。已有库发布时按稳定编码把 `menu.dashboard` 移到该目录下，保留自定义菜单名称且不自动向普通角色授权；普通角色需明确获得目录、页面和读取权限。独立消费指标页面和 `/admin/v1/consumer-metrics` 已删除。遗留消费指标页面及操作权限应停用，且不自动向旧角色授予仪表盘权限。新增用户、奖励与权益按已有账本回查；DAU、MAU、启动、播放完播及广告曝光通过下述事件采集计算。

`GET /admin/v1/tiktok-postback-logs` 需要 `operations.postback_log.read` 与 `X-Application-ID`，支持 `event`、`outcome`、`pageSize` 和绑定应用及筛选条件的 `cursor`。`event` 允许 `video_play_request`、`ep_play`、`minis_ad_show`，`outcome` 允许 `attempt`、`success`、`failure`，每页最多 100 条。同一事实 ID 的发起与终态审计记录合并为一行：存在 `success` 时返回成功，否则存在 `failure` 时返回失败，只有尚无终态时才返回 `attempt`。响应按服务端首次接收时间倒序返回内部用户 ID、事实 ID、事件、当前结果、客户端发生时间和服务端首次接收时间；明细只在 90 日保留窗口内可查，不包含 OpenID、Token、回传 URL、SDK 完整参数或错误原文。后台在“渠道管理”下提供独立“回传日志”菜单，不以仪表盘汇总替代明细。

## 运营统计连接隔离

仪表盘、广告统计、TikTok 回传日志、剧目播放与解锁、投放链接奖励及其排行、分集和趋势查询统一使用 `data.reporting_mysql` 创建的独立 MySQL 连接池。连接池指标以 `db.instance=drama-reporting` 区分，原业务池仍为 `drama`。仅创建另一个 GORM Session 不构成连接隔离，生产初始化会调用两次 Styx MySQL 工厂。

```yaml
data:
  # 原有 mysql 配置继续保留。
  reporting_mysql:
    # 连接地址与凭据独立维护；必须访问业务数据所在的同一逻辑库。
    master: "REPLACE_WITH_REPORTING_MYSQL_DSN"
    max_open_conns: 10
    max_idle_conns: 5
    conn_max_lifetime: 1800s
    conn_max_idle_time: 600s
    slow_threshold: 0.2s
    log_level: warn
```

`reporting_mysql.master` 必填，连接地址、凭据、读副本、连接池、超时与日志参数均由该配置独立维护。整块配置缺失，或 `master` 为空、空白及占位值时，在创建任何数据库连接前拒绝启动并记录具体缺失字段，不从 `mysql` 补齐配置。完整统计配置原样交给 Styx；参数默认值、负数连接限额和其他合法性校验统一由 Styx 处理。

仪表盘优先读已发布快照，由 Worker 每分钟尝试刷新；首次没有快照时才在请求内初始化。重计算使用统计池的同一只读事务，领取租约、续期、日活补齐与发布使用业务池的短写入，避免重查询占满统计连接后无法续期。两池须连接同一逻辑库；统计池仍保留既有分表维护所需权限。消费者登录、播放授权、奖励与权益结算、埋点原始事实写入、后台内容管理和管理员审计继续使用原业务池；不改变 API、统计口径或新旧 Mini 的协议。

统计池启动失败会明确报错并停止初始化，运行中的统计查询失败直接返回既有错误，不切回业务池；就绪检查仍检查业务主池，避免运行时统计连接配额耗尽把 Mini 实例摘除。分池配置本身无需 SQL，但本轮统计刷新改造须先执行 `20261026_001_metric_refresh.sql`，顺序见 [数据库脚本](../sql/README.md#2026-10-26统计刷新租约与精确日活)。连接预算按「实例数 ×（业务池上限 + 统计池上限）」计算；配置读副本时还需计入各副本连接池。分池隔离连接等待，不隔离同一 MySQL 的 CPU、IO 和行锁。

验证：`go test ./internal/conf ./internal/data -run 'Test(Reporting|NewConfigReadsYAML|ResourcesRejectMissing)' -count=1` 覆盖独立连接加载、缺失配置的启动拒绝与日志、全部统计入口路由及统计故障后业务查询；配置隔离 `TEST_MYSQL_DSN` 后运行 `go test ./internal/data -run '^TestResourcesReportingConnectionFailureIntegration$' -count=1 -v`，验证统计连接故障不回退业务地址，并运行 `go test ./internal/data/persistence/mysqlstore -run '^TestReportingPoolIsolationIntegration$' -count=1 -v` 验证真实连接池配额和关闭状态互不影响，不修改业务数据。

## 观众用户列表

- `GET /admin/v1/consumer-users`：需要 `operations.consumer_user.read`，并通过 `X-Application-ID` 指定内部应用主键。管理员账号接口保持独立。
- 查询参数：`pageSize` 默认 20、范围 1–100；`cursor` 首页不传，后续使用上页的 `nextCursor`；`userId` 可选，按规范十进制用户主键精确匹配。游标绑定应用和用户筛选，长度最多 512，跨条件使用或非法时间/主键返回 400。标识符始终使用字符串。旧 `page` 参数返回 400，不能用页码跳到深页。
- 成功 `data` 为 `{ items, hasMore, nextCursor, pageSize }`；条目包含 `id`、`applicationId`、`identityType`（`tiktok` / `guest`）、`createdAt`。不返回 OpenID 摘要、平台凭据或虚构的最近登录时间。无结果返回空数组。
- 按创建时间、数值 ID 倒序读取 16 个用户分片；各分片按 `(application_id, created_at, id)` 索引取 `pageSize + 1` 条，合并最多 `16 × (pageSize + 1)` 条候选后全局取一页。不执行 `COUNT` 或 `OFFSET`，末页返回空 `nextCursor`。单次 SQL 保持本次读取快照，页间不保持事务；较新注册用户在刷新首页后显示。无应用上下文不能查询。
- 后台使用上一页、下一页和每页条数，刷新、筛选和切换应用回到首页，不提供实时总人数和任意页跳转。已有库先根据结构差异生成并审核消费者分页索引 DDL，再配套更新 server/admin；新旧分页合同不兼容，旧后台会收到 400，不能只发布单端。执行前必须核对索引定义，不能盲目重复创建。
- 默认页面为“用户管理 → 用户列表”，路径 `/channels/users`，编码 `menu.consumerusers`。新库通过最终 Schema 初始化；已有库根据权限数据差异生成并审核 DML，补齐目录及操作节点并移动页面，保留既有 ID 和角色授权。超级管理员无需分配权限；普通角色需明确分配新增节点。页面应用选择还需现有的 `applications.read` 权限。新增节点不会自动扩大已有角色权限，服务启动不会改库。
- 本次仅增加管理端合同，`drama-mini` 无对应管理 API 消费方，启动、登录及本地观看状态合同不变。

验证入口：`go test ./internal/biz/operations -run TestConsumerUserListLogging`；指定隔离 `TEST_MYSQL_DSN` 后运行 `go test ./internal/data/persistence/mysqlstore -run TestConsumerUserListIntegration -count=1`。

### 客户端指标采集口径（2026-09-15）

- 仪表盘 `days` 新增 `activeUsers`、`launches`、`playStarts`、`playCompletions`、`rewardedShows`、`adCompletions`、`interstitialShows`。`audience.monthlyActiveUsers` 按查询末日向前 30 个 UTC 自然日去重，不能相加每日活跃；`audience.firstReceivedAt` 是此应用首次收到信号的时间，未收到时为 null。
- DAU 是当日成功上报任一事实的认证用户数。启动是成功建立身份后的页面启动，刷新会产生新启动；同主体凭据续期不重复计数。后台不提供观看进度、剧目历史或逐用户播放查询。
- 播放开始来自官方 Player 实际播放信号，同一播放过程暂停恢复不重计，重播为新过程；结束回调表示播放到结尾，不证明逐秒完整观看。完播必须匹配同一过程的开始，按开始日期归属；迟到完成会更新原日期。
- 激励展示只来自 `show()` 成功，完整观看只来自 `isEnded === true`。激励完播须匹配展示，按展示日期归属；插屏展示单独计数，不进入激励完播率分母。比率按汇总分子/分母计算，分母为零显示“—”。这些是客户端回调统计，不是 TikTok 官方曝光、收入或结算报表，不用于授予权益。
- 行为指标没有历史回填。未升级客户端、网络丢失、页面退出、重试耗尽或时钟不准均可能漏报，不能据此推断官方结算差额。队列仅保存在当前页面内存，最多 200 条、每批 50 条、三次失败后丢弃该批并记录诊断。
- 发布顺序：已有库先根据结构差异生成并审核指标采集 DDL，再更新 server、mini 和 admin。新库使用最终 `init.sql`；服务启动不会自动建表。新后台兼容未返回 `audience` 的旧 API，显示未接入。旧客户端仍可播放和领奖，但没有行为指标。

## 当前应用的解锁策略

后台「解锁策略」使用右上角所选应用作为唯一归属，不再显示「绑定小程序」操作。

- `GET /admin/v1/application-strategy`：要求 `applications.read` 和 `X-Application-ID`，返回 `{ applicationId, monetizationMode, revision, strategy, rules }`；`strategy: null` 表示尚未配置。
- `PUT /admin/v1/application-strategy`：要求 `applications.update` 和 `X-Application-ID`，输入 `{ name, revision, document, rules }`。`revision` 使用 GET 返回的应用消费配置修订，不使用共享策略修订。`rules` 仅保存文档内支付规则的平台商品标识，广告位始终从应用配置读取。
- 保存规则、商品映射、应用归属和消费修订在同一事务中完成，并从已加锁应用行校验模式允许的规则类型；旧修订返回冲突。已有策略必须属于当前应用，归属不匹配直接拒绝；不迁移或复制共享策略。其他应用的规则和修订不变。已获得权益不因策略修改而撤销。
- 使用现有 `unlock_strategies.application_id`、`applications.unlock_strategy_id` 和修订字段，无新增表或外键。旧共享策略与绑定 API 保留兼容，后台策略页不再调用它们。
- 消费者 API 合同保持不变，继续通过应用配置解析策略。支付执行器仍保持未接入门禁，配置商品不会生成可执行购买入口。

## 分类排行榜

通过 `GET /admin/v1/category-rankings` 读取共享官方标签与当前主体自动关联数量，通过 `GET/PUT /admin/v1/category-rankings/{tagID}` 读取或更新 `{revision, items:[{dramaId, visible}]}`。上线自动维护分类，无需手动添加；运营仅调整排序与展示，隐藏保留排名和分类数量。兼容只读候选接口 `GET /admin/v1/category-rankings/{tagID}/targets`。分别要求 `operations.ranking.read/update` 和 `X-Subject-ID`，不要求应用头。详见 [首页分类排行榜](homepage.md#分类排行榜)。


## 指标日期分表与保留策略

- 明细表：`metric_events_YYYYMMDD`，保留今天和此前 89 个 UTC 自然日。写入按经过校验的事实时间路由。`metric_events_template` 仅作部署模板，不写入事实。
- 汇总表：`dashboard_daily_YYYYMM`，每行是应用的一天统计。保留每日计数和截至当日的 MAU 快照，明细清理不会删除汇总。
- `metric_event_keys` 是跨日幂等索引，只保存应用、用户、事件键、类型及首次归属日；同样保留 90 日，按索引每批清理 5000 条。重试不能通过更换日期重复计数。
- `metric_active_users_daily` 按应用、UTC 日和认证用户精确去重；与新事实同事务写入，重复上报不重复增加。MAU 在该集合上滚动三十日去重，正式刷新不再执行跨三十张原始表的递归 CTE 和窗口排序。只为尚未初始化的日期回填历史日活；已初始化的今日、昨日与历史日均跳过原始事件去重，显式重建时重新回源。
- `metric_summary_days` 记录每日新事实修订和已发布修订。普通刷新跨 UTC 日也只重算未初始化、修订变化的日期及被迟到完成影响的开始日；窗口移动时通过去重键的过期日期索引补齐受过期完成影响的开始日，保留完成请求先到的统计口径。首次初始化或显式重建才全窗扫描行为事实。日活不替代奖励与权益账本，账本与 MAU 仍独立保留每日全窗校正。
- Redis 按数据库和应用领取三十秒刷新锁，每十秒续期，释放时校验持有者。MySQL 用短事务领取租约、递增代际，再在独立只读事务计算；发布短事务再次校验持有者、代际和有效期，并原子写入全部汇总及覆盖修订。计算期间不持有状态行写锁。Redis 故障可降级到 MySQL 短租约，失效任务不能覆盖接管者。
- Worker 每分钟尝试刷新各应用汇总、准备次日表，然后清理过期日表；任一应用失败或有其他实例仍在刷新，就跳过本轮九十日事实清理。日活及日修订也按相同窗口分批清理。页面读取已有快照；只有首次无快照才同步初始化，并发初始化返回可重试的服务不可用。Worker 必须运行，否则已有快照不会继续更新。
- MAU 不累加 DAU。完整的 30 日窗口仍在保留期内时重新计算；窗口已经部分过期时保留已有快照。缺少历史快照时 `audience.monthlyActiveUsers` 返回 `null`，页面显示“—”，不能用不完整窗口制造低估的活跃数。
- 行为 SQL 先按 `kind` 聚合事件次数及最早接收时间，再用开始／展示事件的类型范围单独匹配完成键；不再让其他事件进入完成事实联表。`idx_metrics_app_kind_received(application_id,kind,received_at)` 同时支持有序聚合和完成事件范围读取，InnoDB 隐含主键列覆盖用户及事件标识。
- 模板和索引通过部署 SQL 安装。新版行为查询显式使用上述覆盖索引，已有库必须先执行 `20261026_002_metric_behavior_index.sql`，再用 `sql/upgrade-metric-behavior-index.sql` 为全部现存事件日表补齐索引，完成后才能发布全部 API 和 Worker；只改模板不会更新已有日表。运行期只允许从模板创建经过严格日期校验的日/月分表；数据库账号需要这些分表的 CREATE、DROP 和 DML 权限。启动阶段不初始化业务 Schema。

### 广告配置入口

广告位随应用新增、编辑一起填写，也可从「应用管理」列表的“广告位”按钮单独配置所选应用。创建和更新继续使用现有 `adUnits.rewardedVideo`、`adUnits.interstitial` 数组合同，不修改表结构或迁移已有广告位数据。读取、创建与更新分别要求 `applications.read`、`applications.create`、`applications.update`，入口沿用应用管理菜单，不再使用独立 `menu.adconfiguration` 菜单；历史菜单记录由前端忽略，旧 `/channels/ad-configuration` 地址转到应用管理，不自动扩大已有角色权限。
# 官方发布列表与内容范围

官方流程统一在 `/official/publications` 列表内通过状态与按钮控制，不再提供发布工作台二级页面。展示内容状态、发布进度、独立线上状态/版本、其他已授权小程序数。列表通道固定为官方，顶部当前应用仅决定投放链接的目标应用。审核和上线历史保留为只读记录；操作统一回到官方发布列表。独立“官方审核记录”菜单已移除，审核历史从“发布详情 → 审核记录”进入。

| 当前任务 | 操作及结果 |
| --- | --- |
| 未同步、已下线、撤回完成，或任务可重新同步且当前内容有变化 | 同步 TikTok / 同步更新；校验并冻结已保存草稿，默认不转码，完成后停在待送审。 |
| 已同步、待送审 | 提交送审；弹窗选择普通/加急审核。明确未受理的失败可重选优先级重试；加急额度用完默认普通审核。 |
| 审核中或申诉中 | 查询审核/申诉结果；撤回送审需确认。通过后等待管理员确认，不自动上线。 |
| 审核未通过 | 修改内容，或为尚未申诉且单项被拒的对象提交申诉；校验理由、HTTPS 附件和数量，保留单项与整版结果的区别。 |
| 审核通过、待上线 | 确认上线 / 更新上线；提交弹窗绑定的 `publicationId`，排队不等于已经上线。 |
| 已上线 | 下线、授权维护和复制投放链接。轮播/热播仍有引用时仅友好弹出提示，线上内容保持不变。 |
| 确定失败 | 按同步、审核、上线的精确权限恢复对应步骤；上线异常不重新同步或提审。原媒体任务失败按原冻结内容重建。 |
| 官方写入结果未知 | 已有版本的 Album Update 可人工先核对，再在原版本完整且未推进时携带原版本号恢复；首次同步及其他未知写入只查询。缺少可核对标识时看详情处理，禁止新同步、重复送审、盲目重试写入或另起下线。 |

- 点击发布进度打开只读详情抽屉：冻结范围、审核意见、分集处理进度和诊断。查看授权时仅有授权权限的角色可选择、撤权或重试；无该权限只能读明细。详情刷新仅读本地事实，不自动调用官方查询。
- 点击操作与确认提交前分别读取完整任务，核对任务标识、状态、步骤、审核状态、草稿差异及未知结果；上线还核对弹窗中的官方版本。状态变化时提示刷新，不能偷换新任务。服务端继续执行修订、租约、推荐引用和精确权限门禁。
- 处理中的列表每 5 秒读取本地进度，审核中每 10 分钟读取本地进度；隐藏页面或命令执行中暂停定时读取。同步结束后不自动送审，审核通过后不自动上线。离开页面后，过期操作不能继续写入、通知或刷新其他页面；切换或关闭详情后，旧详情读取不能重新打开抽屉。
- `/official/publications?dramaId=...` 恢复详情选择；旧 `/official/dramas/:id/publication` 和 `/dramas/:id/publication` 地址重定向为列表详情。旧全局 `/dramas/reviews` 地址返回官方列表；`/dramas/:id/reviews` 保留该剧只读历史，沿用官方发布菜单与 `content.drama.read`，选中官方发布导航。历史中的“官方发布”与返回按钮恢复对应列表详情，不提供查询官方、撤回、申诉、重试或上下线动作。上线记录菜单保留，记录页同样只读。
- 短剧详情 `onlineEpisodeNumbers` 只取已上线快照；编辑区的 `episodes` 不能替代线上范围。任务可选 `contentSummary.episodeNumbers` 保存冻结完整范围，`previousEpisodeNumbers` 空数组表示首次发布、`null` 表示历史范围未知；缺失时明确未记录，不使用今天的草稿或线上内容补造历史增量。
- 新内容同步、驳回修改、连载更新和草稿编辑不改变旧线上快照、原任务或授权。只有官方确认上线后才切换线上版本。审核历史只记录实际送审，时间取 `submittedAt`，同步异常留在发布列表。
- 所有动作复用既有 `/admin/v1/dramas/*` 合同、Worker 和权限；本次无新接口、字段、数据表或新菜单；需执行 `20261011_002_remove_official_review_menu.sql` 清理旧审核菜单，旧角色不会自动获授官方发布菜单。`/api/v1/*`、`/api/v1/open/*` 与 Mini 合同不变。

### 投放链接广告奖励排行

`GET /admin/v1/dashboard` 新增 `campaignRanking`，复用当前应用范围和 `operations.dashboard.read` 权限。按成功奖励结算次数降序、投放 ID 升序返回前 50 名，包含 `campaignId`、`name`、`code`、`landingUrl`、`rewardCompletions`、`rewardUsers`。领取人数按每条链接内用户去重，多次领取只算一人；不同链接的人数不能相加作为全应用去重人数。

可选 `rankingRange=all` 查询当前应用全部历史排行；省略或 `range` 跟随原闭区间 UTC 日期范围。每日趋势仍受原 90 天范围约束。只统计有可信投放归因的成功结算，停用链接保留历史贡献，无结算链接和自然流量不列入排行；不代表官方曝光或客户端广告完播。已有账本可统计，无需数据库升级或 Mini 更新。

### 广告奖励分析（所有小程序与共享剧）

运营数据默认展示广告奖励分析，原应用行为概览保留为独立标签页，不改变原有应用隔离。以下只读接口复用 `operations.dashboard.read` 精确权限；匿名或无该权限不能读取。

- `GET /admin/v1/reward-analysis`：返回 `dramas`（实际奖励发生剧的逐集结算前 50 名）、`campaigns`（链接前 50 名，包含应用名称、落地剧、逐集和 VIP 广告进度奖励次数）、`unassignedVip`（无剧归属 VIP 奖励次数及人数）。
- `GET /admin/v1/reward-analysis/dramas/{dramaID}/episodes`：目标集奖励明细，合并所选应用；不是播放量或解锁获得的全部集数。已归档避让集序或缺失历史目标无法准确恢复时，集序为 `0`，页面显示未知目标集。
- `GET /admin/v1/reward-analysis/trend?dimension=drama|campaign&targetId=...`：UTC 每日次数、当日去重人数以及整个范围独立去重的 `total`，由业务层补齐无奖励日期。剧趋势仅统计逐集奖励，链接趋势包含所归因的所有奖励。

全部接口要求显式 `scope=all|application`：`all` 查询所有小程序且客户端不发送应用头；`application` 必须通过有效 `X-Application-ID` 指定小程序。原 `/dashboard`、应用配置及消费者资源仍要求应用上下文。排行和分集可传 `range=all` 查询全部历史；省略或 `range` 使用 `from`/`to`。趋势只接受日期范围。日期合同为不晚于 UTC 今天的闭区间，最大 90 天，所有历史仅扩大排行及分集范围。

人数按 `(application_id, visitor_id)` 去重，不推断跨小程序真人身份；相同共享剧按 `drama_id` 合并，各链接按独立 `campaign_id` 保留所属应用。范围去重人数不能通过相加每日人数、各剧人数或各链接人数得到。链接奖励以结算时保存的可信归因为准，不代表所有奖励均发生在落地剧。VIP 广告进度奖励次数不等于 VIP 权益激活次数。报表不计算成本、收入、ROI、广告曝光或完播，使用既有奖励账本，无新增 Schema 或 Mini 采集。

### 剧目广告解锁贡献

- `GET /admin/v1/drama-report?scope=all&from=2026-10-01&to=2026-10-01&page=1&pageSize=20&sort=unlockCount&direction=desc&query=`：独立权限 `operations.drama_report.read`，日期按 UTC 闭区间解释，最多 90 天。`scope=application` 必须带 `X-Application-ID`，`scope=all` 明确合并所有小程序。剧库共享；搜索剧名包含或数字 ID 精确匹配，页大小 1–100，排序只接受 `unlockCount`（默认） / `unlockUsers` / `averageUnlocks` / `playUsers` / `firstEpisodeCompletionRate` 和 `asc` / `desc`。未采集播放指标为 null，界面不展示这两个指标；后端收到这两种排序时按剧 ID 稳定排序。返回 `{items,total,summary}`，包含零解锁剧。`summary` 对全部匹配剧目独立汇总成功结算次数、跨剧去重人数及人均次数，不受分页影响。广告人数按应用及 visitor_id 成对去重；人均为逐集奖励结算次数除以人数，零分母为 null。
- `GET /admin/v1/drama-report/{dramaID}/episodes`：相同权限与应用、日期筛选，返回广告奖励目标集领取人数及结算次数；VIP 排除，一次奖励批量解锁多集仍计一次。无法恢复的历史集序为 0。


### 投放链接奖励贡献

`GET /admin/v1/campaign-report` 需要 `operations.campaign_report.read`。支持 `scope=all`（所有小程序，不带应用头）或 `scope=application`（必须带 `X-Application-ID`），`from`、`to` 为包含首尾的 UTC 日期，最多 90 天。`query` 按名称或编码包含搜索，最多 100 字；`page` 默认 1，`pageSize` 默认 20、最多 100。`sort` 支持 `rewardCompletions`（默认）、`rewardUsers`、`averageRewards`；`direction` 默认 `desc`，支持 `asc`。

返回 `{items,total,summary,unattributed}`，每条链接含应用名称、投放名称/编码、落地剧、状态、链接和 `rewardUsers`、`rewardCompletions`、`averageRewards`、`episodeRewards`、`vipRewards`。人数按应用与用户去重，次数包括逐集解锁和 VIP 进度成功奖励，人均次数为次数/人数，零人数为 null。包含停用和零奖励链接；自然流量不计入。链接不因同剧而合并，人数不能直接跨链接相加。平均值排序将空值放末尾，其他排序用链接 ID 稳定排序。该报表不能代表点击、广告展示、播放或 ROI。

报表 `summary` 包含 `rewardCompletions`、`rewardUsers`、`averageRewards`、`episodeRewards`、`vipRewards`，按日期、应用和搜索范围汇总，跨剧目或链接独立去重。剧目汇总仅计逐集广告解锁。链接 `unattributed` 仅受日期和应用筛选影响，不受链接搜索影响，包含没有有效同应用链接归因的逐集和 VIP 进度奖励。VIP 进度奖励次数不等于 VIP 实际发放次数。

- `GET /admin/v1/drama-report/{dramaID}/trend`：使用 `operations.drama_report.read`，应用及 UTC 日期范围与排行一致，返回每日成功解锁次数、当日人数及范围独立去重总计。
- `GET /admin/v1/campaign-report/{campaignID}/trend`：使用 `operations.campaign_report.read`，相同范围，返回逐集及 VIP 进度成功奖励的每日次数、人数及总计。两个趋势接口均不要求奖励分析页权限，最多 90 天。

两个报表返回 `adShows: {total,rewarded,interstitial,availableFrom,complete}`，仅按所选应用和 UTC 日期汇总现有幂等埋点 `rewarded_show`、`interstitial_show`。不统计 `tt_ad_show_*` 官方回传状态，不受剧目/链接搜索和分页影响，不代表各行贡献；当前埋点没有剧目、链接或启动会话，不能按用户和奖励次数推测分配展示。客户端漏报可能少计。原始事件保留 90 个 UTC 自然日；超出窗口时 `complete=false`，`availableFrom` 提示实际可读取首日，页面提示部分统计。无需新增 SQL 或修改消费者采集协议。


### 官方已选版本上下架

- `GET /admin/v1/dramas/{dramaID}/official-release`：`content.drama.read`，读取本地确认状态，无历史时 `data: null`。仅返回 `selectedPublicationId`、`version`、`status`（已确认 online/offline），不返回冻结快照或媒体地址。
- `POST /admin/v1/dramas/{dramaID}/official-release`：`content.drama.release`（兼容既有 publish 权限），请求 `{ "action": "list|delist", "selectedPublicationId": "内部记录ID" }`。在本次请求内调用平台，200 返回确认结果；失败直接返回错误。版本记录必须与当前已选记录匹配，过期页面返回 409。并发同目标同动作串行校验，已确认成功的重复请求不重复调用平台。
- 剧目列表新增可选 `officialRelease` 摘要，`publication` 始终为最新内容同步/审核记录；`onlineVersion` 仍仅表示消费者可见版本。下架后保留已选版本，可在新版本审核期间重新上线。
- 原新版本 `.../publications/{publicationID}/publish` 接口只选择明确确认的已审核冻结版本；原 `/unpublish` 路由兼容同步下线，新管理端用上述绑定已选记录的命令。上下架操作互斥，普通内容编辑/同步/审核独立进行。

### 官方同步命令的响应

官方媒资同步、送审、刷新审核、撤回、申诉、版本选择、上下架和授权/撤权在当前请求内调用平台，成功返回 HTTP 200 的确认结果；失败直接返回既有稳定错误目录。媒体处理及审核/申诉结论仍可能等待平台，响应中的 queued 仅用于真实媒体结果查询，不表示同步写入已排队。授权批量接口返回 `authorizedCount`，不再返回 `queuedCount`。

`officialRelease` 只含 `selectedPublicationId`、`version`、`status`；`POST /admin/v1/dramas/{dramaID}/official-release` 输入 `{"action":"list|delist","selectedPublicationId":"原记录ID"}`。取消上下架任务 ID、重试任务和新字段迁移；原消费者 `/api/v1/*`、`/api/v1/open/*` 保持不变。

## TikTok 标签关联统计的主体范围

`GET /admin/v1/tiktok-tags` 必须携带有效的 `X-Subject-ID`，返回全局官方目录及英文翻译，`dramaCount` 仅统计该主体当前编辑内容，包含未上线短剧，同一短剧的重复标签只计一次。缺少或不存在的主体返回参数错误，不回退全库统计。`PUT /admin/v1/tiktok-tags/{tagID}` 仍只维护全局英文翻译，不改变短剧归属或关联数量。消费者标签和筛选合同不变。
