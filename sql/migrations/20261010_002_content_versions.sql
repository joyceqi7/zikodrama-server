-- 前置：停止 API 和 Worker，执行全部前序增量；直连尚未上线，移除开发期旧结构，不迁移直连数据。
-- 新模型：待上线只表示就绪，首次直连上架或 TK 同步生成共享版本，直连全应用共用当前上架版本，TK 继续走官方流程。
CREATE TABLE `drama_content_versions` (
 `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '版本主键',
 `drama_id` bigint unsigned NOT NULL COMMENT '剧目主键',
 `version_number` bigint NOT NULL COMMENT '剧内版本号',
 `source_digest` char(64) NOT NULL COMMENT '冻结内容摘要',
 `snapshot_json` json NOT NULL COMMENT '不可变内容快照',
 `created_by` varchar(64) NOT NULL DEFAULT '' COMMENT '首次发布操作人',
 `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '版本生成时间',
 PRIMARY KEY (`id`),
 UNIQUE KEY `uk_version` (`drama_id`,`version_number`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='短剧共享内容版本';


-- 直连使用共享版本和同步事务，开发期旧指针、命令及应用绑定直接清除。
DELETE FROM `direct_publication_operations`;
DELETE FROM `direct_publications`;
DROP TABLE `direct_publication_versions`;
DROP TABLE `direct_application_publications`;
ALTER TABLE `direct_publications` DROP COLUMN `current_operation_id`;
ALTER TABLE `direct_publication_operations`
 DROP INDEX `idx_claim`,
 DROP COLUMN `frozen_input_json`,
 DROP COLUMN `expected_revision`,
 DROP COLUMN `lease_owner`,
 DROP COLUMN `lease_until`,
 DROP COLUMN `retry_count`,
 DROP COLUMN `error_code`,
 ALTER COLUMN `status` SET DEFAULT 'succeeded';
-- 已有 TK 线上快照、发布任务和编辑状态保持原样；共享版本只在后续首次发布当前就绪内容时生成。
