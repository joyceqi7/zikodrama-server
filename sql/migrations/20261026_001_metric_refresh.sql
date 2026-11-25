-- 统计刷新不再在重计算期间持有状态行锁；Redis 仅协调，MySQL 租约和版本负责发布资格。
ALTER TABLE `metric_summary_state`
  ADD COLUMN `refresh_generation` bigint unsigned NOT NULL DEFAULT 0 COMMENT '每次领取刷新任务递增的发布隔离版本',
  ADD COLUMN `refresh_owner` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '当前刷新任务随机持有者，完成或释放后为空',
  ADD COLUMN `refresh_lease_until` datetime(6) DEFAULT NULL COMMENT 'MySQL 时钟下当前刷新任务的租约截止时间',
  ADD COLUMN `snapshot_at` datetime(6) DEFAULT NULL COMMENT '最近成功汇总的统计窗口基准时间，与完成时间分开';

UPDATE `metric_summary_state` SET `snapshot_at` = `refreshed_at`;

CREATE TABLE `metric_active_users_daily` (
  `application_id` bigint unsigned NOT NULL COMMENT '认证应用内部主键',
  `activity_date` varchar(10) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '首次事实归属的UTC日期',
  `user_id` bigint unsigned NOT NULL COMMENT '当天至少存在一个已接受事实的认证用户',
  PRIMARY KEY (`application_id`,`activity_date`,`user_id`),
  KEY `idx_metric_active_expiry` (`activity_date`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='九十日精确日活去重集合，不保存观看内容或权益';

CREATE TABLE `metric_summary_days` (
  `application_id` bigint unsigned NOT NULL COMMENT '认证应用内部主键',
  `date` varchar(10) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '事实归属的UTC日期',
  `revision` bigint unsigned NOT NULL DEFAULT 0 COMMENT '该日新事实批次修订，重复上报不递增',
  `published_revision` bigint unsigned NOT NULL DEFAULT 0 COMMENT '最近成功发布的行为统计所覆盖修订',
  `activity_ready` tinyint(1) NOT NULL DEFAULT 0 COMMENT '该日日活集合已从历史事件完成初始化',
  `behavior_ready` tinyint(1) NOT NULL DEFAULT 0 COMMENT '该日行为计数已生成过完整快照',
  PRIMARY KEY (`application_id`,`date`),
  KEY `idx_metric_summary_day_expiry` (`date`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='统计日初始化与待汇总修订，支持并发写入后继续补算';
