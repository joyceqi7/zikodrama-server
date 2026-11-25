-- 在全部前序增量后执行；审核版本和共享清单分表保存，不在 applications 增加审核字段。
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
