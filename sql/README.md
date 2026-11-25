# 数据库脚本

## feat/veplayer 开发期一次建表

[feat_veplayer_tables.sql](feat_veplayer_tables.sql) 汇总当前实现需要的 6 张新表：`application_playback_profiles`、`direct_publication_versions`、`direct_publications`、`direct_publication_operations`、`direct_homepage_recommendations`、`direct_category_rankings`。直接创建最终结构，不创建已经移除的共享内容版本表、应用绑定表和异步队列字段，不修改已有 TK 内容、任务、媒资或编辑状态。

这份文件是本轮测试库重建的独立入口，前提是这 6 张表均不存在，旧 `drama_content_versions`、`direct_application_publications` 也已删除。选中目标数据库后执行一次；建表完成后再恢复已备份的应用播放模式和直连首页配置。它替代 `20261009_001_direct_playback.sql`、`20261010_001_remove_application_playback_profile_key.sql`、`20261010_002_content_versions.sql`、`20261010_003_channel_publication_versions.sql`，不要叠加执行这些结构脚本。`20261009_002_direct_menus.sql` 独立管理菜单和权限，已应用的测试库保留原菜单和角色授权。

历史增量及冻结初始化基线保持原样，标准增量升级顺序仍见下文；该独立文件放在 `migrations/` 之外，避免标准升级流程重复建表。

2026-10-10 前轮按明确要求备份并删除测试库 `101.47.11.18:3306/drama_test` 的 8 张开发期新增表（含废弃表）；其余 170 张表、6 部短剧及 16 条 TK 发布任务保留。本轮重构更新了此建表文件，最终使用直连自己的版本表，不再创建共享内容版本表。本轮仅在隔离数据库验证新结构，不在测试业务库执行建表或数据恢复。

本轮隔离 MySQL 回归通过：独立建表文件与基线加全部增量的最终列及注释一致，得到 144 张表/96 个默认权限节点；升级保留已有 TK 快照、任务及编辑状态，源媒资和分集身份仍受引用保护。日志：`/tmp/drama-channel-boundary-db-final.log`。

## 2026年10月 VePlayer 增量

20261009_001_direct_playback.sql 新增 application_playback_profiles、direct_publications、direct_publication_versions、direct_application_publications、direct_publication_operations、direct_homepage_recommendations、direct_category_rankings，共 7 张表。不改写官方快照、首页、用户分片或 UID 水位；无外键/CHECK，全部内部关联使用 BIGINT UNSIGNED。无 profile 的历史应用由代码保持官方。

20261009_002_direct_menus.sql 新增 25 个节点，分开官方与直连目录、菜单及精确操作。脚本可重复执行，保留已有 ID、自定义名称和角色关联；不自动授予旧角色新权限。原首页菜单只在仍处于默认父目录时重挂官方目录，管理员自定义父目录保留。历史应用配置引用操作 code 是 application.playback.update；2026-10-13 起播放器选择复用 applications.update，不再检查此历史节点，无需调整现有角色或执行新 SQL。

执行前确认前序迁移均完成，备份并记录旧配置、应用、角色、权益与 UID 水位。先部署支持新引用的全部清理 Worker，关闭直连，核对旧官方回归后再按 [上线手册](../docs/veplayer-release-runbook.md) 开放。迁移只增加结构，开关关闭也不应删除新表；回滚保留版本引用保护，不恢复旧清理 Worker。

共享版本重构前的隔离 MySQL 8.4 验证记录：空库基线加全增量得到 145 表/96 默认节点，旧库升级保留官方首页、应用、UID 水位和角色关系；权限迁移重复执行不重建 ID、不覆盖自定义名称、不扩大已有授权。直连事务、真实 Handler、并发复制和租约栅栏通过。本次未对业务库执行 SQL；下文历史执行记录仍只代表其记录日期和环境。

`init.sql` 固定为本轮迭代前的初始化基线，面向空数据库，不能覆盖已有业务库。后续结构和数据变更独立保存到 `migrations/`，不再合并回 `init.sql`。新环境先执行基线，再按文件名升序执行全部增量脚本；已有环境只执行尚未应用的增量脚本。

首个超级管理员也由 `init.sql` 的末尾可选段落创建。部署系统可在同一 MySQL Session 预先设置 `@drama_admin_username`、`@drama_admin_password_hash` 和 `@drama_admin_nickname`；三个变量都未设置时不创建管理员。密码明文不得写入 SQL、配置或命令历史。

`upgrade-non-uid-auto-increment.sql` 是基线形成前已有库升级 80 张非 UID 记录分表的一次性脚本，其结果已包含在基线，新库不重复执行。此前历史增量结果已合并至基线，并由 Git 历史保留。从本轮开始，增量脚本使用 `YYYYMMDD_NNN_用途.sql` 命名，按日期和当日序号排序；已发布脚本保持不变，修正另加新脚本。每次发布记录目标环境已执行的文件名及结果；服务启动不自动迁移，也不自动重复执行脚本。

## 全新生产库初始化

1. 创建并选中目标空数据库（当前生产配置为 `tk_drama`），字符集使用 `utf8mb4`。本脚本不创建数据库，也不切换数据库。
2. 如需首个超级管理员，在当前数据库会话设置上述三个变量；密码摘要必须是 bcrypt，不能使用明文密码。通过安全的部署输入提供摘要，不将真实凭据提交仓库。
3. 在同一会话执行 `SOURCE /实际路径/drama-server/sql/init.sql;`。脚本会显式切换连接字符集为 `utf8mb4`，避免中文注释乱码。遇到错误立即停止检查，不能忽略错误继续启动；MySQL DDL 不保证整体回滚，部分导入后的库不再是空库，不能直接重跑。
4. 按文件名升序逐个执行 `migrations/` 下的全部增量脚本，从 `20260930_001_drama_operators.sql` 至 `20261026_001_metric_refresh.sql`；不得漏掉 `20261011_003_merge_direct_publication_menu.sql` 和 `20261020_001_postback_details.sql`。每个文件只执行一次并记录结果，使用 `SOURCE /实际路径/drama-server/sql/migrations/文件名;` 执行，全部完成后启动新版服务。初始化基线包含 122 张基础表（96 张分片表）、67 个系统权限节点；基线加当前全部增量合计 148 张表（112 张分片表）。权限应核对新增及移除的实际节点，不能把全部历史 INSERT 声明数当成最终存量，自定义节点另计。审核记录、上线记录和当前发布任务表已包含，无须补跑基线形成前历史 SQL。
5. 启动服务后，在后台配置应用、BytePlus 媒资及 TikTok 分发凭据；初始化不复制测试环境的应用、短剧或平台密钥。未设置管理员变量时不会生成默认账号。

## 消费者主键与分片关联

消费者用户 UID 由 `id_allocators` 中现有 `consumer_domain` 命名空间永久预留号段，`users_00` 至 `users_15` 的 `id` 保持非自增。`next_id` 表示尚未预留的起点，只能在事务中前移；初始化种子仍为 `1`，默认号段大小仍为 `100000`，已有库不得重新执行种子或降低水位。Redis 保存共享当前段、备用段和消费进度，消耗到 80% 时异步预取；应用重启继续使用共享进度，无需修改表结构或已有 UID。Redis 缓存丢失时舍弃未使用的号段，从 MySQL 当前水位继续预留，不能据用户表最大 ID 回退水位。故障恢复与首次切换说明见 [UID 分配与恢复](../docs/uid-allocation.md)。

以下五组各 16 张物理表（后缀 `00` 至 `15`，共 80 张）的 `id` 使用 `BIGINT UNSIGNED AUTO_INCREMENT`，新建记录由数据库生成主键，不再消耗用户号段：

- `entitlement_grants_XX`：权益授予流水。
- `user_entitlement_state_XX`：权益播放鉴权投影。
- `entitlement_revocations_XX`：权益撤销流水。
- `reward_grants_XX`：奖励结算流水。
- `application_vip_progress_XX`：VIP 广告进度。

自增 ID 只在所属物理表内唯一，不保证跨表或跨分片唯一。记录定位仍需用户逻辑桶及应用、用户上下文，先路由到用户分片，再查询该表；`user_entitlement_state_XX.grant_id` 仅引用相同 `XX` 的 `entitlement_grants_XX.id`。奖励、权益和进度继续在同一用户分片事务内提交，来源和领取幂等键保持原样。

## 已有库的非 UID 记录主键升级

此升级保留现有行及其主键，不迁移用户 UID、不重建表、不清库，也不重置 `consumer_domain` 水位。只有上述 80 张表需要改为物理表自增；不得给 `users_XX.id` 添加 `AUTO_INCREMENT`。应用启动不会自动修改这些表，已有库必须先完成结构升级，再运行省略内部 ID 的新版写入代码。

1. 备份目标库，在隔离副本演练，核对五组分表、`BIGINT UNSIGNED` 单列主键与当前业务索引。记录每表行数、最大 ID、业务关联以及 `users_XX` 和 `id_allocators` 状态；缺表、类型不符或其他结构漂移时先处理差异，不能盲目执行生成结果。
2. 在已选中的目标库运行下方只读查询，结果必须为 80 行且没有空的 `alter_ddl`。查询只生成 DDL 文本，不执行 DDL。人工复核库名、表名、当前属性，以及生成结果与 [`upgrade-non-uid-auto-increment.sql`](upgrade-non-uid-auto-increment.sql) 的 80 条语句是否一致；同时核对 `user_entitlement_state_XX.grant_id` 可以安全调整为 `BIGINT UNSIGNED`。安排维护窗口，停止所有相关写入实例，避免新旧 ID 生成方式混跑。
3. 在维护窗口连接已选中的目标库，逐条执行已审核的 `upgrade-non-uid-auto-increment.sql`；任何失败都停止并检查。脚本不会创建或切换数据库。MySQL DDL 不保证整批回滚，不得把部分完成视为升级成功，也不要不经核对直接重跑完整脚本。给已有主键添加 `AUTO_INCREMENT` 不改写现有 ID，后续数据库分配从该表已有最大值之后继续；不要另行指定或降低自增水位。
4. 核对 80 张表均具备自增属性，逐表行数、最大 ID 及业务关联保持一致，`users_XX` 与 `id_allocators` 完全未变。保持所有旧实例停止，全部 DDL 成功后再部署并启动新版代码；验证奖励重试、权益关联、VIP 进度和事务回滚，再恢复业务写入。若尚有未升级表，不启动新版写入代码。

```sql
WITH
shards AS (
  SELECT 0 AS shard UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3
  UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7
  UNION ALL SELECT 8 UNION ALL SELECT 9 UNION ALL SELECT 10 UNION ALL SELECT 11
  UNION ALL SELECT 12 UNION ALL SELECT 13 UNION ALL SELECT 14 UNION ALL SELECT 15
),
families AS (
  SELECT 'entitlement_grants_' AS prefix, '物理分表内自增的权益授予流水标识' AS id_comment
  UNION ALL SELECT 'user_entitlement_state_', '物理分表内自增的权益投影记录标识'
  UNION ALL SELECT 'entitlement_revocations_', '物理分表内自增的权益撤销流水标识'
  UNION ALL SELECT 'reward_grants_', '物理分表内自增的奖励流水标识'
  UNION ALL SELECT 'application_vip_progress_', '物理分表内自增的 VIP 进度记录标识'
),
targets AS (
  SELECT CONCAT(prefix, LPAD(shard, 2, '0')) AS table_name, prefix, id_comment
  FROM families CROSS JOIN shards
)
SELECT DATABASE() AS database_name, targets.table_name,
       columns_info.COLUMN_TYPE AS current_type,
       columns_info.COLUMN_KEY AS current_key,
       columns_info.EXTRA AS current_extra,
       CASE WHEN tables_info.TABLE_TYPE = 'BASE TABLE'
                  AND columns_info.DATA_TYPE = 'bigint'
                  AND columns_info.COLUMN_TYPE LIKE '%unsigned%'
                  AND columns_info.COLUMN_KEY = 'PRI'
            THEN CONCAT(
              'ALTER TABLE `', targets.table_name,
              '` MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT ',
              QUOTE(targets.id_comment),
              CASE WHEN targets.prefix = 'user_entitlement_state_' THEN CONCAT(
                ', MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT ',
                QUOTE('同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id')
              ) ELSE '' END,
              ';'
            )
            ELSE NULL END AS alter_ddl
FROM targets
LEFT JOIN information_schema.TABLES AS tables_info
  ON tables_info.TABLE_SCHEMA = DATABASE() AND tables_info.TABLE_NAME = targets.table_name
LEFT JOIN information_schema.COLUMNS AS columns_info
  ON columns_info.TABLE_SCHEMA = DATABASE() AND columns_info.TABLE_NAME = targets.table_name
 AND columns_info.COLUMN_NAME = 'id'
ORDER BY targets.table_name;
```

应用启动不会初始化或升级基础表，也不会记录 Schema 版本。统计日期表是例外：运行服务按初始化脚本中的模板自动创建日期表，并清理过期日期表。因此运行账号除业务库的增删改查权限外，还需要该库范围内的 `CREATE`、`DROP` 权限，不需授予全局权限。生产库结构变更仍需独立审批和维护窗口。

生产配置的 `app.env` 必须是 `production`（部署命令参数仍为 `prod`）。MySQL、Redis 连接使用已填写的生产连接；上线前另行确认数据库 TLS 证书、两项独立的生产身份密钥、后台与 API 域名及 HTTPS 证书。测试身份密钥不得直接复用到生产。仅执行初始化脚本不代表这些部署配置已完成。

## VisitorToken 版本字段升级

新版 VisitorToken 包含签发、过期时间与用户认证版本。已有库先给 `users_00` 至 `users_15` 添加 `auth_version`；默认值 1 保留现有用户与 UID，不清表、不重置号段。维护窗口内停止旧版写入实例、执行并核对全部 16 张表后，再统一启动新版实例。旧 VisitorToken 因载荷版本变化会收到 `401 unauthorized`，Mini 使用 TikTok 静默登录恢复同一账号，再以原 ClaimID 重试待领取请求。若平台静默登录不可用，保留原待领取记录，不以匿名账号替代。

在目标库核对表名后，执行以下语句生成 16 条 DDL，复核结果后逐条执行；应用启动不自动改表：

```sql
WITH RECURSIVE shards AS (
  SELECT 0 AS shard
  UNION ALL
  SELECT shard + 1 FROM shards WHERE shard < 15
)
SELECT CONCAT('ALTER TABLE `users_', LPAD(shard, 2, '0'),
  '` ADD COLUMN `auth_version` BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT ',
  QUOTE('访问凭据版本，递增后旧 VisitorToken 失效'),
  ' AFTER `open_id_hash`;') AS alter_ddl
FROM shards;
```

完成后核对 `information_schema.COLUMNS` 中 16 个 `users_XX.auth_version` 均为非空且默认 1；同一用户需要强制撤销旧凭据时，仅递增其所在分片行的该字段，并确保操作明确限定 `bucket_id`、`application_id` 和 `id`。正常过期无需修改数据库，凭据绝对有效期默认为两小时。

## 媒资巡检检查点升级

新版后台 Worker 使用单行 MySQL 检查点领取一部剧、逐集观察并在完成后推进游标；实例崩溃后租约过期会重做未完成的剧。已有库需在启动新版 Worker 前添加 `media_reconciliation_checkpoint` 表，DDL 与 `sql/init.sql` 中同名建表语句完全一致。无需迁移既有媒资任务，首轮领取时自动插入 `id=1` 检查点。此表是可恢复的巡检进度，不保存媒资业务状态；不得手工删除正在持有租约的行。

权益写入已改由 `users_XX` 行的 MySQL 事务锁串行化。部署时暂停奖励、权益和 VIP 补发写入，待旧版 Redis 锁实例全部退出后再启动新版，避免新旧实例使用不同互斥机制并行写同一用户。Redis 故障不再使这些 MySQL 权威写入停摆。

## 清理测试用户数据

使用 [cleanup-test-user-data.sql](../docs/cleanup-test-user-data.sql)，仅用于已确认的测试库。它独立于部署初始化脚本。默认库名核对值为 `drama_test`，`@drama_cleanup_apply = 0` 时只输出清理表和 SQL；确认清理范围后改为 `1`，使用新的独立连接执行完整文件。

- 清空六组各 16 张用户、奖励、VIP 进度和权益分表，以及指标明细、去重键、汇总及刷新状态。已应用通用展示增量时，同时清空完整 16 张 `ad_shows_XX`；只有部分展示分表时拒绝清理。日期分表按实际存在的表发现；历史未分片 `users`、`reward_grants`、`application_vip_progress` 存在时也清空。
- 保留短剧、分集、媒资、发布、审核、上线、应用授权、标签、轮播、热播和分类榜；保留管理员、权限、管理审计、应用、投放配置、解锁策略、全局配置与 UID 发号水位。
- 脚本使用 InnoDB 事务内的 `DELETE`，不重置自增值、不改变业务表结构；任一删除失败会回滚。预览行数是元数据估算，执行后另返回实际删除行数。
- 执行前备份待清数据，并暂停测试环境 API 和 Worker 写入。脚本需要创建和删除一次性存储过程及临时表的权限；中断遗留的同名过程按文件末尾说明处理。
- 不清理 Redis，不回退 `id_allocators.next_id`。清理后测试设备重新启动并登录；如需重置本地待领取和历史等数据，在测试设备上另行清除。

## 短剧操作人字段升级

短剧表新增可空的 `created_by`、`updated_by` 管理员主键关联字段，不使用外键。首次创建写入创建人及最新操作人，人工内容编辑、协作修改、媒资上传任务提交与重试、审核/上线/下架和分发配置在同一业务事务更新最新操作人。上传授权查询及诊断记录不更新操作人，Worker 和官方回调不会覆盖人工身份。列表关联管理员当前昵称或用户名，账号已删除时保留 ID；历史身份未知时显示“—”。

已有库先备份、在隔离副本演练，停止管理端写入后执行一次 [20260930_001_drama_operators.sql](migrations/20260930_001_drama_operators.sql)，核对两个字段，再部署新版 API。DDL 不保证整体回滚，部分完成时先检查实际结构，不直接重跑。脚本只新增短剧字段，历史记录保持空值，原有 `updated_at` 不变；审计日志结构与数据保持不变。新库执行 `init.sql` 后也须执行该增量脚本；已有库只执行尚未应用的增量，不重跑初始化基线。

本轮已在一次性 MySQL 8.4 数据库验证基线加增量的新库初始化、仅执行增量的旧库升级、长 ID 及人员字段读写、事务回滚、自动任务不覆盖人工身份，以及最终列集合和中文注释。升级测试确认历史身份及更新时间保持不变，审计表结构不变；尚未在测试或生产业务库执行该增量。

## 剧目播放与解锁报表菜单

`20261001_001_drama_report_menu.sql` 在已有运营数据目录下新增菜单及独立 `operations.drama_report.read` 操作节点，不改业务表、不自动给已有角色授权。前置条件是已执行初始化基线及先前增量，且 `directory.operations` 存在。执行后确认新增两个节点分别位于运营数据目录及报表菜单下；基线及增量合计 69 个默认权限节点。普通角色需显式勾选菜单和查看操作，刷新会话后生效。播放指标尚无剧集归因，报表仅准确统计现有广告奖励。

验证结果：隔离 MySQL 的 `TestDramaReportIntegration` 验证了新库加载、菜单重复执行不新增节点且保留自定义名称、分页及跨应用去重、排序空值末尾、日期边界和接口权限；当前 Schema 集成测试通过。

该增量已在 VIP 多用户模拟使用的一次性 MySQL 数据库中随基线及全部增量执行成功。父节点查询使用 `parent.id`，幂等更新使用 `admin_permissions.id`，避免 MySQL 对 `id` 的歧义解析；未在现有测试或生产业务库执行该增量。


### 投放链接奖励贡献菜单

`20261001_002_campaign_report_menu.sql` 在运营数据目录下新增 `menu.campaign_report` 菜单及 `operations.campaign_report.read` 查看操作，不修改业务表，不自动给已有角色授权。先执行基线及前序增量，执行后默认权限节点合计 71 个。普通角色需勾选菜单及查看操作并刷新会话。可重复执行，保留自定义菜单名称。报表只统计成功广告奖励账本，逐集与 VIP 奖励都纳入，包含停用及零奖励链接。

验证记录：隔离 MySQL 集成测试已通过，覆盖菜单重复执行及自定义名称保留、逐集/VIP 合计和人数去重、跨应用脏归因排除、日期边界、零奖励和停用链接、搜索/分页/排序及独立接口权限；权限节点清单验证为 71 个。

## 2026-10-05 测试环境执行记录

北京时间 2026-10-05 18:43，在 `101.47.11.18:3306/drama_test` 按顺序执行 `20261001_001_drama_report_menu.sql`、`20261001_002_campaign_report_menu.sql`，同一事务提交成功。权限节点从 67 个增加到 71 个；独立连接复查新增四个菜单/操作节点均已启用，父子层级和路由正确。已有角色授权未变。原始执行结果保存在工作区 `test-sql-applied-20261005.json`。

## 通用广告展示事实

[20261008_001_ad_shows.sql](migrations/20261008_001_ad_shows.sql) 新增 16 张按用户桶路由的 `ad_shows_XX`。表只保存主体、事件幂等键、场景、广告位、可选剧目／剧集和 UTC 发生时间，无 `context_json`、策略分组、Offer 或 N。自增主键不占用用户 UID 号段；唯一键按桶、应用、用户和事件隔离，两个普通组合索引支持场景每日与剧目每日查询。首页等场景的剧目／剧集为空，可直接复用表和通用上报／查询 API，无需奖励规则或新增业务列。

已有库按顺序执行尚未应用的增量，核对完整 16 张表及索引后再启动新版 API，然后发布管理端和 Mini。此增量尚未发布，替代本轮入剧专用表草稿，不需要兼容旧入剧展示数据。执行一次即可；部分失败时检查实际表结构，不能直接重跑。不改冻结基线、已有奖励／VIP／权益表，不增加外键。详细合同见 [入剧广告与通用展示事实](../docs/entry-ad-contract.md)。

隔离 MySQL 8.4 的 `TestAdShowFactsUpgradeIntegration`、`TestAdShowCleanupIntegration` 验证升级前奖励保留、并发幂等与冲突拒绝、无内容首页场景、主体／场景／剧目／日期隔离及清理完整性。没有在业务测试库或生产库执行此增量。

## 删除应用级播放配置引用

[20261010_001_remove_application_playback_profile_key.sql](migrations/20261010_001_remove_application_playback_profile_key.sql) 在全部前序增量完成后删除 application_playback_profiles.playback_profile_key，保留 provider、revision、应用和发布关系。当前版本的播放 Token 有效期使用配置文件 `business.direct_playback_token_ttl_seconds`（60～900 秒），SDK AppID 与 License 由 Mini 随包提供。`playback.default` 已不参与运行；本次运行参数调整无需新增 SQL，历史增量保持原样。

停止旧版 API 和 Worker 后执行一次增量，再部署新版 Server 与 Admin；旧代码仍依赖已删除的列，不能与新版混跑。SQL 不修改全局配置，也不重置模式或修订；已发布的直连建表脚本保持不变。上一轮按要求跳过验证；本轮共享内容版本重构已在隔离 MySQL 覆盖此增量及升级回归，未在业务数据库执行 SQL。


## 渠道独立发布版本

[20261010_002_content_versions.sql](migrations/20261010_002_content_versions.sql) 保留为前轮结构增量。[20261010_003_channel_publication_versions.sql](migrations/20261010_003_channel_publication_versions.sql) 紧随其后，删除开发期共享版本表，建立 `direct_publication_versions`，并清除尚未上线的直连发布指针和操作记录。标准增量路径执行两份；已删除全部 VePlayer 新表的测试库改用上文独立建表文件，不叠加这些结构增量。

短剧管理不保存或查询版本，待上线只表示内容就绪。直连上架事务冻结内容并维护剧内直连版本号；TK 继续使用已有 `platformVersion` 与任务中的内容快照，不新增 TK 版本表，不回填历史内容，也不改写已有上线快照、审核任务和编辑状态。各渠道历史快照持续保护媒资及分集身份。最终 Schema 为 144 张表，无外键及 CHECK，权限节点保持 96 个。

停止旧 API 与 Worker 后执行尚未应用的增量，再部署配套 Server/Admin；应用启动不自动迁移。开发期直连结构直接替换，不增加运行时兼容分支。本轮不执行业务库 SQL。


## 简化直连运营入口

[20261011_001_simplify_direct_operations.sql](migrations/20261011_001_simplify_direct_operations.sql) 在全部前序增量之后执行，也适用于已独立建表的测试库。配套发布新版 Server 与 Admin，刷新管理会话导航；应用启动不自动执行 SQL。历史增量及冻结基线保持原样，不重跑旧菜单增量。

脚本在一个事务中把已显式分配的直连上线记录菜单授权合并到现有直连发布菜单，不新增上线、下线或栏目修改权限；未分配该菜单的角色不增加授权。自定义子节点移到直连发布菜单，保留主键、名称及角色关联。移除 `menu.direct_releases`、`operations.direct_home.copy` 及其角色关联，默认节点变为 94 个。脚本可重复执行。

不改短剧、TK 快照/任务、直连当前指针、版本/操作历史、应用、权益或任何首页配置。管理端列表直接上下线，直连二级页面和首页预览、复制接口均不再提供。隔离 MySQL 验证已通过，测试业务库执行情况见下方记录；生产库尚未执行。

## 2026-10-11 测试环境执行记录（直连入口简化）

北京时间 2026-10-11 12:04，在 `101.47.11.18:3306/drama_test` 执行 `20261011_001_simplify_direct_operations.sql`，单事务提交成功。执行前确认保留的 `menu.direct_publications` 已启用，权限和角色关联表均使用 InnoDB；不重跑初始化基线、历史增量或直连独立建表文件。配套 Admin、Server 功能代码已从 `feat/veplayer` 快进合入远端 `test`；此记录不代表容器已经部署。

独立连接复查：权限节点从 96 个变为 94 个，`menu.direct_releases`、`operations.direct_home.copy` 已移除，角色授权仍为 28 条，权限父节点及角色授权均无悬空关联。本次没有需要转移的自定义子节点，也没有旧上线记录菜单的显式角色授权。业务表总数仍为 177；22 张受保护表的行数及内容摘要保持一致，包括 6 部短剧、160 集、16 条 TK 发布任务，以及各 3 条直连当前发布、发布版本和操作记录；官方与直连的轮播、热播、分类配置、应用播放模式及 UID 水位均保留。

按本次用户要求不保留备份，仅保留执行与摘要核对记录：工作区 `artifacts/test-merge-migration-20261011/migration-report.json`。管理端部署新版后刷新会话导航。


## 2026-10-11：官方审核历史收回发布详情

[20261011_002_remove_official_review_menu.sql](migrations/20261011_002_remove_official_review_menu.sql) 在前序菜单增量之后执行；已经独立创建直连表的库也需要执行此菜单增量。默认删除 `menu.review_records` 及其角色关联，当前默认权限节点为 93 个。不会把原审核菜单授权改为官方发布授权，不扩展角色操作权限。已有官方发布菜单和 `content.drama.read` 的管理员从“发布详情 → 审核记录”查看该剧历史。仅有旧审核菜单的角色须由管理员显式分配官方发布菜单后才能使用新入口。

若旧节点含管理员自定义子节点，保留原节点主键及启停状态，改成无页面地址的目录容器，保留自定义子树和它们的角色关联；不搬迁子节点，不使停用子树重新生效。默认库无此容器，自定义容器另计。脚本可重复执行。审核与上线记录、发布任务、快照和 Mini 接口均保留。新版 Server 阻止再次绑定 `/dramas/reviews` 或旧 `reviewRecords` 菜单，Admin 忽略旧会话中的该菜单，旧全局地址返回官方发布列表，带短剧标识的历史地址继续可刷新。

隔离 MySQL 验证已通过：无子节点、启用自定义子树、停用自定义子树三个场景，重复执行、历史 JSON 保留、角色不扩权和关联完整性；当前 Schema 与旧直连升级回归通过。日志 `/tmp/drama-review-menu-db-20261011.log`。测试业务库执行情况见下方记录；生产库尚未执行。

## 2026-10-11 测试环境执行记录（移除独立官方审核菜单）

北京时间 2026-10-11 17:34:39，在 `101.47.11.18:3306/drama_test` 执行 `20261011_002_remove_official_review_menu.sql`，单事务提交成功。执行前确认前序直连菜单清理已完成、官方发布菜单已启用，权限和角色关联表均使用 InnoDB；只执行该增量，不重跑初始化基线或历史迁移。配套 Admin、Server 功能代码已推送 `feat/veplayer` 并快进合入远端 `test`，此记录不代表服务容器已经部署。

独立连接复查：`menu.review_records` 已删除，权限节点由 94 个变为 93 个，角色授权仍为 28 条，权限父节点及角色授权均无悬空关联。本次旧菜单没有自定义子节点或显式角色授权，实际仅删除 1 条菜单记录，没有转换目录或修改角色授权。

事务内 22 张受保护业务表的行数及内容摘要均一致，数据库表数仍为 177。提交后的独立连接核对发现 `dramas`、`tiktok_publications`、`tiktok_release_records`、`category_rankings` 的内容摘要发生并发变化，行数均未变；其余 18 张受保护表（包括审核历史、直连版本和操作历史、应用配置及 UID 水位）摘要保持一致。本次 SQL 仅操作 `admin_permissions`、`admin_role_permissions`，没有业务表写入语句；并发变化来源未追溯，不将事务内一致性结论扩大为执行期间整个业务库静止。

按用户要求未保留备份，仅保留连接身份、影响行数及摘要核对报告：工作区 `artifacts/official-review-menu-migration-20261011/migration-report.json`。管理端使用配套新版后刷新会话导航，审核历史从官方发布详情进入。


## 2026-10-11：直连发布合并至短剧管理

[20261011_003_merge_direct_publication_menu.sql](migrations/20261011_003_merge_direct_publication_menu.sql) 在全部前序迁移后执行，前置为已有 menu.dramas。默认删除 menu.direct_publications，默认节点从 93 个变为 92 个；操作节点移到短剧管理，保留主键、角色关联及自定义子树。旧菜单祖先停用时，移动的子树根继续停用。只有已获 content.drama.read 的旧菜单使用者转移菜单入口授权，不授予任何新的编辑或发布权限。

本轮不新增业务列，不改任一渠道发布快照、修订、版本、任务或首页配置；ready 字段与旧接口仅保留兼容，发布校验直接读取当前保存内容。转码选项写入既有 workflow_json，恢复原直连版本使用既有版本指针。

隔离 MySQL 8.4 验证通过：新库全增量、重复执行、活动与停用祖先、自定义子节点及授权保留、无内容读取权限角色不扩权、发布记录不变。该增量尚未在测试环境或生产业务库执行，部署时须配套新 Server 与 Admin。

## 2026-10-20：TikTok 回传参数诊断

在全部前序增量后执行 [20261020_001_postback_details.sql](migrations/20261020_001_postback_details.sql)。仅新增七天保留的脱敏诊断表，不修改已有日/月分表、应用、发布、奖励、UID 水位或 RBAC。当前基础表数量为 145，权限节点保持 96。

先执行增量并部署 Server，再部署 Admin、最后上传新版 Mini；旧 Mini 的纯计数格式持续兼容。详情仅保存参数白名单、固定场景、构建修订、耗时及脱敏错误，真实 OpenID 不落库；按原维护入口清理，计数仍保留 90 天。详见 [回传诊断合同](../docs/tiktok-postback-diagnostics.md)。隔离 MySQL 8.4 已验证旧库升级、历史事实保留、幂等、阶段合并、应用隔离、事务回滚和七天清理；尚未在测试或生产业务库执行。

## 2026-10-21：独立广告统计菜单

在全部前序增量（包括 `20261020_001_postback_details.sql`）完成后执行一次 [20261021_001_ad_analytics.sql](migrations/20261021_001_ad_analytics.sql)。新增 `metric_ad_events` 九十日广告阶段事实表，及“运营数据 → 广告统计”菜单 `menu.ad_analytics`、独立操作 `operations.ad_analytics.read`。最终基础表数量为 146，本增量增加两个权限节点；不改冻结基线，不增加外键、CHECK 或用户 UID 分配。

按 SQL → Server/维护进程 → Admin → Mini 顺序部署，应用启动不迁移。已有角色不会自动扩权，普通角色需显式分配菜单及查看操作，刷新管理会话。新 Mini 不能早于新版 Server 上线。创建表的 DDL 只执行一次，部分失败先核对实际结构；菜单插入保留现有节点 ID 和自定义名称。

沿用现有 `/api/v1/metrics`，旧纯计数及回传格式继续兼容。广告阶段按应用、认证用户、调用 ID、kind 幂等，与计数同事务；只保留数值错误码及有限维度，不收集 SDK 原始对象、凭据、错误文本或媒体地址。这是客户端请求/展示观测，不是平台计费曝光报表。完整口径、API、能力检测与回滚见 [广告统计合同](../docs/ad-analytics.md)。

隔离 MySQL 8.4 已验证旧库升级及历史数据保留、重复阶段、迟到回调、展示后失败、分位数、事务回滚、九十日清理、认证应用与独立读取权限。尚未在测试或生产业务库执行此脚本。

## 2026-10-26：统计刷新租约与精确日活

完成全部前序增量后执行一次 [20261026_001_metric_refresh.sql](migrations/20261026_001_metric_refresh.sql)。为 `metric_summary_state` 增加刷新持有者、递增代际、租约截止时间与统计窗口时间；保留原刷新时间和首次采集时间，初始窗口时间从原刷新时间复制。新增 `metric_active_users_daily` 精确日活集合和 `metric_summary_days` 日修订状态，最终基础表为 148 张。无外键、CHECK、UID 水位或客户端协议变更；冻结基线保持原样。

部署顺序：停止旧版 Server 中的统计刷新与维护任务，备份并执行尚未应用的增量，核对结构，再统一更新 API 和 Worker。每个 Server 实例可能运行维护循环，因此须覆盖全部旧实例，避免旧长事务刷新与新版发布协议混跑。新旧二进制切换期间可短暂停服；不需要更新 Mini 或 Admin。应用启动不执行迁移；DDL 只执行一次，部分失败时先检查实际结构，不能直接重跑。

首次刷新从保留窗口原始日表补齐日活，可能比普通增量刷新耗时；已有快照仍可读取。之后新事实、精确日活与日修订同事务提交；只回填尚未初始化的日期，已初始化的今日、昨日与历史日均不再重复去重。普通刷新跨 UTC 日也按修订选取行为事件日，首次初始化或显式重建才全窗扫描，保留迟到完播回补和精确三十日去重口径；完成请求先到时，窗口移动通过过期日期索引定向校正受影响的开始日。账本与 MAU 仍独立保留每日全窗校正。本次优化复用既有表和索引，无新增迁移；所有 API 与 Worker 必须统一使用维护日活及日修订的当前写入逻辑。日活、日修订与原始事实按九十日窗口清理，历史月汇总保留。

Redis 锁前缀为 `drama:dashboard:refresh:`，后接数据库身份摘要和应用 ID；TTL 三十秒，每十秒核对持有者续期，释放也核对持有者。Redis 仅减少重复工作，数据库短租约及发布代际负责防止旧任务覆盖。Redis 不可用时使用数据库短租约，清空此类键不会丢统计事实；不得清理 UID 发号键。重查询走独立统计池，租约与发布走业务池短事务；统计池仍连接同一逻辑主库，不因分池隔离数据库 CPU/IO。

部署后检查 Worker 实际运行、刷新错误日志、`refreshed_at` 推进、租约完成后释放、日修订最终追平及仪表盘数值。有刷新仍在进行时维护任务跳过九十日清理；没有 Worker 时已有快照不会随页面查询更新。回滚二进制前同样停止全部新版刷新任务，保留新增表与列，不以删除结构或回退 UID 水位作为回滚手段。

`make verify` 与隔离 MySQL/Redis 的 `make test-integration-local` 均通过，覆盖旧库升级保留历史事实、精确 MAU 与原 SQL 对照、并发幂等及修订发布竞争、计算期状态行可立即加锁、Redis 失效接管后旧任务发布被拒绝、Redis 故障降级、过期事实清理保护及统计连接隔离。同步校正旧迁移测试的权限总数断言，以及旧客户端时间的展示事实断言，未改变对应生产逻辑。未在测试业务库或生产数据库执行本迁移。

## 2026-10-26：行为聚合 SQL 与覆盖索引

本轮直接改写每次行为聚合的 SQL：先按事件类型统计次数与首次接收时间，只在少量类型行上汇总各项指标；完成统计单独限定 `play_start`、`rewarded_show`，按完整主键查找完成事实。完成仍归属开始／展示日，完成键仍限定当前保留窗口，日活仍从精确集合计算。新增 `idx_metrics_app_kind_received(application_id,kind,received_at)`，既支持按类型顺序聚合，也支持只读取开始／展示类型的范围；InnoDB 二级索引已经隐含主键中的用户与事件标识，不重复增加这些列。

新库执行冻结基线与全部有序增量，包含 [20261026_002_metric_behavior_index.sql](migrations/20261026_002_metric_behavior_index.sql)，随后创建的事件日表自动继承索引。既有库按以下顺序升级：

1. 暂停全部旧实例的统计维护任务，保留已有快照读取；先完成所有前序增量，再执行一次 `20261026_002_metric_behavior_index.sql`，为模板增加索引。
2. 在同一个目标业务库执行 [upgrade-metric-behavior-index.sql](upgrade-metric-behavior-index.sql)，为模板及所有现有 `metric_events_YYYYMMDD` 日表补齐索引。脚本使用 mysql 客户端的 `DELIMITER`，需有 ALTER、创建／执行及删除临时过程的权限。表名取自当前库并严格限定日期表名；不会操作其他业务表、事实数据、去重键、汇总或 UID 水位。
3. 核对下面的查询返回零行，确认所有现存日表具备目标索引，再发布全部 API 和 Worker 并恢复维护。新版 SQL 使用 `FORCE INDEX`，索引未补齐时禁止先发布代码。

```sql
SELECT t.table_name,
       GROUP_CONCAT(s.column_name ORDER BY s.seq_in_index SEPARATOR ',') AS index_columns
FROM information_schema.tables AS t
LEFT JOIN information_schema.statistics AS s
  ON s.table_schema=t.table_schema AND s.table_name=t.table_name
 AND s.index_name='idx_metrics_app_kind_received'
WHERE t.table_schema=DATABASE()
  AND (t.table_name='metric_events_template' OR t.table_name REGEXP '^metric_events_[0-9]{8}$')
GROUP BY t.table_name
HAVING index_columns IS NULL OR index_columns<>'application_id,kind,received_at'
    OR SUM(s.sub_part IS NOT NULL OR s.collation<>'A' OR s.non_unique<>1 OR s.is_visible<>'YES' OR s.index_type<>'BTREE')>0;
```

模板增量只执行一次，已有日表升级脚本可以重跑；同名索引的列顺序、前缀、唯一性、可见性或索引类型不符时明确失败，并返回表名、实际列顺序及异常索引部分数量，不将其视为完成。在线建索引使用 `ALGORITHM=INPLACE, LOCK=NONE`，DDL 不能用事务回滚，仍会消耗索引空间与建索引 IO；开始和结束阶段仍需元数据锁，须等待已有刷新事务结束。部分失败后检查错误，再重跑日表升级脚本。保留已有时间和接收时间索引，其他查询仍使用它们；本轮不改变 API 合同，也不在应用启动或刷新路径自动执行 ALTER。

验证入口：`go test ./internal/data/persistence/mysqlstore -run '^Test(DashboardBehavior|MetricBehaviorIndex)' -count=1 -v`。指定隔离 `TEST_MYSQL_DSN` 后覆盖已有日表保留事实、模板继承、重复升级、同名异构索引拒绝、全部计数与原 SQL 对照，以及跨月、身份隔离、窗口边界和两日三十二万条事实的交替性能对照；执行计划必须使用覆盖索引、完成类型范围和完成键唯一主键。另执行 `make verify` 与 `make test-integration-local`。

本机隔离 MySQL 8.4 的聚焦用例、`make verify` 和 `make test-integration-local` 均通过。两日日表共 320000 条混合事实，目标应用 256000 条；在同一数据集和索引结构上交替执行五次，原联表与类型预聚合的中位耗时分别为 198.26 ms、116.98 ms，降低约 41%，所有计数及首次接收时间一致。该结果只描述本机样本的 SQL 执行，不代表业务环境的耗时。浏览器及百万用户压力用例按既有集成入口跳过；本轮未在测试业务库或生产数据库执行新增索引 SQL。

后续脚本复查补齐了不可见索引及索引类型校验。索引专项集成与 `make verify` 重新通过；另用 MySQL 8.4 自带 mysql 客户端整份执行含 `DELIMITER` 的实际文件，验证首次执行、重复执行、遇错停止、修正异常后重跑及成功后删除临时过程。两个目标日表与模板获得索引，相似备份表名未被修改，全部原始事实字段保留。本轮复查未重复上一轮整套集成测试，也未连接业务库执行该脚本。


## 2026-11-02：广告内部码

在全部前序已发布增量后执行一次 [20261102_002_ad_inner_error.sql](migrations/20261102_002_ad_inner_error.sql)，向现有 `metric_ad_events` 添加 `inner_error_code VARCHAR(12) NOT NULL DEFAULT ''`。既有主码、子码不改写，历史内部码未知则保留空值。没有新增表、索引、外键或 CHECK。

未上线的 `20261102_001_ad_diagnostics.sql` 已移除，不执行该文件；不存在对此文件的依赖。失败上下文仅进入服务端日志，领域事件和统计表没有 diagnostics 列。保留 002 文件名便于辨认此次内部码变更。

按 SQL → Server/维护进程 → Admin → Mini 发布，应用不自动迁移。验证使用隔离 MySQL，覆盖历史错误保留、内部码往返、11006/50008 归并筛选、幂等、应用隔离与 HTTP 采集；没有对业务库执行此 SQL。诊断合同见 [广告统计](../docs/ad-analytics.md)。


## 2026-11-05：主体隔离统一升级

全部前序增量之后执行一次 [20261105_001_subjects.sql](migrations/20261105_001_subjects.sql)。原四份主体脚本已合并为这一份：创建主体表并插入 `id=1`，既有应用、短剧、媒资任务、官方及直连发布和首页运营记录通过 `subject_id NOT NULL DEFAULT 1` 归属默认主体；相关唯一键增加主体维度，新主体从空内容开始。

原剧集主键、VID、冻结版本、线上指针、幂等记录、JSON、时间、用户凭据及权益引用保持不变。官方线上快照复制到 `tiktok_drama_states`，发布方配置和 VOD 空间复制给默认主体；`global_configs` 的全部记录及已有字段值保留，只补充缺失的媒体账号字段。原 `dramas.published_snapshot`、`dramas.platform_album_id` 和 `episodes.platform_episode_id` 不删除，只将列注释标记为已废弃，运行时不再读写原列。新版上传只读取当前主体空间，没有旧 ID 映射或内容拆分步骤。

先备份并停止旧 API/Worker，再执行脚本并配套更新 Server/Admin；应用启动不自动迁移。已经执行过原四份主体增量的库不重跑合并脚本。管理员、RBAC、应用身份、广告、奖励及权益边界不变；普通角色不会自动获得新增主体权限。没有新增外键或 CHECK，不调用真实平台，不清空 UID 缓存。

验证覆盖完整新库结构、真实单主体旧库升级、原 ID/时间/JSON/版本和旧 Token 保留、无主体或跨主体读取拒绝、新主体空目录及独立运营配置。具体合同见 [主体升级与验收](../docs/subjects.md)。

2026-11-06 测试库执行记录：按用户授权及“不要备份”的明确要求，在 `101.47.11.18:3306/drama_test` 核对已有部分升级结构后，仅补缺项，未重跑本脚本。执行 21 条 DDL，补齐短剧/媒资任务归属、主体 VOD 空间/生成列和 3 个索引、3 个废弃列及 11 张既有表的归属默认值；最终 13 张表的归属字段均为 `NOT NULL DEFAULT 1`。10 部存量短剧归主体 1，发布历史的原主体归属不变；默认主体空间取自保留的全局配置，废弃列从现存官方状态恢复可找回的值（含 139 条分集平台标识）。17 张表共 326 行既有字段的 SHA-256 执行前后一致，`global_configs` 两条记录未改写，原 178 条分集保留。新连接复查结构和当前源码的媒资配置校验通过；未部署服务或调用平台。

## 2026-11-06：共享 VOD 空间

[20261106_001_shared_vod_space.sql](migrations/20261106_001_shared_vod_space.sql) 在主体统一升级之后执行一次，移除主体空间独占唯一键，原 `vod_space_name` 和生成列 `vod_space_key` 保留原值、标记废弃。所有主体的新上传和新官方发布读取全局 `media.space_name`；直接沿用已有全局空间，不改写配置值与更新时间，不删除 `global_configs` 记录。历史素材、冻结版本与任务的空间保持原样，逻辑归属、权限和上传任务仍按主体隔离，无拆分或旧 ID 映射。

2026-11-06 已在测试库 `101.47.11.18:3306/drama_test` 执行此增量，按用户要求未做备份。空间独占索引已移除，两个主体空间列均标记废弃，原主体及 `global_configs` 共 4 行的既有字段摘要不变。原全局空间存在，当前源码媒资校验通过；未部署服务。


## 2026-11-06：Mini 版本审核与首页加白

全部前序增量之后执行一次 [20261106_001_mini_review.sql](migrations/20261106_001_mini_review.sql)。创建 `application_review_versions`，按应用与版本唯一保存审核开关；创建 `review_content_lists`，按发布主体和播放渠道分别保存共享内容清单及并发修订。历史未配置应用缺省关闭，不向 `applications` 增加审核字段，不改写应用身份、解锁策略、用户权益或现有首页运营配置。应用启动不自动迁移，没有新增外键或 CHECK。

同一脚本分别新增官方和直连发布下的「审核列表」及各自独立查看、修改权限，不给已有普通角色自动授权。按 SQL → Server → Admin → Mini 发布，网关允许 `X-Mini-Version` 请求头。后台先保存目标应用清单，再手填审核版本号并开启开关。详细合同见 [Mini 审核加白](../docs/mini-review.md)。

验证使用独立临时 MySQL 和 Redis，不连接业务库；覆盖真实 HTTP 配置读写、版本与应用隔离、独立审核首页和原首页接口不变、搜索和直达不变、IAA 策略覆盖与恢复、清单和应用设置共享修订、旧游标拒绝，以及清单独立权限。

审核增量尚未合入 main，按用户要求直接修订建表方案，移除先前新增应用审核 JSON 列的 SQL，不保留废弃标记。2026-11-07 只读核对测试库尚无审核 JSON 列、两张审核表及对应权限节点，可直接执行最新脚本。


## 2026-11-07 审核开关独立菜单

在 mini_review 增量之后执行 [20261107_001_review_settings_menu.sql](migrations/20261107_001_review_settings_menu.sql)。仅新增「加白管理 → 审核开关」菜单及独立读写操作，不修改业务表、已有审核配置或普通角色授权；角色需明确分配菜单和所需操作。

2026-11-07 11:03（Asia/Shanghai）已在 `bpa-drama-test01 / drama_test` 依次执行上述两份审核增量，共 12 条语句，按用户要求未备份。新增 `application_review_versions` 和 `review_content_lists` 两张表、两个唯一索引及 10 个菜单/操作节点，菜单父级与路由验证通过；没有新增外键、CHECK 或应用审核 JSON 列。原有 6 个应用、2 条 `global_configs`、99 个既有权限节点及 28 条角色授权的完整字段摘要前后一致；新审核表初始为空，历史应用保持正常首页。代码已推送 `feat/ads` 并合入 `test`，部署时须使用这轮分表及独立审核首页代码，不使用此前应用 JSON 方案的中间提交。

## 主体、共享 VOD 和 Mini 审核合并上线

[合并上线脚本](releases/upgrade-subjects-and-mini-review.sql) 包含主体升级、Mini 审核、共享 VOD 空间和审核开关菜单。前序增量（含 `20261102_002_ad_inner_error.sql`）已完成、这四份增量均未执行且全局 `media.byteplus_account_id` 已配置的现有库，可在维护窗口执行此文件一次。合并脚本沿用全局媒资账号，旧库需要从 `distribution` 回填账号时使用原迁移。已执行其中部分增量的库按实际结构补齐，遇到错误时先核对已经完成的语句。

合并脚本按共享 VOD 的最终结构创建主体表，一次写入默认主体的发布归属和历史空间。短剧表在一次 `ALTER TABLE` 中添加归属列并标记废弃字段。

隔离 MySQL 8.4 验证了合并与逐份执行的表结构、索引和权限节点一致。`TestSubjectContentUpgradeIntegration` 在已有全局媒资账号的旧库上直接执行合并脚本，验证媒资配置、旧 Mini 凭据、详情和播放以及直连发布指针和首页配置的保留。

## 2026-11-09 锁定首集的直连片段

在前序增量完成后执行一次 [20261109_001_entry_preview.sql](migrations/20261109_001_entry_preview.sql)，再更新 Server 和 Worker。新增 `media_jobs.preview_seconds`，既有任务默认为 0。短片任务按主体、空间、线上 VID 和秒数复用，沿用现有媒资任务的领取、重试和清理流程。应用启动不自动迁移。

Worker 使用现有 VOD 账号在原片所在空间提交剪辑并查询结果。只有声明支持首集片段的 Mini 才会创建此类任务。隔离 MySQL 8.4 验证了旧媒资任务和远端标识保留、新增秒数默认为 0，以及并发排队去重和只回收独立片段。
