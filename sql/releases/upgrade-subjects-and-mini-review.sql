-- 主体隔离、共享 VOD 与 Mini 审核一次上线。
-- 合并 20261105_001_subjects.sql、20261106_001_shared_vod_space.sql、
-- 20261106_001_mini_review.sql 和 20261107_001_review_settings_menu.sql。
-- 适用前序增量已完成（含 20261102_002_ad_inner_error.sql）且上述四份均未执行的现有库。
-- 目标库的全局 media 配置须已有有效的 byteplus_account_id，直接沿用该账号。
-- 在维护窗口选中目标业务库、停止旧 API/Worker 后执行一次。遇错停止，核对已完成的语句后补缺。

-- 初始化主体，历史业务记录归属主体 1，VOD 沿用全局 media 配置。
CREATE TABLE `subjects` (
 `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '主体内部主键',
 `name` VARCHAR(128) NOT NULL COMMENT '主体显示名称',
 `remark` VARCHAR(500) NOT NULL DEFAULT '' COMMENT '主体用途说明',
 `vod_space_name` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '已废弃：原主体空间保留，运行时统一使用全局 media.space_name',
 `vod_space_key` VARCHAR(255) GENERATED ALWAYS AS (NULLIF(`vod_space_name`,'')) STORED COMMENT '已废弃：原主体空间索引值保留，不再限制主体共用空间',
 `publisher_owner_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '稳定的官方资产拥有方内部标识，非 applications.id',
 `tiktok_app_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '发布方官方 AppID，字符串保留前导零',
 `client_key` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '发布方 Client Key，不替代应用登录凭据',
 `client_secret` VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT '发布方密钥，仅服务端读取',
 `publisher_app_id` VARCHAR(64) GENERATED ALWAYS AS (NULLIF(`tiktok_app_id`,'')) STORED COMMENT '非空发布方 AppID 唯一性索引值',
 `publisher_key` VARCHAR(255) GENERATED ALWAYS AS (NULLIF(`client_key`,'')) STORED COMMENT '非空发布方唯一性索引值',
 `created_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间 UTC',
 `updated_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '更新时间 UTC',
 PRIMARY KEY (`id`), UNIQUE KEY `uk_subject_name` (`name`), UNIQUE KEY `uk_subject_publisher` (`publisher_key`), UNIQUE KEY `uk_subject_app` (`publisher_app_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='短剧、发布方及 VOD 空间所属主体，管理员和账号凭据全局共享';

-- 从全局配置读取原发布方和空间。主体空间值仅供保留历史。
INSERT INTO subjects(id,name,remark,publisher_owner_id,tiktok_app_id,client_key,client_secret,vod_space_name)
SELECT 1,'现有主体','现有应用及历史官方发布归属',
 COALESCE(NULLIF(JSON_UNQUOTE(JSON_EXTRACT(d.config_value,'$.owner_application_id')),''),'subject-1'),
 COALESCE(JSON_UNQUOTE(JSON_EXTRACT(d.config_value,'$.tiktok_app_id')),''),
 COALESCE(JSON_UNQUOTE(JSON_EXTRACT(d.config_value,'$.client_key')),''),
 COALESCE(JSON_UNQUOTE(JSON_EXTRACT(d.config_value,'$.client_secret')),''),
 COALESCE(JSON_UNQUOTE(JSON_EXTRACT(m.config_value,'$.space_name')),'')
FROM (SELECT 1 AS singleton) seed
LEFT JOIN global_configs d ON d.config_key='distribution'
LEFT JOIN global_configs m ON m.config_key='media';

-- 同一次改表补主体归属并标记废弃字段，原字段值保留供复制线上快照。
ALTER TABLE `dramas`
 ADD COLUMN `subject_id` BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '短剧、分集及媒资的权威所属主体',
 ADD KEY `idx_drama_subject_created` (`subject_id`,`created_at`,`id`),
 MODIFY COLUMN `published_snapshot` JSON DEFAULT NULL COMMENT '已废弃：原线上快照保留，运行时使用 tiktok_drama_states.published_snapshot',
 MODIFY COLUMN `platform_album_id` VARCHAR(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '已废弃：原 Album 标识保留，运行时使用主体发布状态';
ALTER TABLE `episodes`
 MODIFY COLUMN `platform_episode_id` VARCHAR(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '已废弃：原官方分集标识保留，运行时使用冻结发布版本';
ALTER TABLE `media_jobs`
 ADD COLUMN `subject_id` BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '任务和未绑定封面创建时冻结的主体',
 ADD KEY `idx_media_job_subject` (`subject_id`,`id`);

ALTER TABLE `applications` ADD COLUMN `subject_id` BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '所属 TikTok 发布主体；直连不依赖主体发布配置' AFTER `id`, ADD KEY idx_application_subject(subject_id,id);

ALTER TABLE `tiktok_publications` ADD COLUMN `subject_id` BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '发布时固定的主体，不随后台选择变化' AFTER `drama_id`, ADD KEY idx_publication_subject_history(subject_id,drama_id,created_at,id);
ALTER TABLE `tiktok_review_records` ADD COLUMN `subject_id` BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '审核版本所属主体' AFTER `drama_id`, ADD KEY idx_review_subject(subject_id,created_at,id);
ALTER TABLE `tiktok_release_records` ADD COLUMN `subject_id` BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '上线版本所属主体' AFTER `drama_id`, ADD KEY idx_release_subject(subject_id,updated_at,id);

CREATE TABLE `tiktok_drama_states` (
 `subject_id` BIGINT UNSIGNED NOT NULL COMMENT '官方发布主体',
 `drama_id` BIGINT UNSIGNED NOT NULL COMMENT '所属短剧主键',
 `platform_album_id` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '此主体创建的 TikTok Album',
 `published_snapshot` JSON NULL COMMENT '此主体已确认上线内容，未上线或下架为 NULL',
 `updated_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '官方状态更新时间 UTC',
 PRIMARY KEY(subject_id,drama_id), KEY idx_tiktok_state_drama(drama_id,subject_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='每主体独立的官方线上内容投影，不用于直连';
INSERT INTO tiktok_drama_states(subject_id,drama_id,platform_album_id,published_snapshot,updated_at)
SELECT 1,id,platform_album_id,published_snapshot,updated_at FROM dramas
WHERE platform_album_id<>'' OR (published_snapshot IS NOT NULL AND JSON_TYPE(published_snapshot)<>'NULL');

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) p WHERE p.code='directory.applications'),'主体管理','menu.subjects','menu','/channels/subjects',19,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) p WHERE p.code='menu.subjects'),'查看主体','subjects.read','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) p WHERE p.code='menu.subjects'),'创建主体','subjects.create','action','',1,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) p WHERE p.code='menu.subjects'),'编辑主体','subjects.update','action','',2,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
-- 不向已有普通角色自动授予新菜单或操作权限。

-- 线上发布及首页运营均在主体内唯一。
ALTER TABLE `direct_publications` ADD COLUMN `subject_id` BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '上线状态所属主体' AFTER id, DROP INDEX uk_drama, ADD UNIQUE KEY uk_subject_drama(`subject_id`,drama_id), ADD KEY idx_subject_status(`subject_id`,status,drama_id), ADD KEY idx_direct_drama(drama_id,`subject_id`);

ALTER TABLE `direct_publication_versions` ADD COLUMN `subject_id` BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '冻结版本所属主体' AFTER id, DROP INDEX uk_version, ADD UNIQUE KEY uk_subject_version(`subject_id`,drama_id,version_number), ADD KEY idx_direct_version_drama(drama_id,`subject_id`);

ALTER TABLE `direct_publication_operations` ADD COLUMN `subject_id` BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '幂等操作所属主体' AFTER id, DROP INDEX uk_request, ADD UNIQUE KEY uk_subject_request(`subject_id`,drama_id,request_key), ADD KEY idx_subject_operation(`subject_id`,drama_id,id), ADD KEY idx_direct_operation_drama(drama_id,`subject_id`);

ALTER TABLE `homepage_recommendations` ADD COLUMN `subject_id` BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '推荐配置所属主体' AFTER id, DROP INDEX uq_homepage_section, ADD UNIQUE KEY uq_subject_homepage_section(`subject_id`,section);
ALTER TABLE `direct_homepage_recommendations` ADD COLUMN `subject_id` BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '推荐配置所属主体' AFTER id, DROP INDEX uk_key, ADD UNIQUE KEY uk_subject_section(`subject_id`,section);

ALTER TABLE `category_rankings` ADD COLUMN `subject_id` BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '分类榜所属主体' AFTER id, DROP INDEX uq_category_ranking, ADD UNIQUE KEY uq_subject_category_ranking(`subject_id`,tag_id);
ALTER TABLE `direct_category_rankings` ADD COLUMN `subject_id` BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '分类榜所属主体' AFTER id, DROP INDEX uk_key, ADD UNIQUE KEY uk_subject_tag(`subject_id`,tag_id);
-- 新主体从空内容开始；存量剧集主键、线上指针、媒体 VID、应用身份、广告和权益引用不变。

-- 审核版本按应用保存，共享清单按主体与播放渠道保存。
CREATE TABLE `application_review_versions` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '应用审核版本自增主键',
  `application_id` bigint unsigned NOT NULL COMMENT '审核版本所属应用内部主键',
  `version` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '精确匹配 Mini 包的三段版本号',
  `enabled` tinyint(1) NOT NULL DEFAULT '0' COMMENT '当前版本是否开启审核策略',
  `created_at` datetime(6) NOT NULL COMMENT '审核版本首次登记时间',
  `updated_at` datetime(6) NOT NULL COMMENT '版本开关最近修改时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_application_review_version` (`application_id`,`version`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='应用各包版本的独立审核开关';

CREATE TABLE `review_content_lists` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '应用审核共享清单自增主键',
  `subject_id` bigint unsigned NOT NULL COMMENT '共享审核内容所属发布主体',
  `playback_provider` varchar(32) NOT NULL COMMENT '官方或直连发布渠道，tiktok_minis 或 byteplus_web',
  `drama_ids_json` json NOT NULL COMMENT '同主体同渠道应用共享的有序审核短剧列表',
  `revision` bigint unsigned NOT NULL DEFAULT '0' COMMENT '同主体渠道审核版本与列表共同递增的并发修订',
  `created_at` datetime(6) NOT NULL COMMENT '共享清单首次建立时间',
  `updated_at` datetime(6) NOT NULL COMMENT '版本或共享清单最近修改时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_review_content_list_scope` (`subject_id`,`playback_provider`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='同主体官方与直连分开的共享审核内容列表';

-- 新增目录、菜单及精确操作权限，不自动授予已有普通角色。
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES (NULL,'加白管理','directory.whitelist','directory','',65,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) AS p WHERE p.code='directory.official'),'审核列表','menu.official_review_list','menu','/official/review-list',50,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) AS p WHERE p.code='menu.official_review_list'),'查看审核列表','operations.official_review_list.read','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) AS p WHERE p.code='menu.official_review_list'),'修改审核列表','operations.official_review_list.update','action','',10,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) AS p WHERE p.code='directory.direct'),'审核列表','menu.direct_review_list','menu','/direct/review-list',50,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) AS p WHERE p.code='menu.direct_review_list'),'查看审核列表','operations.direct_review_list.read','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) AS p WHERE p.code='menu.direct_review_list'),'修改审核列表','operations.direct_review_list.update','action','',10,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;

-- 加白管理下的独立审核开关菜单及读写权限。
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) AS p WHERE p.code='directory.whitelist'),'审核开关','menu.review_settings','menu','/whitelist/review-settings',10,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) AS p WHERE p.code='menu.review_settings'),'查看审核开关','operations.review_settings.read','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) AS p WHERE p.code='menu.review_settings'),'修改审核开关','operations.review_settings.update','action','',10,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
