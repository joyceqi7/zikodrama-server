-- feat/veplayer 初版重建结构：包含直连初版的 6 张表，建表后须继续执行后续增量（含主体内容隔离）。
-- 用于本轮开发测试库删除新增表后的重建，或尚未创建 VePlayer 表的数据库。
-- 执行前停止相关 API/Worker 写入，并选中目标数据库；本文件不创建或切换数据库。
-- 本文件替代 20261009_001、20261010_001、20261010_002、20261010_003 四份结构脚本，不能与它们叠加执行。
-- 20261009_002_direct_menus.sql 管理菜单与权限，独立执行；测试库已有菜单和授权保留。
-- 不包含开发期旧应用绑定或异步队列结构，不改写已有 TK 数据及短剧编辑状态。
-- 待上线仅表示就绪，直连上架生成自己的版本；TK 使用官方版本和任务快照，各渠道独立发布。
-- 不初始化应用播放模式；测试库已备份的配置在建表后使用独立数据恢复文件还原。
SET NAMES utf8mb4;

CREATE TABLE `application_playback_profiles` (
 `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '主键',
 `application_id` bigint unsigned NOT NULL COMMENT '应用主键',
 `provider` varchar(32) NOT NULL DEFAULT 'tiktok_minis' COMMENT '播放提供方',
 `revision` bigint NOT NULL DEFAULT 1 COMMENT '配置修订',
 `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
 `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '更新时间',
 PRIMARY KEY (`id`),
 UNIQUE KEY `uk_application` (`application_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='application_playback_profiles独立播放数据';

CREATE TABLE `direct_publication_versions` (
 `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '版本主键',
 `drama_id` bigint unsigned NOT NULL COMMENT '剧目主键',
 `version_number` bigint NOT NULL COMMENT '剧内版本号',
 `source_digest` char(64) NOT NULL COMMENT '冻结内容摘要',
 `snapshot_json` json NOT NULL COMMENT '不可变内容快照',
 `created_by` varchar(64) NOT NULL DEFAULT '' COMMENT '直连发布操作人',
 `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '直连版本生成时间',
 PRIMARY KEY (`id`),
 UNIQUE KEY `uk_version` (`drama_id`,`version_number`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='直连发布版本与快照';

CREATE TABLE `direct_publications` (
 `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '主键',
 `drama_id` bigint unsigned NOT NULL COMMENT '剧目主键',
 `status` varchar(32) NOT NULL DEFAULT 'unpublished' COMMENT '独立发布状态',
 `current_version_id` bigint unsigned NULL COMMENT '当前版本指针',
 `revision` bigint NOT NULL DEFAULT 0 COMMENT '发布修订栅栏',
 `updated_by` varchar(64) NOT NULL DEFAULT '' COMMENT '操作人',
 `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
 `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '更新时间',
 PRIMARY KEY (`id`),
 UNIQUE KEY `uk_drama` (`drama_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='direct_publications独立播放数据';

CREATE TABLE `direct_publication_operations` (
 `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '操作主键',
 `drama_id` bigint unsigned NOT NULL COMMENT '剧目主键',
 `request_key` varchar(128) NOT NULL COMMENT '幂等请求键',
 `input_digest` char(64) NOT NULL COMMENT '幂等输入摘要',
 `command_json` json NOT NULL COMMENT '原始操作命令',
 `status` varchar(32) NOT NULL DEFAULT 'succeeded' COMMENT '任务状态',
 `result_version_id` bigint unsigned NULL COMMENT '生效版本',
 `created_by` varchar(64) NOT NULL DEFAULT '' COMMENT '操作人',
 `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '提交时间',
 `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '更新时间',
 PRIMARY KEY (`id`),
 UNIQUE KEY `uk_request` (`drama_id`,`request_key`)
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
