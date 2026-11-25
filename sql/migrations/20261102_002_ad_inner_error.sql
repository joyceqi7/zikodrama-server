-- 独立保存 SDK 内部错误码，子码仍保留原值；失败上下文仅写日志，不增加诊断列。
ALTER TABLE `metric_ad_events`
 ADD COLUMN `inner_error_code` VARCHAR(12) NOT NULL DEFAULT '' COMMENT '官方广告内部错误码，不覆盖主码或子码' AFTER `sub_error_code`;
