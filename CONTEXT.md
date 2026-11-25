# 当前状态

## 2026-11-06 TikTok 标签关联数量按主体隔离

标签目录与英文翻译保持全局共用；“关联短剧”改为按可信主体聚合当前编辑标签，重复标签及同主体多应用不重复计数。`GET /admin/v1/tiktok-tags` 要求有效主体，缺少或不存在的主体返回参数错误，仓储缺少主体也不回退全表统计。英文翻译更新仍全局生效，消费者合同不变，无新增 Schema 或迁移。

Admin 标签页等待主体选项就绪，并以主体为组件身份重新读取关联数；主体切换清除旧列表，A→B→A 的旧响应不能覆盖新数量。服务端聚焦、完整 `make verify`、隔离 MySQL 标签/内容归属/直连隔离回归通过；覆盖双主体计数、空主体、重复标签、删除后数量更新及共享翻译。Admin 聚焦 63 项、完整 verify（503 项组件测试及部署脚本、lint、类型和构建）通过；实际控制台回归覆盖主体切换和迟到响应，产品组件另以隔离内存 API 在 1440×1000、390×844 验收。截图 `../drama-admin/output/playwright/tag-subject-20261106`，日志前缀 `/tmp/drama-tag-subject-` 与 `/tmp/drama-admin-tag-subject-`。未操作远端数据库或部署服务，未调用真实平台；Admin 工作区的其他广告配置改动不属于本次提交。

## 2026-11-06 沿用全局 VOD 空间

所有主体的新上传和新官方发布直接读取原 `global_configs.media.space_name`；发布身份、内容与上传任务仍按主体隔离，历史素材及任务使用冻结空间。主体不再配置空间，运行模型排除原主体空间列，旧后台提交的 `vodSpaceName` 仅兼容接收、不读写入库。全局表单省略空间时保留旧值，不新增配置来源。

新增 `20261106_001_shared_vod_space.sql`，仅移除空间独占唯一键并标记旧空间列废弃，原值保留。已按授权在测试库 `101.47.11.18:3306/drama_test` 执行，遵照“不要备份”；两条主体记录与两条全局配置的既有字段摘要一致，原全局空间存在且通过当前源码校验。没有执行业务数据拆分、清空 Redis 或服务部署。

服务端最终 `make verify`、完整隔离 MySQL/Redis/组合层集成及共享空间最终聚焦升级回归通过。覆盖共用空间、跨主体封面/任务越权、保留旧配置与废弃列、无全局空间不从主体兜底、历史任务冻结空间。Admin 完整 verify（501 项组件测试及部署脚本、lint、类型与构建）通过；本机真实浏览器在 1440×1000、390×844 检查产品组件，API 为内存验收替身，没有调用真实平台。截图在 Admin `output/playwright/shared-vod-20261106`，日志前缀 `/tmp/drama-shared-vod-`。

## 2026-11-06 单主体原地升级与脚本合并

按用户确认的线上现状，存量数据只有一个主体。四份主体增量合并为 `sql/migrations/20261105_001_subjects.sql`，其余三份删除；创建默认主体 `id=1`，存量归属字段使用 `NOT NULL DEFAULT 1`，保留主体隔离索引、原发布方配置及 VOD 空间。原剧集 ID、VID、线上版本、投放和权益引用保持不变，新主体从空内容开始。按用户补充要求，保留 `global_configs` 的全部记录及已有字段值；原线上快照、Album 和分集平台标识列保留原值，注释标记已废弃，运行时读取明确排除原列。

移除跨主体历史拆分命令、旧 ID 字段、映射查询、响应投影和相关专用测试；保留真实主键校验、奖励幂等、主体隔离和 Worker 按权威归属读取能力。升级回归改为从真实无主体旧表直接执行单一脚本，并验证旧 Token、原链接详情/播放及旧代码省略主体字段的写入。已执行原四份增量的库不重跑合并脚本。

聚焦回归、完整 `GOWORK=off make verify` 及隔离 MySQL/Redis/组合层集成均通过。最新日志分别为 `/tmp/drama-config-preserve-focused-retry.FRtNc5`、`/tmp/drama-config-preserve-verify.c2N2M4`、`/tmp/drama-config-preserve-integration.kwFEmR`，没有调用真实平台或部署服务。

按用户授权，在测试库 `101.47.11.18:3306/drama_test` 恢复缺失的 `distribution` 记录：发布方四个字段取自 `subjects.id=1`，VOD 账号与空间取自保留的 `media`；事务提交后重新连接核对存在，`media` 内容摘要不变。当前源码可以读取并校验该库的 `media`。远端 `test` 当时为 `b1ec929`，不含 `feat/ads` 上的旧 `space_name` 兼容修复 `36b1da6`；`deploy.sh test` 固定拉取 `test`，因此只更新功能分支不能修复测试部署。用户已要求提交推送并合入 `test`。

代码已提交至 `feat/ads`（`aa680ad`）并合入 `test`（`d515f6b`）。随后按用户授权，对上述测试库只补实际缺项，未重跑整份合并脚本；按用户明确要求不做备份。补齐短剧/媒资任务归属、主体 VOD 空间及生成列/唯一键、相关索引，13 张表的归属字段均为 `NOT NULL DEFAULT 1`；10 部存量短剧归主体 1，已有发布记录的主体字段保留，不做拆分。

此前已删除的三个废弃列补回并标注中文废弃注释，从保留的主体官方状态恢复可找回的快照/Album 和 139 条分集平台标识；无法据此保证找回所有曾被删除的历史值。执行前后比较 17 张表共 326 行既有字段的 SHA-256，全部一致，包括 `global_configs` 两条记录。新连接复查归属默认值、VOD 列及唯一键、废弃列、原 178 条分集和媒资配置校验均通过，无旧 ID 映射列；未部署服务、清理 Redis 或调用平台。执行结果只含结构与计数，记录在 `/tmp/drama-subject-db-result.json`。

## 2026-11-06 主体隔离审查修复（feat/ads，未提交、未部署）

官方列表缺少独立上架记录时，历史任务回退的外层查询和最近上线子查询同时按可信主体限定；新主体及无主体上下文保持空选择，不借用其他主体版本。正常的本主体历史回退和已有权威上架选择保持不变。新增隔离 MySQL 回归通过实际列表覆盖五种场景，修复前四种场景可稳定检出遗漏。

Admin 保存回调按主体切换/登出的代次验证；过期回调不更新编辑器或列表、不作废当前读取，也不跳转旧新建详情。A→B→A 仍不能恢复旧回调，同主体应用切换及正常保存保持可用；共享草稿的服务端写入不取消。详见 Admin 对应回归。本次修复不新增 SQL、配置、消费端字段或 Mini 改动，原主体迁移与配套部署要求不变。

验证：主体与历史选择聚焦回归、完整 `GOWORK=off make verify`、完整隔离 MySQL/Redis/组合层集成均通过；包括旧库原列不变、旧 VisitorToken、启动、目录、首页、详情和播放授权。Admin 完整 verify 通过（61 文件、483 项测试、19 项部署脚本、lint、类型和构建）。桌面 1440×1000、手机 390×844 使用本机真实 HTTP/MySQL 验证保存响应晚到与新主体详情读取；延迟只通过 `/tmp` Go overlay 注入验收夹具，没有进入产品或正式回归的计时等待。截图 `../drama-admin/output/playwright/subject-fixes-20261106`，临时服务、浏览器和一次性数据库已清理。未执行远端迁移、部署、真实平台调用、真机或百万用户压力测试。日志前缀 `/tmp/drama-subject-fix-`。

## 2026-11-06 主体内容运营隔离（feat/ads，未提交、未部署）

直连上架状态、不可变版本、操作幂等与历史按主体隔离；同主体直连应用共享目录，不同主体独立发布同一草稿。官方、直连两渠道的轮播、热播、分类成员、排序、隐藏与修订按主体保存。管理接口必须明确主体，不要求直连主体配置官方发布凭据；消费目录、更多、搜索、详情、播放与既有落地/奖励目标统一沿用可信应用归属，不接受客户端指定主体。共享草稿、媒资、VOD、管理员与 RBAC 沿用原范围，删除和媒资保护仍检查全部主体。

新增 `sql/migrations/20261106_001_subject_content.sql`，在全部前序增量后执行；七张内容运营表的原记录归最早创建的默认主体，保留原主键、冻结版本、线上指针、幂等记录、JSON、时间与修订。新主体为空且无运行时回退。新缓存命名空间包含主体、应用和渠道，避开旧共享结果；沿用有限 TTL 和提交后失效，没有新增 Mini 请求或高频主体查询。旧 Mini 不增加字段或请求，应用身份、播放修订、VisitorToken、用户与权益不迁移。

验证：聚焦主体/旧库升级/日志/SQL 查询测试、`GOWORK=off make verify`、完整隔离 MySQL/Redis/组合层集成全部通过。新增回归覆盖同剧双主体 V1/V2 与独立幂等、另一主体不能读取操作结果或播放签名、同主体多应用共享、公开目录与推荐为空、推荐候选与下架门禁独立、分类隐藏独立、全局媒资保护、缓存不串用，以及真实旧表迁移前后全部原列摘要不变和旧 Token 启动/目录/首页/详情/播放。历史升级夹具同步按实际迁移顺序加载，不修改已发布增量。

本机真实 HTTP 在 1440×1000 和 390×844 验证主体/应用联动与轮播、热播、分类切换，截图 `../drama-admin/output/playwright/subject-content-20261106`；本轮浏览器、API、Vite 及一次性数据库均正常清理。完整集成默认不运行常驻浏览器和百万用户压力测试，前者已单独启动验收；没有真实 TikTok/BytePlus 调用、真机 Mini 测试、远端迁移或部署。发布须停旧 API/Worker、备份、执行新增量并配套更新 Server/Admin，不能只回退旧二进制。验证日志前缀 `/tmp/drama-subject-content-`。

## 2026-11-05 TikTok 发布主体（feat/ads，未部署）

Server、Admin 新增主体管理、应用固定归属和顶部主体/应用联动。主体只隔离 TikTok 发布方、Album、官方分集映射、审核/上线事实和同主体授权；管理员、RBAC、共享草稿、全局 VOD、直连目录及原应用内用户/广告/权益边界不变。全局 `distribution` 退出合同，发布方移到 `subjects`，VOD 账号 ID 移到 `media` 且对直连保持可选。主体密钥不回显、不进入访问日志。已有官方资产禁止更换发布身份，密钥可以轮换。

迁移为 `sql/migrations/20261105_001_subjects.sql`，默认承接所有已有应用，官方投影迁至 `tiktok_drama_states` 后移除共享草稿的官方字段。现有 Mini 全是直连，无需修改或重新发包；旧 Token、用户 ID、登录身份、直连版本和线上指针均有迁移回归。共享删除检查各主体当前任务及线上状态，历史已替换任务不永久阻止删除；媒资引用与官方分类保留其他主体仍使用的内容。合同及发布顺序见 `docs/subjects.md`，旧 API/Worker 须停写后执行增量，再配套发布 Server/Admin。

验证：`GOWORK=off make verify`、完整 MySQL/Redis/组合层集成、主体及可选 VOD 账号合同聚焦测试通过。完整集成使用本机一次性数据库，最终连接附加 `sql_log_bin=0`；此前既有 tmpfs 被累积 binlog 占满并导致隔离 MySQL 退出，恢复既有测试容器后容量聚焦与完整集成通过，未修改统计实现或删改业务库。Admin 60 个文件共 471 项测试、19 项部署检查、lint/类型/构建通过；真实本机 HTTP 完成桌面与 390px 手机选择器、空主体、新应用自动选中和主体表单验收，截图在 `../output/playwright/subjects-20261105`。浏览器/API/Vite 临时进程已停止。没有真实平台调用、百万用户压力验证、远端 SQL、提交、推送或部署；Mini 工作区无改动。

## 2026-10-11 测试库官方审核菜单迁移已执行

Admin 功能提交 `5bd1c7d`、Server 功能提交 `35e7be4` 均已推送 `feat/veplayer` 并快进合入远端 `test`。北京时间 17:34:39，在 `101.47.11.18:3306/drama_test` 单事务执行 `20261011_002_remove_official_review_menu.sql` 并提交；旧审核菜单删除，权限节点 94→93，角色授权 28 条不变，独立连接确认无悬空父节点或角色关联。旧菜单没有自定义子节点或显式授权。未重跑历史迁移、未保留备份、未执行部署或真实平台调用。

事务内 22 张业务表摘要均未变，表数仍为 177；提交后独立连接发现短剧、TK 发布任务、上线历史及官方分类排名 4 张表发生并发内容更新，行数不变，来源未追溯。其余 18 张受保护表摘要保持一致，包括审核历史、直连版本与操作历史、应用配置及 UID 水位。本 SQL 仅写菜单和角色权限表。执行摘要位于工作区 `artifacts/official-review-menu-migration-20261011/migration-report.json`，前置条件和完整验证结论见 `sql/README.md`。下文未执行、未提交的描述为功能开发时的历史状态。

## 2026-10-11 移除独立官方审核记录菜单

Admin 审核历史收回“官方发布 → 发布详情 → 审核记录”，审核/上线历史只读，已有记录 API 和数据继续保留。默认菜单通过 `sql/migrations/20261011_002_remove_official_review_menu.sql` 删除；有自定义子节点时保留无路由目录及原启停状态。不自动把历史菜单授权转成官方发布权限，不改变任何发布或 Mini 合同。权限菜单校验拒绝 `/dramas/reviews` 和 `reviewRecords`，旧全局前端地址返回官方列表；该剧历史继承官方发布菜单和 `content.drama.read`。

服务端聚焦测试、`make verify` 和隔离 MySQL 的迁移三种场景、当前 Schema、旧直连升级均通过；默认权限节点 93 个。真实本地 HTTP 桌面/手机验证历史和旧链接，无官方平台请求。临时浏览器夹具收到 SIGTERM 后正常结束并清理隔离数据库，API/Vite 已关闭；未执行业务库 SQL，未提交、推送或部署。日志前缀 `/tmp/drama-review-menu-`，详情见 Admin CONTEXT 与 sql/README。

Admin 最终已完成 424 项全量组件、19 项部署脚本、lint、类型检查与构建；两次 verify 的失败及后续复测记录见 Admin CONTEXT，未把中断的命令称为成功。本轮仅变更菜单入口及校验，没有新增消费者 API、Worker 写入或业务 Schema。

## 2026-10-11 官方发布列表控制流程

Admin 取消官方发布工作台二级页面，以列表状态与按钮复用已有同步、送审、查询、撤回、申诉、上线、下线和恢复命令。详情只读展开，授权抽屉按精确权限维护，记录及旧地址回到官方列表详情。未知写入保留原检查点；已有版本的 Album Update 允许人工先核对，再在原版本完整且未推进时携带原版本号恢复冻结内容，首次同步及 Worker 仍只查询。确认上线绑定旧任务并防止确认期间偷换版本；已有线上快照和推荐引用门禁不变。详见 docs/operations-api.md 的“官方发布列表与内容范围”。

本次 Server 仅更新文档，无 Go、API、Worker、菜单、Schema 或 SQL 变更，Mini 未改。Admin 最终完整 verify 通过（422 项组件、19 项部署脚本、lint、类型及构建），本机隔离真实 HTTP 验证送审优先级、上线任务标识、撤回排队及官方推荐拒绝；浏览器阶段由测试夹具设置，没有真实平台调用。桌面/手机界面已核验，具体测试限制及临时夹具超时后的清理记录见 ../drama-admin/CONTEXT.md。未操作远端业务库，未提交、推送或部署。

## 2026-10-11 管理端动作与提示文档同步

运营接口文档将短剧列表动作说明更新为“确认就绪”“批量确认就绪”，结果状态仍为“待上线”；补充直连单条操作失败只弹出友好提示、列表读取失败保留刷新和批量结果保留汇总的管理端规则。本轮仅修改文档，无服务端代码、API、数据库或 Mini 变更；本地隔离浏览器夹具验证真实下线拒绝与状态保留后已正常结束并清理数据库，未连接远端业务库或调用真实平台。

## 2026-10-10 内容管理与渠道独立发布

短剧只维护剧和集及 draft/ready，不保存或查询发布版本，不返回 contentVersionId/contentVersion，不提供短剧版本历史。列表提供单条、批量待上线，整批校验并提交；内容状态筛选与渠道无关。直连使用 direct_publication_versions，在上架事务中独立发号和冻结快照，所有有效直连应用共用目录。TK 使用已有官方 platformVersion 和任务内冻结内容，不增加版本表或回填历史 TK 数据。两渠道独立发布，草稿编辑、确认就绪、驳回修改和连载加集不改变另一渠道线上快照。

渠道状态、审核、授权和投放链接放到官方与直连发布菜单；Mini 只读取当前上线内容。已有 TK 内容、审核任务、旧线上内容和媒资/分集保护保留。标准增量新增 20261010_003；已删除新增表的测试库使用 sql/feat_veplayer_tables.sql 独立重建。本轮不执行业务库 SQL，不提交或推送。验证结果见本轮交付记录与 sql/README.md。

本轮服务端聚焦回归和 `make verify`（全量 Go 测试、vet、构建）通过；隔离 MySQL 回归覆盖批量就绪原子性、渠道独立发布与并发、驳回修改重送、连载加集、旧 TK 快照/任务/媒资保护、独立建表结构一致性。日志：`/tmp/drama-server-channel-final-verify2.log`、`/tmp/drama-channel-boundary-db-final.log`。桌面 1440px 和手机 390px 通过本地真实 Handler 检查列表批量待上线及渠道菜单，无页面横向溢出；未调用真实平台、写入业务数据库或部署。

## 2026年10月 VePlayer 双通道

feat/veplayer 已实现可按播放修订修改的应用 provider、独立直连发布/版本/任务/应用关系、分类/轮播/热播、目录隔离与短时授权，保留消费者原路径及官方合同。新表与权限独立增量，测试配置已开启直连；生产与本地配置保留关闭，启用前全部清理 Worker 必须升级。Go 全量验证、隔离 MySQL/Redis 集成及本机后台流程通过，未执行真实平台调用、业务库迁移或部署。完整状态见 [实施与上线手册](docs/veplayer-release-runbook.md)，下文为各日期的历史记录。

`drama-server` 已按参考工程的 Go 分层结构初始化，保留 `cmd`、`internal/biz`、`internal/data`、`internal/service`、`internal/server`、配置、Schema 和测试骨架。

当前业务实现是迁移基线，不代表新的官方接入合同已经完成。下一阶段需要用 BytePlus VOD 与 TikTok 官方发布、授权和 Minis Link 适配器替换旧平台流程，并保持消费者 API、RBAC 和权益领域逻辑可复用。


## 2026-09-12：移除字幕与多语言

消费者界面固定英语，管理端固定中文。内容与 VIP 文案只维护主字段，标签使用官方名称。应用语言配置、翻译编辑、语言协商及字幕生成/管理/发布阶段已移除；原始音频语言元数据保留。数据库结构以冻结的 `sql/init.sql` 基线及 `sql/migrations/` 的有序增量为准；新库执行两者，已有库只执行未应用增量。服务启动不会自动改库，不再保存或检查结构版本表。管理员使用 JWT 认证，角色权限实时读取；退出登录撤销账号当前版本的全部 JWT。

全局配置持久化在 MySQL global_configs；Redis 仅做五分钟单条缓存。API/Worker 先读缓存，未命中回源；更新先写数据库再删缓存。管理接口隐藏媒资密钥，未替换的掩码保留原值。

C 端用户在 `users` 保存应用范围的 TikTok OpenID 摘要，直接按身份查找或创建；不存储平台令牌或 OpenID 密文，不迁移匿名权益。B 端账号在 `admin_users`，双方共用业务库。

剧集解锁和 VIP 权益的授予事实保留在 16 组 `entitlement_grants_XX` 不可变流水中，撤销另记 `entitlement_revocations_XX`，播放只读 `user_entitlement_state_XX` 目标投影；奖励事实保留在 `reward_grants_XX`。VIP 门槛调整后的补发在启动加载或刷新 VIP 进度时完成。消费者域固定 1024 个逻辑桶并映射到单库 16 组物理表，VisitorToken 携带可信桶号。开发阶段只维护最终 Schema，不编写历史数据迁移或兼容分支。

内容领域以 Drama 为聚合根，episodes 保存分集 VID、空间、时长和处理状态，dramas 保存封面 MID、空间和 StoreUri。删除 media_assets、media_asset_references、media_deletion_jobs、vod_event_inbox，最终共 18 张表。media_jobs 统一保存上传预留、封面临时上传结果和删除任务；内容切换与旧文件删除任务同事务。删除前核对草稿与发布快照，回调同步幂等唤醒任务，视频状态仍由 VOD 查询确认。上传接口统一使用 uploadId/coverUploadId，无旧字段兼容。

C 端目录与详情使用 Redis 缓存；B 端和发布 Worker 在剧变更提交后统一清除。播放、奖励和个人权益继续读权威数据。详见 docs/consumer-catalog-cache.md。

统一媒资拥有方和多应用自动分发使用 MySQL `distribution` 配置及 `drama_distributions` 关系表；发布输入只接收 `priority`，授权独立于发布任务。详情见 docs/drama-distribution.md。

TikTok 送审后暂停，运营手动查询审核/申诉结果，整版通过进入 approved/await_publish，再确认当前任务上线。取消审核轮询；旧排队上线任务缺少 workflow_json.publishRequested 时暂停。接口与兼容规则见 docs/operations-api.md。

## 2026-09-15：数据库按实际业务字段收敛

移除 applications 的 brand_json、launch_url、byteplus_account_id、byteplus_space_name，以及 episodes 的 source_url、upload_job_id。媒资拥有方从 MySQL distribution 配置及 Redis 缓存读取，应用品牌使用 name/logo_url，媒资任务追踪留在 media_jobs。最终 18 张表、223 列均有中文业务注释。开发阶段只维护最新 Schema，不保留本次历史升级或兼容文件；测试库单独同步，不由应用启动迁移。

本次已同步测试库 drama_test：18 张表、223 列，字段属性及中文注释与最新定义一致，保留数据变更前后聚合校验一致。当前工作区通过服务端 make verify、后台 verify（217 项测试）及隔离 MySQL Schema/应用 HTTP 合同测试；未执行服务部署或真实 TikTok/BytePlus 调用。


## 2026-09-15 内部主键与关联统一

应用、内容、运营和 RBAC 实体沿用 MySQL `BIGINT UNSIGNED AUTO_INCREMENT`。当前消费者用户 UID 使用 `id_allocators` 表的 `consumer_domain` 永久预留号段，`users_00` 至 `users_15` 保持非自增；Redis 共享当前段、备用段与发号进度，Lua 原子消费和切换，并在当前号段消耗到 80% 时异步预取下一段。应用重启复用共享进度，无需配置实例机器号；MySQL 既有水位和 UID 均保持不变。权益授予、权益投影、权益撤销、奖励流水和 VIP 进度的内部主键改由各物理表自增，不再占用用户号段；这些记录 ID 只在所属物理表内唯一，查询及 `grant_id` 等关联必须携带用户分片上下文。所有关联列使用同类型普通字段和索引，禁止外键。消费者域的来源幂等键包含逻辑桶，奖励、权益流水、权益投影和进度与用户同分片提交。

应用 `id` 与官方 `tiktokAppId` 分离：内部关联、管理路由、请求应用上下文和策略绑定使用 `id`；官方 AppID 保留原字符串，只参与 Mini 启动解析和平台调用。拥有方全局配置键、官方 VID/MID、ClaimID、请求幂等键及投放启动码保留各自语义，不作为实体主键。审计 ID 对外也使用字符串，避免大整数精度丢失。发布可见性中的拥有方映射为内部应用主键。

当前未提交工作树已通过服务端 `GOWORK=off make verify` 与本地 33306 隔离 MySQL 全部集成测试；专项覆盖超过 2^53 的主键与关联、事务回滚、并发登录、奖励重复结算及实际权益快照。详细命令见现有 MySQL 测试入口；测试不连接 TikTok/BytePlus。后台浏览器在临时独立数据库完成应用创建、编辑、刷新，读回内部 ID `103` 与官方 AppID `0007685293427067586568`，二者无混用。临时页面、服务和数据库已清理。

仅维护最终 Schema，未修改远端测试库或生产库，也未部署。旧字符串主键数据库不能直接运行本版；上线前需另行完成数据库切换并同步 server/admin，应用启动不会自动迁移。

消费者 Mini 的 ID 合同聚焦测试 14 项通过；最终 `make verify` 完成，254 项 Vitest、Node 脚本测试、类型检查和构建通过。后台全量的单项超时及单独复测证据见后台 CONTEXT，本次没有未修复的 ID 关联失败。

## 2026-09-15：观众用户只读查询

新增 `GET /admin/v1/consumer-users`，要求独立读取权限和明确应用上下文。入口校验分页及规范十进制用户 ID；业务层记录实际仓储故障；GORM 子查询聚合 16 个物理分片，按创建时间和数值 ID 倒序分页，返回空数组及总数。只返回内部主键、应用 ID、身份类型和注册时间。

最终 Schema 增加 `menu.consumerusers` / `operations.consumer_user.read`，不自动扩大已有角色权限，不在服务启动时改库；已有库的菜单配置方法见 `docs/operations-api.md`。Mini 没有此管理接口消费方，消费者合同保持不变。

`TestConsumerUserListLogging` 和隔离 MySQL 的 `TestConsumerUserListIntegration` 已通过。覆盖 16 分片全局排序、41 位用户分页、超过 2^53 的主键、跨应用隔离、参数拒绝、未登录/未授权拒绝、普通角色授权成功及撤权即时失效。浏览器已通过真实 HTTP 查询此实现，无真实平台调用或远端数据库写入。

本次最终 `GOWORK=off make verify` 已通过（完整 Go 测试、`go vet`、服务端构建）；未执行部署、远端库变更或真实平台调用。

## TikTok 分类排行榜（2026-09-15）

历史版本曾使用 Redis Hash 保存人工榜单；当前分类榜已改用 MySQL `category_rankings`，禁止继续使用 Redis 持久化。运营 API 为 `/admin/v1/category-rankings` 及 `/{tagID}`、`/{tagID}/targets`，权限 `operations.ranking.read/update`；公开首页 sections 新增 rankings，实时过滤下架、授权和标签归属。后台独立菜单及 H5 首页已接入。规则、权限增量 SQL 与本地验证证据见 `docs/homepage.md`。


## 2026-09-16：应用变现模式

应用新增 `monetizationMode`（IAA / IAP / IAA+IAP），后台新建、编辑和桌面/移动列表使用同一字段。新建与完整更新均要求明确提交模式。按用户最新要求，开发阶段直接维护最终 Schema，已移除本次旧库迁移脚本和省略字段兼容；未操作共享数据库或部署。该字段决定当前应用可配置的解锁方式；最新限制与验证见下文“按当前应用和变现模式配置解锁策略”。

验证：`TestApplicationModeValidation` 与应用登记 HTTP 回归通过；`TestCurrentSchemaIntegrationApplicationMode` 在隔离 MySQL 中覆盖三种模式、枚举拒绝、旧端更新保留值、应用隔离和旧库两次迁移，已用最新工作区 Schema 重测通过。`make vet build` 通过。`make verify` 未通过：初次遇到并行分类测试编译问题，重跑后仅剩 `TestBusinessRepositoriesUseGORM` 对 dashboard_behavior、dashboard_summary、metric_migration、metric_partitions 的 Raw/Exec 约束失败，未扩大本次范围修改这些文件。日志位于 `/tmp/drama-mode-server-verify-retry.log`。小程序公开 Bootstrap 使用独立投影，此次合同不变，未修改或重跑小程序项目。未执行正式迁移或发布。

## 2026-09-16：用户管理目录与测试库同步

按用户要求新增 `directory.consumerusers`（用户管理），将 `menu.consumerusers` 移到该目录下，读取操作保持 `operations.consumer_user.read`。最终 Schema、内存权限夹具和接口说明已同步；当时为既有库准备的权限增量现已合并到 `sql/init.sql` 并删除独立脚本；升级已有库需从结构差异生成并审核 DDL/DML，保留菜单 ID 和角色授权。超管无需另行分配角色权限。

已获授权在测试库 `drama_test` 执行并通过新连接回读：目录 ID `89` → 用户列表 ID `87` → 读取操作 ID `88`，均为 active；无关权限与角色关联未变。连接按测试配置启用 TLS。未部署应用、未修改生产库。

验证通过：RBAC 与内存仓储聚焦测试、本地 33306 隔离 MySQL 的 `TestConsumerUserListIntegration`（实际超管登录返回新目录并可查询）、迁移重复执行及 ID/授权保留检查、`GOWORK=off make verify`。日志位于 `/tmp/drama-user-directory-integration.log` 和 `/tmp/drama-user-directory-verify.log`。本轮未在远端浏览器验证刷新后的显示。

## 2026-09-16：测试库消费者分片迁移

用户明确要求执行 SQL 后，对 `drama_test` 完成增量迁移：由当前 `sql/init.sql` 提取消费者域六组各 16 张分片表，共创建 96 张表；将旧 `users` 的 23 条记录按应用 ID、NUL 分隔符及身份材料的 SHA256 前两字节计算 1024 逻辑桶，再按桶号模 16 路由。保留旧用户 ID、应用、身份摘要和时间字段；原四张旧表保留，未删除或更新。执行前及复制事务内确认旧 `reward_grants`、`entitlements`、`application_vip_progress` 均为空。

隔离 MySQL 使用 64 条含匿名/TikTok 身份、跨应用、超过 2^53 主键的样本验证 SQL 路由与代码哈希一致，逐字段比对通过。测试库复制事务提交后用新连接验证应用 `1` 共 23 个用户，分页为 20 + 3，无重复、无丢失，未引入外键。本次仅执行测试数据库 SQL，未修改业务代码、未部署；未运行全量代码检查或实测远端浏览器。SQL 和执行核验脚本保存在 `/tmp/drama-consumer-shards-ddl.sql`、`/tmp/drama-consumer-users-copy.sql`、`/tmp/drama-migrate-consumer-shards.py`、`/tmp/drama-verify-migrated-list.py`。迁移脚本拒绝目标分片表已存在或旧关联表非空的环境，不能作为任意存量库通用迁移。


## 2026-09-16：按当前应用和变现模式配置解锁策略

当前应用由右上角选择器确定；新增、读取、修改策略均使用该应用 ID，不再要求手动绑定。策略快照返回权威 `monetizationMode`：IAA 允许广告解锁和广告 VIP，IAP 允许单次购买和订阅，IAA+IAP 允许两类。已有不兼容规则保留可见并阻止保存，需先删除；停用规则同样受模式约束。应用切换模式时要求已有完整策略兼容，模式变化递增消费修订，拒绝旧编辑和旧 Offer。IAP 启用及保存购买策略无需广告位，支付能力仍仅可预配置、尚未接入。

服务端在事务内检查当前应用模式，旧策略管理/绑定接口也不能绕过约束。消费者直接使用当前保存配置，不做历史不兼容规则降级；IAP 不返回启动插屏。Mini 公开合同不变，本轮未修改 Mini 源码或运行其完整验证。未提交、推送、部署或操作共享数据库。

验证：业务/模式/运营 Handler/内存仓储聚焦测试通过，`make verify` 完整 Go 测试、vet 和构建通过。隔离 MySQL 的当前应用策略、应用模式、解锁策略、自动广告位关联集成测试通过；新增 `TestCurrentSchemaIntegrationStrategyMode` 进一步验证三种模式、无广告位 IAP 启用、停用规则拒绝、旧绑定接口拒绝、模式变更回滚、跨应用隔离、历史消费投影及模式修改与策略保存并发。并发测试最终恰有一项成功，另一项模式冲突，持久化配置始终兼容。真实后台桌面和手机浏览器新增/修改/切换/回读通过；隔离预览资源已清理。日志：`/tmp/drama-strategy-mode-server-verify.log`、`/tmp/drama-strategy-mode-sql-retest.log`、`/tmp/drama-strategy-mode-concurrency.log`。


## 2026-09-16：开发阶段取消应用模式的历史兼容

用户明确不需要保留历史。删除本次独立变现模式增量脚本，只维护最终 Schema；创建和更新应用均必须明确提交模式，后台请求及策略快照类型同步收紧。当前应用策略直接新建或更新自身记录，不再复制历史共享策略或复用迁移副本；保留归属、模式、事务和并发修订校验。删除消费者读取时停用不兼容历史规则的处理。既有实际调用入口未在本轮整体重构；本地目录准备脚本补齐应用更新的模式字段。没有执行数据库重建、清空、远端写入或部署。

## 2026-09-16：观众列表大数据量游标分页

本轮按大数据量需求替换全量 UNION 排序、COUNT 与 OFFSET。每个用户分片通过 `(application_id, created_at, id)` 联合索引取 `pageSize+1` 条，再合并最多 `16*(pageSize+1)` 个候选。单条 SQL 的时间与主键排他游标保持排序；游标绑定应用和精确 ID 筛选，不授予权限，不跨页保持长期事务。首页不传 cursor，后续传 nextCursor；响应为 `{items,hasMore,nextCursor,pageSize}`，旧 page 参数明确返回 400。Mini 无此管理接口消费方。

最终 Schema 已更新索引；当时的既有库增量 DDL 已在隔离 MySQL 验证在线建索引，现已合并到 `sql/init.sql` 并删除独立脚本。先执行索引，再配套发布 server/admin，旧分页合同不兼容。当前仅实现并验证，未执行远端索引 SQL，未部署。

验证：实际 HTTP 集成覆盖超管导航、41 条用户连续游标翻页、同时间大整数排序、末页、精确搜索、应用与权限隔离、插入新用户不挤动后续页、游标非法格式/时间/主键/跨筛选拒绝；业务失败日志测试与 `GOWORK=off make verify` 通过。`CONSUMER_USER_SCALE_TEST=1` 的百万用户隔离测试覆盖保留旧索引的增量场景：EXPLAIN ANALYZE 确认 16 张分表都用新索引，首页和约第 90 万条边界每个分片实际读取 21 条，外层合并 336 条。

性能日志 `/tmp/drama-cursor-scale.log`：首轮仅新索引时首页约 43 ms、深游标约 35 ms；保留旧索引的复测出现首页约 92 ms、深游标约 2.05 s，随后 EXPLAIN ANALYZE 执行分别约 9/24 ms。测试与其他本地检查并行，未确认波动来源；这些数据不是生产延迟承诺，未做冷缓存、高并发或千万级压测。重点验证的是索引范围读取和候选数量不随页深线性增长。测试创建的数据和临时服务已清理。

验证：模式/运营/Handler/内存仓储聚焦测试通过，消费者夹具补齐模式后相关用例通过，最终 `make verify` 全部通过。隔离 MySQL 的 `TestCurrentSchemaIntegrationApplicationMode`、`TestCurrentSchemaIntegrationApplicationStrategy`、`TestCurrentSchemaIntegrationStrategyMode`、`TestCurrentSchemaIntegrationAdReadinessHTTP` 通过，覆盖最终 Schema、模式必填、相同策略 ID 直接更新、应用隔离、保存回读、资源门禁和模式/策略并发冲突。本轮没有执行正式数据库重建。日志：`/tmp/drama-no-history-focused.log`、`/tmp/drama-no-history-server-verify.log`、`/tmp/drama-no-history-sql.log`、`/tmp/drama-no-history-ad-readiness.log`。

## 2026-09-16：测试库分页索引执行

用户明确要求执行后，已在 `drama_test` 对 `users_00` 至 `users_15` 执行 16 条在线索引 DDL。执行前新索引均不存在，执行后通过新连接逐项核对索引列顺序为 `(application_id,created_at,id)`、非唯一且可见。实际游标查询返回应用 `1` 首页 20 条、下一页 5 条，无重复；`EXPLAIN FORMAT=JSON` 确认 16 个分片均选择 `idx_users_application_created_id`。本次仅新增索引，未部署前后端。执行与核验脚本 `/tmp/drama-apply-pagination-indexes.py` 可跳过定义一致的已完成索引。


## 2026-09-16：短剧推荐全局共享

轮播、人工热播和分类排行榜改为所有小程序共享，后台取消应用选择依赖；候选包含全部已上线短剧。消费接口合同不变，按当前应用授权和上线状态过滤；授权变化不重置共享榜单的排序和隐藏设置。分类使用表 `category_rankings`；轮播和热播使用 MySQL `homepage_recommendations`；结果缓存仍按应用隔离，保存共享配置后统一失效。

本地 MySQL 33306、Redis 36379 的聚焦测试验证共享 HTTP 读写、消费者授权过滤、发布/授权事务、冲突、权限、缓存跨应用失效以及 Schema 模型一致性。日志 `/tmp/shared-final-tests.log`。后台 46 项聚焦测试及构建通过；桌面 1440 与手机 390 视口用真实组件和测试 API 检查排序、隐藏、热播标题保存，未将此声明为部署环境端到端验收。按用户要求未跑全量测试。

## 2026-09-17：Redis 只作缓存

轮播、热播权威配置改存 MySQL `homepage_recommendations`，保留栏目隔离、有序引用、标题、显隐、创建时间与修订冲突。C 端沿用完整结果缓存；管理端直接读库。全局配置、用户、会话撤销、权益、任务和统计均以 MySQL 为准，Redis 不承担唯一业务存储。缓存初始化失败不阻断服务或就绪检查。完整盘点与验收范围见 `docs/redis-storage-audit.md`。

## 2026-09-20：统一数据库初始化脚本与测试库对比

仓库数据库入口收敛为唯一 `sql/init.sql`：最终表结构、索引、系统权限、基础数据和可选超级管理员初始化均在同一文件中，历史增量 SQL 已删除并由 Git 历史保留。新库只对空数据库执行该脚本；已有库必须先做结构对比，再生成并审核独立 DDL/DML，应用启动不自动迁移。相关测试改为从统一脚本提取目标表结构，并新增只允许保留一个 SQL 文件的回归检查。

使用 MySQL 8.4 空库执行 `init.sql` 成功，得到 122 张表、65 条系统权限、默认 0 个管理员、0 个外键或 CHECK。与测试环境 `drama_test` 的 `information_schema` 只读对比显示：122 张固定表全部存在且表级定义一致；测试库另有 11 张运行期指标分表和 3 张旧未分片表。测试库还保留 17 个旧字段、16 个冗余用户单列索引，以及旧版 `media_jobs.active_episode_id` 生成表达式；完整差异和处理顺序见 `docs/test-database-schema-comparison.md`。未对测试库执行 DDL、DML 或删除操作。

聚焦测试与本次改动的隔离副本 `make verify` 通过。共享工作区随后出现尚未完成的 TikTok 回传日志并行改动，当前直接执行全量校验会因缺少 `regexp` 导入和 `Store.ListTikTokPostbackLogs` 实现而编译失败；本次没有修改或覆盖该并行功能。

## 2026-09-20：启动慢查询优化

针对启动时指标分表检查和媒资校准慢查询，`ensureMetricTable` 不再先查询 `information_schema.tables`：每个实例对每张合法日期分表只执行一次 `CREATE TABLE IF NOT EXISTS ... LIKE ...`，成功后保存可丢弃的进程内确认状态；多实例一致性仍由 MySQL 幂等 DDL 保证。目录聚合读取改为一次批量查询全部编辑区分集，再按短剧归组，消除媒资启动校准按短剧逐条读取分集的 N+1，同时保持集号与主键稳定排序。

新增 SQL mock 回归覆盖同一分表只执行一次 DDL，以及多部短剧只发出一次分集查询并正确归组。聚焦竞态测试和当前完整 `make verify`（全部 Go 测试、vet、构建）通过。未部署、提交或推送。


## 2026-09-23：MySQL 预留号段与 Redis 共享 UID 进度

消费者用户发号由 MySQL 事务永久预留互不重叠的号段，Redis 缓存当前段、备用段及消费进度。默认每段 100000，消耗到 80% 时由共享短租约选出的实例异步预取；失败退避重试，原实例退出后可由其他实例在租约到期后接管。Lua 原子发号和切段，正常请求不逐次读 MySQL，也不检查 Redis `run_id`、`master_replid` 或节点角色。应用发布、重启和扩容继续消费同一共享段，不在启动时清理缓存。

Redis 不可用时新用户发号失败，已有用户登录无需申请 UID；不切回进程内发号。缓存丢失后未使用的 UID 允许跳过，从 MySQL 当前水位重新预留，不能回收或重置水位。Redis 快照恢复或主从切换可能回滚游标，本方案不能自动识别：恢复前须阻断发号流量、停止旧实例及旧主写入，删除对应 UID 缓存后才开放发号。不能在线恢复旧游标并稍后清理。流程与恢复边界见 `docs/uid-allocation.md`。

## SQL 迭代维护（2026-09-30）

从本轮开始冻结 `sql/init.sql`，不再把后续变更合并回初始化文件。新增脚本按 `sql/migrations/YYYYMMDD_NNN_用途.sql` 保存并顺序执行，已发布文件不改写；发布记录各环境已执行的文件名与结果。新库执行初始化基线及全部增量，已有库只执行未应用的增量，应用启动不自动迁移。本轮 `20260930_001_drama_operators.sql` 为短剧增加创建人与最新操作人字段，不修改审计表，历史身份保持未知。此前“只有 init.sql”的说明是基线形成时的历史记录，后续遵循本节。


## 2026-10-11 直连运营入口简化

直连只保留“直连发布”单页，列表按钮执行待上线 → 已上线 → 待上线，上线仍检查内容 ready 和精确权限，下线不依赖草稿。删除重复上线记录菜单、直连二级发布路由、直连首页预览和官方首页复制功能及其服务端接口；分类、轮播、热播保留。Server 新增 20261011_001_simplify_direct_operations.sql 合并既有记录菜单的显式查看入口、移除废弃权限并保留自定义子节点，不增加操作授权；业务状态、版本/操作历史、TK 数据与首页配置不变，consumer API 路径未改。

本次 Admin 421 项组件测试、19 项部署脚本测试、lint、类型与构建通过；Server make verify 通过。隔离 MySQL/Redis 的 Direct 与 CurrentSchema 集成通过，覆盖入口删除 404、待上线筛选、上下线、菜单迁移重复执行、已有角色与业务数据保留；本地 Chrome 桌面与 390×844 手机视口均完成列表上下线，无二级跳转。HTTP randomUUID 缺失场景已覆盖。未部署、未执行业务数据库 SQL、未调用真实 TikTok/BytePlus，Mini 未改。验收截图位于 ../artifacts/direct-simplification-20261011。

## 2026-10-11 草稿筛选、直连批量上线与投放复制修复

编辑目录 contentStatus=draft 包含所有非 ready 的历史状态，ready 只匹配显式就绪；查询不改写存储状态或渠道线上快照。直连发布列表只返回 ready 或当前已上线的剧目，保留已上线后重新编辑成草稿的下线入口；草稿下线后须再次确认待上线才重新进入上线待办。单条详情和发布命令合同不变，批量上线由 Admin 逐条调用既有命令，各剧独立事务，失败项幂等重试，不新增服务端批量接口。

Admin 两个发布菜单均使用顶部实际选中的 Mini 生成投放链接，服务端继续按该应用权威播放通道检查线上快照、分集及官方授权。短剧草稿操作、删除引用门禁、批量进度与部分失败提示见 docs/operations-api.md。消费者接口 /api/v1/*、/api/v1/open/* 和旧版播放行为未改。

验证：make verify（Go 测试、vet、构建）通过；隔离 MySQL/Redis 的 LegacyDraftCatalog、BatchContentReadiness、DirectHTTP、ChannelPublicationsPreserveTikTokUpgrade 四项回归通过，覆盖历史状态筛选、就绪确认保留官方快照、草稿过滤、已上线编辑后可下线及旧 Mini 合同。Admin 433 项组件测试、19 项部署脚本测试、lint、类型及构建通过；真实浏览器完成桌面/手机草稿操作、筛选、下线、两部批量上线及跨播放通道应用的复制链接检查。临时浏览器、服务和隔离数据库已清理，未调用真实 TikTok/BytePlus。本轮没有数据库结构变更或新增迁移 SQL，没有操作测试环境业务库；尚未提交、推送或部署。截图位于 ../output/playwright/catalog-actions-20261011，日志前缀 /tmp/drama-catalog-actions-。

## 2026-10-11 轮播与热播引用阻止下线

官方下架与直连下线均在权威 MySQL 事务中校验各自渠道的轮播、热播全部引用，包含隐藏条目与历史纯 ID 配置；仍被引用返回 409 / publication_in_recommendations，提示先从当前渠道的轮播或热播移除再下线。拒绝保持线上内容、版本和修订，不自动清理推荐，不因另一渠道的引用阻断。推荐新增与下线共用剧目锁，复核新增引用仍在线，防止候选查询与下线交错；已有失效引用仍可逐项移除。

官方 Worker 发出下架请求前复核，旧排队任务命中门禁暂停为 failed/delist_album，不调用平台或清除线上快照，也不自动重试；移除后人工重试继续原阶段，重试入口同样检查引用。API 与 Worker 需配套更新并重启旧 Worker，已由旧进程发出的平台请求不能追溯撤销。

验证：聚焦 Go 与完整 make verify 通过；8 项隔离 MySQL/Redis 回归通过，覆盖两渠道真实 HTTP 409、隐藏和旧引用、分别移除轮播/热播、其他渠道独立、官方/直连各 8 轮并发、旧排队任务暂停与移除后恢复以及幂等。Admin 完整 verify 通过（437 项组件、19 项部署脚本、lint、类型、构建）；本地 Chrome 在桌面与 390×844 手机视口验证两个下线入口的友好提示及线上状态保留，截图 ../output/playwright/recommendation-offline-20261011。临时服务、浏览器和隔离数据库已清理，无真实平台调用或测试环境业务库操作。不新增 SQL，不改 Mini 或消费者接口路径；尚未提交、推送或部署。日志前缀 /tmp/drama-recommendation-offline-。

## 2026-10-11 官方版本与同步接口修复

官方同步、送审、人工查询、撤回、申诉提交、选定版本、上下架和人工授权/撤权在当前请求内调用平台并保存结果，成功返回 HTTP 200，失败直接返回稳定错误。只有视频登记/转码完成和审核/申诉结论保留后台查询；这些实际结果不会自动送审或切换线上版本。同步步骤失败不自动重放。后台不再领取旧排队记录中的同步写入阶段，人工恢复仍需版本及修订门禁。

复用 tiktok_release_records 保存当前已选版本，原 tiktok_publications 保存冻结内容和审核事实；上下架不创建任务。已选 V1 可在 V2 同步/审核期间独立上下架；重上架只调用 Album status，沿用 V1 内容和分集映射。明确选择审核通过的 V2 才调用 online_version。版本设置已成功而后续上架响应异常时，保存已确认选择，并直接返回本次失败，避免错误恢复 V1 指针。同步/审核回写只维护映射，不写旧消费者指针。

移除了上一版尚未发布的 task_kind 字段方案和对应增量；本轮无 Schema 变更，不需要迁移 SQL。管理 GET/POST /admin/v1/dramas/{dramaID}/official-release 只携带所见 selectedPublicationId 和 list/delist；不携带任务 ID。短剧锁串行保护版本选择、上下架和本渠道轮播/热播推荐写入，包含隐藏及历史 ID 引用。

官方待同步只比较真实 Album 字段，首帧图、时长等本地元数据、空标签格式与文本末尾空白不会误报。批量授权返回 authorizedCount；部分成功不会伪称整体回滚，失败项由运营明确重试。自动应用策略对账不创建队列，不覆盖人工撤权。

本轮只调整 Server 和 Admin，Mini、/api/v1/*、/api/v1/open/* 保持原契约，不涉及数据库结构变更或部署。自动化仅使用隔离 MySQL/Redis 和本地平台替身，不调用真实 TikTok/BytePlus。

补充：历史同步队列与中断请求在详情、列表和待办筛选一致投影为人工恢复；只读不改历史、不解除未知写入检查点。旧上架记录缺失或旧下线失败仍保留已选 V1 入口。

验证：完整 make verify 与隔离 MySQL/Redis 全量集成通过；覆盖请求内返回、V1/V2 隔离、旧库回退、冻结内容/分集映射、并发幂等、失效租约、未知写入保护、同步授权及旧恢复入口的推荐门禁。本地浏览器在 1440×1000 和 390×844 验证 V2 送审、V1 独立下线/重上架、审核通过仍保留 V1、明确更新上线 V2；所有命令 HTTP 200，未启动发布 Worker。临时服务、浏览器及验收数据库已清理。截图位于 ../drama-admin/output/playwright/synchronous-official-20261011，服务端最终日志 /tmp/drama-sync-server-complete.log 与 /tmp/drama-sync-integration-complete.log。

Admin 完整 verify 通过（432 项组件、19 项部署脚本、lint、类型及构建），最终日志 /tmp/drama-sync-admin-complete-2.log。

## 2026-10-13 未知 Album 更新的人工恢复

原更新返回 HTTP 500 且查询仍为原版本时，旧恢复路径只查询，无法完成实际未写入的更新。人工恢复现在复用原任务和冻结内容：先核对匹配的新版本；只有原版本大于 0、最新版本未推进、原版分集完整且未处于审核/申诉中时，才携带原版本号再次更新。平台版本冲突只再核对，绝不推进条件版本；首次同步、缺项查询及 Worker 保持只读。尚未确认返回 409 / album_update_unconfirmed，保留检查点；原 http_500 或版本冲突原因继续进入日志。恢复完成停在待送审，无自动送审或上线。

复现使用 ECS-drama-test 的日志、配置和测试媒资，共新增两个独立官方草稿，均未送审、未上线、未写业务数据库。第一稿初始更新后查询详情暂不可见；第二稿 7696046873194465300 使用原分集 ID 删除第 7/13/26/30/32 集并从 33 集重排至 28 集，成功生成且查询确认 V2。V2 存在后携带原 V1 更新返回官方 22003，没有生成 V3；因此不能把原 TikTok 500 直接归因于删集或重排。生产剧目 310/任务 339 未执行恢复，本轮代码未部署。

服务端聚焦回归及 make verify 通过，覆盖原版本恢复、已写入只读确认、500 后核对成功、22003 后核对、并发内容冲突不覆盖、首次同步及缺项禁止写入、原任务/冻结内容/待送审状态与日志脱敏。本次无 Schema、配置或消费者合同变更，未额外运行数据库集成测试。

复查补充：官方 version 参数的并发保护已对照官方 API Reference；查询恢复移除用旧映射补齐新版本缺项的路径。最新版本必须返回全部分集并逐项一致，否则保持未确认且不写入；完整可见后只读恢复。新增回归覆盖 33→28 新版本缺一集/缺全部集以及 11+29 集查询只返回新增集后再完整可见，避免将已有 ID 误当作内容已写入的证据。
