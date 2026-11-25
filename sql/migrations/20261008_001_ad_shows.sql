-- 通用成功展示事实沿用用户桶的 16 张物理分表；只保存广告通用字段与可选剧目或剧集来源。
-- 此增量尚未发布，替代本轮入剧专用展示表草稿；不修改已存在的奖励或权益表。

CREATE TABLE `ad_shows_00` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';

CREATE TABLE `ad_shows_01` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';

CREATE TABLE `ad_shows_02` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';

CREATE TABLE `ad_shows_03` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';

CREATE TABLE `ad_shows_04` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';

CREATE TABLE `ad_shows_05` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';

CREATE TABLE `ad_shows_06` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';

CREATE TABLE `ad_shows_07` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';

CREATE TABLE `ad_shows_08` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';

CREATE TABLE `ad_shows_09` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';

CREATE TABLE `ad_shows_10` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';

CREATE TABLE `ad_shows_11` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';

CREATE TABLE `ad_shows_12` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';

CREATE TABLE `ad_shows_13` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';

CREATE TABLE `ad_shows_14` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';

CREATE TABLE `ad_shows_15` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的广告展示事实标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '已认证消费者逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '已认证内部应用主键',
  `visitor_id` bigint unsigned NOT NULL COMMENT '已认证消费者持久主体',
  `event_id` varchar(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '一次成功展示的不可变幂等键',
  `scene` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '已接入的展示场景编码',
  `ad_unit_id` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '实际成功展示的公开广告位',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧目主键',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '可选来源剧集主键',
  `occurred_at` datetime(6) NOT NULL COMMENT 'SDK 确认展示的 UTC 时刻',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_ad_show_event` (`bucket_id`, `application_id`, `visitor_id`, `event_id`),
  KEY `idx_ad_show_user_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `occurred_at`),
  KEY `idx_ad_show_drama_period` (`bucket_id`, `application_id`, `visitor_id`, `scene`, `drama_id`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='跨场景成功广告展示事实，不发放权益';
