-- 测试环境消费者数据清理；适用 MySQL 8.0/8.4。
-- 独立维护脚本，不参与新环境部署初始化。
-- 清理：用户、权益授予/投影/撤销、领奖、VIP 进度、入剧展示事实、指标明细/去重/汇总。
-- 保留：短剧、分集、媒资、发布/审核/上线/分发、标签、轮播、热播、分类榜；
--       管理员及权限、管理审计、应用、投放配置、解锁策略、全局配置、id_allocators。
-- 不删除业务表、不重置自增值、不回退 UID 水位，也不清理 Redis UID 缓存。
--
-- 用法：在 SQL 客户端选中测试数据库，以新的独立连接执行整个文件。
-- 默认只列出清理范围；核对目标库后将 @drama_cleanup_apply 改为 1 才删除数据。
-- 正式清理前暂停该测试环境 API 和 Worker 写入，并备份待清数据；否则仍会产生新数据。
-- 需要临时使用 CREATE ROUTINE / ALTER ROUTINE / EXECUTE / CREATE TEMPORARY TABLES 权限。
-- 任一删除失败会回滚整个数据清理；不要使用忽略 SQL 错误的执行选项。
SET @drama_cleanup_expected_database = 'drama_test';
SET @drama_cleanup_apply = 0;

DELIMITER $$

CREATE PROCEDURE cleanup_drama_test_user_data_20260923(IN expected_database VARCHAR(64), IN apply_cleanup BOOLEAN)
SQL SECURITY INVOKER
BEGIN
    DECLARE finished BOOLEAN DEFAULT FALSE;
    DECLARE target_table VARCHAR(64);
    DECLARE affected_rows BIGINT DEFAULT 0;
    DECLARE table_cursor CURSOR FOR
        SELECT table_name FROM tmp_drama_cleanup_targets ORDER BY delete_order, table_name;
    DECLARE CONTINUE HANDLER FOR NOT FOUND SET finished = TRUE;
    DECLARE EXIT HANDLER FOR SQLEXCEPTION
    BEGIN
        ROLLBACK;
        DROP TEMPORARY TABLE IF EXISTS tmp_drama_cleanup_targets;
        RESIGNAL;
    END;

    IF DATABASE() IS NULL OR expected_database IS NULL OR expected_database = ''
        OR BINARY DATABASE() <> BINARY expected_database THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'Selected database does not match the expected test database';
    END IF;
    IF apply_cleanup IS NULL OR apply_cleanup NOT IN (0, 1) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'apply_cleanup must be 0 (preview) or 1 (delete)';
    END IF;

    CREATE TEMPORARY TABLE tmp_drama_cleanup_targets (
        table_name VARCHAR(64) NOT NULL PRIMARY KEY,
        delete_order INT NOT NULL,
        estimated_rows BIGINT UNSIGNED NOT NULL,
        deleted_rows BIGINT UNSIGNED NOT NULL DEFAULT 0,
        table_engine VARCHAR(64) NOT NULL
    ) ENGINE=InnoDB;

    -- 只匹配明确的消费者/统计表；不存在的历史表不报错，未知表不自动删除。
    INSERT INTO tmp_drama_cleanup_targets (table_name, delete_order, estimated_rows, table_engine)
    SELECT TABLE_NAME,
        CASE
            WHEN TABLE_NAME REGEXP '^users(_(0[0-9]|1[0-5]))?$' THEN 90
            WHEN TABLE_NAME REGEXP '^reward_grants(_(0[0-9]|1[0-5]))?$' THEN 80
            WHEN TABLE_NAME REGEXP '^entitlement_grants_(0[0-9]|1[0-5])$' THEN 70
            ELSE 10
        END,
        COALESCE(TABLE_ROWS, 0), COALESCE(ENGINE, '')
    FROM information_schema.TABLES
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'
        AND (
            TABLE_NAME REGEXP '^(users|reward_grants|application_vip_progress|entitlement_grants|user_entitlement_state|entitlement_revocations|ad_shows)_(0[0-9]|1[0-5])$'
            OR TABLE_NAME REGEXP '^metric_events_[0-9]{8}$'
            OR TABLE_NAME REGEXP '^dashboard_daily_[0-9]{6}$'
            OR TABLE_NAME IN (
                'metric_event_keys', 'metric_summary_state',
                'metric_events_template', 'dashboard_daily_template',
                'users', 'reward_grants', 'application_vip_progress'
            )
        );

    -- 现行消费者域必须包含六组各 16 张分表，避免在不完整或错误结构上执行部分清理。
    IF (SELECT COUNT(*) FROM tmp_drama_cleanup_targets
        WHERE table_name REGEXP '^(users|reward_grants|application_vip_progress|entitlement_grants|user_entitlement_state|entitlement_revocations)_(0[0-9]|1[0-5])$') <> 96 THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'Expected 96 consumer shard tables; inspect the database schema first';
    END IF;
    -- 兼容尚未升级的测试库；已升级时必须包含完整 16 张展示事实分表，禁止部分清理。
    IF (SELECT COUNT(*) FROM tmp_drama_cleanup_targets
        WHERE table_name REGEXP '^ad_shows_(0[0-9]|1[0-5])$') NOT IN (0, 16) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'Expected 0 or 16 entry ad show tables; inspect the database schema first';
    END IF;
    IF (SELECT COUNT(*) FROM tmp_drama_cleanup_targets
        WHERE table_name IN ('metric_event_keys', 'metric_summary_state', 'metric_events_template', 'dashboard_daily_template')) <> 4 THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'Required metric tables are missing; inspect the database schema first';
    END IF;
    IF EXISTS (SELECT 1 FROM tmp_drama_cleanup_targets WHERE table_engine <> 'InnoDB') THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'All cleanup targets must use InnoDB for transactional rollback';
    END IF;

    -- estimated_rows 来自元数据，仅供估算；delete_sql 供审阅，预览模式不执行这些语句。
    SELECT DATABASE() AS database_name, table_name, estimated_rows,
        CONCAT('DELETE FROM `', REPLACE(DATABASE(), '`', '``'), '`.`', table_name, '`;') AS delete_sql
    FROM tmp_drama_cleanup_targets ORDER BY delete_order, table_name;

    IF apply_cleanup = 1 THEN
        START TRANSACTION;
        OPEN table_cursor;
        cleanup_loop: LOOP
            FETCH table_cursor INTO target_table;
            IF finished THEN
                LEAVE cleanup_loop;
            END IF;
            -- 表名均来自以上白名单，仍引用当前库以避免错误的会话默认库影响执行。
            SET @drama_cleanup_statement = CONCAT('DELETE FROM `', REPLACE(DATABASE(), '`', '``'), '`.`', target_table, '`');
            PREPARE cleanup_statement FROM @drama_cleanup_statement;
            EXECUTE cleanup_statement;
            SET affected_rows = ROW_COUNT();
            DEALLOCATE PREPARE cleanup_statement;
            UPDATE tmp_drama_cleanup_targets SET deleted_rows = affected_rows WHERE table_name = target_table;
        END LOOP;
        CLOSE table_cursor;
        COMMIT;
        SELECT table_name, deleted_rows FROM tmp_drama_cleanup_targets ORDER BY delete_order, table_name;
        SELECT 'committed' AS cleanup_status, COUNT(*) AS cleaned_tables, SUM(deleted_rows) AS deleted_rows
        FROM tmp_drama_cleanup_targets;
    ELSE
        SELECT 'preview_only' AS cleanup_status, COUNT(*) AS planned_tables FROM tmp_drama_cleanup_targets;
    END IF;
    DROP TEMPORARY TABLE tmp_drama_cleanup_targets;
    SET @drama_cleanup_statement = NULL;
END$$

DELIMITER ;

CALL cleanup_drama_test_user_data_20260923(@drama_cleanup_expected_database, @drama_cleanup_apply);
DROP PROCEDURE cleanup_drama_test_user_data_20260923;

-- 清理成功后，测试设备重新启动 Mini 并登录；需要完全从零测试时同时清除客户端本地待领取、
-- 已解锁展示、历史与收藏。客户端本地数据不在 MySQL 中，本文件不操作设备或 Redis。
-- 若执行中断留下同名过程，确认它属于本脚本后单独删除，再重新执行本文件。
