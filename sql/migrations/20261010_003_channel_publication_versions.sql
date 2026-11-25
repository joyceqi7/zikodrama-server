-- 前置：停止 API/Worker，并执行全部前序增量；直连尚未上线，直接移除本轮共享版本结构。
-- 只清除开发期直连发布数据，不修改 dramas、episodes、TK 快照、任务或审核记录。
-- TK 沿用官方 platformVersion 与任务内冻结内容；短剧管理不查询或返回发布版本。
DELETE FROM `direct_publication_operations`;
DELETE FROM `direct_publications`;
DROP TABLE `drama_content_versions`;

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
