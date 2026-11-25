-- 前置：完成全部前序增量，先备份并停止旧 API/Worker 后执行，配套更新 Server/Admin。
-- 旧数据只有一个主体：初始化 id=1 的默认主体，全部存量记录原地补归属。
-- 现有直连版本、应用登录身份、UID、广告和权益表不变；不调用平台、不重新送审。
CREATE TABLE `subjects` (
 `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '主体内部主键',
 `name` VARCHAR(128) NOT NULL COMMENT '主体显示名称',
 `remark` VARCHAR(500) NOT NULL DEFAULT '' COMMENT '主体用途说明',
 `vod_space_name` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '主体独占的 VOD 空间，空值表示尚未配置',
 `vod_space_key` VARCHAR(255) GENERATED ALWAYS AS (NULLIF(`vod_space_name`,'')) STORED COMMENT '非空空间唯一索引值',
 `publisher_owner_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '稳定的官方资产拥有方内部标识，非 applications.id',
 `tiktok_app_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '发布方官方 AppID，字符串保留前导零',
 `client_key` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '发布方 Client Key，不替代应用登录凭据',
 `client_secret` VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT '发布方密钥，仅服务端读取',
 `publisher_app_id` VARCHAR(64) GENERATED ALWAYS AS (NULLIF(`tiktok_app_id`,'')) STORED COMMENT '非空发布方 AppID 唯一性索引值',
 `publisher_key` VARCHAR(255) GENERATED ALWAYS AS (NULLIF(`client_key`,'')) STORED COMMENT '非空发布方唯一性索引值',
 `created_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间 UTC',
 `updated_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '更新时间 UTC',
 PRIMARY KEY (`id`), UNIQUE KEY `uk_subject_name` (`name`), UNIQUE KEY `uk_subject_publisher` (`publisher_key`), UNIQUE KEY `uk_subject_app` (`publisher_app_id`), UNIQUE KEY `uk_subject_vod_space` (`vod_space_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='短剧、发布方及 VOD 空间所属主体，管理员和账号凭据全局共享';

INSERT INTO subjects(id,name,remark,publisher_owner_id,tiktok_app_id,client_key,client_secret)
SELECT 1,'现有主体','现有应用及历史官方发布归属',
 COALESCE(JSON_UNQUOTE(JSON_EXTRACT(g.config_value,'$.owner_application_id')),''),
 COALESCE(JSON_UNQUOTE(JSON_EXTRACT(g.config_value,'$.tiktok_app_id')),''),
 COALESCE(JSON_UNQUOTE(JSON_EXTRACT(g.config_value,'$.client_key')),''),
 COALESCE(JSON_UNQUOTE(JSON_EXTRACT(g.config_value,'$.client_secret')),'')
FROM (SELECT 1 AS singleton) seed LEFT JOIN global_configs g ON g.config_key='distribution';
UPDATE subjects SET publisher_owner_id='subject-1' WHERE id=1 AND publisher_owner_id='';

-- 原空间归默认主体；保留全局 media 配置供旧服务读取，新版上传只用主体空间。
UPDATE subjects s JOIN global_configs g ON g.config_key='media'
SET s.vod_space_name=COALESCE(JSON_UNQUOTE(JSON_EXTRACT(g.config_value,'$.space_name')),''),s.updated_at=s.updated_at
WHERE s.id=1;

ALTER TABLE `dramas`
 ADD COLUMN `subject_id` BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '短剧、分集及媒资的权威所属主体',
 ADD KEY `idx_drama_subject_created` (`subject_id`,`created_at`,`id`);
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

-- 原字段保留原值，仅标记废弃；运行时从主体投影与冻结版本读取官方状态。
ALTER TABLE `dramas`
 MODIFY COLUMN `published_snapshot` JSON DEFAULT NULL COMMENT '已废弃：原线上快照保留，运行时使用 tiktok_drama_states.published_snapshot',
 MODIFY COLUMN `platform_album_id` VARCHAR(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '已废弃：原 Album 标识保留，运行时使用主体发布状态';
ALTER TABLE `episodes`
 MODIFY COLUMN `platform_episode_id` VARCHAR(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '已废弃：原官方分集标识保留，运行时使用冻结发布版本';

-- VOD 账号和凭据全局共享，账号标识供空间上传授权及官方发布使用。
UPDATE global_configs m JOIN global_configs d ON d.config_key='distribution'
SET m.config_value=JSON_SET(m.config_value,'$.byteplus_account_id',COALESCE(JSON_UNQUOTE(JSON_EXTRACT(d.config_value,'$.byteplus_account_id')),'')),m.updated_at=m.updated_at
WHERE m.config_key='media'
 AND NOT JSON_CONTAINS_PATH(m.config_value,'one','$.byteplus_account_id')
 AND JSON_TYPE(JSON_EXTRACT(d.config_value,'$.byteplus_account_id'))='STRING';
-- global_configs 的所有记录保留，已有字段值不覆盖；发布方配置仅复制给默认主体。

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
