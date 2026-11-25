-- 前置条件：已执行全部前序增量，并停止旧版 API 与 Worker；直连统一读取 global_configs 的 playback.default。
-- 仅移除应用级配置引用，保留应用播放模式、修订、应用身份和发布关系；不修改全局配置值。
ALTER TABLE `application_playback_profiles` DROP COLUMN `playback_profile_key`;
