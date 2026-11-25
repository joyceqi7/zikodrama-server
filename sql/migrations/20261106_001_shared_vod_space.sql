-- 前置：已完成 20261105_001_subjects.sql；所有主体统一使用 global_configs.media.space_name。
-- 直接沿用现有全局媒资配置，不改写 global_configs 的任何记录。

-- 历史主体空间列保留原值并废弃，运行时不再读写；短剧及上传任务仍按 subject_id 隔离。
ALTER TABLE `subjects`
 DROP INDEX uk_subject_vod_space,
 MODIFY COLUMN `vod_space_name` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '已废弃：原主体空间保留，运行时统一使用全局 media.space_name',
 MODIFY COLUMN `vod_space_key` VARCHAR(255) GENERATED ALWAYS AS (NULLIF(vod_space_name,'')) STORED COMMENT '已废弃：原主体空间索引值保留，不再限制主体共用空间';
