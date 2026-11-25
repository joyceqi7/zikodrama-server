-- 广告统计复用指标采集与幂等，不改变奖励、发布及 TikTok 回传日志。
CREATE TABLE `metric_ad_events` (
 `application_id` BIGINT UNSIGNED NOT NULL COMMENT '认证应用主键',
 `user_id` BIGINT UNSIGNED NOT NULL COMMENT '认证用户主键',
 `event_id` VARCHAR(64) NOT NULL COMMENT '广告调用事实键',
 `kind` VARCHAR(40) NOT NULL COMMENT '不可变调用阶段',
 `ad_type` VARCHAR(16) NOT NULL COMMENT '激励或插屏广告',
 `ad_unit_id` VARCHAR(128) NOT NULL COMMENT '实际调用的广告位',
 `scene` VARCHAR(32) NOT NULL COMMENT '固定业务入口',
 `client_version` VARCHAR(64) NOT NULL COMMENT 'Mini构建修订',
 `os` VARCHAR(16) NOT NULL COMMENT '公开系统分类或未知',
 `tik_tok_version` VARCHAR(32) NOT NULL COMMENT 'TikTok客户端版本或未知',
 `stage` VARCHAR(16) NOT NULL COMMENT '调用错误或超时所处阶段',
 `reason` VARCHAR(32) NOT NULL DEFAULT '' COMMENT '固定结果分类',
 `error_code` VARCHAR(12) NOT NULL DEFAULT '' COMMENT '官方数值错误码',
 `sub_error_code` VARCHAR(12) NOT NULL DEFAULT '' COMMENT '官方数值子码',
 `elapsed_ms` DOUBLE NULL COMMENT '业务调用开始后的耗时毫秒',
 `show_elapsed_ms` DOUBLE NULL COMMENT '展示请求开始后的耗时毫秒',
 `evidence` VARCHAR(32) NOT NULL DEFAULT '' COMMENT '展示Promise或完播补证',
 `occurred_at` DATETIME(6) NOT NULL COMMENT '调用发起时间及统计归属',
 `received_at` DATETIME(6) NOT NULL COMMENT '阶段首次接收时间',
 PRIMARY KEY (`application_id`,`user_id`,`event_id`,`kind`),
 KEY `idx_ad_retention` (`occurred_at`),
 KEY `idx_ad_range` (`application_id`,`occurred_at`),
 KEY `idx_ad_placement` (`application_id`,`ad_unit_id`,`occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='九十日广告调用诊断指标';

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at) VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) p WHERE p.code='directory.operations'),'广告统计','menu.ad_analytics','menu','/channels/ad-analytics',5,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at) VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) p WHERE p.code='menu.ad_analytics'),'查看广告统计','operations.ad_analytics.read','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
-- 不向现有普通角色自动授权，超级管理员沿用现有全权限机制。
