> 2026-10-11 简化补充：当前直连运营仅保留“直连发布”单页，列表按钮完成待上线 → 已上线 → 待上线；直连上线记录、二级发布页、首页预览及复制官方配置均已移除。下文此前评估中的相应条目不再实施，现行合同以上线手册与 operations-api.md 为准。

> 2026-10-11 流程调整：当前实现以 [上线手册](veplayer-release-runbook.md) 和 [运营接口合同](operations-api.md) 为准。直连发布合并至短剧管理，删除手动确认就绪步骤；完整草稿可直接直连上线或独立同步官方。重新上线恢复原版本，更新才发布新草稿；官方登记必需、转码可选。下文原方案中独立直连发布菜单与 ready 门禁属于历史设计，不再作为当前实现要求。


# VePlayer 双通道完整实现方案

本文保留初版双通道设计记录。2026-10-10 已改为待上线只表示就绪，短剧不维护版本，直连上架生成自己的版本、TikTok 沿用官方版本与任务快照、所有直连应用共用目录；开发期旧直连任务和应用发布关系直接删除。当前合同与迁移以 [运营接口合同](operations-api.md) 和 [上线手册](veplayer-release-runbook.md) 为准。

日期：2026-10-10

适用项目：drama-server、drama-admin、drama-mini

实施分支：三个项目的 feat/veplayer

状态：三端代码已实现，本机自动化与后台浏览器验证已完成；真实旧 Mini 包、License 和目标 TikTok 容器待上线验收。

本次在现有三个项目内增加独立 BytePlus VePlayer 播放通道。后台将官方运营和直连运营分开入口、分开发布状态、分开首页配置；新版 Mini 共用页面、广告和权益流程，通过播放器适配器支持两种通道。已上线旧 Mini 无需升级，继续使用原路径、原官方字段及原有业务行为。

本方案是三端实施的主文档。表中标为“新增”的路由、字段和表已在 feat/veplayer 实现。实际配置、部署步骤与本次验证证据见 [实施与上线手册](veplayer-release-runbook.md)。功能实现与生产开放是两个阶段，真正上线前仍须完成旧包和目标 TikTok 容器验收。

## 1 已确认的目标与边界

| 项目 | 实施决定 |
| --- | --- |
| 运行容器 | 两种 Mini 均运行在 TikTok Minis 内 |
| 官方播放 | 保留 TTMinis.getPlayer("byteplus")，使用 TikTok Album、Episode 和 BytePlus VID |
| 直连播放 | 不登记到 TikTok 媒资库；使用 BytePlus VID 和服务端签发的临时播放凭据 |
| License | 独立播放器按基础版设计，不依赖高级跨视频预加载 |
| 转码 | 可选；符合目标环境要求的原片可以直接播放 |
| Mini 代码 | 一套新版代码、一套页面、一套 API 客户端；两个播放器适配器 |
| Mini 接口 | 保留 /api/v1/* 和 /api/v1/open/*，保留所有现有业务路径 |
| 兼容方式 | 复用 playbackSdk.provider 并扩展字段，不增加版本请求头或 clientVersion 参数 |
| Admin | 与服务端同步升级，不承担旧版 Admin 兼容 |
| 后台运营 | 官方与直连分开菜单入口，页面组件可以复用 |
| 首页配置 | 官方与直连独立持久化；同一通道内多应用共享，按应用可见性过滤 |
| 公共内容 | 复用剧目、分集内部 ID、资料编辑和上传能力，线上快照独立 |
| 用户和变现 | 共用现有登录、UID、广告事实、奖励、权益、VIP 与报告体系 |
| 服务部署 | 同一代码库；有隔离需求时可拆运行实例，但不复制一套业务系统 |
| 应用规则 | 每个应用当前使用一个 provider；新版 Mini 上线后允许按播放修订修改，历史无绑定保持官方 |
| 第一阶段直连版本 | 同一部剧的直连应用共用一个线上版本，应用可单独上线或下架 |
| 历史数据 | 保留现有内容、用户、权益、配置与 ID，不重建库、不重置 UID |
| 本次非目标 | IAP 支付接入、订阅、字幕、多语言、跨设备观看历史、每应用独立版本、自动转码流水线、混合播放器剧库 |

三类客户端都必须覆盖：

| 客户端 | 应用 provider | 服务端响应与客户端处理 |
| --- | --- | --- |
| 已上线旧 Mini | tiktok_minis | 原官方响应，原 SDK 行为 |
| 新版 Mini 使用官方播放器 | tiktok_minis | 消费同一官方响应，由官方适配器执行 |
| 新版 Mini 使用独立 VePlayer | byteplus_web | 消费扩展配置及直连播放凭据，由直连适配器执行 |

旧、新官方 Mini 可以在同一 AppID 下并存。新增字段不会让旧包获得直连播放能力，因此不能把仍有旧包运行的应用直接改为 byteplus_web。

## 2 代码基线与实际耦合点

| 项目 | 基线提交 |
| --- | --- |
| drama-server | a64b753 |
| drama-admin | 07a83e8 |
| drama-mini | 37d343d |

以上是方案核对时的代码基线；发布验收还需取得实际已上线 Mini 包及其版本，不能只用当前源码代替。

| 耦合点 | 当前实现 | 必须处理的原因 |
| --- | --- | --- |
| 启动配置 | [consumer_bootstrap.go](../internal/biz/consumer_bootstrap.go) 固定官方 provider | 新通道配置必须由应用解析，官方应用保留原分支 |
| Mini 启动 | [runtime.ts](../../drama-mini/src/runtime/runtime.ts) 只接受官方 provider | 扩展已知类型，未知值拒绝，应用身份校验继续保留 |
| 目录可见性 | [discovery.go](../internal/data/persistence/mysqlstore/discovery.go) 只查官方 published_snapshot | 公开目录、登录目录、推荐、落地与报告关联都要选择正确快照 |
| 播放与奖励 | [playback_service.go](../internal/biz/consumer/playback_service.go)、[reward_next_ad.go](../internal/biz/consumer/reward_next_ad.go) | 首次播放、批量播放、奖励结果和连续解锁都依赖官方媒资条件 |
| 页面播放来源 | [playbackEngine.ts](../../drama-mini/src/player/playbackEngine.ts)、[播放页](../../drama-mini/src/pages/theater/index.tsx) | 页面多处直接使用 Album/Episode，需转换为统一内部来源 |
| 分类成员 | [category_rankings.go](../internal/data/persistence/mysqlstore/category_rankings.go) | 当前成员由官方发布事务维护，不能直接承载直连成员 |
| 首页配置 | [homepage_recommendations.go](../internal/data/persistence/mysqlstore/homepage_recommendations.go) | 当前按 section 唯一查询，没有通道维度 |
| 自动分发 | [distribution.go](../internal/biz/operations/distribution.go) 遍历有效应用 | 需排除直连应用，避免进入官方授权关系与任务 |
| 媒资清理 | [drama_media_jobs.go](../internal/data/persistence/mysqlstore/drama_media_jobs.go) | 必须识别直连版本和处理中任务的引用 |
| Worker 启动 | [worker.go](../internal/biz/worker.go) | 运行实例拆分时要明确任务角色，不能重复启动所有职责 |

当前普通响应解析不会因为新增未知字段直接拒绝，但旧 Mini 会校验 playbackSdk.provider、channel，以及奖励返回中的关键状态。兼容性要求是保留旧应用的字段、语义和状态约束，不是任意改变 JSON 后依赖客户端忽略。

## 3 后台信息架构

### 3.1 菜单与页面

公共短剧管理负责内容与上传；官方运营、直连运营分别负责发布和首页运营。菜单由服务端 RBAC 下发，保留现有自定义目录、菜单 ID 和角色关联。

| 分组 | 页面 | 路由与实现 |
| --- | --- | --- |
| 公共内容 | 短剧管理 | 保留 /dramas，展示两通道摘要，编辑及上传共用 |
| 官方运营 | 官方发布 | 新增 /official/publications 作为固定官方范围的列表入口，复用现有列表能力 |
| 官方运营 | 单剧发布工作台 | 保留 /dramas/{dramaID}/publication 及现有官方流程 |
| 官方运营 | 审核记录 | 保留 /dramas/reviews 及单剧记录路径 |
| 官方运营 | 上线记录 | 保留 /dramas/releases 及单剧记录路径 |
| 官方运营 | 分类排行榜 | 保留 /content/category-rankings |
| 官方运营 | 轮播管理 | 保留 /content/carousel |
| 官方运营 | 热播短剧 | 保留 /content/hot-dramas |
| 直连运营 | 直连发布 | 新增 /direct/publications |
| 直连运营 | 单剧发布工作台 | 新增 /direct/dramas/{dramaID}/publication |
| 直连运营 | 上线记录 | 新增 /direct/releases，支持按剧目过滤 |
| 直连运营 | 分类排行榜 | 新增 /direct/category-rankings |
| 直连运营 | 轮播管理 | 新增 /direct/carousel |
| 直连运营 | 热播短剧 | 新增 /direct/hot-dramas |
| 应用及变现 | 应用、广告、策略、报告 | 保留现有页面，补充通道信息和内容选择能力 |
| 系统管理 | 配置、权限、角色、审计 | 保留现有页面，新增直连配置与操作权限 |

旧官方页面路径与书签继续有效。官方发布新入口只是独立的运营入口，不创建第二套官方状态。公共列表提供明确的“官方发布”“直连发布”链接，不使用一个含义随隐式通道变化的发布按钮。

后台不再依赖页面顶部切换通道。路由确定页面作用域；共用组件接收固定 provider，查询、保存、候选和预览均使用该作用域。

### 3.2 公共短剧列表

公共字段包括内部 ID、封面、标题、草稿分集数、负责人、备注和最近操作人。官方、直连分别展示线上版本、上线状态、覆盖应用数及最近操作状态。

筛选分为通道、线上状态、是否有待发布修改、最近操作是否失败。不能用一个 publishStatus 合并两种流程。

列表示例：

| 短剧 | 官方通道 | 直连通道 |
| --- | --- | --- |
| A | v3 已上线，授权 4 个应用 | v2 已上线，覆盖 2 个应用 |
| B | 尚未同步 | v1 已上线，覆盖 1 个应用 |
| C | 新版审核中，线上仍为 v1 | 未发布 |

## 4 发布状态和运营流程

### 4.1 状态分层

| 维度 | 状态或数据 | 规则 |
| --- | --- | --- |
| 公共草稿 | 资料、分集与内容摘要 sourceDigest | 编辑不自动改变任一线上快照 |
| 官方发布 | 原有同步、审核、上线、下架状态 | 使用现有 TikTok 状态与适配器 |
| 直连通道发布 | unpublished、published、offline；当前版本指针 | 与官方 published_snapshot 独立 |
| 应用关系 | enabled 及应用自身启用状态 | 决定该应用是否提供此剧，不持有独立内容版本 |
| 最近直连操作 | queued、validating、succeeded、failed | 最终提交是单个事务，不暴露中间 committing 状态；操作失败不改写原线上状态 |
| 待发布修改 | 草稿摘要与当前直连版本摘要不同 | 派生提示，不是覆盖线上状态的枚举 |

published 且没有任何应用启用时显示“已发布，0 个应用上线”。offline 表示整个直连通道被下架。某应用停用、从某应用移除与整个通道下架是不同动作。

页面必须同时显示“当前线上版本”和“最近操作”。例如“v1 已上线；v2 更新失败”，不能显示成整部剧不可播放。

### 4.2 直连发布工作台

工作台包含四个区域：

1. 当前版本：版本号、分集数量、发布时间、发布人、上线应用数。
2. 待发布修改：新增、移除、替换分集，以及标题、封面、标签等差异。
3. 发布检查：逐项展示缺失资料、未完成媒资、空间不匹配、无效应用配置等阻塞原因。
4. 应用上线：应用名称、官方 AppID、上线关系、当前有效版本、应用停用原因和操作入口。

直连页没有“TikTok 审核通过”或虚构审核阶段。第一阶段不增加定时发布、分应用版本灰度或直接编辑历史线上快照。

### 4.3 首次发布

1. 运营保存公共草稿，完成封面和分集上传。
2. 在直连发布列表选择短剧，打开直连工作台。
3. 发布检查读取当前草稿、源媒资状态和目标应用配置，返回 sourceDigest 与检查项。
4. 点击“发布并上线”，选择至少一个 provider=byteplus_web 的有效应用。
5. 确认版本内容与影响范围。请求携带操作幂等键、期望通道修订号、sourceDigest 和目标应用 ID。
6. 服务端确认摘要未变化后冻结本次输入，创建操作记录。此后草稿的新修改不混入本次发布。
7. Worker 校验冻结内容和媒资，不在长数据库事务中调用外部服务。
8. 最终事务写入不可变版本、切换直连版本指针、写入应用关系、维护直连分类并更新操作结果。
9. 提交后失效对应缓存，页面显示完成结果。

新通道只要求原片可用和目标播放环境可接受的源格式。转码不是必需状态，不要求原片必须存在 720p 等派生产物。

### 4.4 更新上线版本

更新冻结新的草稿快照。成功前，所有现有直连应用继续读取原版本。

第一阶段更新对该剧全部已启用的直连应用统一生效；确认页列出完整影响范围，也可以选择额外上线的新应用。不能在此页面暗中让某个应用停留旧版本。

最终提交失败或检查失败时，不切版本、不改变分类、不改变应用上线关系。最近操作保留可诊断的失败原因，重试遵循幂等和当前修订检查。

### 4.5 应用上下线和通道下架

| 操作 | 内容版本 | 应用关系 | 首页和播放 |
| --- | --- | --- | --- |
| 增加上线应用 | 不创建版本 | 启用选定关系 | 该应用开始读取当前版本 |
| 从某应用下架 | 不创建版本 | 停用该关系 | 只影响该应用 |
| 停用应用 | 不创建版本 | 保留原内容关系 | 整个应用不可用 |
| 下架直连通道 | 保留版本供记录与恢复，通道设为 offline | 保留历史关系用于展示 | 所有直连应用停止新授权 |
| 恢复上线 | 选定保留版本并重新检查 | 明确选择恢复的应用，未选中关系停用 | 提交后恢复 |
| 回滚版本 | 将当前指针切至可用的保留版本 | 保持当前启用关系 | 全部当前直连应用统一回滚 |

全通道下架不删除媒体。恢复时不能无提示地把历史所有应用重新上线。已签发凭据或媒体地址有各自有效期，不能把下架描述为所有旧地址立即失效。

### 4.6 操作可靠性

操作幂等键按剧目和请求键唯一；同键同输入返回同一操作，同键不同输入返回冲突。每部剧同时只允许一个修改直连发布状态的活动操作。

应用上下线、发布、下架和回滚共同校验通道修订。存在活动发布操作时，拒绝会改变其版本或应用范围的并发写入；接受任务时保存更新后的执行修订，最终提交以该修订和任务租约共同校验。任务失败释放操作占用，但不修改当前线上指针。

使用 MySQL 租约与修订号接管任务，防止失去租约的 Worker 提交。发布事务按固定顺序锁定通道记录、应用关系和分类记录；跨类型操作遵循相同顺序。数据库提交后进程中断时，重试读取已提交结果，不创建重复版本。

## 5 公共内容和媒资边界

公共草稿与上传能力复用，线上版本必须完整保存发布时标题、简介、封面、标签、分集内部 ID、集序、VID、空间、时长和必要海报引用。

以下写入口必须一起修改：

| 写入口 | 必须保证的约束 |
| --- | --- |
| 保存剧目和分集 | 不回写任何历史线上快照，不用草稿覆盖消费者读取 |
| 删除短剧 | 不能只检查官方 onlineVersion；有直连版本、关系或活动任务时不得直接删除 |
| 删除或替换分集 | 保留线上快照及权益仍引用的内部 ID，处理双方通道的冻结与历史引用 |
| 修改集序 | 不通过 ID 重用让已有权益指向另一集；保留现有历史分集策略 |
| 替换封面或视频 | 产生新引用与删除意图，但实际删除前检查两种通道和活动操作 |
| 海报补全 | 必须匹配快照中的分集 ID 和 VID，不向旧版补入新草稿媒资的海报 |
| 清理未完成上传 | 同时检查冻结发布操作是否引用该资源 |
| 官方送审期间编辑 | 保留现有冻结约束，不借直连发布绕过官方冻结内容保护 |

首期保留全部直连历史版本用于记录和回滚，这些版本持续保护所引用的媒资。历史版本回收另行设计，不在本次自动删除旧版本或关联视频；对应存储增长需纳入运营成本观察。

首批试点优先使用新上传 VID。之后即使复用已有 VID，也必须先完成全体清理 Worker 的引用保护升级。

## 6 数据模型和迁移设计

以下表名为实施命名。所有内部关联字段采用 BIGINT UNSIGNED，API 表示为十进制字符串；平台 ID、VID、标签 ID 按其原始字符串保存。表和列写中文业务注释，不使用外键或 CHECK，关联校验由业务事务完成。

### 6.1 新增表

| 表 | 关键字段 | 约束与用途 |
| --- | --- | --- |
| application_playback_profiles | application_id、provider、revision、created_at、updated_at | application_id 唯一；缺少记录的历史应用解析为官方通道 |
| direct_publications | id、drama_id、status、current_version_id、revision、updated_by | drama_id 唯一；一个剧的直连状态与版本入口 |
| direct_publication_versions | id、drama_id、version_number、source_digest、snapshot_json、created_by、created_at | drama_id 与 version_number 唯一；版本内容不可变 |
| direct_publication_operations | id、drama_id、request_key、input_digest、command_json、status、result_version_id、created_by、时间字段 | kind 等原始命令保存在 command_json；剧目与 request_key 唯一；同步事务的幂等结果和审计事实 |
| direct_homepage_recommendations | id、section、revision、config_json、created_at、updated_at | section 唯一，仅 carousel 和 hot；直连配置独立 |
| direct_category_rankings | id、tag_id、revision、items_json、created_at、updated_at | tag_id 唯一；只保存直连分类成员和排序 |

直连版本与操作记录增加按剧目、时间及任务状态查询所需的联合索引。JSON 快照用于保持完整版本，不要求目录每次反序列化全部分集；目录采用专门摘要投影或受控 SQL 字段选择，并通过实际 EXPLAIN 和大剧集样本检查性能。

消费者目录使用专门的摘要 SELECT，从当前版本快照选择标题、封面、标签和首集信息，不加载完整分集数组；应用关系过滤在数据库完成。管理发布列表批量加载当前草稿、分集及当前版本摘要，在内存计算待发布修改和组合筛选，不加载全部历史快照；完整工作台才读取历史版本。大目录性能验收若需要进一步数据库分页或摘要投影，以后续变更明确引入。

application_playback_profiles 不保存 TikTok Client Secret 或 BytePlus AK/SK。直连 Token 有效期读取启动文件 business.direct_playback_token_ttl_seconds，应用不保存配置引用；License 和 SDK AppID 由 Mini 随包维护，VOD 凭据仍由全局媒资配置服务负责权限和掩码。

### 6.2 保持原有权威数据

官方的 published_snapshot、tiktok_publications、官方分发关系、homepage_recommendations 和 category_rankings 保持原职责。新直连记录不写入官方发布快照，也不伪造 Album/Episode。

不新增用户表、奖励账本、广告尝试服务或消费者会话表。UID、16 个用户分片、权益关联和现有幂等键保持不变。

### 6.3 迁移顺序

1. 使用新的 sql/migrations/YYYYMMDD_NNN_用途.sql 创建直连业务表和必要索引。
2. 使用单独增量加入直连菜单与精确操作权限，保留已有节点及角色关联。
3. 历史应用无需批量改 provider；缺省分支明确为 tiktok_minis。
4. 原首页配置保持原位；直连配置为空，不自动复制、不自动回退。
5. 新库执行冻结的 init.sql 及全部增量；已有库只执行尚未应用的增量。
6. 更新 sql/README.md，记录脚本顺序、前置条件、备份及验证结果。
7. 启动不自动改库，不修改已发布增量，不清空 Redis 或 UID 共享进度。

迁移前后核对旧表行数、配置内容、应用标识、官方快照、用户 UID、奖励及权益关联。DDL 按目标 MySQL 版本演练，不能假设整批 DDL 可事务回滚。

## 7 应用配置与播放器配置

应用的 provider 由服务端权威记录确定。历史无绑定应用保持官方；新建应用选择通道，新版 Mini 上线后已有应用也可通过整体编辑按 playbackRevision 修改通道。切换由 applications.update 应用编辑权限控制，切入直连需部署开启且启动文件的 Token 有效期通过校验；应用资料和播放绑定在同一事务保存，过期修订整体拒绝。切回官方保留递增修订，身份、广告位、策略、权益及两通道历史内容保持原归属。应用不提供独立配置引用修改接口。

直连播放配置至少包括：

| 配置 | 可见范围 | 说明 |
| --- | --- | --- |
| BytePlus SDK AppID | Mini 构建资源 | 与随包 License 一起维护，不通过服务端下发；与 TikTok AppID、内部 applicationId 分开 |
| License 文件 | Mini 构建资源 | `.lic` 随包提供，不在后台配置地址或通过启动接口下发 |
| 许可域名与 SDK 版本 | 后台及构建校验 | 核对实际页面运行域名和已验证 SDK |
| 媒资配置引用 | 仅服务端 | 解析凭据、空间、区域，不能覆盖原官方分发配置 |
| 凭据有效期 | 服务端 YAML，响应给出 expiresAt | business.direct_playback_token_ttl_seconds，开启直连时为 60～900 秒，修改后重启生效 |
| 媒体地址有效期与播放域名 | BytePlus VOD 控制台配置 | 与 playAuthToken 的有效期区分，本次不新增媒体地址代理或自定义有效期接口 |
| 播放通道 | 服务端 | 由应用 provider 和 playbackRevision 决定；新通道故障不能自动回退到官方 |
| 能力声明 | 可向客户端下发 | 基础版 mediaPreload=false，不能由客户端任意提升 |

应用停用和直连功能关闭应阻止该应用的新消费请求；停用直连入口不修改官方应用配置，也不触发密钥轮换或用户重新分配 UID。

## 8 消费者 API 兼容合同

### 8.1 路径和公共行为

所有既有路径、HTTP 包装、身份恢复、错误语义和官方字段保留。不增加 X-Mini-Contract，不新增消费者版本路由，不通过 query 中的 provider 决定内容权限。

公开请求仍从 tiktokAppId 解析有效应用；受保护请求从 VisitorToken 的可信应用上下文解析。来源不一致、应用无效或未知 provider 均明确拒绝，不回退到默认应用。

| 方法和路径 | 改造内容 |
| --- | --- |
| GET /api/v1/health | 保持现有行为 |
| POST /api/v1/bootstrap | 官方分支保持原配置；直连分支附加 SDK 公开配置 |
| GET /api/v1/open/catalog | 按应用选择线上目录，继续公开、分页、不创建用户 |
| GET /api/v1/open/home/carousel | 选择对应通道轮播配置和可见摘要 |
| GET /api/v1/open/home/hot | 选择对应通道人工热播配置和可见摘要 |
| GET /api/v1/open/home/rankings | 选择对应通道分类，应用过滤后省略空分类 |
| GET /api/v1/home | 保留现有受保护接口，使用对应目录 |
| GET /api/v1/discovery | 保留过滤、排序、分页语义，使用对应目录 |
| GET /api/v1/dramas/{dramaID} | 读取对应通道发布快照 |
| POST /api/v1/play/resolve | 共用权益判定，分别生成官方标识或直连凭据 |
| POST /api/v1/play/resolve-batch | 保留最多五集、按输入顺序响应，每项独立授权 |
| POST /api/v1/reward-grants | 共用结算，返回对应通道播放信息及下一广告目标 |
| GET /api/v1/me/entry-ad | 保留计数合同，关联内容检查使用对应目录 |
| GET /api/v1/me/ad-shows | 保留广告事实查询合同 |
| POST /api/v1/me/ad-shows | 保留展示事实和幂等语义，检查正确的内容范围 |
| POST /api/v1/vip/claim | 保留 VIP 领取语义 |
| GET /api/v1/me/unlocks | 权益不迁移，关联对应通道仍可见的内容 |
| POST /api/v1/metrics | 原事件保持兼容，可增加受控通道诊断维度 |

公开目录和首页不返回 VID、playAuthToken、个人权益或奖励 Offer。

### 8.2 启动配置字段

保留 playbackSdk.provider 和 channel。官方应用仍返回下面的核心结构，不要求新增必填字段：

~~~json
{
  "playbackSdk": {
    "provider": "tiktok_minis",
    "channel": "byteplus"
  }
}
~~~

直连应用返回扩展结构。此处是自有 API 合同，由适配器转换为最终 SDK 参数：

~~~json
{
  "playbackSdk": {
    "provider": "byteplus_web",
    "channel": "byteplus",
    "capabilities": {
      "mediaPreload": false
    }
  }
}
~~~

SDK AppID 在 Mini 包内按字符串保存；实际 SDK 要求数值时，只在 SDK 边界转换。License 由 Mini 的 `.lic` 文件随包提供，后台不配置地址，启动接口不下发许可内容或 SDK AppID。

### 8.3 播放响应字段

官方通道保留所有现有字段。playback.episodeId 继续表示 TikTok 平台 Episode ID；请求中的 episodeId 和顶层 episode.id 继续表示内部业务 ID，不得混用。

直连只增加所需字段，不填假的 albumId 或 platform episodeId。播放身份由启动的 provider 确定，新 Mini 在内部补上明确的来源类型，不要求修改旧官方响应：

~~~json
{
  "code": 0,
  "message": "OK",
  "data": {
    "status": "playable",
    "drama": {
      "id": "1001"
    },
    "episode": {
      "id": "2001"
    },
    "playback": {
      "channel": "byteplus",
      "vid": "example_vid",
      "playAuthToken": "temporary_play_credential",
      "expiresAt": "2026-10-10T00:05:00Z",
      "posterUrl": "https://assets.example.com/poster.jpg",
      "title": "Episode 1",
      "episodeNumber": 1,
      "durationSeconds": 90
    }
  }
}
~~~

示例仅列关键字段，实际 drama、episode 摘要保留原有完整合同。expiresAt 表示本次播放凭据的到期时刻，不表示已兑换媒体地址的到期时刻。

status 继续使用 playable、access_required、unavailable。官方 access_required 的 preload 语义保留；基础版直连的锁定响应省略媒体 preload，不签发可播放 Token，仅返回允许展示的元数据和解锁入口。

### 8.4 奖励返回兼容

旧客户端要求奖励结果的 claimId 匹配，status 为 granted、already_granted 或 entry_satisfied；如果带 playback，其状态必须为 playable。保持这些约束。

直连奖励事务成功但凭据生成失败时，返回已确认的奖励事实并省略 playback。新 Mini 使用同一身份重新解析播放，不重新展示广告、不生成新 claimId。客户端清除待领取记录与播放恢复分开处理。

entry_satisfied 仅保留旧响应兼容，仍不附带解锁、VIP 或播放结果。2026-10-24 起，新服务端入剧达标后也幂等保存有效事实，以 granted／already_granted 返回最新进度，不增加内容权益。连续解锁 nextAd 和多集授予目标的选择都必须使用相同的通道目录及媒资就绪判断。

### 8.5 错误和新增字段默认规则

旧官方分支沿用现有错误映射。直连依赖故障通过新增或已有统一错误目录映射，不能伪装成 401 并触发静默登录循环，也不能原样返回 BytePlus 异常。

新增的能力字段缺省时，由官方适配器保持已有策略。明确返回的未知 provider 必须拒绝；不能把未知值当作官方。直连配置缺失时拒绝直连启动，不返回伪造配置。

## 9 服务端领域与适配边界

新增通道目录选择和播放来源构造边界，保持权益、奖励和身份服务共用。建议按职责实现 PublishedCatalog、PlaybackSourceResolver 与 DirectPublicationService；名称可随现有代码风格调整，业务边界不能合并成遍布页面和 Handler 的 provider 判断。

播放解析顺序：

1. 校验已认证应用和用户。
2. 根据权威应用 profile 选择目录。
3. 查找该应用可见的发布快照与目标分集。
4. 校验该通道媒资就绪条件。
5. 执行现有免费、逐集权益或 VIP 判断。
6. 无权益时返回解锁入口；有权益时生成相应播放信息。
7. 官方返回原标识；直连在服务端完成临时凭据签发。

现有 Go BytePlus SDK 已包含 GetPlayAuthToken 能力，签发留在 Go 适配器内，不增加 Node 签发服务。长期凭据、空间选择、播放域名和签发配置由服务端解析。

凭据每次签发前读取当前可见性与权益，不建设逐次播放账本。不将逐用户播放结果放进公开缓存。短时内存复用必须绑定应用、用户、发布版本、VID 和凭据有效期，身份变化即清理。

商业化保持现状：入剧广告与 VIP 强制引导由 Mini 执行，服务端播放权限仍按既有免费、权益、VIP 判断。本次不悄然把这些客户端引导改成另一套服务端硬门禁。

## 10 新版 Mini 实现

### 10.1 统一播放器接口

保留 PlaybackEngine 的挂载、切换来源、播放、暂停、seek、倍速、静音、快照和销毁接口，新增 BytePlusWebPlayerEngine。官方引擎继续独立封装 TTMinis.getPlayer。

API 客户端按启动 provider 校验字段，再转换为内部可区分的来源：

- 官方来源：provider、channel、albumId、平台 episodeId、vid 和展示元数据。
- 直连来源：provider、vid、playAuthToken、expiresAt 和展示元数据。

页面不再自行比较 Album/Episode 判断是否同一播放目标，统一使用内部剧目、分集、媒资和发布来源身份。切源代次、广告代次和用户代次分别保护迟到事件，旧来源首帧不能撤掉新来源封面。

### 10.2 功能清单

| 模块 | 实施内容 |
| --- | --- |
| 启动 | 接受两种已知 provider，保留包内 AppID 与返回身份校验 |
| API 类型 | 扩展直连字段，官方现有响应继续可解析 |
| 播放器加载 | 官方与直连分别加载，SDK/CSS 按应用构建和平台要求打包 |
| 播放和暂停 | 统一真实播放事件语义，不把调用 play 等同实际出画面 |
| 首帧与缓冲 | 统一 first-frame、waiting、seek 后首帧和封面撤除时序 |
| 上下滑和选集 | 共用交互，取消过期请求，确保单一当前播放器 |
| 连播和结束 | 当前来源 ended 才能推进下一集，迟到事件不重复切集 |
| 倍速与静音 | 验证当前 0.75、1、1.25、1.5、2 倍速和音量恢复能力 |
| 本地续播 | 保留内部 ID 和进度，ready 后恢复 seek，再等待有效画面 |
| 广告衔接 | 广告前暂停，按原状态恢复，用户换号或离页不恢复旧来源 |
| 解锁 | 支持两种奖励播放结果，缺播放信息时重新解析 |
| 连续广告 | nextAd、自动解锁、多集授予与当前目标保持一致 |
| VIP | 保留进度、手动领取、自然日到期及入剧倒计时 |
| 凭据刷新 | 到期或可恢复凭据故障时有限次重新解析，保留当前位置 |
| 预加载 | 官方保持已验证能力；直连基础版关闭高级跨视频预加载 |
| 数据预取 | 保留封面、详情和目录预取；不提前向锁定集签发 Token |
| 历史和收藏 | 原应用与 Visitor 本地隔离不变，不存播放凭据 |
| 平台能力 | TikTok 登录、广告、分享、前后台、导航及截图控制继续复用 |
| 报错 | 固定英语友好提示，日志不含 Token、Cookie、签名 URL |
| 页面 | 首页、分类、更多、播放、选集、账户及协议页面共用 |

普通播放器当前视频的正常缓冲不等于高级跨视频预加载。禁止通过隐藏播放器或多个播放实例绕过基础版能力限制。

### 10.3 构建与回退

configs/apps 中仍按 TikTok 应用打包；一套源码不意味着所有应用使用同一个 AppID。具体 SDK 包名、版本、资源入口和 License 参数在平台验证后锁定。

构建可裁剪不需要的适配器，或在平台允许范围内按需加载。不能用未经验证的 CDN 注入绕开 Minis 打包要求。SDK 不可用时展示稳定错误，不自动改为另一通道、不增加原生 video 回退。

官方限制测试改为按通道验证；官方构建继续禁止直连 Token 和第三方播放回退。AGENTS.md、官方播放说明及测试规则在代码实施时同步更新，明确用户已经确认的双通道范围。

## 11 分类排行榜详细实现

### 11.1 页面

官方和直连分别进入各自菜单，复用分类编辑器。顶部显示当前运营范围，提供只属于当前通道的“预览应用”。分类按钮展示标签名称和成员数，隐藏成员计入后台数量。

列表包含排名、封面、剧名、线上版本、展示开关、可展示应用数和排序操作。不能手动添加不属于该线上标签的剧目。

### 11.2 成员与排序规则

| 事件 | 行为 |
| --- | --- |
| 首次发布带标签的剧 | 追加到该通道分类末尾，默认展示 |
| 草稿改标签 | 不影响线上分类 |
| 新版本保留原标签 | 保留排序和隐藏状态 |
| 新版本新增标签 | 加入新分类末尾 |
| 新版本移除标签 | 移除对应成员 |
| 隐藏成员 | 保留成员和原位置，只影响当前分类 |
| 某应用下架 | 保留通道成员，由该应用读取时过滤 |
| 全通道下架 | 移除该通道分类成员 |
| 再次上线 | 作为新成员追加，保持当前官方分类已有语义 |
| 回滚版本 | 按回滚快照标签计算成员差异，持续存在成员保留运营设置 |

发布、下架和分类成员更新在同一事务内完成。任何分类写入失败都不能留下“版本已经上线、分类仍旧”的半完成状态。草稿标签从不作为消费者分类来源。

分类配置只影响分类区域；隐藏不影响轮播、热播、更多或直接播放。消费者按应用过滤后连续展示排名，空分类不返回。

标签 ID 和英文展示名继续复用现有体系，第一阶段不新增自定义分类树。标签字典的显示名称是公共配置，修改会影响两通道，后台必须明确标注这个共享范围。

## 12 轮播和热播详细实现

### 12.1 独立配置和共用组件

官方与直连分别使用各自配置表和 API，复用 CarouselManagement 类组件。组件作用域由路由固定，不依赖可随手切换的全局应用选择器。

| 项目 | 轮播 | 热播 |
| --- | --- | --- |
| 最大条数 | 保留 20 条 | 保留 15 条 |
| 排序 | 人工顺序 | 人工顺序，不按播放量自动排名 |
| 标题 | 可覆盖，最多 200 字符，留空取线上剧名 | 同左 |
| 封面 | 对应通道线上快照 | 对应通道线上快照 |
| 显隐 | 只控制本栏目条目 | 只控制本栏目条目 |
| 重复 | 同一栏目不重复同剧 | 同一栏目不重复同剧 |
| 跨栏目 | 可以同时出现在轮播和热播 | 可以同时出现在轮播和热播 |
| 空结果 | 隐藏区域，不自动补位 | 隐藏区域，不自动补位 |

### 12.2 添加和编辑

1. 搜索当前通道已发布且未全通道下架的内容。
2. 候选展示剧名、线上版本、上线应用数量；0 个应用可见时明确提示。
3. 选择短剧，设置标题和展示状态。
4. 保存内部 dramaId、标题覆盖、visible 与顺序，不复制播放标识。
5. 服务端复核候选当前仍有效，按 revision 提交；失效的新引用拒绝保存。
6. 列表排序和开关沿用即时保存，保存期间锁定冲突操作；失败保留现状，刷新后继续。

既有条目下架后保留引用，在后台显示失效原因，消费者省略。移除推荐条目不删除剧目或媒体。封面和默认标题跟随当前通道线上版本，草稿与另一通道更新不改变它。

### 12.3 复制配置

提供“复制官方配置到直连”辅助操作，默认仅用于空的直连配置。先预览差异，再确认写入；已有目标配置必须明确选择替换，不静默覆盖。

轮播、热播仅复制直连已具备发布资格的内部剧目引用，列出跳过项。分类复制只调整目标分类现有成员的顺序和显隐，不能借复制操作创建不属于该标签的成员。目标独有成员追加在后，源配置不变，复制后不自动同步。

复制采用目标 revision 校验并写审计日志。不引入实时继承或官方配置兜底。

## 13 应用预览和消费者首页

### 13.1 同一套展示判定

实际展示需要同时满足：应用有效、通道匹配、通道版本可用、应用已授权或上线、模块条目可见；分类还需匹配该线上快照标签。首页展示不要求用户已解锁。

后台预览复用相同查询规则，额外返回管理员可见的过滤原因：下架、未上线到该应用、官方授权未生效、条目隐藏、标签已变更或应用停用。

预览只展示目录和推荐效果，不签发观众播放 Token，也不提供模拟播放器。切换预览应用不改变编辑范围；所选应用只用于展示计算。

### 13.2 Mini 首页行为

轮播、热播、分类继续各自独立请求，先返回先渲染；一个模块失败不阻塞其他区域和“更多”。公开接口与登录后的目录使用同一通道和可见性规则。

More Drama 继续是独立的全部可见目录，不随分类标签选择改变，也不受某个推荐位隐藏影响。保留分页、返回位置、回到顶部和封面预取。

新旧 Mini 的首页摘要结构保持一致；播放 provider 在启动时确定，不向每张卡片重复塞入 Token 或 SDK 初始化参数。

| 操作 | 轮播 | 热播 | 分类 | 更多与新播放授权 |
| --- | --- | --- | --- | --- |
| 隐藏轮播位 | 本位隐藏 | 不变 | 不变 | 不变 |
| 隐藏热播位 | 不变 | 本位隐藏 | 不变 | 不变 |
| 隐藏分类成员 | 不变 | 不变 | 当前分类隐藏 | 不变 |
| 从某应用下架 | 该应用过滤 | 该应用过滤 | 该应用过滤 | 该应用停止提供 |
| 全直连通道下架 | 直连过滤 | 直连过滤 | 移除直连成员 | 直连停止提供 |

## 14 Admin API 和权限设计

### 14.1 新增管理接口

消费者路径不变；管理端增加明确的 direct 资源路径，使菜单与后端作用域一致。以下新增接口均使用原 HTTP 包装和字符串 ID。

| 方法与路径 | 用途 |
| --- | --- |
| GET /admin/v1/direct/dramas | 直连发布列表，筛选状态、待更新和最近失败 |
| GET /admin/v1/direct/dramas/{dramaID}/publication | 当前版本、操作、应用关系和草稿差异 |
| POST /admin/v1/direct/dramas/{dramaID}/publication/checks | 发布检查，返回阻塞项和 sourceDigest，不修改线上版本 |
| POST /admin/v1/direct/dramas/{dramaID}/publication/operations | 提交 publish、offline、restore 或 rollback，带 requestId 与 expectedRevision |
| GET /admin/v1/direct/publication-operations/{operationID} | 查询操作进度及结果 |
| GET /admin/v1/direct/releases | 上线、更新、下架和回滚记录，支持剧目过滤 |
| GET /admin/v1/direct/dramas/{dramaID}/applications | 应用上线关系与有效状态 |
| PUT /admin/v1/direct/dramas/{dramaID}/applications | 按修订更新关系，不能提交官方应用 |
| GET 和 PUT /admin/v1/direct/carousel | 直连轮播读取与保存 |
| GET /admin/v1/direct/carousel/targets | 直连轮播候选 |
| GET 和 PUT /admin/v1/direct/hot-dramas | 直连热播读取与保存 |
| GET /admin/v1/direct/hot-dramas/targets | 直连热播候选 |
| GET /admin/v1/direct/category-rankings | 直连分类及成员数 |
| GET 和 PUT /admin/v1/direct/category-rankings/{tagID} | 分类成员排序与显隐 |
| GET /admin/v1/direct/home-preview?applicationId=... | 直连应用首页预览与过滤原因 |
| GET /admin/v1/home-preview?applicationId=... | 官方首页预览与过滤原因 |
| POST /admin/v1/direct/home-config-copy/preview | 计算官方配置复制结果，不写入 |
| POST /admin/v1/direct/home-config-copy | 携带预览 digest；核对来源、目标与可复制成员摘要后原子复制 |

操作提交返回已接收的操作 ID 与状态；终态查询仍返回同一操作。数据格式、长度、枚举、数量等在 Handler 校验；业务权限、归属、可用性、事务与幂等在 Service 校验。

应用管理已有读写接口包含 playbackProvider 与 playbackRevision，不接收或返回配置引用。官方新入口继续复用现有官方业务接口，不为它重建 TikTok API。

### 14.2 权限

新增精确操作权限，实际编码：

- operations.direct_publication.read、publish、offline、restore、rollback。
- operations.direct_distribution.read、update。
- operations.direct_carousel.read、update。
- operations.direct_hot.read、update。
- operations.direct_ranking.read、update。
- operations.direct_home.copy。
- 播放器选择复用 applications.update；历史 application.playback.update 不再参与接口鉴权。

只读发布检查要求 operations.direct_publication.read；提交动作按 kind 精确检查 publish、offline、restore 或 rollback。直连首页预览入口要求 operations.direct_publication.read，官方入口要求 content.drama.read；每个栏目额外检查自身读取权限，未授权栏目返回空内容并列入 omittedSections，不泄露配置或过滤原因。

菜单与业务操作分别校验。原官方角色不自动获得直连权限，父目录授权不隐式包含新操作。直连复制同时检查源栏目读取权限和目标栏目写入权限。

## 15 Worker 缓存和媒资清理

### 15.1 Worker

继续在同一服务代码库使用 MySQL 任务和租约。直连上下架使用同步事务。官方自动分发、手动授权入口和对账队列都排除直连应用。

每个服务实例运行媒体、官方发布和指标维护任务，不按部署参数筛选职责。媒体与官方发布任务通过 MySQL 租约协调领取，指标维护沿用幂等更新。

### 15.2 清理保护

删除判断至少包括公共草稿、官方线上快照、保留的直连历史版本、直连活动操作、未完成上传和既有其他资源引用。空间与 VID 联合匹配，不能只比较 VID。

原始视频、封面和相关素材均适用。官方下架、直连下架或某应用解除关系不能直接删除共享媒体。所有可能执行删除的旧 Worker 升级完成前，不启用直连写入。

### 15.3 缓存

官方缓存保留现有命名和语义；直连结果使用独立命名空间，键包含应用、栏目或查询参数，以及发布/配置失效版本。直连保存不重排官方配置，不将直连结果写进官方缓存键。

发布、版本回滚、上下线、应用停用、首页保存、标签显示名变化都触发结果失效。两通道结果键隔离，但首期复用既有全局目录失效版本，因此直连写入也会促使官方结果重新回源；这只影响缓存命中率，不改变官方业务内容或配置。

先提交 MySQL，再变更失效版本并清理可重建结果；旧查询不能向新版本回填。沿用现有失效重试、日志和 TTL 兜底。Redis 故障回源，不缓存数据库错误，不清空 UID 发号键。

后台预览读取提交后的配置并计算实际可见性。已打开的 Mini 通过既有刷新、重新进入和缓存过期获得新展示，不承诺主动推送；播放和奖励仍读取权威状态。

## 16 其他功能覆盖

| 功能 | 实施要求 |
| --- | --- |
| TikTok 静默登录 | 两通道共用，AppID 与内部应用 ID 不混用，身份命名空间不变 |
| 用户 UID | 保留 MySQL 水位、Redis 共享进度和原分片规则 |
| 上传与续传 | 复用 STS、批量任务、重选文件、重试和确认，不重复上传已成功资源 |
| 入剧广告 | 保留用户或用户加剧目范围、展示或完成计数、自然日和阈值语义 |
| 广告事实 | 展示和完整观看独立保存，达标不丢弃事实；entry_satisfied 仅保留旧响应兼容 |
| 逐集与组合解锁 | 使用对应通道媒资就绪条件，幂等和每日额度不变 |
| VIP | 保留单独累计、手动领取、应用时区自然日到期 |
| 待领奖和补报 | 原 claimId、原广告事实 ID、原应用和用户隔离 |
| IAP 配置 | 保留已有配置能力，不宣称本次完成支付执行 |
| 投放活动 | 候选与落地目标支持对应目录，归因不改变权益判断 |
| Minis Link | 继续由官方接口生成链接，不构造未经验证的替代链接 |
| 分享 | 保留内部 dramaId 和 episodeId 定位，按当前目录校验 |
| TikTok 事件回传 | 保留真实播放与广告事实，直连不伪造 Album/Episode |
| 报告 | 保留原应用、用户、奖励和广告事实，内容关联覆盖直连，增加必要通道筛选 |
| 指标含义 | 不把当前广告贡献报告扩写为真实收入、ROI 或完整观看时长 |
| 管理员与 RBAC | 原登录和会话机制共用，新增操作权限显式分配 |
| 审计 | 记录通道、版本、应用范围、操作者和结果，不保存播放凭据 |
| 固定语言与字幕 | Mini 英语、Admin 中文；维持无字幕和无多语言范围 |

## 17 分阶段实施任务

任务按依赖顺序推进。允许在前置条件明确后并行开发互不依赖模块，但发布前统一验收。

| 阶段 | 编号与任务 | 交付和完成条件 |
| --- | --- | --- |
| P0 | P0-01 记录真实旧 Mini 包与官方应用基线；P0-02 验证目标容器和基础版 SDK；P0-03 固定 SDK 包与配置 | 旧包可回归，平台能力结论明确，真实环境最小播放通过 |
| P1 | P1-01 新增表与迁移；P1-02 应用 profile 解析；P1-03 官方任务过滤；P1-04 全链路引用与删除保护 | 功能关闭时旧流程通过，所有新引用有清理保护 |
| P2 | P2-01 直连状态和操作；P2-02 发布/下架/回滚事务；P2-03 应用关系；P2-04 分类事务 | 失败不改变线上，幂等、并发、崩溃恢复及目录一致性通过 |
| P3 | P3-01 通道目录；P3-02 签发适配器；P3-03 单集与批量播放；P3-04 奖励和连续解锁 | 新增字段合同完成，未授权不签发，已领奖失败可恢复 |
| P4 | P4-01 直连轮播热播；P4-02 分类管理；P4-03 应用预览；P4-04 缓存隔离与配置复制 | 官方配置不变，直连应用展示符合发布与分配规则 |
| P5 | P5-01 Admin 菜单和权限；P5-02 独立发布页面；P5-03 首页运营页面；P5-04 应用与报告适配 | 两组运营入口明确，桌面和移动视口可用 |
| P6 | P6-01 Mini 来源类型；P6-02 直连引擎；P6-03 页面与广告恢复；P6-04 构建及官方限制测试 | 一套新版 Mini 在两通道工作，官方功能未退化 |
| P7 | P7-01 全功能三类客户端回归；P7-02 数据迁移演练；P7-03 旧包兼容；P7-04 小流量试点 | 验收矩阵有证据，回滚路径可执行 |
| P8 | P8-01 完成 API、部署、首页、播放器与维护文档；P8-02 更新仓库约束 | 文档与实现一致，不保留已经过时的单通道规则 |

P0 的平台开放和 License 域名未确认时，内部接口、数据与测试可继续开发；生产直连入口保持关闭，不能把普通浏览器播放成功当作容器验收完成。

## 18 代码改造定位

| 项目 | 现有位置 | 计划修改 |
| --- | --- | --- |
| server | internal/biz/application、application_resolver.go、consumer_bootstrap.go | 应用 profile 与安全公开配置 |
| server | internal/biz/consumer/publication.go、service.go、playback_service.go | 通道目录和播放来源边界 |
| server | internal/biz/consumer/reward_service.go、reward_next_ad.go、reward_strategy_episode.go | 所有官方就绪判断和领奖后播放结果 |
| server | internal/biz/operations/content.go、publication.go、media_jobs.go | 公共内容保护，官方流程保持，清理检查扩展 |
| server | internal/biz/operations 下新增直连发布服务 | 快照、操作、应用关系和上下线事务 |
| server | internal/data/media/byteplusmedia | 直连播放凭据签发和源媒资校验 |
| server | internal/data/persistence/mysqlstore 下新增直连仓储 | 新表、目录摘要、事务与索引 |
| server | internal/biz/homepage、internal/data/homepage_result_cache.go | 复用业务规则，配置和缓存分通道 |
| server | internal/service/consumer/handler.go | 原路径与包装，绑定对应业务服务 |
| server | internal/service/operations、internal/server、RBAC 校验 | 新管理接口、菜单页面标识和精确权限 |
| server | internal/biz/worker.go、operations/distribution.go | 职责配置、直连操作执行、官方应用过滤 |
| server | sql/migrations、sql/README.md | 增量结构与权限，保留冻结基线 |
| admin | src/app/navigation.tsx、routes.ts、domain/API 类型 | 独立菜单、路由、页面标识和权限 |
| admin | src/features/dramas | 共用目录和上传，新增直连列表、工作台及记录 |
| admin | CarouselManagement、CategoryRankingManagement | 固定通道包装、预览、独立配置和版本冲突 |
| admin | applications、global configuration、growth | 播放配置、目标选择与报告关联 |
| mini | src/domain/types.ts、src/api/httpMiniDramaApi.ts | 扩展类型、校验和奖励恢复 |
| mini | src/runtime/runtime.ts、publicHomeCache.ts | provider 选择、缓存及身份隔离 |
| mini | src/player | 新增直连引擎，统一回调，分开预加载策略 |
| mini | src/pages/theater/index.tsx | 移除页面对官方媒体标识的直接依赖 |
| mini | src/components/HomeHighlights.tsx、src/pages/square/index.tsx | 保持 UI，验证目录和缓存行为 |
| mini | configs/apps、构建脚本、tests/official-player-policy.node.mjs | 多应用打包和按通道约束 |

实施前用现有测试复核受影响边界，新增中文业务注释。日志沿用现有结构化入口，Service/Worker 的错误分支不得因“上层已记录”而静默丢弃实际错误。

## 19 验收矩阵

| 编号 | 场景 | 预期 |
| --- | --- | --- |
| C01 | 实际旧 Mini 包连接新服务端 | 启动、首页、官方播放、广告、领奖、VIP 正常 |
| C02 | 新旧官方 Mini 在同一应用并存 | 用户和权益一致，不重复发号或重新计算奖励 |
| C03 | 新 Mini 使用官方 provider | 原官方字段和预加载行为有效 |
| C04 | 新 Mini 使用直连 provider | 原片、基础 License 和真实容器正常播放 |
| C05 | 新字段缺省或未知 provider | 官方缺省扩展正常；未知 provider 明确拒绝 |
| C06 | 跨应用、伪造 VID 或目录目标 | 不越权，不向未授权内容签发凭据 |
| C07 | 单集与五集批量解析 | 顺序保持，每项独立授权，锁定直连集无 Token |
| C08 | 凭据到期、页面隐藏后恢复 | 重新检查权限并恢复位置，不无限循环 |
| C09 | 快速连续切集、迟到首帧 | 不串视频、不重复播放、不错误撤封面 |
| C10 | 广告取消、失败、重复回调 | 不发奖，播放器状态可恢复 |
| C11 | 奖励提交后响应丢失 | 原 claimId 重试，不重复入账 |
| C12 | 奖励已提交但签发失败 | 权益保留，重新解析播放，不重看广告 |
| C13 | 入剧广告四种计数组合 | 保持范围、服务器自然日与原键幂等；达标后有效事实仍保存 |
| C14 | VIP 手动领取和跨日到期 | 原策略与应用时区一致 |
| C15 | 连续广告、多集解锁 | 目标来自正确通道，额度和幂等不变 |
| C16 | 直连首次发布 | 版本、应用关系、分类一次成功提交 |
| C17 | 更新检查或提交失败 | 旧版仍在线，分类和首页不半更新 |
| C18 | 发布时继续修改草稿 | 本次仍用冻结输入，后续修改显示待发布 |
| C19 | 两个管理员并发发布或排序 | 修订冲突可见，不静默覆盖 |
| C20 | Worker 失去租约或提交后重启 | 不重复版本，不重复应用关系或操作 |
| C21 | 官方与直连标签和封面不同 | 各读各自快照，互不覆盖 |
| C22 | 只改草稿 | 两种线上目录与推荐均不变 |
| C23 | 某直连应用下架 | 只影响目标应用，其余继续可见 |
| C24 | 官方全局下架 | 直连状态、成员及媒体不被误删 |
| C25 | 直连全局下架和恢复 | 首页、新授权一致变化；恢复范围明确 |
| C26 | 调整直连轮播热播排序 | 官方配置内容和顺序完全不变 |
| C27 | 隐藏推荐或分类成员 | 只影响当前模块，更多和播放权限不变 |
| C28 | 复制配置含不可用条目 | 明确跳过，源不变，目标修订受保护 |
| C29 | 空栏目或过滤后空分类 | 不自动补官方内容，Mini 正确隐藏区域 |
| C30 | 发布或授权变化后的缓存 | 旧查询不回填新版本，应用不串数据 |
| C31 | Redis 故障 | 内容回源；不丢配置、不清 UID 键 |
| C32 | 删除、替换及清理共享资源 | 双通道和活动任务的有效引用都被保护 |
| C33 | 旧库增量升级与空库初始化 | 结构一致，旧配置、UID、权益和角色关系保留 |
| C34 | 新角色权限和菜单 | 无操作权限不能调用直连接口，父节点不自动扩权 |
| C35 | Admin 快速切页、应用预览 | 迟到请求不覆盖当前作用域，不写错通道 |
| C36 | 投放、分享、回传与报告 | 落地正确、身份不串、不伪造官方媒体标识 |
| C37 | 日志和异常 | 不含 Token、Cookie、密钥或签名媒体地址 |
| C38 | 桌面与移动后台 | 长标题、大分集列表、弹窗、表格和错误恢复可用 |

测试分为三层：领域及组件测试、真实 Handler 与隔离 MySQL/Redis 测试、TikTok 容器真机验收。自动化测试禁止调用真实 TikTok 或 BytePlus；测试替身只在测试代码中，不成为产品开关。

实现阶段验证命令：

~~~bash
# drama-server
GOWORK=off make verify
GOWORK=off make build
make integration-up
make test-integration-local
make integration-down

# drama-admin
npx pnpm@11.11.0 verify

# drama-mini
make verify
~~~

先运行相关聚焦测试，再执行项目要求的完整检查。make package 只在真正进行平台打包或设备验收时执行。旧包、系统版本、应用、服务提交、用例和结果应记录为验收证据，不把过去文档中的测试结果当作本次通过。

## 20 发布顺序与回滚

发布按以下顺序执行：

1. 保存实际旧 Mini 包、当前应用与配置基线，完成数据库备份和增量演练。
2. 执行新增表和权限迁移，不重建旧表、不改官方配置。
3. 发布当前服务端与全部相关 Worker，暂不切换应用的播放通道。
4. 核对所有实例都认识新引用和应用通道，官方队列过滤生效。
5. 用实际旧 Mini 完成关键链路回归。
6. 发布配套 Admin，给指定角色显式授予新权限。
7. 配置 YAML 中的播放 Token 有效期和新的 TikTok 应用，先使用新上传内容完成发布。
8. 构建新版 Mini，分别验证官方应用和直连应用。
9. 按权限和当前 playbackRevision 将一个试点应用切入 byteplus_web。观察播放失败、首帧、广告恢复、签发、奖励、发布与清理任务。
10. 验收通过后扩大范围，保留官方应用原通道。

回滚优先级：

- 单应用故障：关闭该直连应用入口或下架该应用内容，官方继续服务。
- 单剧版本故障：回滚到仍可用的保留版本，同步分类及缓存。
- 首页配置错误：从已记录的前值按当前 revision 恢复目标通道配置。
- 服务故障：回滚到已经支持新 Schema 和引用保护的兼容版本。
- 不直接恢复不认识直连引用的旧清理 Worker。必要时先停止删除任务并核对全部实例，再执行受控恢复。

仅按 API 路径或部署实例分开不能隔离共享 MySQL、Redis、CPU 和 Worker 负载。试点同时观察官方延迟与错误率，新增任务使用有界并发，不在本次引入新消费者启动限流规则。

## 21 平台前提和待验证项

| 验证项 | 完成要求 | 未完成时的边界 |
| --- | --- | --- |
| TikTok 容器开放 | 确认目标 AppID 可使用所选独立 SDK，取得所需平台支持 | 直连生产入口保持关闭 |
| 实际 SDK | 已固定 @byteplus/veplayer 1.19.1，直连分支懒加载 JS/CSS、等待 License 初始化；真实容器仍须验证 | 不在生产配置中猜测接口 |
| Web License | 匹配页面实际运行域名、VOD 应用和有效许可 | 不把播放 CDN 域名当许可站点域名 |
| 原片兼容 | 目标 iOS/Android、编码、容器、拖动、码率和弱网通过 | 不宣称任意原片都能播放 |
| 广告与平台事件 | 真实广告恢复和回传行为验证 | 不以回调成功等同收入或投放归因确认 |
| 官方宿主版本 | 维持实际已支持的 TikTok 范围，核对官方 SDK 参数变化 | 不用独立 BytePlus Token 替代官方凭据合同 |

TikTok 当前公开文档要求使用官方播放器，第三方或原生视频默认受限，并提供迁移豁免渠道；不能仅因为 BytePlus SDK 在普通浏览器工作，就判定目标 Minis 可上线。[TikTok Minis Player](https://developers.tiktok.com/docs/en/minis-player)

BytePlus 支持不转码时播放上传原片；参数和可用输出需要匹配实际资源。[Common playback configurations](https://docs.byteplus.com/api/docs/byteplus-vod/common-playback-configurations)

高级跨视频预加载不属于本次基础版能力范围。[Web Player SDK Preloading](https://docs.byteplus.com/en/docs/byteplus-vod/preloading_web)

Web/H5 许可绑定的是网站运行域名，VOD appId 与 TikTok AppID 不同。[SDK applications](https://docs.byteplus.com/api/docs/byteplus-vod/docs-sdk-management)

平台确认与真机通过是上线前提，不妨碍先完成受控的内部数据、接口和测试开发。

## 22 文档同步清单

本次已同步以下资料：

- [消费者接口合同](api-contract.md)：添加直连字段和默认规则，保留官方合同。
- [运营接口](operations-api.md)：新增直连发布、推荐、复制和预览接口。
- [首页配置](homepage.md)：明确两个运营入口、独立配置和应用过滤。
- [全局配置](global-configs.md)：直连 profile、许可资源和凭据引用。
- [部署说明](deployment.md)：增量顺序、Worker 职责、开关和安全回滚版本。
- [数据库迁移说明](../sql/README.md)：新增结构和权限的执行证据。
- [Mini 官方播放说明](../../drama-mini/docs/tiktok-minis-official-player.md)：保留官方边界，并链接新的直连适配说明。
- [Mini 直连播放说明](../../drama-mini/docs/veplayer-direct.md)：基础版参数、授权恢复、事件与真机验收。
- [实施与上线手册](veplayer-release-runbook.md)：迁移、配置、回滚、验证结果和未执行的外部验收。
- 三端 AGENTS.md、开发协议和相关测试规则：将已确认的双通道边界写清，保留日志、幂等、身份和真实平台测试约束。

完成标准是三种客户端场景、两种独立运营流程和本方案验收矩阵均有对应实现与证据；只完成首次直连播放不算本次改造完成。
