ALTER TABLE `media_jobs`
    ADD COLUMN `preview_seconds` SMALLINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '独立首集片段的播放秒数，其他任务为零';
