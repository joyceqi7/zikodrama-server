-- 在目标业务库执行；只为现有指标模板和事件日表补索引，不修改事实、去重键、汇总和 UID 水位。
-- 发布前先停止全部实例的统计维护任务，执行模板增量及本脚本，核对索引覆盖后再更新全部 API 和 Worker。
-- DDL 不属于事务回滚范围；中途失败后可重新执行本脚本，只跳过已经具备完整且可见目标索引的表。
DELIMITER $$

DROP PROCEDURE IF EXISTS `upgrade_metric_behavior_index`$$

CREATE PROCEDURE `upgrade_metric_behavior_index`()
BEGIN
  DECLARE finished boolean DEFAULT false;
  DECLARE metric_table varchar(64);
  DECLARE index_columns varchar(255);
  DECLARE invalid_index_parts int DEFAULT 0;
  DECLARE failure_message varchar(128);
  DECLARE metric_tables CURSOR FOR
    SELECT table_name FROM information_schema.tables
    WHERE table_schema=DATABASE()
      AND (table_name='metric_events_template' OR table_name REGEXP '^metric_events_[0-9]{8}$')
    ORDER BY table_name;
  DECLARE CONTINUE HANDLER FOR NOT FOUND SET finished=true;

  OPEN metric_tables;
  table_loop: LOOP
    FETCH metric_tables INTO metric_table;
    IF finished THEN
      LEAVE table_loop;
    END IF;

    SELECT GROUP_CONCAT(column_name ORDER BY seq_in_index SEPARATOR ','),COALESCE(SUM(sub_part IS NOT NULL OR collation<>'A' OR non_unique<>1 OR is_visible<>'YES' OR index_type<>'BTREE'),0)
    INTO index_columns,invalid_index_parts
    FROM information_schema.statistics
    WHERE table_schema=DATABASE() AND table_name=metric_table AND index_name='idx_metrics_app_kind_received';
    IF index_columns IS NOT NULL AND (index_columns<>'application_id,kind,received_at' OR invalid_index_parts>0) THEN
      SET failure_message=LEFT(CONCAT('Invalid metric index on ',metric_table,': ',index_columns,'; invalid_parts=',invalid_index_parts),128);
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT=failure_message;
    END IF;
    IF index_columns IS NULL THEN
      SET @metric_behavior_index_ddl=CONCAT('ALTER TABLE `',metric_table,'` ADD INDEX `idx_metrics_app_kind_received` (`application_id`,`kind`,`received_at`), ALGORITHM=INPLACE, LOCK=NONE');
      PREPARE metric_behavior_index_statement FROM @metric_behavior_index_ddl;
      EXECUTE metric_behavior_index_statement;
      DEALLOCATE PREPARE metric_behavior_index_statement;
    END IF;
  END LOOP;
  CLOSE metric_tables;
END$$

CALL `upgrade_metric_behavior_index`()$$
DROP PROCEDURE `upgrade_metric_behavior_index`$$

DELIMITER ;
