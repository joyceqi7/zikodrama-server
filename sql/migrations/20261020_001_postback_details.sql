-- 官方事件诊断与计数分离保存，保留七天，不记录真实 OpenID 或凭据。
CREATE TABLE `metric_postback_details` (
 `application_id` BIGINT UNSIGNED NOT NULL COMMENT '认证应用主键',
 `user_id` BIGINT UNSIGNED NOT NULL COMMENT '认证用户主键',
 `event_id` VARCHAR(64) NOT NULL COMMENT '固定回传事实键',
 `kind` VARCHAR(40) NOT NULL COMMENT '回传阶段类型',
 `scene` VARCHAR(32) NOT NULL COMMENT '业务入口分类',
 `client_version` VARCHAR(64) NOT NULL COMMENT '客户端构建修订',
 `error_code` VARCHAR(12) NOT NULL DEFAULT '' COMMENT '官方数值错误码',
 `payload` JSON NOT NULL COMMENT '经白名单校验的脱敏诊断',
 `received_at` DATETIME(6) NOT NULL COMMENT '首次接收时间及保留期起点',
 PRIMARY KEY (`application_id`,`user_id`,`event_id`,`kind`),
 KEY `idx_postback_retention` (`received_at`),
 KEY `idx_postback_scene` (`application_id`,`scene`,`received_at`),
 KEY `idx_postback_version` (`application_id`,`client_version`,`received_at`),
 KEY `idx_postback_error` (`application_id`,`error_code`,`received_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='七天保留的官方事件脱敏诊断';
