-- Drama 新环境唯一初始化脚本，汇总当前最终表结构、索引、系统权限与基础配置。
-- 仅对空数据库执行一次；已有数据库必须先对比结构并单独审核变更 DDL，不能直接覆盖。
-- API 和 Worker 不会自动初始化或升级基础表；统计日期表由运行服务按模板动态创建和清理。
-- 表间关联由业务校验和事务维护，禁止数据库外键。执行前须创建并选中目标数据库。

SET NAMES utf8mb4;

CREATE TABLE `id_allocators` (
  `namespace` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '发号命名空间，消费者域固定为 consumer_domain',
  `next_id` bigint unsigned NOT NULL COMMENT '尚未分配的下一标识，申请号段时在事务内永久推进',
  `segment_size` int unsigned NOT NULL COMMENT '每个实例单次保留的号段大小',
  `updated_at` datetime(6) NOT NULL COMMENT '最近一次申请号段的 UTC 时间',
  PRIMARY KEY (`namespace`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='全局标识号段水位';

INSERT INTO `id_allocators` (`namespace`, `next_id`, `segment_size`, `updated_at`)
VALUES ('consumer_domain', 1, 100000, UTC_TIMESTAMP(6));


CREATE TABLE `entitlement_grants_00` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 00）';
CREATE TABLE `entitlement_grants_01` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 01）';
CREATE TABLE `entitlement_grants_02` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 02）';
CREATE TABLE `entitlement_grants_03` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 03）';
CREATE TABLE `entitlement_grants_04` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 04）';
CREATE TABLE `entitlement_grants_05` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 05）';
CREATE TABLE `entitlement_grants_06` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 06）';
CREATE TABLE `entitlement_grants_07` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 07）';
CREATE TABLE `entitlement_grants_08` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 08）';
CREATE TABLE `entitlement_grants_09` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 09）';
CREATE TABLE `entitlement_grants_10` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 10）';
CREATE TABLE `entitlement_grants_11` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 11）';
CREATE TABLE `entitlement_grants_12` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 12）';
CREATE TABLE `entitlement_grants_13` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 13）';
CREATE TABLE `entitlement_grants_14` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 14）';
CREATE TABLE `entitlement_grants_15` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益授予流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `data` json NOT NULL COMMENT '发放时冻结的权益范围与参数 JSON',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益来源类型，如广告、订单或订阅',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '可信来源事件标识',
  `source_item` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '同一来源事件内的权益项编码',
  `valid_from` datetime(6) NOT NULL COMMENT '权益生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '权益失效时间，NULL 表示不限制期限',
  `revoked_at` datetime(6) DEFAULT NULL COMMENT '兼容领域快照，新增流水始终为 NULL，撤销另记不可变事件',
  `granted_at` datetime(6) NOT NULL COMMENT '权益首次授予时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_source_item` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`,`source_item`),
  KEY `idx_entitlement_user_time` (`bucket_id`,`application_id`,`visitor_id`,`granted_at`),
  KEY `idx_entitlement_application_time` (`application_id`,`granted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变消费者权益授予流水（分片 15）';
CREATE TABLE `user_entitlement_state_00` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 00）';
CREATE TABLE `user_entitlement_state_01` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 01）';
CREATE TABLE `user_entitlement_state_02` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 02）';
CREATE TABLE `user_entitlement_state_03` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 03）';
CREATE TABLE `user_entitlement_state_04` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 04）';
CREATE TABLE `user_entitlement_state_05` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 05）';
CREATE TABLE `user_entitlement_state_06` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 06）';
CREATE TABLE `user_entitlement_state_07` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 07）';
CREATE TABLE `user_entitlement_state_08` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 08）';
CREATE TABLE `user_entitlement_state_09` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 09）';
CREATE TABLE `user_entitlement_state_10` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 10）';
CREATE TABLE `user_entitlement_state_11` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 11）';
CREATE TABLE `user_entitlement_state_12` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 12）';
CREATE TABLE `user_entitlement_state_13` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 13）';
CREATE TABLE `user_entitlement_state_14` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 14）';
CREATE TABLE `user_entitlement_state_15` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益投影记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `grant_id` bigint unsigned NOT NULL COMMENT '同一用户分片内的权益授予流水主键，关联 entitlement_grants_XX.id',
  `type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权益类型编码',
  `target_key` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'application、drama 或 episode 规范化鉴权目标',
  `data` json NOT NULL COMMENT '播放策略判定所需的权益快照',
  `valid_from` datetime(6) NOT NULL COMMENT '投影生效时间，使用 UTC',
  `valid_until` datetime(6) DEFAULT NULL COMMENT '投影失效时间，NULL 表示不限制期限',
  `created_at` datetime(6) NOT NULL COMMENT '投影创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_state_grant_target` (`grant_id`,`target_key`),
  KEY `idx_entitlement_state_playback` (`bucket_id`,`application_id`,`visitor_id`,`target_key`,`valid_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户当前有效权益的播放鉴权投影（分片 15）';
CREATE TABLE `entitlement_revocations_00` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 00）';
CREATE TABLE `entitlement_revocations_01` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 01）';
CREATE TABLE `entitlement_revocations_02` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 02）';
CREATE TABLE `entitlement_revocations_03` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 03）';
CREATE TABLE `entitlement_revocations_04` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 04）';
CREATE TABLE `entitlement_revocations_05` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 05）';
CREATE TABLE `entitlement_revocations_06` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 06）';
CREATE TABLE `entitlement_revocations_07` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 07）';
CREATE TABLE `entitlement_revocations_08` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 08）';
CREATE TABLE `entitlement_revocations_09` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 09）';
CREATE TABLE `entitlement_revocations_10` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 10）';
CREATE TABLE `entitlement_revocations_11` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 11）';
CREATE TABLE `entitlement_revocations_12` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 12）';
CREATE TABLE `entitlement_revocations_13` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 13）';
CREATE TABLE `entitlement_revocations_14` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 14）';
CREATE TABLE `entitlement_revocations_15` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的权益撤销流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者号段标识',
  `source_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源类型',
  `source_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '被撤销权益的来源事件标识',
  `revoked_at` datetime(6) NOT NULL COMMENT '撤销生效时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_entitlement_revocation_source` (`bucket_id`,`application_id`,`visitor_id`,`source_type`,`source_id`),
  KEY `idx_entitlement_revocation_time` (`application_id`,`revoked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='不可变权益来源撤销流水（分片 15）';
CREATE TABLE `reward_grants_00` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 00）';
CREATE TABLE `reward_grants_01` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 01）';
CREATE TABLE `reward_grants_02` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 02）';
CREATE TABLE `reward_grants_03` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 03）';
CREATE TABLE `reward_grants_04` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 04）';
CREATE TABLE `reward_grants_05` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 05）';
CREATE TABLE `reward_grants_06` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 06）';
CREATE TABLE `reward_grants_07` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 07）';
CREATE TABLE `reward_grants_08` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 08）';
CREATE TABLE `reward_grants_09` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 09）';
CREATE TABLE `reward_grants_10` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 10）';
CREATE TABLE `reward_grants_11` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 11）';
CREATE TABLE `reward_grants_12` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 12）';
CREATE TABLE `reward_grants_13` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 13）';
CREATE TABLE `reward_grants_14` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 14）';
CREATE TABLE `reward_grants_15` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的奖励流水标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `entitlements_json` json DEFAULT NULL COMMENT '本次奖励授予的权益项 JSON 快照',
  `rule_snapshot_json` json DEFAULT NULL COMMENT '领取时冻结的规则 JSON 快照，避免后续改规则影响结算',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '规则隔离的累计进度键，与周期键共同定位广告进度',
  `claim_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '客户端奖励领取标识，唯一键保证重复提交不重复结算',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `campaign_id` bigint unsigned DEFAULT NULL COMMENT '本次领取关联的投放活动标识，NULL 表示无归因',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的短剧标识，非短剧奖励可为 NULL',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '奖励关联的分集标识，非逐集奖励可为 NULL',
  `unlock_episode_ids_json` json DEFAULT NULL COMMENT '本次解锁的分集标识列表 JSON',
  `ad_unit_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '本次广告所用的官方广告位标识',
  `scene` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'unlock_reward' COMMENT '领取广告时的业务场景编码',
  `reward_type` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'episode_unlock' COMMENT '奖励类型编码，用于结算与统计',
  `period_key` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '按应用时区与规则生成的奖励周期键',
  `period_starts_at` datetime(6) DEFAULT NULL COMMENT '奖励周期开始时间，使用 UTC，无周期时为 NULL',
  `period_expires_at` datetime(6) DEFAULT NULL COMMENT '奖励周期结束时间，使用 UTC，无周期时为 NULL',
  `reward_required_count` int NOT NULL DEFAULT '0' COMMENT '该奖励完成一轮所需的广告次数快照',
  `daily_reward_limit` int NOT NULL DEFAULT '0' COMMENT '领取时的每日奖励次数上限，0 表示不限',
  `entitlement_scope` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '奖励授权范围编码，如单集或全目录',
  `granted_at` datetime(6) NOT NULL COMMENT '奖励成功结算时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reward_claim` (`bucket_id`,`claim_id`),
  KEY `idx_reward_grant_visitor_type_time` (`application_id`,`visitor_id`,`reward_type`,`granted_at`),
  KEY `idx_reward_grant_application_time` (`application_id`,`granted_at`),
  KEY `idx_reward_grant_campaign_time` (`application_id`,`campaign_id`,`granted_at`),
  KEY `idx_reward_grant_drama` (`drama_id`),
  KEY `idx_reward_grant_episode` (`episode_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='激励广告成功结算流水，与用户权益同分片事务写入（分片 15）';
CREATE TABLE `admin_audit_logs` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '数据库自增审计记录主键',
  `request_id` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '请求唯一标识，用于避免重复记录同一操作',
  `actor_id` bigint unsigned NOT NULL COMMENT '操作管理员标识，关联 admin_users.id',
  `actor_username` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '操作时的管理员用户名快照',
  `application_id` bigint unsigned DEFAULT NULL COMMENT '操作所属应用自增主键，关联 applications.id，跨应用操作可为 NULL',
  `action` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '执行的操作权限编码',
  `resource_type` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '被操作资源的类型编码',
  `resource_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '被操作资源的标识',
  `result` varchar(20) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '操作结果分类',
  `http_status` int NOT NULL COMMENT '操作返回的 HTTP 状态码',
  `client_ip` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '发起操作的客户端 IP 地址',
  `created_at` datetime(6) NOT NULL COMMENT '记录创建时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_admin_audit_request` (`request_id`),
  KEY `idx_admin_audit_created` (`created_at`),
  KEY `idx_admin_audit_actor_created` (`actor_id`,`created_at`),
  KEY `idx_admin_audit_application_created` (`application_id`,`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='运营后台操作审计日志，记录操作者、资源、结果与请求来源';
CREATE TABLE `admin_permissions` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '数据库自增权限节点主键',
  `parent_id` bigint unsigned DEFAULT NULL COMMENT '父节点，NULL 表示根节点',
  `type` varchar(20) NOT NULL DEFAULT 'action' COMMENT '目录、页面或操作，页面使用 menu 值',
  `route` varchar(200) NOT NULL DEFAULT '' COMMENT '页面节点绑定的站内路径',
  `icon` varchar(64) NOT NULL DEFAULT '' COMMENT '目录或页面使用的 Lucide 图标名称',
  `sort_order` int NOT NULL DEFAULT 0 COMMENT '同级显示顺序',
  `status` varchar(20) NOT NULL DEFAULT 'active' COMMENT '停用会使整个子树失效',
  `remark` varchar(500) NOT NULL DEFAULT '' COMMENT '权限用途说明',
  `name` varchar(100) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权限节点显示名称',
  `code` varchar(128) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '权限节点唯一编码，操作鉴权按该编码精确匹配',
  `created_at` datetime(6) NOT NULL COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_admin_permissions_code` (`code`),
  KEY `idx_admin_permissions_parent_order` (`parent_id`,`sort_order`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='可自定义的后台目录、页面和操作权限树，角色明确分配节点';
CREATE TABLE `admin_role_permissions` (
  `role_id` bigint unsigned NOT NULL COMMENT '角色主键，关联 admin_roles.id',
  `permission_id` bigint unsigned NOT NULL COMMENT '明确授予的权限节点主键，关联 admin_permissions.id',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  PRIMARY KEY (`role_id`,`permission_id`),
  KEY `idx_admin_role_permissions_permission` (`permission_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='运营后台角色与权限的多对多关联';
CREATE TABLE `admin_roles` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '数据库自增角色主键',
  `name` varchar(100) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '角色唯一显示名称',
  `code` varchar(128) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '角色唯一业务编码',
  `remark` varchar(500) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '角色职责与用途说明',
  `status` varchar(20) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'active' COMMENT '角色状态，active 为启用，disabled 为停用',
  `created_at` datetime(6) NOT NULL COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_admin_roles_name` (`name`),
  UNIQUE KEY `uk_admin_roles_code` (`code`),
  KEY `idx_admin_roles_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='运营后台角色定义及启停状态';
CREATE TABLE `admin_user_roles` (
  `user_id` bigint unsigned NOT NULL COMMENT '管理员主键，关联 admin_users.id',
  `role_id` bigint unsigned NOT NULL COMMENT '角色主键，关联 admin_roles.id',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  PRIMARY KEY (`user_id`,`role_id`),
  KEY `idx_admin_user_roles_role` (`role_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='运营后台用户与角色的多对多关联';
CREATE TABLE `admin_users` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '数据库自增管理员主键',
  `username` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '管理员唯一登录名',
  `password_hash` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '管理员密码的 bcrypt 摘要，禁止存储或记录明文密码',
  `nickname` varchar(100) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '管理员展示昵称',
  `status` varchar(20) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'active' COMMENT '账号状态，active 为启用，disabled 为停用',
  `is_super` tinyint(1) NOT NULL DEFAULT '0' COMMENT '是否为不受角色授权限制的超级管理员',
  `auth_version` bigint NOT NULL DEFAULT '1' COMMENT '认证版本，递增后使该账号旧版本 JWT 全部失效',
  `created_at` datetime(6) NOT NULL COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_admin_users_username` (`username`),
  KEY `idx_admin_users_status_super` (`status`,`is_super`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='运营后台管理员账号、密码摘要与认证版本';
CREATE TABLE `application_campaigns` (
	`landing_url` text COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '官方生成的 Minis Link',
	`minis_path` varchar(1024) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '生成链接时的小程序路径',
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '投放活动业务标识',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `code` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '应用内唯一的投放活动编码',
  `name` varchar(128) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '投放活动展示名称',
  `external_campaign_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '外部广告平台 Campaign 标识，按字符串保存',
  `external_ad_group_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '外部广告平台 Ad Group 标识，按字符串保存',
  `external_ad_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '外部广告平台 Ad 标识，按字符串保存',
  `external_creative_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '外部广告平台 Creative 标识，按字符串保存',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '落地短剧标识，空字符串表示未绑定',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '落地分集标识，空字符串表示未绑定',
  `experiment_key` varchar(128) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '投放实验分组键，空字符串表示未分组',
  `status` varchar(20) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'disabled' COMMENT '活动状态，active 为启用，disabled 为停用',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_application_campaign_code` (`application_id`,`code`),
  KEY `idx_application_campaign_status` (`application_id`,`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='应用投放活动与 TikTok 外部投放标识、落地短剧的绑定';
CREATE TABLE `application_vip_progress_00` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 00）';
CREATE TABLE `application_vip_progress_01` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 01）';
CREATE TABLE `application_vip_progress_02` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 02）';
CREATE TABLE `application_vip_progress_03` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 03）';
CREATE TABLE `application_vip_progress_04` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 04）';
CREATE TABLE `application_vip_progress_05` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 05）';
CREATE TABLE `application_vip_progress_06` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 06）';
CREATE TABLE `application_vip_progress_07` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 07）';
CREATE TABLE `application_vip_progress_08` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 08）';
CREATE TABLE `application_vip_progress_09` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 09）';
CREATE TABLE `application_vip_progress_10` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 10）';
CREATE TABLE `application_vip_progress_11` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 11）';
CREATE TABLE `application_vip_progress_12` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 12）';
CREATE TABLE `application_vip_progress_13` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 13）';
CREATE TABLE `application_vip_progress_14` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 14）';
CREATE TABLE `application_vip_progress_15` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '物理分表内自增的 VIP 进度记录标识',
  `bucket_id` smallint unsigned NOT NULL COMMENT '用户稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
  `visitor_id` bigint unsigned NOT NULL COMMENT '消费者标识，关联 users.id',
  `progress_key` varchar(192) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '广告规则进度隔离键，不同规则分别累计',
  `period_key` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '按应用时区与规则划分的业务周期键',
  `completed_count` int NOT NULL DEFAULT '0' COMMENT '本周期已完成的有效广告次数',
  `required_count` int NOT NULL COMMENT '当前进度采用的广告完成门槛',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_vip_progress` (`bucket_id`,`application_id`,`visitor_id`,`progress_key`,`period_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='访客在应用业务周期内完成 VIP 激励广告的累计进度（分片 15）';
CREATE TABLE `unlock_strategies` (
  `application_id` bigint unsigned NOT NULL COMMENT '策略唯一所属的应用主键',
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '应用解锁策略内部标识',
  `revision` int NOT NULL COMMENT '策略规则修订号，防止并发编辑覆盖',
  `rules_json` json NOT NULL COMMENT '含 schemaVersion 的玩法规则与权益模板 JSON',
  `created_at` datetime(6) NOT NULL COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_unlock_strategy_application` (`application_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='每个应用唯一的解锁策略，玩法条件与权益模板统一保存为 JSON';
CREATE TABLE `applications` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '数据库自增主键，供内部关联使用',
  `name` varchar(128) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '小程序展示名称，也是消费者品牌名称',
  `logo_url` varchar(2048) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '小程序公开 HTTPS 图标地址，空字符串表示未配置',
  `tiktok_app_id` varchar(255) COLLATE utf8mb4_bin NOT NULL COMMENT '官方 AppID，平台身份唯一键，内部关联使用 id',
  `identity_hash_namespace` varchar(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT '仅兼容迁移前的用户身份摘要',
  `client_key` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'TikTok 应用 Client Key，不是 Client Secret',
  `client_secret` varchar(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT '当前小程序 Client Secret，仅服务端读取，不通过接口返回',
  `secret_ref` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '服务端解析 TikTok Client Secret 的环境变量引用名',
  `region_code` varchar(16) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'GLOBAL' COMMENT '应用运营地域码，GLOBAL 表示未限定单一地域',
  `time_zone` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'UTC' COMMENT '奖励自然日划分使用的 IANA 时区，如 UTC',
  `compliance_base_url` text COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '应用合规页面的 HTTPS 基础地址，空字符串表示未配置',
  `monetization_mode` varchar(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'IAA' COMMENT '应用变现模式：IAA 广告、IAP 内购、IAA+IAP 混合',
  `ad_units_json` json NOT NULL COMMENT 'TikTok 官方广告位编码 JSON，区分激励视频与插屏',
  `status` varchar(20) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'active' COMMENT '应用状态，active 为启用，disabled 为停用',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  `unlock_strategy_id` bigint unsigned DEFAULT NULL COMMENT '应用专属解锁策略标识，NULL 表示尚未配置',
  `unlock_bindings_json` json DEFAULT NULL COMMENT '解锁策略所需的应用商品等资源映射 JSON',
  KEY `idx_application_unlock_strategy` (`unlock_strategy_id`),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_applications_tiktok_app_id` (`tiktok_app_id`),
  KEY `idx_applications_client_key` (`client_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='TikTok Minis 应用主数据、品牌、地域、广告位编码与平台配置';
CREATE TABLE `dramas` (
  `assignee` varchar(128) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '当前发布链路负责人',
  `operation_note` text COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '运营交接备注',
  `operation_due_at` datetime(6) DEFAULT NULL COMMENT '运营期望完成时间，UTC',
  `cover_mid` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'BytePlus 封面素材 MID，按字符串保存',
  `cover_space_name` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '封面素材所在的 BytePlus 空间',
  `cover_file_name` varchar(1000) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '封面素材的 BytePlus StoreUri，供删除原文件使用',
	`published_snapshot` json DEFAULT NULL COMMENT '已上架内容快照，独立于编辑中的草稿',
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '短剧聚合业务标识',
  `title` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '短剧主标题',
  `description` text COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '短剧主简介',
  `cover_url` text COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '短剧公开封面地址',
  `status` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '短剧站内内容状态',
  `year` int NOT NULL DEFAULT '0' COMMENT '短剧出品年份，0 表示未填写',
  `language` varchar(16) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'zh' COMMENT '原始音频语言代码，不表示界面或字幕语言',
  `drama_type` int NOT NULL DEFAULT '0' COMMENT 'TikTok 官方短剧类型枚举值',
  `tag_ids_json` json NOT NULL COMMENT 'TikTok 官方标签标识列表 JSON',
  `publish_status` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '当前草稿的发布流程状态',
  `platform_album_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'TikTok 官方 Album 标识，普通内容编辑不得覆盖',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  KEY `idx_dramas_publish_status` (`publish_status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='全局短剧内容与站内发布状态主数据';
CREATE TABLE `episodes` (
  `media_space_name` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '当前视频所在的 BytePlus VOD 空间',
	`is_draft` boolean NOT NULL DEFAULT TRUE COMMENT '是否仍属于编辑区，线上历史分集保留主键',
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '分集业务标识，历史上线分集保留该主键',
  `drama_id` bigint unsigned NOT NULL COMMENT '所属短剧标识，关联 dramas.id',
  `episode_number` int NOT NULL COMMENT '剧内集序号，编辑替换时历史保留行可使用负数避让',
  `title` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '分集主标题',
  `description` text COLLATE utf8mb4_unicode_ci COMMENT '分集主简介，NULL 表示未填写',
  `vid` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '当前分集的 BytePlus 视频 VID，按字符串保存',
  `platform_episode_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'TikTok 官方分集标识，普通内容编辑不得覆盖',
  `poster_url` text COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '分集公开海报地址，供列表与播放器展示',
  `duration_seconds` int NOT NULL DEFAULT '0' COMMENT '视频时长，单位秒，0 表示尚未获取',
  `media_status` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '当前分集媒资处理状态',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_episode_number` (`drama_id`,`episode_number`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='短剧分集、媒资标识、播放时长与处理状态';
CREATE TABLE `tiktok_publications` (
	`workflow_json` json NOT NULL COMMENT '冻结内容及逐视频的官方处理事实',
	`revision` bigint unsigned NOT NULL DEFAULT 0 COMMENT '比较并交换修订号',
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '可恢复发布任务标识',
  `drama_id` bigint unsigned NOT NULL COMMENT '所属短剧标识，关联 dramas.id',
  `owner_application_id` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '媒资拥有方的全局分发配置键，非 applications.id',
  `priority` varchar(16) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '发布优先级编码',
  `status` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '发布任务状态',
  `stage` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '当前官方发布流程阶段',
  `platform_cover_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'TikTok Image API 返回的封面标识',
  `platform_album_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'TikTok 官方 Album 标识，按字符串保存',
  `platform_version` int NOT NULL DEFAULT '0' COMMENT 'TikTok 官方剧目版本号',
  `review_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'TikTok 官方审核记录标识',
  `review_status` int NOT NULL DEFAULT '0' COMMENT 'TikTok 官方审核状态枚举值',
  `platform_publish_status` int NOT NULL DEFAULT '0' COMMENT 'TikTok 官方发布状态枚举值',
  `episode_ids_json` json NOT NULL COMMENT '本次发布固定的分集标识列表 JSON',
  `error_code` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '最近一次失败的稳定错误分类',
  `error_message` varchar(500) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '最近一次失败的可公开错误摘要，不含凭据或媒体地址',
  `attempts` int NOT NULL DEFAULT '0' COMMENT '任务已执行的重试次数',
  `next_attempt_at` datetime(6) NOT NULL COMMENT '任务下次允许领取或重试的时间，使用 UTC',
  `lease_owner` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '当前领取任务的 Worker 标识，空字符串表示未领取',
  `lease_expires_at` datetime(6) NOT NULL DEFAULT '1970-01-01 00:00:00.000000' COMMENT 'Worker 租约到期时间，过期后允许其他 Worker 领取',
  `submitted_at` datetime(6) DEFAULT NULL COMMENT '提交官方审核时间，未提交时为 NULL',
  `online_at` datetime(6) DEFAULT NULL COMMENT '官方上线完成时间，未上线时为 NULL',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  KEY `idx_tiktok_publications_drama_history` (`drama_id`,`created_at`,`id`),
  KEY `idx_tiktok_publications_due` (`status`,`next_attempt_at`,`lease_expires_at`),
  KEY `idx_tiktok_publications_owner` (`owner_application_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='TikTok 官方剧目同步、审核、上线与下架的可恢复任务';
CREATE TABLE `media_jobs` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '媒资任务业务标识',
  `idempotency_key` varchar(128) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '媒资请求幂等键，唯一约束防止重复建任务',
  `episode_id` bigint unsigned DEFAULT NULL COMMENT '上传所属分集主键，封面等非分集任务为 NULL',
  `kind` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '任务类型，upload 为视频上传，cover 为封面上传，delete 为删除',
  `drama_id` bigint unsigned DEFAULT NULL COMMENT '任务所属短剧标识，删除短剧后仍保留，未绑定时为 NULL',
  `resource_class` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'BytePlus 资源类别，用于选择视频或素材接口',
  `space_name` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '任务创建时固定的 BytePlus 空间',
  `provider_file_name` varchar(1000) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'BytePlus 素材 StoreUri，供原文件删除使用',
  `public_url` text COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '封面上传返回的公开地址，供保存短剧使用',
  `file_name` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '上传文件的原始名称',
  `file_size` bigint NOT NULL COMMENT '上传文件大小，单位字节',
  `expected_vid` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '上传预留阶段确定的预期视频 VID',
  `remote_vid` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'BytePlus 最终返回的视频 VID 或封面 MID',
  `upload_request_id` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT 'BytePlus 上传请求标识，供任务追踪',
  `status` varchar(32) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '媒资任务执行状态',
  `processing_started_at` datetime(6) DEFAULT NULL COMMENT '媒资进入处理阶段的时间，尚未开始时为 NULL',
  `processing_deadline_at` datetime(6) DEFAULT NULL COMMENT '创建任务时固定的处理截止时间，未进入处理时可为 NULL',
  `error_message` varchar(1000) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '最近一次失败的可公开错误摘要，不含凭据或媒体地址',
  `attempt_count` int NOT NULL DEFAULT '0' COMMENT '媒资任务已执行的尝试次数',
  `next_attempt_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '任务下次允许领取或重试的时间，使用 UTC',
  `lease_owner` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '当前领取任务的 Worker 标识，空字符串表示未领取',
  `lease_expires_at` datetime(6) DEFAULT NULL COMMENT 'Worker 租约到期时间，NULL 表示未领取',
  `created_at` datetime(6) NOT NULL COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL COMMENT '记录最近更新时间，使用 UTC',
  `active_episode_id` bigint unsigned GENERATED ALWAYS AS ((case when (`kind` = 'upload' AND `status` in (_utf8mb4'uploading',_utf8mb4'queued',_utf8mb4'running',_utf8mb4'processing')) then `episode_id` else NULL end)) STORED COMMENT '进行中上传任务的分集标识生成列，唯一索引限制每集一个有效上传',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_media_jobs_idempotency` (`idempotency_key`),
  UNIQUE KEY `uq_media_jobs_active_episode` (`active_episode_id`),
  KEY `idx_media_jobs_status_created` (`status`,`created_at`),
  KEY `idx_media_jobs_episode_created` (`episode_id`,`created_at`),
  KEY `idx_media_jobs_drama` (`drama_id`,`kind`),
  KEY `idx_media_jobs_status_updated` (`status`,`updated_at`),
  KEY `idx_media_jobs_status_due` (`status`,`next_attempt_at`),
  KEY `idx_media_jobs_lease` (`status`,`lease_expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='媒资上传、处理轮询、租约领取与失败重试任务';
CREATE TABLE `media_reconciliation_checkpoint` (
  `id` tinyint unsigned NOT NULL COMMENT '单行检查点标识，固定为 1',
  `cursor_id` bigint unsigned NOT NULL DEFAULT '0' COMMENT '最近完成巡检的短剧 ID，本轮到尾后归零',
  `pending_cursor_id` bigint unsigned NOT NULL DEFAULT '0' COMMENT '当前已领取但未完成巡检的短剧 ID',
  `lease_owner` varchar(96) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '当前巡检实例，空字符串表示无租约',
  `lease_expires_at` datetime(6) DEFAULT NULL COMMENT '租约截止时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '检查点最近更新时间，使用 UTC',
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='多实例媒资巡检的租约与可恢复游标';
CREATE TABLE `users_00` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 00）';
CREATE TABLE `users_01` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 01）';
CREATE TABLE `users_02` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 02）';
CREATE TABLE `users_03` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 03）';
CREATE TABLE `users_04` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 04）';
CREATE TABLE `users_05` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 05）';
CREATE TABLE `users_06` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 06）';
CREATE TABLE `users_07` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 07）';
CREATE TABLE `users_08` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 08）';
CREATE TABLE `users_09` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 09）';
CREATE TABLE `users_10` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 10）';
CREATE TABLE `users_11` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 11）';
CREATE TABLE `users_12` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 12）';
CREATE TABLE `users_13` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 13）';
CREATE TABLE `users_14` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 14）';
CREATE TABLE `users_15` (
  `id` bigint unsigned NOT NULL COMMENT '消费者号段业务标识，对应 API 的 visitorId',
  `bucket_id` smallint unsigned NOT NULL COMMENT '身份哈希确定的稳定逻辑桶，范围 0 到 1023',
  `application_id` bigint unsigned NOT NULL COMMENT '所属小程序内部标识',
  `open_id_hash` char(64) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT '应用范围内 TikTok OpenID 摘要，匿名开发用户为 NULL',
  `auth_version` bigint unsigned NOT NULL DEFAULT '1' COMMENT '访问凭据版本，递增后旧 VisitorToken 失效',
  `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '记录创建时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '记录最近更新时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_application_open_id` (`bucket_id`,`application_id`,`open_id_hash`),
  UNIQUE KEY `uq_users_application_id` (`bucket_id`,`application_id`,`id`),
  KEY `idx_users_application_created_id` (`application_id`,`created_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='C 端用户及应用内 TikTok 身份摘要（分片 15）';


CREATE TABLE `tiktok_tag_english_names` (
  `tag_id` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT '官方 TikTok 标签字符串标识',
  `english_name` varchar(100) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '' COMMENT '运营英文展示名，空值回退官方名称',
  `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '最近维护时间，使用 UTC',
  PRIMARY KEY (`tag_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='全局 TikTok 标签英文展示翻译';

-- 初始化可自定义权限树；角色保存明确节点 ID，不隐式授权以后新增的子节点。
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES (NULL, '内容管理', 'directory.content', 'directory', '', 0, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES (NULL, '应用管理', 'directory.applications', 'directory', '', 10, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES (NULL, '变现管理', 'directory.commercialization', 'directory', '', 20, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES (NULL, '运营数据', 'directory.operations', 'directory', '', 30, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES (NULL, '系统管理', 'directory.system', 'directory', '', 40, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'directory.content'), '短剧管理', 'menu.dramas', 'menu', '/dramas', 0, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'directory.content'), 'TikTok 标签', 'menu.tiktoktags', 'menu', '/content/tiktok-tags', 10, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'directory.applications'), 'TikTok 应用', 'menu.applications', 'menu', '/channels/applications', 20, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'directory.commercialization'), '解锁策略', 'menu.unlockstrategies', 'menu', '/monetization/unlock-strategy', 30, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'directory.commercialization'), '广告配置', 'menu.adconfiguration', 'menu', '/channels/ad-configuration', 20, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'directory.operations'), '投放配置', 'menu.campaigns', 'menu', '/channels/campaigns', 60, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'directory.system'), '配置管理', 'menu.globalconfigs', 'menu', '/system/configuration', 70, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'directory.system'), '用户', 'menu.users', 'menu', '/system/users', 80, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'directory.system'), '角色', 'menu.roles', 'menu', '/system/roles', 90, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'directory.system'), '权限', 'menu.permissions', 'menu', '/system/permissions', 100, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'directory.system'), '审计日志', 'menu.audit', 'menu', '/system/audit-log', 110, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.globalconfigs'), '查看全局配置', 'system.config.read', 'action', '', 0, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.globalconfigs'), '管理全局配置', 'system.config.update', 'action', '', 10, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.dramas'), '查看剧目', 'content.drama.read', 'action', '', 20, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.dramas'), '创建剧目', 'content.drama.create', 'action', '', 30, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.dramas'), '编辑剧目', 'content.drama.update', 'action', '', 40, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.dramas'), '删除剧目', 'content.drama.delete', 'action', '', 50, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.dramas'), '上传媒资', 'content.media.upload', 'action', '', 60, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.dramas'), '提交、重试与下架 TikTok 剧目', 'content.drama.publish', 'action', '', 70, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.dramas'), '同步短剧到 TikTok', 'content.drama.sync', 'action', '', 71, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.dramas'), '管理 TikTok 审核', 'content.drama.review', 'action', '', 72, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.dramas'), '上线和下架 TikTok 短剧', 'content.drama.release', 'action', '', 73, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.dramas'), '配置短剧应用分发', 'content.drama.distribute', 'action', '', 74, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.tiktoktags'), '查看 TikTok 标签', 'content.tiktok_tag.read', 'action', '', 0, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.tiktoktags'), '维护 TikTok 标签英文翻译', 'content.tiktok_tag.update', 'action', '', 10, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.applications'), '查看 TikTok 应用', 'applications.read', 'action', '', 90, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.applications'), '创建 TikTok 应用', 'applications.create', 'action', '', 100, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.applications'), '编辑 TikTok 应用', 'applications.update', 'action', '', 110, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.campaigns'), '查看投放活动', 'operations.campaign.read', 'action', '', 130, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.campaigns'), '创建投放活动', 'operations.campaign.create', 'action', '', 140, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.campaigns'), '编辑投放活动', 'operations.campaign.update', 'action', '', 150, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.users'), '查看用户', 'rbac.user.read', 'action', '', 160, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.users'), '创建用户', 'rbac.user.create', 'action', '', 170, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.users'), '编辑用户', 'rbac.user.update', 'action', '', 180, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.users'), '重置用户密码', 'rbac.user.reset_password', 'action', '', 190, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.users'), '分配用户角色', 'rbac.user.assign_role', 'action', '', 200, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.roles'), '查看角色', 'rbac.role.read', 'action', '', 210, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.roles'), '创建角色', 'rbac.role.create', 'action', '', 220, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.roles'), '编辑角色', 'rbac.role.update', 'action', '', 230, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.roles'), '删除角色', 'rbac.role.delete', 'action', '', 240, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.roles'), '分配角色权限', 'rbac.role.assign_permission', 'action', '', 250, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.permissions'), '查看权限', 'rbac.permission.read', 'action', '', 260, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.audit'), '查看审计日志', 'audit.read', 'action', '', 270, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.permissions'), '新增权限', 'rbac.permission.create', 'action', '', 280, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.permissions'), '编辑权限', 'rbac.permission.update', 'action', '', 290, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.permissions'), '删除权限', 'rbac.permission.delete', 'action', '', 300, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));


-- 默认短剧运营职责模板。
INSERT INTO admin_roles (name, code, remark, status, created_at, updated_at) VALUES
  ('短剧内容编辑', 'drama_content_editor', '维护短剧资料、分集与媒资', 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)),
  ('短剧审核运营', 'drama_review_operator', '同步短剧并处理 TikTok 审核', 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)),
  ('短剧上线运营', 'drama_release_operator', '确认上线、下架及应用分发', 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)),
  ('短剧运营负责人', 'drama_operations_manager', '管理短剧从内容到分发的完整链路', 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6))
ON DUPLICATE KEY UPDATE status='active', updated_at=CURRENT_TIMESTAMP(6);
INSERT IGNORE INTO admin_role_permissions (role_id, permission_id, created_at) SELECT r.id, p.id, CURRENT_TIMESTAMP(6) FROM admin_roles r JOIN admin_permissions p WHERE r.code='drama_content_editor' AND p.code IN ('directory.content','menu.dramas','content.drama.read','content.drama.create','content.drama.update','content.drama.delete','content.media.upload');
INSERT IGNORE INTO admin_role_permissions (role_id, permission_id, created_at) SELECT r.id, p.id, CURRENT_TIMESTAMP(6) FROM admin_roles r JOIN admin_permissions p WHERE r.code='drama_review_operator' AND p.code IN ('directory.content','menu.dramas','content.drama.read','content.drama.sync','content.drama.review');
INSERT IGNORE INTO admin_role_permissions (role_id, permission_id, created_at) SELECT r.id, p.id, CURRENT_TIMESTAMP(6) FROM admin_roles r JOIN admin_permissions p WHERE r.code='drama_release_operator' AND p.code IN ('directory.content','menu.dramas','content.drama.read','content.drama.release','content.drama.distribute');
INSERT IGNORE INTO admin_role_permissions (role_id, permission_id, created_at) SELECT r.id, p.id, CURRENT_TIMESTAMP(6) FROM admin_roles r JOIN admin_permissions p WHERE r.code='drama_operations_manager' AND p.code IN ('directory.content','menu.dramas','content.drama.read','content.drama.create','content.drama.update','content.drama.delete','content.media.upload','content.drama.sync','content.drama.review','content.drama.release','content.drama.distribute');

CREATE TABLE `drama_distributions` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '数据库自增记录主键',
 `drama_id` bigint unsigned NOT NULL COMMENT '所属短剧标识，关联 dramas.id',
 `application_id` bigint unsigned NOT NULL COMMENT '所属小程序自增主键，关联 applications.id',
 `owner_application_id` varchar(64) NOT NULL COMMENT '媒资拥有方的全局分发配置键，非 applications.id',
 `album_id` varchar(255) NOT NULL COMMENT '授权对应的 TikTok 官方 Album 标识',
 `enabled` boolean NOT NULL DEFAULT TRUE COMMENT '运营是否允许该剧分发到当前应用',
 `desired` boolean NOT NULL DEFAULT FALSE COMMENT '结合应用启停与剧目上线状态计算的授权目标',
 `status` varchar(32) NOT NULL DEFAULT 'pending' COMMENT '分发状态，pending、authorized、revoked 或 failed',
 `attempts` int NOT NULL DEFAULT 0 COMMENT '当前授权目标下的连续失败次数',
 `error_code` varchar(128) NOT NULL DEFAULT '' COMMENT '最近一次授权失败的安全错误分类',
 `next_attempt_at` datetime(6) NOT NULL COMMENT '任务下次允许领取或重试的时间，使用 UTC',
 `revision` bigint NOT NULL DEFAULT 0 COMMENT '授权意图修订号，防止旧 Worker 覆盖新目标',
 `lease_owner` varchar(255) NOT NULL DEFAULT '' COMMENT '当前领取任务的 Worker 标识，空字符串表示未领取',
 `lease_expires_at` datetime(6) NOT NULL COMMENT 'Worker 租约到期时间，过期后允许其他 Worker 领取',
 PRIMARY KEY (`id`),
  UNIQUE KEY `uq_drama_distribution` (`drama_id`,`application_id`),
 KEY `idx_distribution_queue` (`status`,`next_attempt_at`,`lease_expires_at`),
 KEY `idx_distribution_app` (`application_id`,`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='短剧应用分发、授权意图及可恢复任务';

-- 增加轮播菜单和精确操作权限，保留已有自定义节点；不自动扩展角色授权。
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'directory.content'), '轮播管理', 'menu.carousel', 'menu', '/content/carousel', 10, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.carousel'), '查看轮播', 'operations.carousel.read', 'action', '', 0, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.carousel'), '配置轮播', 'operations.carousel.update', 'action', '', 10, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;

-- 新增渠道管理下的独立仪表盘菜单和读取权限；不自动扩展现有角色授权，可重复执行。
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at) VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) p WHERE p.code='directory.operations'),'仪表盘','menu.dashboard','menu','/channels/dashboard',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at) VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) p WHERE p.code='menu.dashboard'),'查看仪表盘','operations.dashboard.read','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at) VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) p WHERE p.code='directory.operations'),'回传日志','menu.postbacklogs','menu','/channels/postback-logs',10,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at) VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) p WHERE p.code='menu.postbacklogs'),'查看回传日志','operations.postback_log.read','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;

-- 观众用户列表使用独立菜单与读取权限；不扩大已有角色的明确授权。
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES (NULL, '用户管理', 'directory.consumerusers', 'directory', '', 35, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'directory.consumerusers'), '用户列表', 'menu.consumerusers', 'menu', '/channels/users', 0, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.consumerusers'), '查看观众用户', 'operations.consumer_user.read', 'action', '', 0, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;

-- 人工热播菜单和精确操作权限；不自动扩权既有角色。
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'directory.content'), '热播短剧', 'menu.hot', 'menu', '/content/hot-dramas', 20, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.hot'), '查看热播', 'operations.hot.read', 'action', '', 0, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.hot'), '配置热播', 'operations.hot.update', 'action', '', 10, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;

-- 客户端指标采集，仅由运营仪表盘消费。
-- 分类排行榜权限升级，可重复执行；保留现有菜单和角色授权。
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'directory.content'), '分类排行榜', 'menu.categoryrankings', 'menu', '/content/category-rankings', 30, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.categoryrankings'), '查看分类排行榜', 'operations.ranking.read', 'action', '', 0, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO `admin_permissions` (`parent_id`, `name`, `code`, `type`, `route`, `sort_order`, `status`, `created_at`, `updated_at`) VALUES ((SELECT p.id FROM (SELECT id, code FROM admin_permissions) AS p WHERE p.code = 'menu.categoryrankings'), '配置分类排行榜', 'operations.ranking.update', 'action', '', 10, 'active', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;

CREATE TABLE `metric_events_template` (
  `bucket_id` smallint unsigned NOT NULL COMMENT '可信用户逻辑桶',
  `application_id` bigint unsigned NOT NULL COMMENT '应用内部主键',
  `user_id` bigint unsigned NOT NULL COMMENT '认证用户主键',
  `event_id` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '客户端事实去重键',
  `kind` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '计数事实类型',
  `occurred_at` datetime(6) NOT NULL COMMENT '经时间窗口校验的事实时间',
  `received_at` datetime(6) NOT NULL COMMENT '服务器首次接收时间',
  PRIMARY KEY (`bucket_id`,`application_id`,`user_id`,`event_id`,`kind`),
  KEY `idx_metrics_app_time_user` (`application_id`,`occurred_at`,`user_id`),
  KEY `idx_metrics_app_received` (`application_id`,`received_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='运营计数去重信号，不是观看历史或权益凭据';

CREATE TABLE `dashboard_daily_template` (
  `application_id` bigint unsigned NOT NULL COMMENT '应用内部主键',
  `date` varchar(10) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'UTC 统计日期',
  `new_users` bigint NOT NULL DEFAULT 0 COMMENT '新增用户数',
  `reward_completions` bigint NOT NULL DEFAULT 0 COMMENT '奖励结算次数',
  `episode_unlocks` bigint NOT NULL DEFAULT 0 COMMENT '授予解锁集数',
  `vip_activations` bigint NOT NULL DEFAULT 0 COMMENT 'VIP 授予次数',
  `active_users` bigint NOT NULL DEFAULT 0 COMMENT '当日去重用户数',
  `launches` bigint NOT NULL DEFAULT 0 COMMENT '启动次数',
  `play_starts` bigint NOT NULL DEFAULT 0 COMMENT '播放开始次数',
  `play_completions` bigint NOT NULL DEFAULT 0 COMMENT '匹配开始的完播次数',
  `rewarded_shows` bigint NOT NULL DEFAULT 0 COMMENT '激励展示次数',
  `ad_completions` bigint NOT NULL DEFAULT 0 COMMENT '匹配展示的广告完播次数',
  `interstitial_shows` bigint NOT NULL DEFAULT 0 COMMENT '插屏展示次数',
  `tiktok_postback_attempts` bigint NOT NULL DEFAULT 0 COMMENT 'TikTok关键行为回传尝试次数',
  `tiktok_postback_successes` bigint NOT NULL DEFAULT 0 COMMENT 'TikTok SDK确认回传成功次数',
  `tiktok_postback_failures` bigint NOT NULL DEFAULT 0 COMMENT 'TikTok关键行为回传失败次数',
  `monthly_active_users` bigint DEFAULT NULL COMMENT '截至当日的30日去重快照，未知为空',
  `updated_at` datetime(6) NOT NULL COMMENT '汇总生成时间',
  PRIMARY KEY (`application_id`,`date`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='仪表盘每日汇总月表模板';

CREATE TABLE `metric_event_keys` (
  `application_id` bigint unsigned NOT NULL COMMENT '应用内部主键',
  `user_id` bigint unsigned NOT NULL COMMENT '认证用户主键',
  `event_id` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '跨日事实去重键',
  `kind` varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '事实类型',
  `event_date` varchar(10) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '首次归属UTC日期',
  PRIMARY KEY (`application_id`,`user_id`,`event_id`,`kind`),
  KEY `idx_metric_key_expiry` (`event_date`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='保留90日的精简跨日去重索引';

CREATE TABLE `metric_summary_state` (
  `application_id` bigint unsigned NOT NULL COMMENT '应用内部主键',
  `refreshed_at` datetime(6) DEFAULT NULL COMMENT '最近成功汇总时间，空值表示需要刷新',
  `first_received_at` datetime(6) DEFAULT NULL COMMENT '应用首次收到采集信号的时间',
  PRIMARY KEY (`application_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='仪表盘汇总刷新状态';

-- 全局配置以 MySQL 为权威存储，Redis 只保存可重建缓存。
CREATE TABLE `global_configs` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '配置内部主键',
  `config_key` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '全局配置唯一键',
  `config_value` json NOT NULL COMMENT '配置原始对象，仅供服务端读取',
  `remark` varchar(500) NOT NULL DEFAULT '' COMMENT '配置用途说明',
  `created_at` datetime(6) DEFAULT NULL COMMENT '首次创建时间，历史未知时为空',
  `updated_at` datetime(6) DEFAULT NULL COMMENT '最后修改时间，历史未知时为空',
  `updated_by` varchar(64) NOT NULL DEFAULT '' COMMENT '最近操作人标识',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_global_config_key` (`config_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='全局配置权威存储';
INSERT INTO global_configs (config_key, config_value, remark, created_at, updated_at, updated_by)
VALUES ('distribution', '{}', '媒资拥有方配置', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6), 'system')
ON DUPLICATE KEY UPDATE id=id;

-- 全局共享分类榜。
CREATE TABLE `category_rankings` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '全局分类自增主键',
  `tag_id` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL COMMENT 'TikTok 官方字符串标签标识',
  `revision` bigint NOT NULL DEFAULT 0 COMMENT '成员或运营配置变化时递增的并发修订号',
  `items_json` json NOT NULL COMMENT '自动关联的有序短剧成员，包含展示状态',
  `created_at` datetime(6) NOT NULL COMMENT '分类首次关联时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL COMMENT '分类成员或配置最近变更时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_category_ranking` (`tag_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='全局分类及自动关联的短剧排行榜';

-- 轮播和热播的权威存储；新库由初始化脚本创建，不由应用启动自动建表。
CREATE TABLE `homepage_recommendations` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '推荐配置内部主键',
  `section` varchar(16) COLLATE utf8mb4_bin NOT NULL COMMENT '栏目标识：carousel 或 hot',
  `revision` bigint NOT NULL DEFAULT 0 COMMENT '整体替换的并发修订版本',
  `config_json` json NOT NULL COMMENT '有序字符串剧目引用及标题、显隐、创建时间',
  `created_at` datetime(6) NOT NULL COMMENT '配置入库时间，使用 UTC',
  `updated_at` datetime(6) NOT NULL COMMENT '配置最近修改时间，使用 UTC',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_homepage_section` (`section`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='全局轮播和人工热播配置';


-- 共享审核及上线记录，初始化时不自动给已有角色扩大菜单授权。
CREATE TABLE `tiktok_review_records` (
 `id` bigint unsigned NOT NULL COMMENT '关联发布任务ID，同审核版本幂等键',
 `drama_id` bigint unsigned NOT NULL COMMENT '共享短剧ID',
 `title` varchar(255) NOT NULL DEFAULT '' COMMENT '送审时名称',
 `status` varchar(32) NOT NULL COMMENT '独立业务状态',
 `actor_id` varchar(32) NOT NULL DEFAULT '' COMMENT '发起管理员ID，历史不可追溯为空',
 `record_json` json NOT NULL COMMENT '可公开的官方任务结果，不含内部账号和内容快照',
 `created_at` datetime(6) NOT NULL COMMENT '首次进入审核流程时间，使用 UTC',
 `updated_at` datetime(6) NOT NULL COMMENT '审核状态最近更新时间，使用 UTC',
 PRIMARY KEY (`id`), KEY `idx_drama_created` (`drama_id`,`created_at`), KEY `idx_status_created` (`status`,`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='每次送审的独立审核记录，冻结版本关联发布任务';

CREATE TABLE `tiktok_release_records` (
 `id` bigint unsigned NOT NULL COMMENT '关联发布任务ID，同审核版本幂等键',
 `drama_id` bigint unsigned NOT NULL COMMENT '共享短剧ID',
 `title` varchar(255) NOT NULL DEFAULT '' COMMENT '送审时名称',
 `status` varchar(32) NOT NULL COMMENT '独立业务状态',
 `actor_id` varchar(32) NOT NULL DEFAULT '' COMMENT '发起管理员ID，历史不可追溯为空',
 `record_json` json NOT NULL COMMENT '可公开的官方任务结果，不含内部账号和内容快照',
 `created_at` datetime(6) NOT NULL COMMENT '首次进入上线流程时间，使用 UTC',
 `updated_at` datetime(6) NOT NULL COMMENT '上线状态最近更新时间，使用 UTC',
 PRIMARY KEY (`id`), KEY `idx_drama_created` (`drama_id`,`created_at`), KEY `idx_status_created` (`status`,`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='每次审核版本的上线记录，幂等重试复用同一记录';

-- 可选超级管理员初始化开始。
-- 部署系统可在执行本文件前于同一 MySQL Session 设置以下变量：
--   @drama_admin_username      超级管理员用户名；
--   @drama_admin_password_hash 至少 12 字符密码对应的 60 字符 bcrypt 摘要；
--   @drama_admin_nickname      可选展示名称。
-- 三个变量均未设置时只初始化结构与系统数据，不创建管理员。
-- 本段不接收、不生成也不保存密码明文；用户名冲突时直接失败，不覆盖已有管理员。
INSERT INTO `admin_users`
  (`username`, `password_hash`, `nickname`, `status`, `is_super`, `auth_version`, `created_at`, `updated_at`)
SELECT
  CASE
    WHEN LOWER(TRIM(@drama_admin_username)) REGEXP '^[a-z][a-z0-9_.-]{2,63}$'
      THEN LOWER(TRIM(@drama_admin_username))
    ELSE NULL
  END,
  CASE
    WHEN CHAR_LENGTH(TRIM(@drama_admin_password_hash)) = 60
      AND LEFT(TRIM(@drama_admin_password_hash), 4) IN ('$2a$', '$2b$', '$2y$')
      THEN TRIM(@drama_admin_password_hash)
    ELSE NULL
  END,
  COALESCE(NULLIF(TRIM(@drama_admin_nickname), ''), '超级管理员'),
  'active',
  1,
  1,
  CURRENT_TIMESTAMP(6),
  CURRENT_TIMESTAMP(6)
WHERE @drama_admin_username IS NOT NULL
   OR @drama_admin_password_hash IS NOT NULL
   OR @drama_admin_nickname IS NOT NULL;

SET @drama_admin_username = NULL;
SET @drama_admin_password_hash = NULL;
SET @drama_admin_nickname = NULL;
-- 可选超级管理员初始化结束。
