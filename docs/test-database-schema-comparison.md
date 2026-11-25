# 测试数据库结构对比

对比日期：2026-09-20
对比对象：`sql/init.sql` 在空 MySQL 8.4 数据库中的执行结果，与测试环境 `drama_test` 的 `information_schema` 只读快照。对比过程没有修改测试数据库。

## 总体结果

- 初始化脚本包含 122 张固定业务表；测试库包含 136 张表。
- 122 张固定表在测试库中全部存在，表引擎、行格式和字符集一致。
- 两侧都没有外键和 `CHECK` 约束，符合项目数据库约束。
- 初始化脚本没有测试库缺失的字段或索引。

## 测试库额外表

测试库比初始化脚本多 14 张表：

- 11 张运行期指标分表：`dashboard_daily_202606` 至 `dashboard_daily_202609`，以及 `metric_events_20260916` 至 `metric_events_20260921`。这些表由指标维护任务按日期从模板创建，不应固化到初始化脚本。
- 3 张旧版未分片表：`users`、`reward_grants`、`application_vip_progress`。当前代码使用 16 路分表，这三张表属于历史遗留。

只读行数核对显示：旧 `users` 有 23 行，旧 `reward_grants` 和 `application_vip_progress` 均为 0 行。因此不能直接删除旧 `users`，应先确认 23 行已完整迁入分表。

## 固定表差异

测试库仍保留 17 个已从最终结构移除的字段：

- `applications.monetization_revision`。
- `reward_grants_00` 至 `reward_grants_15` 的 `configuration_revision`。

测试库的 2 条应用记录中旧 `monetization_revision` 均为非零；奖励分表目前只有 `reward_grants_07` 有 1 条记录，其旧 `configuration_revision` 为 0。当前代码不再读取这些字段，但删除前仍应根据结构差异生成独立 DDL，并经过备份和人工审核。

`media_jobs.active_episode_id` 的生成表达式仍是旧版本，未把 `uploading` 纳入每集唯一的进行中上传状态。最终初始化脚本包含 `uploading`、`queued`、`running`、`processing`。

`users_00` 至 `users_15` 各多一个单列索引 `idx_users_application(application_id)`。最终结构已经使用覆盖游标分页的复合索引 `idx_users_application_created_id(application_id, created_at, id)`，旧单列索引属于冗余索引。

测试库另有以下非功能性漂移：

- 20 组表存在字段物理顺序差异，字段名称和类型一致。
- 150 个字段注释仍使用旧术语或缺少最新说明，主要是 Sonyflake 改为号段 ID、分类成员状态说明，以及审核记录时间字段注释。

## 建议处理顺序

1. 先备份并确认测试环境没有依赖旧未分片表的数据读取，再决定是否删除三张遗留表。
2. 根据本报告生成并审核变现修订字段清理和媒资生成列升级 DDL，执行后重新核对字段定义。
3. 使用 `EXPLAIN` 确认消费者列表查询稳定选择复合索引后，再删除 16 个冗余单列索引。
4. 字段顺序不影响运行，可不调整；字段注释可在维护窗口统一修正。

本报告只记录差异，没有对测试库执行 DDL、DML 或删除操作。
