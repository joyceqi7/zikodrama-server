-- 短剧操作人字段增量，新库在初始化基线后执行，已有库执行前备份并在隔离副本演练。
-- 本脚本只执行一次，先核对两个字段均不存在，应用启动不自动迁移。
-- 历史身份保持未知；后续人工修改只更新最新操作人，不推断历史创建人。
ALTER TABLE `dramas`
  ADD COLUMN `created_by` BIGINT UNSIGNED DEFAULT NULL COMMENT '创建管理员主键，历史未知时为空，创建后不覆盖',
  ADD COLUMN `updated_by` BIGINT UNSIGNED DEFAULT NULL COMMENT '最近成功人工操作的管理员主键，自动任务不覆盖';
