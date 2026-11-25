-- 已有库非 UID 记录主键升级：五组各 16 张物理表，共 80 张。
-- 仅在目标库结构与 sql/init.sql 一致、完成备份和隔离演练后，在维护窗口停止旧版写入实例执行。
-- 本脚本保留已有行和 ID，不修改 users_XX、id_allocators 或 consumer_domain 水位。
-- MySQL DDL 不能整批回滚；中途失败时先核对已完成的表，再执行剩余语句，切勿盲目重跑。
-- 请先按 sql/README.md 的只读检查查询核对目标库。执行时须由连接指定目标数据库。
SET NAMES utf8mb4;

-- 权益授予流水：entitlement_grants_00 至 entitlement_grants_15。
ALTER TABLE `entitlement_grants_00`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';
ALTER TABLE `entitlement_grants_01`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';
ALTER TABLE `entitlement_grants_02`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';
ALTER TABLE `entitlement_grants_03`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';
ALTER TABLE `entitlement_grants_04`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';
ALTER TABLE `entitlement_grants_05`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';
ALTER TABLE `entitlement_grants_06`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';
ALTER TABLE `entitlement_grants_07`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';
ALTER TABLE `entitlement_grants_08`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';
ALTER TABLE `entitlement_grants_09`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';
ALTER TABLE `entitlement_grants_10`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';
ALTER TABLE `entitlement_grants_11`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';
ALTER TABLE `entitlement_grants_12`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';
ALTER TABLE `entitlement_grants_13`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';
ALTER TABLE `entitlement_grants_14`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';
ALTER TABLE `entitlement_grants_15`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识';

-- 权益投影：user_entitlement_state_00 至 user_entitlement_state_15。
ALTER TABLE `user_entitlement_state_00`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';
ALTER TABLE `user_entitlement_state_01`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';
ALTER TABLE `user_entitlement_state_02`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';
ALTER TABLE `user_entitlement_state_03`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';
ALTER TABLE `user_entitlement_state_04`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';
ALTER TABLE `user_entitlement_state_05`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';
ALTER TABLE `user_entitlement_state_06`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';
ALTER TABLE `user_entitlement_state_07`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';
ALTER TABLE `user_entitlement_state_08`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';
ALTER TABLE `user_entitlement_state_09`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';
ALTER TABLE `user_entitlement_state_10`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';
ALTER TABLE `user_entitlement_state_11`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';
ALTER TABLE `user_entitlement_state_12`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';
ALTER TABLE `user_entitlement_state_13`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';
ALTER TABLE `user_entitlement_state_14`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';
ALTER TABLE `user_entitlement_state_15`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  MODIFY COLUMN `grant_id` BIGINT UNSIGNED NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id';

-- 权益撤销流水：entitlement_revocations_00 至 entitlement_revocations_15。
ALTER TABLE `entitlement_revocations_00`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';
ALTER TABLE `entitlement_revocations_01`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';
ALTER TABLE `entitlement_revocations_02`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';
ALTER TABLE `entitlement_revocations_03`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';
ALTER TABLE `entitlement_revocations_04`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';
ALTER TABLE `entitlement_revocations_05`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';
ALTER TABLE `entitlement_revocations_06`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';
ALTER TABLE `entitlement_revocations_07`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';
ALTER TABLE `entitlement_revocations_08`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';
ALTER TABLE `entitlement_revocations_09`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';
ALTER TABLE `entitlement_revocations_10`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';
ALTER TABLE `entitlement_revocations_11`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';
ALTER TABLE `entitlement_revocations_12`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';
ALTER TABLE `entitlement_revocations_13`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';
ALTER TABLE `entitlement_revocations_14`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';
ALTER TABLE `entitlement_revocations_15`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识';

-- 奖励结算流水：reward_grants_00 至 reward_grants_15。
ALTER TABLE `reward_grants_00`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';
ALTER TABLE `reward_grants_01`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';
ALTER TABLE `reward_grants_02`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';
ALTER TABLE `reward_grants_03`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';
ALTER TABLE `reward_grants_04`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';
ALTER TABLE `reward_grants_05`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';
ALTER TABLE `reward_grants_06`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';
ALTER TABLE `reward_grants_07`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';
ALTER TABLE `reward_grants_08`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';
ALTER TABLE `reward_grants_09`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';
ALTER TABLE `reward_grants_10`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';
ALTER TABLE `reward_grants_11`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';
ALTER TABLE `reward_grants_12`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';
ALTER TABLE `reward_grants_13`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';
ALTER TABLE `reward_grants_14`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';
ALTER TABLE `reward_grants_15`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识';

-- VIP 广告进度：application_vip_progress_00 至 application_vip_progress_15。
ALTER TABLE `application_vip_progress_00`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
ALTER TABLE `application_vip_progress_01`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
ALTER TABLE `application_vip_progress_02`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
ALTER TABLE `application_vip_progress_03`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
ALTER TABLE `application_vip_progress_04`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
ALTER TABLE `application_vip_progress_05`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
ALTER TABLE `application_vip_progress_06`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
ALTER TABLE `application_vip_progress_07`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
ALTER TABLE `application_vip_progress_08`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
ALTER TABLE `application_vip_progress_09`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
ALTER TABLE `application_vip_progress_10`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
ALTER TABLE `application_vip_progress_11`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
ALTER TABLE `application_vip_progress_12`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
ALTER TABLE `application_vip_progress_13`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
ALTER TABLE `application_vip_progress_14`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
ALTER TABLE `application_vip_progress_15`
  MODIFY COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识';
