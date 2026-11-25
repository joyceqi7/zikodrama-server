-- 独立发布与官方数据隔离，先执行全部前序增量，不修改冻结初始化基线。

CREATE TABLE `application_playback_profiles` (
 `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '主键',
 `application_id` bigint unsigned NOT NULL COMMENT '应用主键',
 `provider` varchar(32) NOT NULL DEFAULT 'tiktok_minis' COMMENT '播放提供方',
 `playback_profile_key` varchar(64) NOT NULL DEFAULT '' COMMENT '直连公开配置引用',
 `revision` bigint NOT NULL DEFAULT 1 COMMENT '配置修订',
 `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
 `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '更新时间',
 PRIMARY KEY (`id`),
 UNIQUE KEY `uk_application` (`application_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='application_playback_profiles独立播放数据';

CREATE TABLE `direct_publications` (
 `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '主键',
 `drama_id` bigint unsigned NOT NULL COMMENT '剧目主键',
 `status` varchar(32) NOT NULL DEFAULT 'unpublished' COMMENT '独立发布状态',
 `current_version_id` bigint unsigned NULL COMMENT '当前版本指针',
 `current_operation_id` bigint unsigned NULL COMMENT '排他操作指针',
 `revision` bigint NOT NULL DEFAULT 0 COMMENT '发布修订栅栏',
 `updated_by` varchar(64) NOT NULL DEFAULT '' COMMENT '操作人',
 `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
 `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '更新时间',
 PRIMARY KEY (`id`),
 UNIQUE KEY `uk_drama` (`drama_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='direct_publications独立播放数据';

CREATE TABLE `direct_publication_versions` (
 `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '版本主键',
 `drama_id` bigint unsigned NOT NULL COMMENT '剧目主键',
 `version_number` bigint NOT NULL COMMENT '剧内版本号',
 `source_digest` char(64) NOT NULL COMMENT '冻结内容摘要',
 `snapshot_json` json NOT NULL COMMENT '不可变发布快照',
 `created_by` varchar(64) NOT NULL DEFAULT '' COMMENT '发布人',
 `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '发布时间',
 PRIMARY KEY (`id`),
 UNIQUE KEY `uk_version` (`drama_id`,`version_number`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='direct_publication_versions独立播放数据';

CREATE TABLE `direct_application_publications` (
 `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '主键',
 `drama_id` bigint unsigned NOT NULL COMMENT '剧目主键',
 `application_id` bigint unsigned NOT NULL COMMENT '应用主键',
 `enabled` tinyint NOT NULL DEFAULT 0 COMMENT '应用启用状态',
 `revision` bigint NOT NULL DEFAULT 1 COMMENT '分发修订',
 `updated_by` varchar(64) NOT NULL DEFAULT '' COMMENT '操作人',
 `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
 `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '更新时间',
 PRIMARY KEY (`id`),
 UNIQUE KEY `uk_drama_app` (`drama_id`,`application_id`),
 KEY `idx_app_enabled` (`application_id`,`enabled`,`drama_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='direct_application_publications独立播放数据';

CREATE TABLE `direct_publication_operations` (
 `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '操作主键',
 `drama_id` bigint unsigned NOT NULL COMMENT '剧目主键',
 `request_key` varchar(128) NOT NULL COMMENT '幂等请求键',
 `input_digest` char(64) NOT NULL COMMENT '幂等输入摘要',
 `command_json` json NOT NULL COMMENT '原始操作命令',
 `frozen_input_json` json NOT NULL COMMENT '冻结执行快照',
 `status` varchar(32) NOT NULL DEFAULT 'queued' COMMENT '任务状态',
 `expected_revision` bigint NOT NULL COMMENT '执行修订栅栏',
 `result_version_id` bigint unsigned NULL COMMENT '生效版本',
 `lease_owner` varchar(64) NOT NULL DEFAULT '' COMMENT '执行租约所有者',
 `lease_until` datetime(6) NULL COMMENT '租约截止时间',
 `retry_count` int NOT NULL DEFAULT 0 COMMENT '租约领取次数',
 `error_code` varchar(64) NOT NULL DEFAULT '' COMMENT '稳定失败原因',
 `created_by` varchar(64) NOT NULL DEFAULT '' COMMENT '操作人',
 `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '提交时间',
 `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '更新时间',
 PRIMARY KEY (`id`),
 UNIQUE KEY `uk_request` (`drama_id`,`request_key`),
 KEY `idx_claim` (`status`,`lease_until`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='direct_publication_operations独立播放数据';

CREATE TABLE `direct_homepage_recommendations` (
 `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '主键',
 `section` varchar(32) NOT NULL COMMENT '栏目',
 `revision` bigint NOT NULL DEFAULT 0 COMMENT '修订',
 `config_json` json NOT NULL COMMENT '有序推荐配置',
 `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
 `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '更新时间',
 PRIMARY KEY (`id`),
 UNIQUE KEY `uk_key` (`section`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='direct_homepage_recommendations独立运营配置';

CREATE TABLE `direct_category_rankings` (
 `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '主键',
 `tag_id` varchar(64) NOT NULL COMMENT '标签主键',
 `revision` bigint NOT NULL DEFAULT 0 COMMENT '修订',
 `items_json` json NOT NULL COMMENT '有序自动成员',
 `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
 `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '更新时间',
 PRIMARY KEY (`id`),
 UNIQUE KEY `uk_key` (`tag_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='direct_category_rankings独立运营配置';
